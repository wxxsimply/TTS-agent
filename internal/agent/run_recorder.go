package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tts-agent/internal/store"
)

// RunRecorder persists one graph execution. Postgres stores trace metadata and
// checkpoints atomically; larger outputs are isolated as authorized artifacts
// instead of being embedded in checkpoint state.
type RunRecorder interface {
	StartRun(ctx context.Context, state *State, start NodeID) error
	RecordStep(ctx context.Context, state *State, sequence int, step StepTrace) error
	FinishRun(ctx context.Context, state *State, status string, errorType string) error
}

type NoopRunRecorder struct{}

func (NoopRunRecorder) StartRun(context.Context, *State, NodeID) error { return nil }

func (NoopRunRecorder) RecordStep(context.Context, *State, int, StepTrace) error { return nil }

func (NoopRunRecorder) FinishRun(context.Context, *State, string, string) error { return nil }

type PostgresRunRecorder struct {
	db *store.Postgres
}

func NewPostgresRunRecorder(db *store.Postgres) *PostgresRunRecorder {
	return &PostgresRunRecorder{db: db}
}

func (r *PostgresRunRecorder) StartRun(ctx context.Context, state *State, start NodeID) error {
	if r == nil || r.db == nil || r.db.Pool == nil {
		return fmt.Errorf("agent run recorder database is not configured")
	}
	checkpoint := checkpointFromState(state, "", start, 0)
	checkpointJSON, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("marshal initial agent checkpoint: %w", err)
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin agent run record: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_runs (
			id, user_id, conversation_id, dataset_id, domain, graph_version,
			start_node, current_node, status, attempts, step_count, run_source
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, 'running', 0, 0, $8)`,
		state.RunID, nullableUUID(state.UserID), nullableUUID(state.ConversationID),
		nullableUUID(state.DatasetID), state.Domain, state.GraphVersion, start, state.RunSource,
	); err != nil {
		return fmt.Errorf("start agent run record: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_checkpoints (
			id, run_id, sequence, current_node, next_node, graph_version, state_data
		) VALUES ($1, $2, 0, NULL, $3, $4, $5)`,
		uuid.New(), state.RunID, start, state.GraphVersion, checkpointJSON,
	); err != nil {
		return fmt.Errorf("insert initial agent checkpoint: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit agent run record: %w", err)
	}
	return nil
}

func (r *PostgresRunRecorder) RecordStep(ctx context.Context, state *State, sequence int, step StepTrace) error {
	if r == nil || r.db == nil || r.db.Pool == nil {
		return fmt.Errorf("agent run recorder database is not configured")
	}
	checkpoint := checkpointFromState(state, step.Node, step.Next, sequence)
	checkpointJSON, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("marshal agent checkpoint: %w", err)
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin agent step record: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := validateJobLease(ctx, tx, state); err != nil {
		return err
	}

	var errorType interface{}
	if step.Error != "" {
		errorType = classifyAgentError([]string{step.Error})
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_steps (
			id, run_id, sequence, node, next_node, status,
			attempt, duration_ms, error_type
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		uuid.New(), state.RunID, sequence, step.Node, nullableNodeID(step.Next),
		step.Status, step.Attempt, step.DurationMS, errorType,
	); err != nil {
		return fmt.Errorf("insert agent step record: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_checkpoints (
			id, run_id, sequence, current_node, next_node, graph_version, state_data
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		uuid.New(), state.RunID, sequence, step.Node, step.Next, state.GraphVersion, checkpointJSON,
	); err != nil {
		return fmt.Errorf("insert agent checkpoint: %w", err)
	}
	if step.Node == NodeExecuteSQL && step.Status == StepCompleted {
		if err := insertRunArtifact(ctx, tx, state.RunID, sequence, ArtifactRows, state.Rows); err != nil {
			return err
		}
	}
	if step.Node == NodeReport && step.Status == StepCompleted {
		if err := insertRunArtifact(ctx, tx, state.RunID, sequence, ArtifactReport, state.Report); err != nil {
			return err
		}
	}
	if step.Node == NodeChart && step.Status == StepCompleted {
		if err := insertRunArtifact(ctx, tx, state.RunID, sequence, ArtifactChart, state.ChartConfig); err != nil {
			return err
		}
	}
	currentNode := step.Next
	if currentNode == "" {
		currentNode = step.Node
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_runs
		SET current_node = $2, attempts = $3, step_count = $4, updated_at = NOW()
		WHERE id = $1`,
		state.RunID, currentNode, step.Attempt, sequence,
	); err != nil {
		return fmt.Errorf("update agent run progress: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit agent step record: %w", err)
	}
	return nil
}

func (r *PostgresRunRecorder) FinishRun(ctx context.Context, state *State, status string, errorType string) error {
	if r == nil || r.db == nil || r.db.Pool == nil {
		return fmt.Errorf("agent run recorder database is not configured")
	}
	currentNode := state.CurrentNode
	if status == RunCompleted {
		currentNode = NodeEnd
	} else if currentNode == "" {
		currentNode = state.NextNode
	}
	var storedErrorType interface{}
	if errorType != "" {
		storedErrorType = errorType
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin finish agent run record: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := validateJobLease(ctx, tx, state); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE agent_runs
		SET current_node = $2, status = $3, attempts = $4, step_count = $5,
			error_type = $6, input_tokens = $7, output_tokens = $8, llm_calls = $9,
			estimated_cost_yuan = $10, prompt_version = $11,
			finished_at = NOW(), updated_at = NOW()
		WHERE id = $1`,
		state.RunID, currentNode, status, state.Attempts, state.StepSequence, storedErrorType,
		state.InputTokens, state.OutputTokens, state.LLMCalls, state.EstimatedCostYuan, state.PromptVersion,
	)
	if err != nil {
		return fmt.Errorf("finish agent run record: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit agent run finish: %w", err)
	}
	return nil
}

func insertRunArtifact(ctx context.Context, tx pgx.Tx, runID uuid.UUID, sequence int, kind string, value interface{}) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal agent run artifact %s: %w", kind, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_run_artifacts (id, run_id, step_sequence, kind, payload)
		VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), runID, sequence, kind, payload,
	); err != nil {
		return fmt.Errorf("insert agent run artifact %s: %w", kind, err)
	}
	return nil
}

func validateJobLease(ctx context.Context, tx pgx.Tx, state *State) error {
	if state == nil || state.JobID == uuid.Nil {
		return nil
	}
	var valid bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM agent_jobs
			WHERE id = $1 AND run_id = $2 AND status = 'running'
				AND lease_owner = $3 AND attempt = $4 AND lease_expires_at > NOW()
		)`, state.JobID, state.RunID, state.WorkerID, state.JobAttempt,
	).Scan(&valid); err != nil {
		return fmt.Errorf("validate agent job lease: %w", err)
	}
	if !valid {
		return ErrJobLeaseLost
	}
	return nil
}

func nullableUUID(id uuid.UUID) interface{} {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func nullableNodeID(id NodeID) interface{} {
	if id == "" {
		return nil
	}
	return id
}
