package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *PostgresRunRecorder) AcquireResume(ctx context.Context, runID, userID uuid.UUID) (*ResumeSnapshot, error) {
	if r == nil || r.db == nil || r.db.Pool == nil {
		return nil, fmt.Errorf("agent run recorder database is not configured")
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin resume acquisition: %w", err)
	}
	defer tx.Rollback(ctx)

	var status, graphVersion string
	var errorType *string
	var updatedAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT status, error_type, graph_version, updated_at
		FROM agent_runs
		WHERE id = $1 AND user_id = $2
		FOR UPDATE`, runID, userID,
	).Scan(&status, &errorType, &graphVersion, &updatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load agent run for resume: %w", err)
	}
	if graphVersion != CurrentGraphVersion {
		return nil, ErrGraphVersionUnsupported
	}
	if status == RunCompleted {
		return nil, ErrRunCompleted
	}
	if status == RunRunning && updatedAt.After(time.Now().Add(-ResumeStaleAfter)) {
		return nil, ErrRunActive
	}
	if status == RunFailed && !recoverableErrorType(errorType) {
		return nil, ErrRunNotRecoverable
	}
	if status != RunInterrupted && status != RunFailed && status != RunRunning {
		return nil, ErrRunNotRecoverable
	}

	var checkpointJSON []byte
	err = tx.QueryRow(ctx, `
		SELECT state_data
		FROM agent_checkpoints
		WHERE run_id = $1
		ORDER BY sequence DESC
		LIMIT 1`, runID,
	).Scan(&checkpointJSON)
	if err == pgx.ErrNoRows {
		return nil, ErrCheckpointNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load latest agent checkpoint: %w", err)
	}
	var checkpoint StateCheckpoint
	if err := json.Unmarshal(checkpointJSON, &checkpoint); err != nil {
		return nil, fmt.Errorf("decode latest agent checkpoint: %w", err)
	}
	if checkpoint.GraphVersion != CurrentGraphVersion {
		return nil, ErrGraphVersionUnsupported
	}
	if checkpoint.NextNode == "" {
		return nil, ErrRunNotRecoverable
	}

	snapshot := &ResumeSnapshot{Checkpoint: checkpoint}
	if err := loadLatestArtifact(ctx, tx, runID, ArtifactRows, checkpoint.Sequence, &snapshot.Rows); err != nil {
		return nil, err
	}
	if err := loadLatestArtifact(ctx, tx, runID, ArtifactReport, checkpoint.Sequence, &snapshot.Report); err != nil {
		return nil, err
	}
	if err := loadLatestArtifact(ctx, tx, runID, ArtifactChart, checkpoint.Sequence, &snapshot.ChartConfig); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = 'running', current_node = $3, error_type = NULL,
			finished_at = NULL, resume_count = resume_count + 1,
			resumed_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND user_id = $2`,
		runID, userID, checkpoint.NextNode,
	); err != nil {
		return nil, fmt.Errorf("acquire agent run for resume: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit resume acquisition: %w", err)
	}
	return snapshot, nil
}

func (r *PostgresRunRecorder) GetRun(ctx context.Context, runID, userID uuid.UUID) (*RunRecord, error) {
	if r == nil || r.db == nil || r.db.Pool == nil {
		return nil, fmt.Errorf("agent run recorder database is not configured")
	}
	record := &RunRecord{}
	err := r.db.Pool.QueryRow(ctx, `
		SELECT r.id, r.graph_version, r.domain, r.start_node, r.current_node,
			CASE WHEN r.status = 'running' AND r.updated_at < $3
				THEN 'interrupted' ELSE r.status END,
			r.attempts, r.step_count, r.resume_count,
			CASE WHEN r.status = 'running' AND r.updated_at < $3
				THEN COALESCE(r.error_type, 'runtime_interrupted') ELSE r.error_type END,
			r.started_at, r.finished_at, r.updated_at,
			r.input_tokens, r.output_tokens, r.llm_calls, r.estimated_cost_yuan,
			r.prompt_version, r.queue_wait_ms, r.run_source,
			COALESCE((SELECT MAX(c.sequence) FROM agent_checkpoints c WHERE c.run_id = r.id), 0)
		FROM agent_runs r
		WHERE r.id = $1 AND r.user_id = $2`, runID, userID, time.Now().Add(-ResumeStaleAfter),
	).Scan(
		&record.ID, &record.GraphVersion, &record.Domain, &record.StartNode, &record.CurrentNode,
		&record.Status, &record.Attempts, &record.StepCount, &record.ResumeCount, &record.ErrorType,
		&record.StartedAt, &record.FinishedAt, &record.UpdatedAt,
		&record.InputTokens, &record.OutputTokens, &record.LLMCalls, &record.EstimatedCostYuan,
		&record.PromptVersion, &record.QueueWaitMS, &record.RunSource, &record.LatestSequence,
	)
	if err == pgx.ErrNoRows {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get agent run: %w", err)
	}
	return record, nil
}

func (r *PostgresRunRecorder) MarkStaleRunsInterrupted(ctx context.Context) (int64, error) {
	if r == nil || r.db == nil || r.db.Pool == nil {
		return 0, fmt.Errorf("agent run recorder database is not configured")
	}
	command, err := r.db.Pool.Exec(ctx, `
		UPDATE agent_runs
		SET status = 'interrupted', error_type = 'runtime_interrupted',
			finished_at = NOW(), updated_at = NOW()
		WHERE status = 'running' AND updated_at < $1`, time.Now().Add(-ResumeStaleAfter))
	if err != nil {
		return 0, fmt.Errorf("mark stale agent runs interrupted: %w", err)
	}
	return command.RowsAffected(), nil
}

func (r *PostgresRunRecorder) ListSteps(ctx context.Context, runID, userID uuid.UUID) ([]PersistedStep, error) {
	if r == nil || r.db == nil || r.db.Pool == nil {
		return nil, fmt.Errorf("agent run recorder database is not configured")
	}
	var exists bool
	if err := r.db.Pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM agent_runs WHERE id = $1 AND user_id = $2)`, runID, userID,
	).Scan(&exists); err != nil {
		return nil, fmt.Errorf("authorize agent run steps: %w", err)
	}
	if !exists {
		return nil, ErrRunNotFound
	}
	rows, err := r.db.Pool.Query(ctx, `
		SELECT sequence, node, COALESCE(next_node, ''), status, attempt,
			duration_ms, error_type, created_at
		FROM agent_steps
		WHERE run_id = $1
		ORDER BY sequence`, runID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent run steps: %w", err)
	}
	defer rows.Close()
	steps := make([]PersistedStep, 0)
	for rows.Next() {
		var step PersistedStep
		if err := rows.Scan(
			&step.Sequence, &step.Node, &step.Next, &step.Status,
			&step.Attempt, &step.DurationMS, &step.ErrorType, &step.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent run step: %w", err)
		}
		steps = append(steps, step)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent run steps: %w", err)
	}
	return steps, nil
}

func (r *PostgresRunRecorder) GetResult(ctx context.Context, runID, userID uuid.UUID) (*RunOutput, error) {
	if r == nil || r.db == nil || r.db.Pool == nil {
		return nil, fmt.Errorf("agent run recorder database is not configured")
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin get agent result: %w", err)
	}
	defer tx.Rollback(ctx)
	output := &RunOutput{RunID: runID}
	err = tx.QueryRow(ctx, `
		SELECT status, error_type FROM agent_runs WHERE id = $1 AND user_id = $2`, runID, userID,
	).Scan(&output.Status, &output.ErrorType)
	if err == pgx.ErrNoRows {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authorize agent result: %w", err)
	}
	if output.Status == RunQueued || output.Status == RunRunning || output.Status == RunRetryWait {
		return nil, ErrRunActive
	}
	var checkpointJSON []byte
	if err := tx.QueryRow(ctx, `
		SELECT state_data FROM agent_checkpoints WHERE run_id = $1 ORDER BY sequence DESC LIMIT 1`, runID,
	).Scan(&checkpointJSON); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrCheckpointNotFound
		}
		return nil, fmt.Errorf("load agent result checkpoint: %w", err)
	}
	var checkpoint StateCheckpoint
	if err := json.Unmarshal(checkpointJSON, &checkpoint); err != nil {
		return nil, fmt.Errorf("decode agent result checkpoint: %w", err)
	}
	output.SQL = checkpoint.SQL
	if err := loadLatestArtifact(ctx, tx, runID, ArtifactRows, checkpoint.Sequence, &output.Rows); err != nil {
		return nil, err
	}
	if err := loadLatestArtifact(ctx, tx, runID, ArtifactReport, checkpoint.Sequence, &output.Report); err != nil {
		return nil, err
	}
	if err := loadLatestArtifact(ctx, tx, runID, ArtifactChart, checkpoint.Sequence, &output.ChartConfig); err != nil {
		return nil, err
	}
	output.ResultCount = len(output.Rows)
	output.Summary = output.Report.Conclusion
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit get agent result: %w", err)
	}
	return output, nil
}

func recoverableErrorType(errorType *string) bool {
	if errorType == nil {
		return false
	}
	switch *errorType {
	case "runtime_error", "runtime_interrupted", "timeout", "database_error":
		return true
	default:
		return false
	}
}

func loadLatestArtifact(ctx context.Context, tx pgx.Tx, runID uuid.UUID, kind string, maxSequence int, target interface{}) error {
	var payload []byte
	err := tx.QueryRow(ctx, `
		SELECT payload
		FROM agent_run_artifacts
		WHERE run_id = $1 AND kind = $2 AND step_sequence <= $3
		ORDER BY step_sequence DESC
		LIMIT 1`, runID, kind, maxSequence,
	).Scan(&payload)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load agent run artifact %s: %w", kind, err)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("decode agent run artifact %s: %w", kind, err)
	}
	return nil
}
