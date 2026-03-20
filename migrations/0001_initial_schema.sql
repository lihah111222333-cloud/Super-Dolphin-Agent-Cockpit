-- Baseline schema for multi-agent orchestration
-- Applied through db.postgres.ensure_schema -> _apply_sql_migrations

CREATE TABLE IF NOT EXISTS audit_events (
    id BIGSERIAL PRIMARY KEY,
    ts TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    event_type TEXT NOT NULL,
    action TEXT NOT NULL,
    result TEXT NOT NULL,
    actor TEXT NOT NULL DEFAULT '',
    target TEXT NOT NULL DEFAULT '',
    detail TEXT NOT NULL DEFAULT '',
    level TEXT NOT NULL DEFAULT 'INFO',
    extra JSONB
);

CREATE INDEX IF NOT EXISTS idx_audit_events_ts ON audit_events (ts DESC);
CREATE INDEX IF NOT EXISTS idx_audit_events_event_type ON audit_events (event_type);
CREATE INDEX IF NOT EXISTS idx_audit_events_action ON audit_events (action);
CREATE INDEX IF NOT EXISTS idx_audit_events_result ON audit_events (result);
CREATE INDEX IF NOT EXISTS idx_audit_events_actor ON audit_events (actor);

CREATE TABLE IF NOT EXISTS system_logs (
    id BIGSERIAL PRIMARY KEY,
    ts TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    level TEXT NOT NULL,
    logger TEXT NOT NULL,
    message TEXT NOT NULL,
    raw TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_system_logs_ts ON system_logs (ts DESC);
CREATE INDEX IF NOT EXISTS idx_system_logs_level ON system_logs (level);
CREATE INDEX IF NOT EXISTS idx_system_logs_logger ON system_logs (logger);

CREATE TABLE IF NOT EXISTS topology_approvals (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    requested_by TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    expire_at TIMESTAMPTZ NOT NULL,
    reviewed_at TIMESTAMPTZ,
    reviewer TEXT NOT NULL DEFAULT '',
    review_note TEXT NOT NULL DEFAULT '',
    arch_hash TEXT NOT NULL,
    proposed_architecture JSONB NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_topology_approvals_status_created_at ON topology_approvals (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_topology_approvals_arch_hash ON topology_approvals (arch_hash);

CREATE TABLE IF NOT EXISTS topology_approval_archives (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    requested_by TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    expire_at TIMESTAMPTZ NOT NULL,
    reviewed_at TIMESTAMPTZ,
    reviewer TEXT NOT NULL DEFAULT '',
    review_note TEXT NOT NULL DEFAULT '',
    arch_hash TEXT NOT NULL,
    proposed_architecture JSONB NOT NULL,
    archived_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_topology_approval_archives_archived_at ON topology_approval_archives (archived_at DESC);

CREATE TABLE IF NOT EXISTS shared_files (
    path TEXT PRIMARY KEY,
    content TEXT NOT NULL,
    updated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_shared_files_updated_at ON shared_files (updated_at DESC);

CREATE TABLE IF NOT EXISTS prompts (
    id BIGSERIAL PRIMARY KEY,
    agent_key TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    prompt_text TEXT NOT NULL DEFAULT '',
    is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (agent_key, tool_name)
);

CREATE INDEX IF NOT EXISTS idx_prompts_agent_key ON prompts (agent_key);
CREATE INDEX IF NOT EXISTS idx_prompts_sort_order ON prompts (sort_order, agent_key);

CREATE TABLE IF NOT EXISTS agent_interactions (
    id BIGSERIAL PRIMARY KEY,
    thread_id TEXT NOT NULL DEFAULT '',
    parent_id BIGINT,
    sender TEXT NOT NULL,
    receiver TEXT NOT NULL DEFAULT '',
    msg_type TEXT NOT NULL DEFAULT 'task',
    status TEXT NOT NULL DEFAULT 'pending',
    requires_review BOOLEAN NOT NULL DEFAULT FALSE,
    reviewed_by TEXT NOT NULL DEFAULT '',
    review_note TEXT NOT NULL DEFAULT '',
    reviewed_at TIMESTAMPTZ,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_interactions_thread_created ON agent_interactions (thread_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_interactions_sender_receiver ON agent_interactions (sender, receiver);
CREATE INDEX IF NOT EXISTS idx_agent_interactions_status_review ON agent_interactions (status, requires_review, created_at DESC);

CREATE TABLE IF NOT EXISTS prompt_templates (
    id BIGSERIAL PRIMARY KEY,
    prompt_key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    agent_key TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    prompt_text TEXT NOT NULL,
    variables JSONB,
    tags JSONB,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_prompt_templates_agent_tool ON prompt_templates (agent_key, tool_name);
CREATE INDEX IF NOT EXISTS idx_prompt_templates_enabled ON prompt_templates (enabled, updated_at DESC);

CREATE TABLE IF NOT EXISTS prompt_template_versions (
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

CREATE INDEX IF NOT EXISTS idx_prompt_template_versions_key_id ON prompt_template_versions (prompt_key, id DESC);

CREATE TABLE IF NOT EXISTS command_cards (
    id BIGSERIAL PRIMARY KEY,
    card_key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    command_template TEXT NOT NULL,
    args_schema JSONB,
    risk_level TEXT NOT NULL DEFAULT 'normal',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_command_cards_risk_enabled ON command_cards (risk_level, enabled, updated_at DESC);

CREATE TABLE IF NOT EXISTS command_card_runs (
    id BIGSERIAL PRIMARY KEY,
    card_key TEXT NOT NULL,
    requested_by TEXT NOT NULL DEFAULT '',
    params JSONB NOT NULL DEFAULT '{}'::jsonb,
    rendered_command TEXT NOT NULL,
    risk_level TEXT NOT NULL DEFAULT 'normal',
    status TEXT NOT NULL DEFAULT 'pending_review',
    requires_review BOOLEAN NOT NULL DEFAULT TRUE,
    interaction_id BIGINT,
    output TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    exit_code INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    executed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_command_card_runs_status_created ON command_card_runs (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_command_card_runs_card_key ON command_card_runs (card_key, created_at DESC);
