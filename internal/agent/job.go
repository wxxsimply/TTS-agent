package agent

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	RunQueued    = "queued"
	RunRetryWait = "retry_wait"
	RunCancelled = "cancelled"

	JobQueued    = "queued"
	JobRunning   = "running"
	JobRetryWait = "retry_wait"
	JobCompleted = "completed"
	JobFailed    = "failed"
	JobCancelled = "cancelled"
)

var (
	ErrNoJobAvailable = errors.New("no agent job is available")
	ErrJobNotFound    = errors.New("agent job not found")
	ErrJobActive      = errors.New("agent job is active")
	ErrJobTerminal    = errors.New("agent job is already terminal")
	ErrJobCancelled   = errors.New("agent job cancellation requested")
	ErrJobLeaseLost   = errors.New("agent job lease was lost")
	ErrJobQueueFull   = errors.New("agent job queue is full")
	ErrUserJobLimit   = errors.New("user active job limit reached")
)

type EnqueueOptions struct {
	IdempotencyKey string
	Priority       int
	MaxAttempts    int
	QueueLimit     int
	UserLimit      int
}

type EnqueueResult struct {
	JobID        uuid.UUID `json:"job_id"`
	RunID        uuid.UUID `json:"run_id"`
	Status       string    `json:"status"`
	Created      bool      `json:"created"`
	QueuedAt     time.Time `json:"queued_at"`
	Deduplicated bool      `json:"deduplicated"`
}

type AgentJob struct {
	ID              uuid.UUID  `json:"id"`
	RunID           uuid.UUID  `json:"run_id"`
	UserID          uuid.UUID  `json:"-"`
	Status          string     `json:"status"`
	Priority        int        `json:"priority"`
	Attempt         int        `json:"attempt"`
	MaxAttempts     int        `json:"max_attempts"`
	ScheduledAt     time.Time  `json:"scheduled_at"`
	LeaseOwner      *string    `json:"-"`
	LeaseExpiresAt  *time.Time `json:"lease_expires_at,omitempty"`
	HeartbeatAt     *time.Time `json:"heartbeat_at,omitempty"`
	CancelRequested bool       `json:"cancel_requested"`
	LastErrorType   *string    `json:"last_error_type,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type JobStats struct {
	Queued             int     `json:"queued"`
	Running            int     `json:"running"`
	RetryWait          int     `json:"retry_wait"`
	Completed          int     `json:"completed"`
	Failed             int     `json:"failed"`
	Cancelled          int     `json:"cancelled"`
	AverageQueueMS     float64 `json:"average_queue_ms"`
	AverageExecutionMS float64 `json:"average_execution_ms"`
	SuccessRate        float64 `json:"success_rate"`
}

type JobOutcome struct {
	Completed bool
	ErrorType string
}

type JobStore interface {
	Enqueue(ctx context.Context, state *State, options EnqueueOptions) (*EnqueueResult, error)
	ClaimNext(ctx context.Context, workerID string, lease time.Duration, userConcurrency int) (*AgentJob, error)
	LoadClaimedSnapshot(ctx context.Context, job *AgentJob, workerID string) (*ResumeSnapshot, error)
	Heartbeat(ctx context.Context, jobID uuid.UUID, workerID string, lease time.Duration) (bool, error)
	FinishJob(ctx context.Context, job *AgentJob, workerID string, outcome JobOutcome, backoffBase time.Duration) (string, error)
	Cancel(ctx context.Context, runID, userID uuid.UUID) (string, error)
	Retry(ctx context.Context, runID, userID uuid.UUID) (string, error)
	GetJob(ctx context.Context, runID, userID uuid.UUID) (*AgentJob, error)
	ReapExpired(ctx context.Context) (int64, error)
	SaveResultMessage(ctx context.Context, result *Result) error
}

func retryableJobError(errorType string) bool {
	switch errorType {
	case "runtime_error", "runtime_interrupted", "timeout", "database_error", "llm_rate_limit", "llm_circuit_open", "llm_upstream":
		return true
	default:
		return false
	}
}
