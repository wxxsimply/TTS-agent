package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tts-agent/internal/store"
)

type PostgresJobStore struct {
	db *store.Postgres
}

func NewPostgresJobStore(db *store.Postgres) *PostgresJobStore {
	return &PostgresJobStore{db: db}
}

func (s *PostgresJobStore) Enqueue(ctx context.Context, state *State, options EnqueueOptions) (*EnqueueResult, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return nil, fmt.Errorf("agent job database is not configured")
	}
	if options.MaxAttempts < 1 {
		options.MaxAttempts = 1
	}
	if options.Priority < -10 {
		options.Priority = -10
	}
	if options.Priority > 10 {
		options.Priority = 10
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin enqueue agent job: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('agent-job-capacity'))`); err != nil {
		return nil, fmt.Errorf("lock agent job capacity: %w", err)
	}

	if options.IdempotencyKey != "" {
		lockKey := state.UserID.String() + ":" + options.IdempotencyKey
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
			return nil, fmt.Errorf("lock agent job idempotency key: %w", err)
		}
		var existing EnqueueResult
		err := tx.QueryRow(ctx, `
			SELECT id, run_id, status, created_at
			FROM agent_jobs
			WHERE user_id = $1 AND idempotency_key = $2`, state.UserID, options.IdempotencyKey,
		).Scan(&existing.JobID, &existing.RunID, &existing.Status, &existing.QueuedAt)
		if err == nil {
			existing.Deduplicated = true
			return &existing, tx.Commit(ctx)
		}
		if err != pgx.ErrNoRows {
			return nil, fmt.Errorf("find idempotent agent job: %w", err)
		}
	}
	if options.QueueLimit > 0 {
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM agent_jobs
			WHERE status IN ('queued', 'running', 'retry_wait')`).Scan(&count); err != nil {
			return nil, fmt.Errorf("count active agent jobs: %w", err)
		}
		if count >= options.QueueLimit {
			return nil, ErrJobQueueFull
		}
	}
	if options.UserLimit > 0 {
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM agent_jobs
			WHERE user_id = $1 AND status IN ('queued', 'running', 'retry_wait')`, state.UserID,
		).Scan(&count); err != nil {
			return nil, fmt.Errorf("count user active agent jobs: %w", err)
		}
		if count >= options.UserLimit {
			return nil, ErrUserJobLimit
		}
	}

	if state.RunID == uuid.Nil {
		state.RunID = uuid.New()
	}
	if state.GraphVersion == "" {
		state.GraphVersion = CurrentGraphVersion
	}
	if state.RunSource == "" {
		state.RunSource = "interactive"
	}
	state.NextNode = NodeGenerateSQL
	checkpoint := checkpointFromState(state, "", NodeGenerateSQL, 0)
	checkpointJSON, err := json.Marshal(checkpoint)
	if err != nil {
		return nil, fmt.Errorf("marshal queued agent checkpoint: %w", err)
	}
	now := time.Now().UTC()
	jobID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_runs (
			id, user_id, conversation_id, dataset_id, domain, graph_version,
			start_node, current_node, status, attempts, step_count, started_at, updated_at, run_source
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$7,'queued',0,0,$8,$8,$9)`,
		state.RunID, state.UserID, nullableUUID(state.ConversationID), nullableUUID(state.DatasetID),
		state.Domain, state.GraphVersion, NodeGenerateSQL, now, state.RunSource,
	); err != nil {
		return nil, fmt.Errorf("insert queued agent run: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_checkpoints (
			id, run_id, sequence, current_node, next_node, graph_version, state_data, created_at
		) VALUES ($1,$2,0,NULL,$3,$4,$5,$6)`,
		uuid.New(), state.RunID, NodeGenerateSQL, state.GraphVersion, checkpointJSON, now,
	); err != nil {
		return nil, fmt.Errorf("insert queued agent checkpoint: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_jobs (
			id, run_id, user_id, status, priority, max_attempts,
			scheduled_at, idempotency_key, created_at, updated_at
		) VALUES ($1,$2,$3,'queued',$4,$5,NOW(),$6,NOW(),NOW())`,
		jobID, state.RunID, state.UserID, options.Priority, options.MaxAttempts,
		nullableString(options.IdempotencyKey),
	); err != nil {
		return nil, fmt.Errorf("insert agent job: %w", err)
	}
	if state.ConversationID != uuid.Nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO messages (id, conversation_id, role, content, created_at)
			VALUES ($1, $2, 'user', $3, $4)`,
			uuid.New(), state.ConversationID, state.Query, now,
		); err != nil {
			return nil, fmt.Errorf("insert queued user message: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit agent job enqueue: %w", err)
	}
	return &EnqueueResult{JobID: jobID, RunID: state.RunID, Status: JobQueued, Created: true, QueuedAt: now}, nil
}

func (s *PostgresJobStore) ClaimNext(ctx context.Context, workerID string, lease time.Duration, userConcurrency int) (*AgentJob, error) {
	if s == nil || s.db == nil || s.db.Pool == nil {
		return nil, fmt.Errorf("agent job database is not configured")
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin claim agent job: %w", err)
	}
	defer tx.Rollback(ctx)
	job := &AgentJob{}
	if userConcurrency < 1 {
		userConcurrency = 1
	}
	var previousStatus string
	err = tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id, status AS previous_status
			FROM agent_jobs
			WHERE cancel_requested = FALSE AND attempt < max_attempts AND (
				(status IN ('queued', 'retry_wait') AND scheduled_at <= NOW()) OR
				(status = 'running' AND lease_expires_at < NOW())
			)
			AND (
				SELECT COUNT(*) FROM agent_jobs active
				WHERE active.user_id = agent_jobs.user_id AND active.status = 'running'
					AND active.lease_expires_at > NOW()
			) < $3
			ORDER BY priority DESC, scheduled_at, created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE agent_jobs j
		SET status = 'running', attempt = j.attempt + 1, lease_owner = $1,
			lease_expires_at = NOW() + ($2 * INTERVAL '1 second'),
			heartbeat_at = NOW(), started_at = COALESCE(j.started_at, NOW()),
			finished_at = NULL, updated_at = NOW()
		FROM candidate c
		WHERE j.id = c.id
		RETURNING j.id, j.run_id, j.user_id, j.status, j.priority,
			j.attempt, j.max_attempts, j.scheduled_at, j.lease_owner,
			j.lease_expires_at, j.heartbeat_at, j.cancel_requested,
			j.last_error_type, j.started_at, j.finished_at,
			j.created_at, j.updated_at, c.previous_status`,
		workerID, int64(lease/time.Second), userConcurrency,
	).Scan(
		&job.ID, &job.RunID, &job.UserID, &job.Status, &job.Priority,
		&job.Attempt, &job.MaxAttempts, &job.ScheduledAt, &job.LeaseOwner,
		&job.LeaseExpiresAt, &job.HeartbeatAt, &job.CancelRequested,
		&job.LastErrorType, &job.StartedAt, &job.FinishedAt,
		&job.CreatedAt, &job.UpdatedAt, &previousStatus,
	)
	if err == pgx.ErrNoRows {
		return nil, ErrNoJobAvailable
	}
	if err != nil {
		return nil, fmt.Errorf("claim agent job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = 'running', current_node = COALESCE((
			SELECT next_node FROM agent_checkpoints WHERE run_id = $1 ORDER BY sequence DESC LIMIT 1
		), current_node), error_type = NULL, finished_at = NULL,
			resume_count = resume_count + $2, resumed_at = CASE WHEN $2 = 1 THEN NOW() ELSE resumed_at END,
			queue_wait_ms = GREATEST(0, (EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000)::BIGINT),
			updated_at = NOW()
		WHERE id = $1`, job.RunID, boolInt(previousStatus != JobQueued),
	); err != nil {
		return nil, fmt.Errorf("mark claimed agent run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit agent job claim: %w", err)
	}
	return job, nil
}

func (s *PostgresJobStore) LoadClaimedSnapshot(ctx context.Context, job *AgentJob, workerID string) (*ResumeSnapshot, error) {
	if job == nil {
		return nil, ErrJobNotFound
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin load claimed job: %w", err)
	}
	defer tx.Rollback(ctx)
	var cancelRequested bool
	err = tx.QueryRow(ctx, `
		SELECT cancel_requested
		FROM agent_jobs
		WHERE id = $1 AND run_id = $2 AND user_id = $3 AND status = 'running'
			AND lease_owner = $4 AND lease_expires_at > NOW()`,
		job.ID, job.RunID, job.UserID, workerID,
	).Scan(&cancelRequested)
	if err == pgx.ErrNoRows {
		return nil, ErrJobLeaseLost
	}
	if err != nil {
		return nil, fmt.Errorf("validate claimed agent job: %w", err)
	}
	if cancelRequested {
		return nil, ErrJobCancelled
	}
	snapshot, err := loadResumeSnapshot(ctx, tx, job.RunID)
	if err != nil {
		return nil, err
	}
	if snapshot.Checkpoint.GraphVersion != CurrentGraphVersion {
		return nil, ErrGraphVersionUnsupported
	}
	if job.Attempt > 1 && job.LastErrorType != nil && retryableJobError(*job.LastErrorType) && snapshot.Checkpoint.NextNode == NodeGenerateSQL {
		snapshot.Checkpoint.Attempts = 0
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit load claimed job: %w", err)
	}
	return snapshot, nil
}

func (s *PostgresJobStore) Heartbeat(ctx context.Context, jobID uuid.UUID, workerID string, lease time.Duration) (bool, error) {
	var cancelRequested bool
	err := s.db.Pool.QueryRow(ctx, `
		WITH heartbeat AS (
			UPDATE agent_jobs
			SET heartbeat_at = NOW(), lease_expires_at = NOW() + ($3 * INTERVAL '1 second'), updated_at = NOW()
			WHERE id = $1 AND status = 'running' AND lease_owner = $2
			RETURNING run_id, cancel_requested
		)
		UPDATE agent_runs r SET updated_at = NOW()
		FROM heartbeat h WHERE r.id = h.run_id
		RETURNING h.cancel_requested`, jobID, workerID, int64(lease/time.Second),
	).Scan(&cancelRequested)
	if err == pgx.ErrNoRows {
		return false, ErrJobLeaseLost
	}
	if err != nil {
		return false, fmt.Errorf("heartbeat agent job: %w", err)
	}
	return cancelRequested, nil
}

func (s *PostgresJobStore) FinishJob(ctx context.Context, job *AgentJob, workerID string, outcome JobOutcome, backoffBase time.Duration) (string, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin finish agent job: %w", err)
	}
	defer tx.Rollback(ctx)
	var cancelRequested bool
	var attempt, maxAttempts int
	err = tx.QueryRow(ctx, `
		SELECT cancel_requested, attempt, max_attempts
		FROM agent_jobs
		WHERE id = $1 AND run_id = $2 AND status = 'running' AND lease_owner = $3
		FOR UPDATE`, job.ID, job.RunID, workerID,
	).Scan(&cancelRequested, &attempt, &maxAttempts)
	if err == pgx.ErrNoRows {
		return "", ErrJobLeaseLost
	}
	if err != nil {
		return "", fmt.Errorf("lock completed agent job: %w", err)
	}
	status := JobFailed
	runStatus := RunFailed
	scheduledAt := time.Now().UTC()
	if cancelRequested {
		status, runStatus = JobCancelled, RunCancelled
	} else if outcome.Completed {
		status, runStatus = JobCompleted, RunCompleted
	} else if retryableJobError(outcome.ErrorType) && attempt < maxAttempts {
		status, runStatus = JobRetryWait, RunRetryWait
		delay := backoffBase * time.Duration(math.Pow(2, float64(max(0, attempt-1))))
		if delay > time.Minute {
			delay = time.Minute
		}
		scheduledAt = scheduledAt.Add(delay)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_jobs
		SET status = $2::varchar, scheduled_at = $3, lease_owner = NULL,
			lease_expires_at = NULL, heartbeat_at = NULL,
			last_error_type = NULLIF($4, ''),
			finished_at = CASE WHEN $2::varchar IN ('completed','failed','cancelled') THEN NOW() ELSE NULL END,
			updated_at = NOW()
		WHERE id = $1`, job.ID, status, scheduledAt, outcome.ErrorType,
	); err != nil {
		return "", fmt.Errorf("finish agent job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = $2::varchar, error_type = NULLIF($3, ''),
			finished_at = CASE WHEN $2::varchar IN ('completed','failed','cancelled') THEN NOW() ELSE NULL END,
			updated_at = NOW()
		WHERE id = $1`, job.RunID, runStatus, outcome.ErrorType,
	); err != nil {
		return "", fmt.Errorf("finish queued agent run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit agent job finish: %w", err)
	}
	return status, nil
}

func (s *PostgresJobStore) Cancel(ctx context.Context, runID, userID uuid.UUID) (string, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin cancel agent job: %w", err)
	}
	defer tx.Rollback(ctx)
	var jobID uuid.UUID
	var status string
	err = tx.QueryRow(ctx, `
		SELECT id, status FROM agent_jobs WHERE run_id = $1 AND user_id = $2 FOR UPDATE`, runID, userID,
	).Scan(&jobID, &status)
	if err == pgx.ErrNoRows {
		return "", ErrJobNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load agent job for cancellation: %w", err)
	}
	if status == JobCompleted || status == JobFailed || status == JobCancelled {
		return "", ErrJobTerminal
	}
	newStatus := status
	if status == JobQueued || status == JobRetryWait {
		newStatus = JobCancelled
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_jobs SET cancel_requested = TRUE, status = $2::varchar,
			finished_at = CASE WHEN $2::varchar = 'cancelled' THEN NOW() ELSE finished_at END,
			updated_at = NOW() WHERE id = $1`, jobID, newStatus,
	); err != nil {
		return "", fmt.Errorf("cancel agent job: %w", err)
	}
	if newStatus == JobCancelled {
		if _, err := tx.Exec(ctx, `
			UPDATE agent_runs SET status = 'cancelled', error_type = 'cancelled', finished_at = NOW(), updated_at = NOW()
			WHERE id = $1`, runID,
		); err != nil {
			return "", fmt.Errorf("cancel queued agent run: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit agent job cancellation: %w", err)
	}
	return newStatus, nil
}

func (s *PostgresJobStore) Retry(ctx context.Context, runID, userID uuid.UUID) (string, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin retry agent job: %w", err)
	}
	defer tx.Rollback(ctx)
	var jobID uuid.UUID
	var jobStatus string
	err = tx.QueryRow(ctx, `
		SELECT id, status FROM agent_jobs WHERE run_id = $1 AND user_id = $2 FOR UPDATE`, runID, userID,
	).Scan(&jobID, &jobStatus)
	if err == pgx.ErrNoRows {
		return "", ErrJobNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load agent job for retry: %w", err)
	}
	if jobStatus == JobRunning || jobStatus == JobQueued || jobStatus == JobRetryWait {
		return "", ErrJobActive
	}
	if jobStatus == JobCompleted {
		return "", ErrJobTerminal
	}
	var nextNode NodeID
	if err := tx.QueryRow(ctx, `
		SELECT next_node FROM agent_checkpoints WHERE run_id = $1 ORDER BY sequence DESC LIMIT 1`, runID,
	).Scan(&nextNode); err != nil {
		if err == pgx.ErrNoRows {
			return "", ErrCheckpointNotFound
		}
		return "", fmt.Errorf("load retry checkpoint: %w", err)
	}
	if nextNode == "" || nextNode == NodeEnd {
		return "", ErrRunNotRecoverable
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_jobs
		SET status = 'queued', attempt = 0, scheduled_at = NOW(), cancel_requested = FALSE,
			lease_owner = NULL, lease_expires_at = NULL, heartbeat_at = NULL,
			last_error_type = NULL, started_at = NULL, finished_at = NULL, updated_at = NOW()
		WHERE id = $1`, jobID,
	); err != nil {
		return "", fmt.Errorf("retry agent job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_runs SET status = 'queued', current_node = $2, error_type = NULL,
			finished_at = NULL, updated_at = NOW() WHERE id = $1`, runID, nextNode,
	); err != nil {
		return "", fmt.Errorf("retry agent run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit agent job retry: %w", err)
	}
	return JobQueued, nil
}

func (s *PostgresJobStore) GetJob(ctx context.Context, runID, userID uuid.UUID) (*AgentJob, error) {
	job := &AgentJob{}
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, run_id, user_id, status, priority, attempt, max_attempts,
			scheduled_at, lease_owner, lease_expires_at, heartbeat_at,
			cancel_requested, last_error_type, started_at, finished_at, created_at, updated_at
		FROM agent_jobs WHERE run_id = $1 AND user_id = $2`, runID, userID,
	).Scan(
		&job.ID, &job.RunID, &job.UserID, &job.Status, &job.Priority,
		&job.Attempt, &job.MaxAttempts, &job.ScheduledAt, &job.LeaseOwner,
		&job.LeaseExpiresAt, &job.HeartbeatAt, &job.CancelRequested,
		&job.LastErrorType, &job.StartedAt, &job.FinishedAt, &job.CreatedAt, &job.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, ErrJobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get agent job: %w", err)
	}
	return job, nil
}

func (s *PostgresJobStore) GetStats(ctx context.Context, userID uuid.UUID) (*JobStats, error) {
	stats := &JobStats{}
	err := s.db.Pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'running'),
			COUNT(*) FILTER (WHERE status = 'retry_wait'),
			COUNT(*) FILTER (WHERE status = 'completed'),
			COUNT(*) FILTER (WHERE status = 'failed'),
			COUNT(*) FILTER (WHERE status = 'cancelled'),
			COALESCE(AVG(EXTRACT(EPOCH FROM (started_at - created_at)) * 1000)
				FILTER (WHERE started_at IS NOT NULL), 0)::FLOAT8,
			COALESCE(AVG(EXTRACT(EPOCH FROM (finished_at - started_at)) * 1000)
				FILTER (WHERE finished_at IS NOT NULL AND started_at IS NOT NULL), 0)::FLOAT8,
			COALESCE(
				COUNT(*) FILTER (WHERE status = 'completed')::FLOAT8 /
				NULLIF(COUNT(*) FILTER (WHERE status IN ('completed','failed','cancelled')), 0),
				0
			)::FLOAT8
		FROM agent_jobs
		WHERE user_id = $1`, userID,
	).Scan(
		&stats.Queued, &stats.Running, &stats.RetryWait,
		&stats.Completed, &stats.Failed, &stats.Cancelled,
		&stats.AverageQueueMS, &stats.AverageExecutionMS, &stats.SuccessRate,
	)
	if err != nil {
		return nil, fmt.Errorf("get agent job stats: %w", err)
	}
	return stats, nil
}

func (s *PostgresJobStore) SaveResultMessage(ctx context.Context, result *Result) error {
	if result == nil || result.ConversationID == uuid.Nil {
		return nil
	}
	preview := result.ResultRows
	if len(preview) > 20 {
		preview = preview[:20]
	}
	chartJSON, _ := json.Marshal(result.ChartConfig)
	previewJSON, _ := json.Marshal(preview)
	resultJSON, _ := json.Marshal(result.ResultRows)
	reportJSON, _ := json.Marshal(result.Report)
	content := result.Summary
	if result.Status == RunFailed && result.Error != "" {
		content = result.Error
	}
	var sqlValue interface{}
	if result.SQL != "" {
		sqlValue = result.SQL
	}
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO messages (
			id, conversation_id, agent_run_id, role, content, sql_text,
			chart_config, result_preview, result_data, report, result_count, created_at
		) VALUES ($1,$2,$3,'assistant',$4,$5,$6,$7,$8,$9,$10,NOW())
		ON CONFLICT (agent_run_id) WHERE agent_run_id IS NOT NULL DO NOTHING`,
		uuid.New(), result.ConversationID, result.RunID, content, sqlValue,
		chartJSON, previewJSON, resultJSON, reportJSON, result.ResultCount,
	)
	if err != nil {
		return fmt.Errorf("save asynchronous agent result message: %w", err)
	}
	_, _ = s.db.Pool.Exec(ctx, `UPDATE conversations SET updated_at = NOW() WHERE id = $1`, result.ConversationID)
	return nil
}

func (s *PostgresJobStore) ReapExpired(ctx context.Context) (int64, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin reap agent jobs: %w", err)
	}
	defer tx.Rollback(ctx)
	cancelled, err := tx.Exec(ctx, `
		WITH expired AS (
			UPDATE agent_jobs
			SET status = 'cancelled', lease_owner = NULL, lease_expires_at = NULL,
				heartbeat_at = NULL, last_error_type = 'cancelled', finished_at = NOW(), updated_at = NOW()
			WHERE status = 'running' AND cancel_requested = TRUE AND lease_expires_at < NOW()
			RETURNING run_id
		)
		UPDATE agent_runs
		SET status = 'cancelled', error_type = 'cancelled', finished_at = NOW(), updated_at = NOW()
		WHERE id IN (SELECT run_id FROM expired)`)
	if err != nil {
		return 0, fmt.Errorf("reap cancelled agent jobs: %w", err)
	}
	exhausted, err := tx.Exec(ctx, `
		WITH exhausted AS (
			UPDATE agent_jobs
			SET status = 'failed', lease_owner = NULL, lease_expires_at = NULL,
				heartbeat_at = NULL, last_error_type = 'runtime_interrupted', finished_at = NOW(), updated_at = NOW()
			WHERE status = 'running' AND lease_expires_at < NOW() AND attempt >= max_attempts
			RETURNING run_id
		)
		UPDATE agent_runs
		SET status = 'failed', error_type = 'runtime_interrupted', finished_at = NOW(), updated_at = NOW()
		WHERE id IN (SELECT run_id FROM exhausted)`)
	if err != nil {
		return 0, fmt.Errorf("reap exhausted agent jobs: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit reap agent jobs: %w", err)
	}
	return cancelled.RowsAffected() + exhausted.RowsAffected(), nil
}

func loadResumeSnapshot(ctx context.Context, tx pgx.Tx, runID uuid.UUID) (*ResumeSnapshot, error) {
	var checkpointJSON []byte
	err := tx.QueryRow(ctx, `
		SELECT state_data FROM agent_checkpoints WHERE run_id = $1 ORDER BY sequence DESC LIMIT 1`, runID,
	).Scan(&checkpointJSON)
	if err == pgx.ErrNoRows {
		return nil, ErrCheckpointNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load queued agent checkpoint: %w", err)
	}
	var checkpoint StateCheckpoint
	if err := json.Unmarshal(checkpointJSON, &checkpoint); err != nil {
		return nil, fmt.Errorf("decode queued agent checkpoint: %w", err)
	}
	if checkpoint.NextNode == "" || checkpoint.NextNode == NodeEnd {
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
	return snapshot, nil
}

func nullableString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
