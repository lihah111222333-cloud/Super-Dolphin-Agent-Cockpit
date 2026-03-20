DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.tables
        WHERE table_schema = current_schema()
          AND table_name = 'prompt_template_versions'
    ) AND NOT EXISTS (
        SELECT 1
        FROM information_schema.tables
        WHERE table_schema = current_schema()
          AND table_name = 'prompt_versions'
    ) THEN
        ALTER TABLE prompt_template_versions RENAME TO prompt_versions;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS prompt_versions (
    id BIGSERIAL PRIMARY KEY,
    prompt_key TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    agent_key TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    prompt_text TEXT NOT NULL,
    variables JSONB,
    tags JSONB,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    source_updated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

DROP INDEX IF EXISTS idx_prompt_template_versions_key_id;
CREATE INDEX IF NOT EXISTS idx_prompt_versions_key_id ON prompt_versions (prompt_key, id DESC);

CREATE TABLE IF NOT EXISTS task_traces (
    id BIGSERIAL PRIMARY KEY,
    trace_id TEXT NOT NULL,
    span_id TEXT NOT NULL UNIQUE,
    parent_span_id TEXT NOT NULL DEFAULT '',
    span_name TEXT NOT NULL,
    component TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running',
    input_payload JSONB,
    output_payload JSONB,
    error_text TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    duration_ms INT NOT NULL DEFAULT 0,
    CONSTRAINT chk_task_traces_status
        CHECK (status IN ('running', 'ok', 'error', 'cancelled')),
    CONSTRAINT chk_task_traces_duration_non_negative
        CHECK (duration_ms >= 0)
);

CREATE INDEX IF NOT EXISTS idx_task_traces_trace_started
    ON task_traces (trace_id, started_at ASC, id ASC);

CREATE INDEX IF NOT EXISTS idx_task_traces_component_started
    ON task_traces (component, started_at DESC);

