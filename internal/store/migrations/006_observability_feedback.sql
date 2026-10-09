ALTER TABLE agent_runs
    ADD COLUMN IF NOT EXISTS input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS llm_calls INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS estimated_cost_yuan NUMERIC(14, 6) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS prompt_version VARCHAR(128) NOT NULL DEFAULT 'sql-report/v1',
    ADD COLUMN IF NOT EXISTS queue_wait_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS run_source VARCHAR(32) NOT NULL DEFAULT 'interactive';

ALTER TABLE agent_runs DROP CONSTRAINT IF EXISTS agent_runs_run_source_check;
ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_run_source_check
    CHECK (run_source IN ('interactive', 'evaluation', 'webhook'));

CREATE TABLE IF NOT EXISTS agent_feedback (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    message_id UUID REFERENCES messages(id) ON DELETE SET NULL,
    feedback_type VARCHAR(32) NOT NULL CHECK (feedback_type IN (
        'helpful', 'not_helpful', 'data_error', 'sql_error', 'chart_error'
    )),
    comment TEXT NOT NULL DEFAULT '',
    promoted_to_eval BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_feedback_created_at
    ON agent_feedback(created_at DESC);

CREATE TABLE IF NOT EXISTS evaluation_cases (
    id UUID PRIMARY KEY,
    source_feedback_id UUID UNIQUE REFERENCES agent_feedback(id) ON DELETE SET NULL,
    domain VARCHAR(64) NOT NULL,
    question TEXT NOT NULL,
    expected_tables JSONB NOT NULL DEFAULT '[]'::jsonb,
    status VARCHAR(32) NOT NULL DEFAULT 'candidate' CHECK (status IN ('candidate', 'approved', 'retired')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_evaluation_cases_status
    ON evaluation_cases(status, created_at DESC);
