package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type OperationsOverview struct {
	WindowHours       int     `json:"window_hours"`
	TotalRuns         int64   `json:"total_runs"`
	CompletedRuns     int64   `json:"completed_runs"`
	FailedRuns        int64   `json:"failed_runs"`
	SuccessRate       float64 `json:"success_rate"`
	P95DurationMS     float64 `json:"p95_duration_ms"`
	AverageQueueMS    float64 `json:"average_queue_ms"`
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	LLMCalls          int64   `json:"llm_calls"`
	EstimatedCostYuan float64 `json:"estimated_cost_yuan"`
	QueuedJobs        int64   `json:"queued_jobs"`
	RunningJobs       int64   `json:"running_jobs"`
	RetryWaitJobs     int64   `json:"retry_wait_jobs"`
}

type RecentRunFailure struct {
	RunID        uuid.UUID `json:"run_id"`
	ErrorType    string    `json:"error_type"`
	GraphVersion string    `json:"graph_version"`
	DurationMS   int64     `json:"duration_ms"`
	StartedAt    time.Time `json:"started_at"`
}

type AgentFeedback struct {
	ID             uuid.UUID  `json:"id"`
	RunID          uuid.UUID  `json:"run_id"`
	UserID         uuid.UUID  `json:"user_id"`
	MessageID      *uuid.UUID `json:"message_id,omitempty"`
	FeedbackType   string     `json:"feedback_type"`
	Comment        string     `json:"comment"`
	PromotedToEval bool       `json:"promoted_to_eval"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (p *Postgres) GetOperationsOverview(ctx context.Context, hours int) (*OperationsOverview, error) {
	if hours < 1 || hours > 24*90 {
		hours = 24
	}
	overview := &OperationsOverview{WindowHours: hours}
	err := p.Pool.QueryRow(ctx, `
		WITH run_stats AS (
			SELECT COUNT(*) FILTER (WHERE status IN ('completed','failed','cancelled','interrupted')) AS total,
				COUNT(*) FILTER (WHERE status = 'completed') AS completed,
				COUNT(*) FILTER (WHERE status IN ('failed','interrupted')) AS failed,
				COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (finished_at - started_at)) * 1000)
					FILTER (WHERE finished_at IS NOT NULL), 0)::FLOAT8 AS p95_ms,
				COALESCE(AVG(queue_wait_ms), 0)::FLOAT8 AS avg_queue_ms,
				COALESCE(SUM(input_tokens), 0)::BIGINT AS input_tokens,
				COALESCE(SUM(output_tokens), 0)::BIGINT AS output_tokens,
				COALESCE(SUM(llm_calls), 0)::BIGINT AS llm_calls,
				COALESCE(SUM(estimated_cost_yuan), 0)::FLOAT8 AS cost
			FROM agent_runs WHERE run_source = 'interactive' AND started_at >= NOW() - ($1::integer * INTERVAL '1 hour')
		), job_stats AS (
			SELECT COUNT(*) FILTER (WHERE status = 'queued') AS queued,
				COUNT(*) FILTER (WHERE status = 'running') AS running,
				COUNT(*) FILTER (WHERE status = 'retry_wait') AS retry_wait
			FROM agent_jobs
		)
		SELECT total, completed, failed,
			CASE WHEN total = 0 THEN 0 ELSE completed::FLOAT8 / total END,
			p95_ms, avg_queue_ms, input_tokens, output_tokens, llm_calls, cost,
			queued, running, retry_wait
		FROM run_stats CROSS JOIN job_stats`, hours,
	).Scan(
		&overview.TotalRuns, &overview.CompletedRuns, &overview.FailedRuns, &overview.SuccessRate,
		&overview.P95DurationMS, &overview.AverageQueueMS, &overview.InputTokens,
		&overview.OutputTokens, &overview.LLMCalls, &overview.EstimatedCostYuan,
		&overview.QueuedJobs, &overview.RunningJobs, &overview.RetryWaitJobs,
	)
	return overview, err
}

func (p *Postgres) ListRecentRunFailures(ctx context.Context, limit int) ([]RecentRunFailure, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := p.Pool.Query(ctx, `
		SELECT id, COALESCE(error_type, 'unknown'), graph_version,
			COALESCE((EXTRACT(EPOCH FROM (COALESCE(finished_at, NOW()) - started_at)) * 1000)::BIGINT, 0), started_at
		FROM agent_runs WHERE run_source = 'interactive' AND status IN ('failed','interrupted')
		ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RecentRunFailure, 0, limit)
	for rows.Next() {
		var item RecentRunFailure
		if err := rows.Scan(&item.RunID, &item.ErrorType, &item.GraphVersion, &item.DurationMS, &item.StartedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (p *Postgres) UpsertAgentFeedback(ctx context.Context, feedback *AgentFeedback) error {
	if feedback.ID == uuid.Nil {
		feedback.ID = uuid.New()
	}
	return p.Pool.QueryRow(ctx, `
		INSERT INTO agent_feedback (id, run_id, user_id, message_id, feedback_type, comment)
		SELECT $1, r.id, $3, $4, $5, $6 FROM agent_runs r
		WHERE r.id = $2 AND r.user_id = $3
			AND ($4::uuid IS NULL OR EXISTS (SELECT 1 FROM messages m WHERE m.id = $4 AND m.agent_run_id = r.id))
		ON CONFLICT (run_id, user_id) DO UPDATE SET
			message_id = EXCLUDED.message_id, feedback_type = EXCLUDED.feedback_type,
			comment = EXCLUDED.comment, updated_at = NOW()
		RETURNING id, created_at`, feedback.ID, feedback.RunID, feedback.UserID,
		feedback.MessageID, feedback.FeedbackType, feedback.Comment,
	).Scan(&feedback.ID, &feedback.CreatedAt)
}

func (p *Postgres) ListAgentFeedback(ctx context.Context, limit int) ([]AgentFeedback, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := p.Pool.Query(ctx, `
		SELECT id, run_id, user_id, message_id, feedback_type, comment, promoted_to_eval, created_at
		FROM agent_feedback ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AgentFeedback, 0, limit)
	for rows.Next() {
		var item AgentFeedback
		if err := rows.Scan(&item.ID, &item.RunID, &item.UserID, &item.MessageID, &item.FeedbackType, &item.Comment, &item.PromotedToEval, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) PromoteFeedbackToEvaluation(ctx context.Context, feedbackID uuid.UUID) (uuid.UUID, error) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	var evalID uuid.UUID
	emptyTables, _ := json.Marshal([]string{})
	err = tx.QueryRow(ctx, `
		INSERT INTO evaluation_cases (id, source_feedback_id, domain, question, expected_tables)
		SELECT $2, f.id, r.domain,
			COALESCE((SELECT state_data->>'query' FROM agent_checkpoints WHERE run_id = r.id ORDER BY sequence ASC LIMIT 1), ''),
			$3::jsonb
		FROM agent_feedback f JOIN agent_runs r ON r.id = f.run_id
		WHERE f.id = $1
		ON CONFLICT (source_feedback_id) DO UPDATE SET source_feedback_id = EXCLUDED.source_feedback_id
		RETURNING id`, feedbackID, uuid.New(), emptyTables,
	).Scan(&evalID)
	if err == pgx.ErrNoRows {
		return uuid.Nil, fmt.Errorf("feedback not found")
	}
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_feedback SET promoted_to_eval = TRUE, updated_at = NOW() WHERE id = $1`, feedbackID); err != nil {
		return uuid.Nil, err
	}
	return evalID, tx.Commit(ctx)
}
