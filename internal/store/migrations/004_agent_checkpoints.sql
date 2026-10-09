ALTER TABLE agent_runs DROP CONSTRAINT IF EXISTS agent_runs_status_check;
ALTER TABLE agent_runs
    ADD CONSTRAINT agent_runs_status_check
    CHECK (status IN ('running', 'completed', 'failed', 'interrupted'));

ALTER TABLE agent_runs ADD COLUMN IF NOT EXISTS resume_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_runs ADD COLUMN IF NOT EXISTS resumed_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS agent_checkpoints (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    current_node VARCHAR(64),
    next_node VARCHAR(64) NOT NULL,
    graph_version VARCHAR(128) NOT NULL,
    state_data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_agent_checkpoints_run_sequence
    ON agent_checkpoints(run_id, sequence DESC);

CREATE TABLE IF NOT EXISTS agent_run_artifacts (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step_sequence INTEGER NOT NULL,
    kind VARCHAR(64) NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id, step_sequence, kind)
);

CREATE INDEX IF NOT EXISTS idx_agent_run_artifacts_run_kind
    ON agent_run_artifacts(run_id, kind, step_sequence DESC);
