PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    filename TEXT NOT NULL,
    applied_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_codex_binding (
    agent_id TEXT PRIMARY KEY,
    codex_thread_id TEXT NOT NULL UNIQUE,
    rollout_path TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0,
    cwd TEXT NOT NULL DEFAULT '',
    archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN (0, 1))
);

CREATE TABLE IF NOT EXISTS agent_provider_binding (
    agent_id TEXT PRIMARY KEY,
    provider TEXT NOT NULL CHECK(provider <> ''),
    provider_thread_id TEXT NOT NULL DEFAULT '',
    codex_thread_id TEXT NOT NULL DEFAULT '',
    rollout_path TEXT NOT NULL DEFAULT '',
    cwd TEXT NOT NULL DEFAULT '',
    parent_agent_id TEXT NOT NULL DEFAULT '',
    agent_type TEXT NOT NULL DEFAULT '',
    agent_memory_scope TEXT NOT NULL DEFAULT '',
    archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN (0, 1)),
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0,
    session_uuid TEXT NOT NULL DEFAULT '',
    codex_home TEXT NOT NULL DEFAULT '',
    codex_instance_key TEXT NOT NULL DEFAULT '',
    codex_model_provider TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS agent_status (
    agent_id TEXT PRIMARY KEY,
    agent_name TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'unknown' CHECK(status IN ('running', 'idle', 'stuck', 'error', 'disconnected', 'unknown')),
    stagnant_sec INTEGER NOT NULL DEFAULT 0 CHECK(stagnant_sec >= 0),
    error TEXT NOT NULL DEFAULT '',
    output_tail TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(output_tail)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_threads (
    thread_id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    cwd TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'running',
    port INTEGER NOT NULL DEFAULT 0,
    pid INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0,
    finished_at INTEGER,
    last_event_type TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    workspace_run_key TEXT NOT NULL DEFAULT '',
    owner_thread_id TEXT NOT NULL DEFAULT '',
    config_override TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(config_override)),
    prompt_snapshot TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(prompt_snapshot)),
    parent_agent_id TEXT NOT NULL DEFAULT '',
    agent_type TEXT NOT NULL DEFAULT '',
    agent_memory_scope TEXT NOT NULL DEFAULT '',
    agent_key TEXT NOT NULL DEFAULT '',
    prompt_version_id INTEGER,
    pending_launch INTEGER NOT NULL DEFAULT 0 CHECK(pending_launch IN (0, 1)),
    manually_renamed INTEGER NOT NULL DEFAULT 0 CHECK(manually_renamed IN (0, 1))
);

CREATE TABLE IF NOT EXISTS audit_events (
    id INTEGER PRIMARY KEY,
    ts INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    action TEXT NOT NULL,
    result TEXT NOT NULL,
    actor TEXT NOT NULL DEFAULT '',
    target TEXT NOT NULL DEFAULT '',
    detail TEXT NOT NULL DEFAULT '',
    level TEXT NOT NULL DEFAULT 'INFO',
    extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra))
);

CREATE TABLE IF NOT EXISTS bus_exception_logs (
    id INTEGER PRIMARY KEY,
    ts INTEGER NOT NULL,
    category TEXT NOT NULL DEFAULT 'unknown',
    severity TEXT NOT NULL DEFAULT 'error',
    source TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    traceback TEXT NOT NULL DEFAULT '',
    extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra))
);

CREATE TABLE IF NOT EXISTS prompts (
    id INTEGER PRIMARY KEY,
    agent_key TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    prompt_text TEXT NOT NULL DEFAULT '',
    is_pinned INTEGER NOT NULL DEFAULT 0 CHECK(is_pinned IN (0, 1)),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(agent_key, tool_name)
);

CREATE TABLE IF NOT EXISTS prompt_templates (
    id INTEGER PRIMARY KEY,
    prompt_key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    agent_key TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    prompt_text TEXT NOT NULL,
    variables TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(variables)),
    tags TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(tags)),
    description TEXT NOT NULL DEFAULT '',
    when_to_use TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    manually_edited INTEGER NOT NULL DEFAULT 0 CHECK(manually_edited IN (0, 1)),
    match_when TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(match_when)),
    priority INTEGER NOT NULL DEFAULT 0,
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS prompt_template_versions (
    id INTEGER PRIMARY KEY,
    prompt_key TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    agent_key TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    prompt_text TEXT NOT NULL,
    variables TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(variables)),
    tags TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(tags)),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    source_updated_at INTEGER,
    created_at INTEGER NOT NULL,
    archived_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS prompt_versions (
    id INTEGER PRIMARY KEY,
    prompt_key TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    agent_key TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    prompt_text TEXT NOT NULL,
    variables TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(variables)),
    tags TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(tags)),
    description TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    source_updated_at INTEGER,
    created_at INTEGER NOT NULL,
    archived_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS prompt_template_sections (
    id INTEGER PRIMARY KEY,
    template_id INTEGER NOT NULL REFERENCES prompt_templates(id) ON DELETE CASCADE,
    section_key TEXT NOT NULL,
    region TEXT NOT NULL CHECK(region IN ('static', 'dynamic')),
    ordinal INTEGER NOT NULL DEFAULT 0,
    body TEXT NOT NULL,
    enable_when TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(enable_when)),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    trigger_type TEXT NOT NULL DEFAULT 'always' CHECK(trigger_type IN ('always', 'keyword', 'recall')),
    recall_topic TEXT NOT NULL DEFAULT '',
    UNIQUE(template_id, section_key)
);

CREATE TABLE IF NOT EXISTS prompt_recall_topics (
    cwd TEXT NOT NULL,
    topic TEXT NOT NULL,
    template_id INTEGER NOT NULL,
    section_key TEXT NOT NULL,
    PRIMARY KEY(cwd, topic),
    CHECK(trim(cwd) <> ''),
    CHECK(trim(topic) <> ''),
    CHECK(template_id >= 0)
);

CREATE TABLE IF NOT EXISTS prompt_routing_tests (
    id INTEGER PRIMARY KEY,
    input TEXT NOT NULL UNIQUE,
    expected_prompt_key TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS prompt_intent_drafts (
    id INTEGER PRIMARY KEY,
    draft_key TEXT NOT NULL UNIQUE,
    cwd TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL CHECK(kind IN ('expert', 'recall', 'default_rule')),
    raw_input TEXT NOT NULL,
    source_type TEXT NOT NULL DEFAULT 'user_input',
    source_url TEXT NOT NULL DEFAULT '',
    origin_hash TEXT NOT NULL DEFAULT '',
    license_hint TEXT NOT NULL DEFAULT '',
    generated_card TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(generated_card)),
    confidence REAL NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft', 'ready_to_save', 'enabled', 'rejected')),
    scope TEXT NOT NULL DEFAULT 'project' CHECK(scope IN ('project', 'global')),
    issues TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(issues)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS command_cards (
    id INTEGER PRIMARY KEY,
    card_key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    command_template TEXT NOT NULL,
    args_schema TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(args_schema)),
    risk_level TEXT NOT NULL DEFAULT 'normal',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS command_card_versions (
    id INTEGER PRIMARY KEY,
    card_key TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    command_template TEXT NOT NULL,
    args_schema TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(args_schema)),
    risk_level TEXT NOT NULL DEFAULT 'normal',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    source_updated_at INTEGER,
    created_at INTEGER NOT NULL,
    archived_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS command_card_runs (
    id INTEGER PRIMARY KEY,
    card_key TEXT NOT NULL,
    requested_by TEXT NOT NULL DEFAULT '',
    params TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(params)),
    rendered_command TEXT NOT NULL,
    risk_level TEXT NOT NULL DEFAULT 'normal',
    status TEXT NOT NULL DEFAULT 'pending_review',
    requires_review INTEGER NOT NULL DEFAULT 1 CHECK(requires_review IN (0, 1)),
    interaction_id INTEGER,
    output TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    exit_code INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    executed_at INTEGER
);

CREATE TABLE IF NOT EXISTS shared_files (
    path TEXT PRIMARY KEY,
    content TEXT NOT NULL,
    updated_by TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_feedback_events (
    id INTEGER PRIMARY KEY,
    thread_id TEXT NOT NULL,
    turn_id TEXT NOT NULL DEFAULT '',
    agent_key TEXT NOT NULL DEFAULT '',
    prompt_version_id INTEGER,
    event_type TEXT NOT NULL,
    actor TEXT NOT NULL DEFAULT '',
    payload TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(payload)),
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS session_insights (
    id INTEGER PRIMARY KEY,
    thread_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    local_turn_id TEXT NOT NULL DEFAULT '',
    provider_turn_id TEXT NOT NULL DEFAULT '',
    started_at INTEGER,
    completed_at INTEGER,
    duration_ms INTEGER NOT NULL DEFAULT 0 CHECK(duration_ms >= 0),
    success INTEGER CHECK(success IN (0, 1)),
    status TEXT NOT NULL DEFAULT 'unknown',
    stop_reason TEXT NOT NULL DEFAULT '',
    tool_calls INTEGER NOT NULL DEFAULT 0 CHECK(tool_calls >= 0),
    tool_calls_observed INTEGER NOT NULL DEFAULT 1 CHECK(tool_calls_observed IN (0, 1)),
    tool_failures INTEGER NOT NULL DEFAULT 0 CHECK(tool_failures >= 0),
    tool_failures_observed INTEGER NOT NULL DEFAULT 1 CHECK(tool_failures_observed IN (0, 1)),
    approval_requests INTEGER NOT NULL DEFAULT 0 CHECK(approval_requests >= 0),
    approval_requests_observed INTEGER NOT NULL DEFAULT 0 CHECK(approval_requests_observed IN (0, 1)),
    token_input INTEGER NOT NULL DEFAULT 0 CHECK(token_input >= 0),
    token_output INTEGER NOT NULL DEFAULT 0 CHECK(token_output >= 0),
    token_total INTEGER NOT NULL DEFAULT 0 CHECK(token_total >= 0),
    token_snapshot_observed INTEGER NOT NULL DEFAULT 0 CHECK(token_snapshot_observed IN (0, 1)),
    context_window_tokens INTEGER NOT NULL DEFAULT 0 CHECK(context_window_tokens >= 0),
    ui_projection TEXT NOT NULL DEFAULT '',
    skills_selected TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(skills_selected)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS hook_pending_reviews (
    hook_call_id TEXT PRIMARY KEY,
    topic TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    thread_id TEXT NOT NULL DEFAULT '',
    turn_id TEXT NOT NULL DEFAULT '',
    subscriber_lease TEXT NOT NULL DEFAULT '',
    payload TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(payload)),
    decision TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    default_action TEXT NOT NULL DEFAULT 'reject',
    status TEXT NOT NULL DEFAULT 'pending',
    created_at INTEGER NOT NULL,
    deadline_at INTEGER NOT NULL,
    resolved_at INTEGER,
    idempotency_key TEXT NOT NULL DEFAULT '',
    resolved_by TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS agent_interactions (
    id INTEGER PRIMARY KEY,
    thread_id TEXT NOT NULL DEFAULT '',
    parent_id INTEGER,
    sender TEXT NOT NULL,
    receiver TEXT NOT NULL DEFAULT '',
    msg_type TEXT NOT NULL DEFAULT 'task',
    status TEXT NOT NULL DEFAULT 'pending',
    requires_review INTEGER NOT NULL DEFAULT 0 CHECK(requires_review IN (0, 1)),
    reviewed_by TEXT NOT NULL DEFAULT '',
    review_note TEXT NOT NULL DEFAULT '',
    reviewed_at INTEGER,
    payload TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(payload)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS topology_approvals (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    requested_by TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    expire_at INTEGER NOT NULL,
    reviewed_at INTEGER,
    reviewer TEXT NOT NULL DEFAULT '',
    review_note TEXT NOT NULL DEFAULT '',
    arch_hash TEXT NOT NULL,
    proposed_architecture TEXT NOT NULL CHECK(json_valid(proposed_architecture))
);

CREATE TABLE IF NOT EXISTS topology_approval_archives (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    requested_by TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    expire_at INTEGER NOT NULL,
    reviewed_at INTEGER,
    reviewer TEXT NOT NULL DEFAULT '',
    review_note TEXT NOT NULL DEFAULT '',
    arch_hash TEXT NOT NULL,
    proposed_architecture TEXT NOT NULL CHECK(json_valid(proposed_architecture)),
    archived_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS ui_preferences (
    cwd TEXT NOT NULL DEFAULT '',
    key TEXT NOT NULL,
    value TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(value)),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY(cwd, key)
);

CREATE TABLE IF NOT EXISTS system_logs (
    id INTEGER PRIMARY KEY,
    ts INTEGER NOT NULL,
    level TEXT NOT NULL,
    logger TEXT NOT NULL,
    message TEXT NOT NULL,
    raw TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    component TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    thread_id TEXT NOT NULL DEFAULT '',
    trace_id TEXT NOT NULL DEFAULT '',
    event_type TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER,
    extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra))
);

CREATE TABLE IF NOT EXISTS task_traces (
    id INTEGER PRIMARY KEY,
    trace_id TEXT NOT NULL,
    span_id TEXT NOT NULL UNIQUE,
    parent_span_id TEXT NOT NULL DEFAULT '',
    span_name TEXT NOT NULL,
    component TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running' CHECK(status IN ('running', 'ok', 'error', 'cancelled')),
    input_payload TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(input_payload)),
    output_payload TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(output_payload)),
    error_text TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(metadata)),
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    duration_ms INTEGER NOT NULL DEFAULT 0 CHECK(duration_ms >= 0)
);

CREATE TABLE IF NOT EXISTS cwd_instance_locks (
    cwd TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL,
    pid INTEGER NOT NULL DEFAULT 0,
    acquired_at INTEGER NOT NULL,
    heartbeat_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS turn_dedupe_registry (
    dedupe_key TEXT PRIMARY KEY,
    local_turn_id TEXT NOT NULL,
    provider_turn_id TEXT NOT NULL DEFAULT '',
    thread_id TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    terminal_at INTEGER
);

CREATE TABLE IF NOT EXISTS cron_jobs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    prompt TEXT NOT NULL,
    schedule_type TEXT NOT NULL DEFAULT 'cron',
    schedule_expr TEXT NOT NULL CHECK(schedule_expr <> ''),
    timezone TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT 'codex' CHECK(provider IN ('codex', 'claude')),
    model TEXT NOT NULL DEFAULT '',
    cwd TEXT NOT NULL CHECK(cwd <> ''),
    config TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(config)),
    skills TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(skills)),
    notify_channel TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    next_run_at INTEGER NOT NULL,
    last_scheduled_at INTEGER,
    last_run_at INTEGER,
    claimed_at INTEGER,
    claimed_by TEXT NOT NULL DEFAULT '',
    lease_expires_at INTEGER,
    claim_token TEXT NOT NULL DEFAULT '',
    thread_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    active_turn_id TEXT NOT NULL DEFAULT '',
    last_turn_id TEXT NOT NULL DEFAULT '',
    failure_count INTEGER NOT NULL DEFAULT 0 CHECK(failure_count >= 0),
    max_attempts INTEGER NOT NULL DEFAULT 0 CHECK(max_attempts >= 0),
    next_retry_at INTEGER,
    last_status TEXT NOT NULL DEFAULT '',
    last_error_at INTEGER,
    last_error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS cron_job_runs (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES cron_jobs(id) ON DELETE CASCADE,
    scheduled_at INTEGER NOT NULL,
    idempotency_key TEXT NOT NULL,
    dedupe_key TEXT NOT NULL DEFAULT '',
    thread_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    turn_id TEXT NOT NULL DEFAULT '',
    submitted_at INTEGER,
    status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'submitting', 'submitted', 'running', 'finished', 'failed', 'observe_lost')),
    error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS datasource_v2_documents (
    id INTEGER PRIMARY KEY,
    source_path TEXT NOT NULL,
    file_name TEXT NOT NULL,
    extension TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    content_hash TEXT,
    chunk_count INTEGER NOT NULL DEFAULT 0,
    total_chars INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'importing' CHECK(status IN ('importing', 'ready', 'failed')),
    error_message TEXT,
    created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
    updated_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
    UNIQUE(source_path),
    CHECK(source_path <> ''),
    CHECK(file_name <> ''),
    CHECK(size_bytes >= 0),
    CHECK(chunk_count >= 0),
    CHECK(total_chars >= 0),
    CHECK(status <> 'ready' OR content_hash IS NOT NULL)
);

CREATE TABLE IF NOT EXISTS datasource_v2_text_chunks (
    id INTEGER PRIMARY KEY,
    document_id INTEGER NOT NULL REFERENCES datasource_v2_documents(id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL,
    content TEXT NOT NULL,
    char_count INTEGER NOT NULL,
    byte_count INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
    UNIQUE(document_id, chunk_index),
    CHECK(chunk_index >= 0),
    CHECK(content <> ''),
    CHECK(char_count > 0),
    CHECK(byte_count > 0)
);

CREATE TABLE IF NOT EXISTS task_acks (
    id INTEGER PRIMARY KEY,
    ack_key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    assigned_to TEXT NOT NULL DEFAULT '',
    requested_by TEXT NOT NULL DEFAULT '',
    priority TEXT NOT NULL DEFAULT 'normal',
    status TEXT NOT NULL DEFAULT 'pending',
    progress INTEGER NOT NULL DEFAULT 0 CHECK(progress >= 0 AND progress <= 100),
    ack_message TEXT NOT NULL DEFAULT '',
    result_summary TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(metadata)),
    due_at INTEGER,
    acked_at INTEGER,
    started_at INTEGER,
    finished_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS task_dags (
    id INTEGER PRIMARY KEY,
    dag_key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'draft',
    created_by TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(metadata)),
    started_at INTEGER,
    finished_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    trigger TEXT NOT NULL DEFAULT 'manual' CHECK(trigger IN ('manual', 'auto', 'scheduled', 'external')),
    owner_id TEXT NOT NULL DEFAULT '',
    cron_expr TEXT NOT NULL DEFAULT '',
    next_run_at INTEGER,
    version INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS task_dag_runs (
    id INTEGER PRIMARY KEY,
    run_key TEXT NOT NULL UNIQUE,
    dag_key TEXT NOT NULL,
    dag_version_snapshot INTEGER NOT NULL DEFAULT 0,
    trigger_source TEXT NOT NULL DEFAULT '' CHECK(trigger_source IN ('manual', 'auto', 'scheduled', 'external', '')),
    status TEXT NOT NULL DEFAULT 'running' CHECK(status IN ('running', 'succeeded', 'failed', 'cancelled')),
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    events TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(events)),
    budget_used INTEGER NOT NULL DEFAULT 0,
    budget_limit INTEGER,
    metadata TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(metadata)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS task_dag_nodes (
    id INTEGER PRIMARY KEY,
    dag_key TEXT NOT NULL,
    node_key TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    node_type TEXT NOT NULL DEFAULT 'task',
    assigned_to TEXT NOT NULL DEFAULT '',
    depends_on TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(depends_on) AND json_type(depends_on) = 'array'),
    status TEXT NOT NULL DEFAULT 'pending',
    command_ref TEXT NOT NULL DEFAULT '',
    config TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(config)),
    result TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(result)),
    started_at INTEGER,
    finished_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    active_turn_id TEXT,
    active_wakeup_id INTEGER,
    last_event_at INTEGER,
    run_id INTEGER REFERENCES task_dag_runs(id) ON DELETE CASCADE,
    reads TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(reads) AND json_type(reads) = 'array'),
    writes TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(writes) AND json_type(writes) = 'array'),
    spawning_thread_id TEXT
);

CREATE TABLE IF NOT EXISTS task_dag_wakeups (
    id INTEGER PRIMARY KEY,
    dag_key TEXT NOT NULL,
    node_key TEXT NOT NULL,
    run_id INTEGER REFERENCES task_dag_runs(id) ON DELETE CASCADE,
    wakeup_kind TEXT NOT NULL,
    target_agent_id TEXT NOT NULL,
    prompt_payload TEXT NOT NULL CHECK(json_valid(prompt_payload)),
    idempotency_key TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_retry_at INTEGER NOT NULL,
    claimed_at INTEGER,
    claimed_by TEXT NOT NULL DEFAULT '',
    lease_expires_at INTEGER,
    sent_at INTEGER,
    bound_turn_id TEXT,
    turn_bound_at INTEGER,
    last_error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK(status NOT IN ('pending', 'dispatching') OR trim(dag_key) = '' OR trim(node_key) = '' OR run_id IS NOT NULL)
);

CREATE TABLE IF NOT EXISTS task_dag_worker_leases (
    target_agent_id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    lease_expires_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS workspace_runs (
    id INTEGER PRIMARY KEY,
    run_key TEXT NOT NULL UNIQUE,
    dag_key TEXT NOT NULL DEFAULT '',
    source_root TEXT NOT NULL,
    workspace_path TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_by TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(metadata)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    finished_at INTEGER
);

CREATE TABLE IF NOT EXISTS workspace_run_files (
    id INTEGER PRIMARY KEY,
    run_key TEXT NOT NULL REFERENCES workspace_runs(run_key) ON DELETE CASCADE,
    relative_path TEXT NOT NULL,
    baseline_sha256 TEXT NOT NULL DEFAULT '',
    workspace_sha256 TEXT NOT NULL DEFAULT '',
    source_sha256_before TEXT NOT NULL DEFAULT '',
    source_sha256_after TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'tracked',
    last_error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(run_key, relative_path)
);

CREATE TABLE IF NOT EXISTS runtime_locks (
    lock_key TEXT PRIMARY KEY,
    holder TEXT NOT NULL,
    lease_expires_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_agent_provider_binding_provider_thread ON agent_provider_binding(provider, provider_thread_id) WHERE provider_thread_id <> '';
CREATE INDEX IF NOT EXISTS idx_agent_provider_binding_codex_thread ON agent_provider_binding(codex_thread_id) WHERE codex_thread_id <> '';
CREATE INDEX IF NOT EXISTS idx_acb_codex_thread ON agent_codex_binding(codex_thread_id);
CREATE INDEX IF NOT EXISTS idx_acb_created_at_desc ON agent_codex_binding(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_acb_cwd ON agent_codex_binding(cwd);

CREATE INDEX IF NOT EXISTS idx_agent_status_status_updated ON agent_status(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_status_updated_at_desc ON agent_status(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_threads_status ON agent_threads(status);
CREATE INDEX IF NOT EXISTS idx_agent_threads_port ON agent_threads(port);
CREATE INDEX IF NOT EXISTS idx_agent_threads_pid ON agent_threads(pid);
CREATE INDEX IF NOT EXISTS idx_agent_threads_workspace_run_key ON agent_threads(workspace_run_key);
CREATE INDEX IF NOT EXISTS idx_agent_threads_owner_thread_id ON agent_threads(owner_thread_id);
CREATE INDEX IF NOT EXISTS idx_agent_threads_agent_key ON agent_threads(agent_key) WHERE agent_key <> '';
CREATE INDEX IF NOT EXISTS idx_agent_threads_pending_launch ON agent_threads(pending_launch) WHERE pending_launch = 1;

CREATE INDEX IF NOT EXISTS idx_audit_events_ts ON audit_events(ts DESC);
CREATE INDEX IF NOT EXISTS idx_audit_events_event_type ON audit_events(event_type);
CREATE INDEX IF NOT EXISTS idx_audit_events_action ON audit_events(action);
CREATE INDEX IF NOT EXISTS idx_audit_events_result ON audit_events(result);
CREATE INDEX IF NOT EXISTS idx_audit_events_actor ON audit_events(actor);
CREATE INDEX IF NOT EXISTS idx_bus_exception_logs_ts ON bus_exception_logs(ts DESC);
CREATE INDEX IF NOT EXISTS idx_bus_exception_logs_category ON bus_exception_logs(category);
CREATE INDEX IF NOT EXISTS idx_bus_exception_logs_severity ON bus_exception_logs(severity);

CREATE INDEX IF NOT EXISTS idx_prompts_agent_key ON prompts(agent_key);
CREATE INDEX IF NOT EXISTS idx_prompts_sort_order ON prompts(sort_order, agent_key);
CREATE INDEX IF NOT EXISTS idx_prompt_templates_agent_tool ON prompt_templates(agent_key, tool_name);
CREATE INDEX IF NOT EXISTS idx_prompt_templates_enabled ON prompt_templates(enabled, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_prompt_templates_auto_route ON prompt_templates(enabled, priority DESC) WHERE match_when <> '{}';
CREATE INDEX IF NOT EXISTS idx_prompt_versions_key_id ON prompt_versions(prompt_key, id DESC);
CREATE INDEX IF NOT EXISTS idx_prompt_template_versions_key_id ON prompt_template_versions(prompt_key, id DESC);
CREATE INDEX IF NOT EXISTS idx_prompt_template_sections_lookup ON prompt_template_sections(template_id, enabled, region, ordinal);
CREATE INDEX IF NOT EXISTS idx_prompt_sections_recall_topic_lookup ON prompt_template_sections(recall_topic) WHERE trigger_type = 'recall' AND recall_topic <> '';
CREATE INDEX IF NOT EXISTS idx_prompt_intent_drafts_cwd_status_updated ON prompt_intent_drafts(cwd, status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_prompt_intent_drafts_kind_cwd ON prompt_intent_drafts(kind, cwd);

CREATE INDEX IF NOT EXISTS idx_command_cards_risk_enabled ON command_cards(risk_level, enabled, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_command_card_versions_key_id ON command_card_versions(card_key, id DESC);
CREATE INDEX IF NOT EXISTS idx_command_card_runs_status_created ON command_card_runs(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_command_card_runs_card_key ON command_card_runs(card_key, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_shared_files_updated_at ON shared_files(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_feedback_events_thread ON agent_feedback_events(thread_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_feedback_events_agent_key ON agent_feedback_events(agent_key, created_at DESC) WHERE agent_key <> '';
CREATE INDEX IF NOT EXISTS idx_agent_feedback_events_prompt_version ON agent_feedback_events(prompt_version_id, created_at DESC) WHERE prompt_version_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_feedback_events_event_type ON agent_feedback_events(event_type, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_session_insights_thread_created ON session_insights(thread_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_session_insights_created ON session_insights(created_at DESC, id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_session_insights_local_turn ON session_insights(thread_id, local_turn_id) WHERE thread_id <> '' AND local_turn_id <> '';
CREATE UNIQUE INDEX IF NOT EXISTS uq_session_insights_provider_turn ON session_insights(provider, agent_id, provider_turn_id) WHERE provider <> '' AND agent_id <> '' AND provider_turn_id <> '';
CREATE INDEX IF NOT EXISTS idx_session_insights_approval_observed ON session_insights(approval_requests_observed, thread_id, created_at DESC, id DESC) WHERE approval_requests_observed = 1;
CREATE INDEX IF NOT EXISTS idx_session_insights_token_observed ON session_insights(token_snapshot_observed, thread_id, created_at DESC, id DESC) WHERE token_snapshot_observed = 1;

CREATE INDEX IF NOT EXISTS idx_hook_pending_agent ON hook_pending_reviews(agent_id, status);
CREATE INDEX IF NOT EXISTS idx_hook_pending_deadline ON hook_pending_reviews(deadline_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_agent_interactions_thread_created ON agent_interactions(thread_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_interactions_sender_receiver ON agent_interactions(sender, receiver);
CREATE INDEX IF NOT EXISTS idx_agent_interactions_status_review ON agent_interactions(status, requires_review, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_topology_approvals_status_created_at ON topology_approvals(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_topology_approvals_arch_hash ON topology_approvals(arch_hash);
CREATE INDEX IF NOT EXISTS idx_topology_approval_archives_archived_at ON topology_approval_archives(archived_at DESC);
CREATE INDEX IF NOT EXISTS idx_ui_preferences_key ON ui_preferences(key);

CREATE INDEX IF NOT EXISTS idx_system_logs_ts_id ON system_logs(ts DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_system_logs_level_ts_id ON system_logs(level, ts DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_system_logs_source_ts_id ON system_logs(source, ts DESC, id DESC) WHERE source <> '';
CREATE INDEX IF NOT EXISTS idx_system_logs_agent_ts_id ON system_logs(agent_id, ts DESC, id DESC) WHERE agent_id <> '';
CREATE INDEX IF NOT EXISTS idx_system_logs_thread_ts_id ON system_logs(thread_id, ts DESC, id DESC) WHERE thread_id <> '';
CREATE INDEX IF NOT EXISTS idx_system_logs_logger ON system_logs(logger);
CREATE INDEX IF NOT EXISTS idx_system_logs_event ON system_logs(event_type) WHERE event_type <> '';
CREATE INDEX IF NOT EXISTS idx_system_logs_tool ON system_logs(tool_name) WHERE tool_name <> '';

CREATE INDEX IF NOT EXISTS idx_task_traces_trace_started ON task_traces(trace_id, started_at, id);
CREATE INDEX IF NOT EXISTS idx_task_traces_component_started ON task_traces(component, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_cwd_instance_locks_heartbeat ON cwd_instance_locks(heartbeat_at);
CREATE INDEX IF NOT EXISTS idx_turn_dedupe_registry_updated_at ON turn_dedupe_registry(updated_at);
CREATE INDEX IF NOT EXISTS idx_turn_dedupe_registry_live ON turn_dedupe_registry(dedupe_key) WHERE terminal_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_cron_jobs_due ON cron_jobs(COALESCE(next_retry_at, next_run_at)) WHERE enabled = 1;
CREATE INDEX IF NOT EXISTS idx_cron_jobs_claim ON cron_jobs(claimed_by, lease_expires_at) WHERE claim_token <> '';
CREATE UNIQUE INDEX IF NOT EXISTS uq_cron_job_runs_idempotency ON cron_job_runs(job_id, idempotency_key) WHERE job_id <> '' AND idempotency_key <> '';
CREATE UNIQUE INDEX IF NOT EXISTS uq_cron_job_runs_dedupe_key ON cron_job_runs(dedupe_key) WHERE dedupe_key <> '';
CREATE INDEX IF NOT EXISTS idx_cron_job_runs_job_created ON cron_job_runs(job_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_cron_job_runs_status_active ON cron_job_runs(status, updated_at DESC, id DESC) WHERE status IN ('pending', 'submitting', 'submitted', 'running');
CREATE INDEX IF NOT EXISTS idx_cron_job_runs_turn_running ON cron_job_runs(turn_id) WHERE turn_id <> '' AND status = 'running';
CREATE INDEX IF NOT EXISTS idx_datasource_v2_text_chunks_document_order ON datasource_v2_text_chunks(document_id, chunk_index);

CREATE INDEX IF NOT EXISTS idx_task_acks_status ON task_acks(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_acks_priority ON task_acks(priority, status);
CREATE INDEX IF NOT EXISTS idx_task_acks_assigned_to ON task_acks(assigned_to);
CREATE INDEX IF NOT EXISTS idx_task_acks_due_at ON task_acks(due_at);
CREATE INDEX IF NOT EXISTS idx_task_dags_status ON task_dags(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_dags_updated_id ON task_dags(updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_task_dags_next_run_scheduled ON task_dags(next_run_at) WHERE trigger = 'scheduled';

CREATE INDEX IF NOT EXISTS idx_task_dag_nodes_dag_key ON task_dag_nodes(dag_key, id);
CREATE INDEX IF NOT EXISTS idx_task_dag_nodes_status ON task_dag_nodes(status);
CREATE INDEX IF NOT EXISTS idx_task_dag_nodes_assigned_status ON task_dag_nodes(assigned_to, status);
CREATE INDEX IF NOT EXISTS idx_task_dag_nodes_run_id ON task_dag_nodes(run_id);
CREATE INDEX IF NOT EXISTS idx_task_dag_nodes_spawning_thread_id ON task_dag_nodes(spawning_thread_id) WHERE spawning_thread_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_task_dag_nodes_template_dag_node ON task_dag_nodes(dag_key, node_key) WHERE run_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_task_dag_nodes_runtime_dag_run_node ON task_dag_nodes(dag_key, run_id, node_key) WHERE run_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_task_dag_runs_run_key ON task_dag_runs(run_key);
CREATE INDEX IF NOT EXISTS idx_task_dag_runs_dag_key_started ON task_dag_runs(dag_key, started_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_task_dag_runs_dag_status_started ON task_dag_runs(dag_key, status, started_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_task_dag_runs_status ON task_dag_runs(status);
CREATE INDEX IF NOT EXISTS idx_task_dag_runs_running ON task_dag_runs(dag_key, started_at DESC, id DESC) WHERE status = 'running';

CREATE INDEX IF NOT EXISTS idx_task_dag_wakeups_poll ON task_dag_wakeups(status, next_retry_at, id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_task_dag_wakeups_sent_target ON task_dag_wakeups(target_agent_id, sent_at DESC, id DESC) WHERE status = 'sent' AND sent_at IS NOT NULL AND bound_turn_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_task_dag_wakeups_run_node ON task_dag_wakeups(run_id, dag_key, node_key);
CREATE INDEX IF NOT EXISTS idx_task_dag_wakeups_run_id ON task_dag_wakeups(run_id);

CREATE INDEX IF NOT EXISTS idx_workspace_runs_status_updated ON workspace_runs(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_workspace_runs_dag ON workspace_runs(dag_key, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_workspace_run_files_run_state ON workspace_run_files(run_key, state, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_workspace_run_files_run_path ON workspace_run_files(run_key, relative_path);
CREATE INDEX IF NOT EXISTS idx_runtime_locks_expiry ON runtime_locks(lease_expires_at);

CREATE TRIGGER IF NOT EXISTS trg_prevent_agent_codex_binding_rebind
BEFORE UPDATE ON agent_codex_binding
FOR EACH ROW
WHEN NEW.agent_id <> OLD.agent_id OR NEW.codex_thread_id <> OLD.codex_thread_id
BEGIN
    SELECT RAISE(ABORT, 'agent_codex_binding identity is immutable');
END;

CREATE TRIGGER IF NOT EXISTS trg_prevent_agent_provider_binding_rebind
BEFORE UPDATE ON agent_provider_binding
FOR EACH ROW
WHEN NEW.agent_id <> OLD.agent_id
  OR NEW.provider <> OLD.provider
  OR (OLD.provider_thread_id <> '' AND NEW.provider_thread_id <> OLD.provider_thread_id)
  OR (OLD.codex_instance_key <> '' AND NEW.codex_instance_key <> OLD.codex_instance_key)
  OR (OLD.codex_model_provider <> '' AND NEW.codex_model_provider <> OLD.codex_model_provider)
  OR (
      OLD.codex_home <> ''
      AND NEW.codex_home <> OLD.codex_home
      AND (
          (OLD.codex_instance_key <> '' AND NEW.codex_instance_key <> OLD.codex_instance_key)
          OR (OLD.codex_model_provider <> '' AND NEW.codex_model_provider <> OLD.codex_model_provider)
      )
  )
BEGIN
    SELECT RAISE(ABORT, 'agent_provider_binding identity is immutable');
END;

INSERT OR IGNORE INTO schema_migrations(version, name, filename, applied_at)
VALUES (103, 'sqlite baseline', '001_baseline.sql', 0);
