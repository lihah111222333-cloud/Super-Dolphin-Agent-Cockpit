CREATE TABLE IF NOT EXISTS terminal_outcome_current_heads (
    agent_id TEXT PRIMARY KEY CHECK(agent_id <> ''),
    capability TEXT NOT NULL CHECK(capability = 'terminal_outcome_commit_v2'),
    public_thread_id TEXT NOT NULL CHECK(public_thread_id <> ''),
    provider_turn_id TEXT NOT NULL CHECK(provider_turn_id <> ''),
    session_id TEXT NOT NULL CHECK(session_id <> ''),
    generation INTEGER NOT NULL CHECK(generation > 0),
    expected_active_state TEXT NOT NULL CHECK(expected_active_state <> ''),
    version INTEGER NOT NULL CHECK(version > 0),
    state TEXT NOT NULL CHECK(state IN ('active', 'terminal')),
    terminal_event_id TEXT NOT NULL DEFAULT '',
    terminal_identity TEXT NOT NULL DEFAULT '',
    activated_at INTEGER NOT NULL CHECK(activated_at > 0),
    updated_at INTEGER NOT NULL CHECK(updated_at > 0),
    CHECK(
        (state = 'active' AND terminal_event_id = '' AND terminal_identity = '') OR
        (state = 'terminal' AND terminal_event_id <> '' AND terminal_identity <> '')
    )
);

CREATE TABLE IF NOT EXISTS public_terminal_outcome_history (
    terminal_identity TEXT PRIMARY KEY CHECK(terminal_identity <> ''),
    event_id TEXT NOT NULL UNIQUE CHECK(event_id <> ''),
    agent_id TEXT NOT NULL CHECK(agent_id <> ''),
    head_version INTEGER NOT NULL CHECK(head_version > 0),
    schema_version INTEGER NOT NULL CHECK(schema_version = 2),
    projection_kind TEXT NOT NULL CHECK(projection_kind IN ('turn_completed', 'agent_failed', 'agent_stopped', 'process_failed', 'process_stopped')),
    public_thread_id TEXT NOT NULL CHECK(public_thread_id <> ''),
    provider_turn_id TEXT NOT NULL CHECK(provider_turn_id <> ''),
    session_id TEXT NOT NULL CHECK(session_id <> ''),
    generation INTEGER NOT NULL CHECK(generation > 0),
    expected_active_state TEXT NOT NULL CHECK(expected_active_state <> ''),
    public_outcome_json TEXT NOT NULL CHECK(json_valid(public_outcome_json)),
    public_report TEXT NOT NULL CHECK(public_report <> ''),
    occurred_at INTEGER NOT NULL CHECK(occurred_at > 0)
);

CREATE INDEX IF NOT EXISTS idx_public_terminal_history_agent_generation
    ON public_terminal_outcome_history(agent_id, generation, head_version);

CREATE TABLE IF NOT EXISTS terminal_outcome_private_dag_payloads (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    terminal_identity TEXT NOT NULL UNIQUE CHECK(terminal_identity <> ''),
    owner_agent_id TEXT NOT NULL CHECK(owner_agent_id <> ''),
    public_thread_id TEXT NOT NULL CHECK(public_thread_id <> ''),
    provider_turn_id TEXT NOT NULL CHECK(provider_turn_id <> ''),
    payload_json TEXT NOT NULL CHECK(json_valid(payload_json)),
    created_at INTEGER NOT NULL CHECK(created_at > 0),
    FOREIGN KEY(terminal_identity) REFERENCES public_terminal_outcome_history(terminal_identity)
);

CREATE TABLE IF NOT EXISTS terminal_outcome_outbox_v2 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    terminal_identity TEXT NOT NULL UNIQUE CHECK(terminal_identity <> ''),
    event_id TEXT NOT NULL UNIQUE CHECK(event_id <> ''),
    public_payload_json TEXT NOT NULL CHECK(json_valid(public_payload_json)),
    private_dag_payload_id INTEGER,
    status TEXT NOT NULL CHECK(status IN ('pending', 'claimed', 'projected', 'poisoned')),
    claimed_by TEXT NOT NULL DEFAULT '',
    claim_token TEXT NOT NULL DEFAULT '',
    lease_expires_at INTEGER,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count >= 0),
    last_error TEXT NOT NULL DEFAULT '',
    projected_at INTEGER,
    created_at INTEGER NOT NULL CHECK(created_at > 0),
    FOREIGN KEY(terminal_identity) REFERENCES public_terminal_outcome_history(terminal_identity),
    FOREIGN KEY(private_dag_payload_id) REFERENCES terminal_outcome_private_dag_payloads(id),
    CHECK(
        (status IN ('pending', 'projected', 'poisoned') AND claimed_by = '' AND claim_token = '' AND lease_expires_at IS NULL) OR
        (status = 'claimed' AND claimed_by <> '' AND claim_token <> '' AND lease_expires_at IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_terminal_outcome_outbox_v2_claim
    ON terminal_outcome_outbox_v2(status, lease_expires_at, id)
    WHERE status IN ('pending', 'claimed');

INSERT OR IGNORE INTO terminal_outcome_current_heads (
    agent_id, capability, public_thread_id, provider_turn_id, session_id, generation,
    expected_active_state, version, state, terminal_event_id, terminal_identity,
    activated_at, updated_at
)
SELECT agent_id, capability, public_thread_id, provider_turn_id, session_id, generation,
       expected_active_state, 1, state,
       CASE WHEN state = 'terminal' THEN event_id ELSE '' END,
       CASE WHEN state = 'terminal' THEN terminal_identity ELSE '' END,
       updated_at, updated_at
FROM terminal_outcome_heads;

INSERT OR IGNORE INTO public_terminal_outcome_history (
    terminal_identity, event_id, agent_id, head_version, schema_version, projection_kind,
    public_thread_id, provider_turn_id, session_id, generation, expected_active_state,
    public_outcome_json, public_report, occurred_at
)
SELECT p.terminal_identity, p.event_id, p.agent_id, 1, p.schema_version, p.projection_kind,
       p.public_thread_id, p.provider_turn_id, p.session_id, p.generation, h.expected_active_state,
       p.public_outcome_json, p.public_report, p.occurred_at
FROM public_terminal_outcomes p
JOIN terminal_outcome_heads h
  ON h.agent_id = p.agent_id AND h.terminal_identity = p.terminal_identity;

INSERT OR IGNORE INTO terminal_outcome_outbox_v2 (
    id, terminal_identity, event_id, public_payload_json, private_dag_payload_id,
    status, claimed_by, claim_token, lease_expires_at, attempt_count, last_error,
    projected_at, created_at
)
SELECT o.id, p.terminal_identity, o.event_id,
       json_set(o.payload_json, '$.identity.headVersion', 1), NULL,
       CASE WHEN o.status = 'projected' THEN 'projected' ELSE 'pending' END,
       '', '', NULL, CASE WHEN o.status = 'claimed' THEN 1 ELSE 0 END, '',
       o.projected_at, o.created_at
FROM terminal_outcome_outbox o
JOIN public_terminal_outcomes p ON p.event_id = o.event_id;
