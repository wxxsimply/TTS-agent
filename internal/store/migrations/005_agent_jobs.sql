ALTER TABLE agent_runs DROP CONSTRAINT IF EXISTS agent_runs_status_check;
ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_status_check
    CHECK (status IN (
        'queued', 'running', 'retry_wait', 'completed',
        'failed', 'interrupted', 'cancelled'
    ));

CREATE TABLE IF NOT EXISTS agent_jobs (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL UNIQUE REFERENCES agent_runs(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    status VARCHAR(32) NOT NULL CHECK (status IN (
        'queued', 'running', 'retry_wait', 'completed', 'failed', 'cancelled'
    )),
    priority INTEGER NOT NULL DEFAULT 0 CHECK (priority BETWEEN -10 AND 10),
    attempt INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3 CHECK (max_attempts BETWEEN 1 AND 20),
    scheduled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_owner VARCHAR(255),
    lease_expires_at TIMESTAMPTZ,
    heartbeat_at TIMESTAMPTZ,
    idempotency_key VARCHAR(128),
    cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
    last_error_type VARCHAR(64),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_agent_jobs_user_idempotency
    ON agent_jobs(user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_agent_jobs_claim
    ON agent_jobs(priority DESC, scheduled_at, created_at)
    WHERE status IN ('queued', 'retry_wait', 'running');

CREATE INDEX IF NOT EXISTS idx_agent_jobs_user_status
    ON agent_jobs(user_id, status, created_at DESC);

ALTER TABLE messages ADD COLUMN IF NOT EXISTS agent_run_id UUID REFERENCES agent_runs(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_messages_agent_run_id
    ON messages(agent_run_id)
    WHERE agent_run_id IS NOT NULL;
