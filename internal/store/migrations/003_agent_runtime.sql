CREATE TABLE IF NOT EXISTS agent_runs (
    id UUID PRIMARY KEY,
    user_id UUID REFERENCES app_users(id) ON DELETE SET NULL,
    conversation_id UUID REFERENCES conversations(id) ON DELETE SET NULL,
    dataset_id UUID REFERENCES datasets(id) ON DELETE SET NULL,
    domain VARCHAR(64) NOT NULL,
    graph_version VARCHAR(128) NOT NULL,
    start_node VARCHAR(64) NOT NULL,
    current_node VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL CHECK (status IN ('running', 'completed', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    step_count INTEGER NOT NULL DEFAULT 0,
    error_type VARCHAR(64),
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_runs_user_started_at
    ON agent_runs(user_id, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_runs_status_started_at
    ON agent_runs(status, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_runs_conversation_id
    ON agent_runs(conversation_id);

CREATE TABLE IF NOT EXISTS agent_steps (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    node VARCHAR(64) NOT NULL,
    next_node VARCHAR(64),
    status VARCHAR(32) NOT NULL CHECK (status IN ('completed', 'retrying', 'failed')),
    attempt INTEGER NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    error_type VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_agent_steps_run_sequence
    ON agent_steps(run_id, sequence);
