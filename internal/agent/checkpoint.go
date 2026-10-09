package agent

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

const ResumeStaleAfter = 5 * time.Minute

const (
	RunInterrupted = "interrupted"
	ArtifactRows   = "query_rows"
	ArtifactReport = "report"
	ArtifactChart  = "chart_config"
)

var (
	ErrRunNotFound             = errors.New("agent run not found")
	ErrRunCompleted            = errors.New("agent run is already completed")
	ErrRunActive               = errors.New("agent run is still active")
	ErrRunNotRecoverable       = errors.New("agent run is not recoverable")
	ErrCheckpointNotFound      = errors.New("agent checkpoint not found")
	ErrGraphVersionUnsupported = errors.New("agent graph version is not compatible")
)

// StateCheckpoint intentionally excludes result rows, reports, prompts,
// model responses, credentials, and execution traces. Larger outputs are
// stored as separately authorized run artifacts.
type StateCheckpoint struct {
	RunID             uuid.UUID `json:"run_id"`
	GraphVersion      string    `json:"graph_version"`
	UserID            uuid.UUID `json:"user_id,omitempty"`
	ConversationID    uuid.UUID `json:"conversation_id,omitempty"`
	DatasetID         uuid.UUID `json:"dataset_id,omitempty"`
	Domain            string    `json:"domain"`
	Query             string    `json:"query"`
	SQL               string    `json:"sql,omitempty"`
	Errors            []string  `json:"errors,omitempty"`
	Status            string    `json:"status,omitempty"`
	Attempts          int       `json:"attempts"`
	SeenSQL           []string  `json:"seen_sql,omitempty"`
	DatasetTimeHint   string    `json:"dataset_time_hint,omitempty"`
	DatasetTable      string    `json:"dataset_table,omitempty"`
	CurrentNode       NodeID    `json:"current_node,omitempty"`
	NextNode          NodeID    `json:"next_node"`
	Sequence          int       `json:"sequence"`
	InputTokens       int64     `json:"input_tokens,omitempty"`
	OutputTokens      int64     `json:"output_tokens,omitempty"`
	LLMCalls          int       `json:"llm_calls,omitempty"`
	EstimatedCostYuan float64   `json:"estimated_cost_yuan,omitempty"`
	PromptVersion     string    `json:"prompt_version,omitempty"`
	RunSource         string    `json:"run_source,omitempty"`
}

type ResumeSnapshot struct {
	Checkpoint  StateCheckpoint
	Rows        []map[string]interface{}
	Report      Report
	ChartConfig map[string]interface{}
}

type RunRecord struct {
	ID                uuid.UUID  `json:"id"`
	GraphVersion      string     `json:"graph_version"`
	Domain            string     `json:"domain"`
	StartNode         NodeID     `json:"start_node"`
	CurrentNode       NodeID     `json:"current_node"`
	Status            string     `json:"status"`
	Attempts          int        `json:"attempts"`
	StepCount         int        `json:"step_count"`
	ResumeCount       int        `json:"resume_count"`
	ErrorType         *string    `json:"error_type,omitempty"`
	StartedAt         time.Time  `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
	LatestSequence    int        `json:"latest_checkpoint_sequence"`
	Job               *AgentJob  `json:"job,omitempty"`
	InputTokens       int64      `json:"input_tokens"`
	OutputTokens      int64      `json:"output_tokens"`
	LLMCalls          int        `json:"llm_calls"`
	EstimatedCostYuan float64    `json:"estimated_cost_yuan"`
	PromptVersion     string     `json:"prompt_version"`
	QueueWaitMS       int64      `json:"queue_wait_ms"`
	RunSource         string     `json:"run_source"`
}

type RunOutput struct {
	RunID       uuid.UUID                `json:"run_id"`
	Status      string                   `json:"status"`
	SQL         string                   `json:"sql,omitempty"`
	Summary     string                   `json:"summary,omitempty"`
	Rows        []map[string]interface{} `json:"result_rows,omitempty"`
	ResultCount int                      `json:"result_count"`
	Report      Report                   `json:"report"`
	ChartConfig map[string]interface{}   `json:"chart_config,omitempty"`
	ErrorType   *string                  `json:"error_type,omitempty"`
}

type PersistedStep struct {
	Sequence   int        `json:"sequence"`
	Node       NodeID     `json:"node"`
	Next       NodeID     `json:"next,omitempty"`
	Status     StepStatus `json:"status"`
	Attempt    int        `json:"attempt"`
	DurationMS int64      `json:"duration_ms"`
	ErrorType  *string    `json:"error_type,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type RunCheckpointStore interface {
	AcquireResume(ctx context.Context, runID, userID uuid.UUID) (*ResumeSnapshot, error)
	GetRun(ctx context.Context, runID, userID uuid.UUID) (*RunRecord, error)
	ListSteps(ctx context.Context, runID, userID uuid.UUID) ([]PersistedStep, error)
}

func checkpointFromState(state *State, current, next NodeID, sequence int) StateCheckpoint {
	seen := make([]string, 0, len(state.SeenSQL))
	for fingerprint := range state.SeenSQL {
		seen = append(seen, fingerprint)
	}
	return StateCheckpoint{
		RunID: state.RunID, GraphVersion: state.GraphVersion, UserID: state.UserID,
		ConversationID: state.ConversationID, DatasetID: state.DatasetID,
		Domain: state.Domain, Query: state.Query, SQL: state.SQL,
		Errors: append([]string(nil), state.Errors...), Status: state.Status,
		Attempts: state.Attempts, SeenSQL: seen, DatasetTimeHint: state.DatasetTimeHint,
		DatasetTable: state.DatasetTable, CurrentNode: current, NextNode: next,
		Sequence:    sequence,
		InputTokens: state.InputTokens, OutputTokens: state.OutputTokens,
		LLMCalls: state.LLMCalls, EstimatedCostYuan: state.EstimatedCostYuan,
		PromptVersion: state.PromptVersion,
		RunSource:     state.RunSource,
	}
}

func (checkpoint StateCheckpoint) Restore() *State {
	seen := make(map[string]struct{}, len(checkpoint.SeenSQL))
	for _, fingerprint := range checkpoint.SeenSQL {
		seen[fingerprint] = struct{}{}
	}
	return &State{
		RunID: checkpoint.RunID, GraphVersion: checkpoint.GraphVersion,
		UserID: checkpoint.UserID, ConversationID: checkpoint.ConversationID,
		DatasetID: checkpoint.DatasetID, Domain: checkpoint.Domain,
		Query: checkpoint.Query, SQL: checkpoint.SQL,
		Errors: append([]string(nil), checkpoint.Errors...), Status: checkpoint.Status,
		Attempts: checkpoint.Attempts, SeenSQL: seen,
		DatasetTimeHint: checkpoint.DatasetTimeHint, DatasetTable: checkpoint.DatasetTable,
		CurrentNode: checkpoint.CurrentNode, NextNode: checkpoint.NextNode,
		StepSequence: checkpoint.Sequence,
		InputTokens:  checkpoint.InputTokens, OutputTokens: checkpoint.OutputTokens,
		LLMCalls: checkpoint.LLMCalls, EstimatedCostYuan: checkpoint.EstimatedCostYuan,
		PromptVersion: checkpoint.PromptVersion,
		RunSource:     checkpoint.RunSource,
	}
}
