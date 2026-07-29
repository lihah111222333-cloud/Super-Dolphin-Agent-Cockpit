CREATE TABLE IF NOT EXISTS terminal_outcome_heads (
    agent_id TEXT PRIMARY KEY CHECK(agent_id <> ''),
    capability TEXT NOT NULL CHECK(capability = 'terminal_outcome_commit_v2'),
    public_thread_id TEXT NOT NULL CHECK(public_thread_id <> ''),
    provider_turn_id TEXT NOT NULL CHECK(provider_turn_id <> ''),
    session_id TEXT NOT NULL CHECK(session_id <> ''),
    generation INTEGER NOT NULL CHECK(generation > 0),
    event_id TEXT NOT NULL CHECK(event_id <> ''),
    terminal_identity TEXT NOT NULL CHECK(terminal_identity <> ''),
    expected_active_state TEXT NOT NULL CHECK(expected_active_state <> ''),
    state TEXT NOT NULL CHECK(state IN ('active', 'terminal')),
    updated_at INTEGER NOT NULL CHECK(updated_at > 0)
);

CREATE TABLE IF NOT EXISTS public_terminal_outcomes (
    agent_id TEXT PRIMARY KEY CHECK(agent_id <> ''),
    schema_version INTEGER NOT NULL CHECK(schema_version = 2),
    projection_kind TEXT NOT NULL CHECK(projection_kind IN ('turn_completed', 'agent_failed', 'agent_stopped', 'process_failed', 'process_stopped')),
    public_thread_id TEXT NOT NULL CHECK(public_thread_id <> ''),
    provider_turn_id TEXT NOT NULL CHECK(provider_turn_id <> ''),
    session_id TEXT NOT NULL CHECK(session_id <> ''),
    generation INTEGER NOT NULL CHECK(generation > 0),
    event_id TEXT NOT NULL UNIQUE CHECK(event_id <> ''),
    terminal_identity TEXT NOT NULL UNIQUE CHECK(terminal_identity <> ''),
    public_outcome_json TEXT NOT NULL CHECK(json_valid(public_outcome_json)),
    public_report TEXT NOT NULL CHECK(public_report <> ''),
    occurred_at INTEGER NOT NULL CHECK(occurred_at > 0)
);

CREATE TABLE IF NOT EXISTS terminal_outcome_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE CHECK(event_id <> ''),
    payload_json TEXT NOT NULL CHECK(json_valid(payload_json)),
    status TEXT NOT NULL CHECK(status IN ('pending', 'claimed', 'projected')),
    claimed_by TEXT NOT NULL DEFAULT '',
    claimed_at INTEGER,
    projected_at INTEGER,
    created_at INTEGER NOT NULL CHECK(created_at > 0),
    FOREIGN KEY(event_id) REFERENCES public_terminal_outcomes(event_id)
);

CREATE INDEX IF NOT EXISTS idx_terminal_outcome_outbox_claim
    ON terminal_outcome_outbox(status, claimed_at, id)
    WHERE status IN ('pending', 'claimed');
