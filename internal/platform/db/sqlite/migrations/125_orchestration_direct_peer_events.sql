CREATE TABLE orchestration_direct_peer_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE CHECK(length(trim(event_id)) > 0),
    event_type TEXT NOT NULL CHECK(event_type IN (
        'grant.active', 'grant.revoked', 'message.accepted',
        'message.submitted', 'message.delivery_failed'
    )),
    workspace_root TEXT NOT NULL CHECK(length(trim(workspace_root)) > 0),
    grant_id TEXT NOT NULL DEFAULT '',
    message_id TEXT NOT NULL DEFAULT '',
    sender_agent_id TEXT NOT NULL CHECK(length(trim(sender_agent_id)) > 0),
    receiver_agent_id TEXT NOT NULL CHECK(length(trim(receiver_agent_id)) > 0),
    grant_revision INTEGER NOT NULL CHECK(grant_revision > 0),
    payload_json TEXT NOT NULL CHECK(json_valid(payload_json) AND length(payload_json) <= 4194304),
    created_at INTEGER NOT NULL CHECK(created_at > 0),
    CHECK(sender_agent_id <> receiver_agent_id),
    CHECK(
        (event_type LIKE 'grant.%' AND grant_id <> '' AND message_id = '') OR
        (event_type LIKE 'message.%' AND grant_id = '' AND message_id <> '')
    )
);

CREATE UNIQUE INDEX idx_direct_peer_events_grant_type
    ON orchestration_direct_peer_events(grant_id, event_type)
    WHERE grant_id <> '';

CREATE UNIQUE INDEX idx_direct_peer_events_message_type
    ON orchestration_direct_peer_events(message_id, event_type)
    WHERE message_id <> '';

CREATE TRIGGER trg_direct_peer_event_grant_exists
BEFORE INSERT ON orchestration_direct_peer_events
WHEN NEW.grant_id <> '' AND NOT EXISTS (
    SELECT 1 FROM orchestration_direct_peer_grants WHERE grant_id = NEW.grant_id
)
BEGIN
    SELECT RAISE(ABORT, 'direct peer event references missing grant');
END;

CREATE TRIGGER trg_direct_peer_event_message_exists
BEFORE INSERT ON orchestration_direct_peer_events
WHEN NEW.message_id <> '' AND NOT EXISTS (
    SELECT 1 FROM orchestration_direct_peer_messages WHERE message_id = NEW.message_id
)
BEGIN
    SELECT RAISE(ABORT, 'direct peer event references missing message');
END;

CREATE INDEX idx_direct_peer_events_receiver_cursor
    ON orchestration_direct_peer_events(workspace_root, receiver_agent_id, id);

CREATE TABLE orchestration_direct_peer_event_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK(status IN ('pending', 'claimed', 'projected', 'poisoned')),
    claimed_by TEXT NOT NULL DEFAULT '',
    claim_token TEXT NOT NULL DEFAULT '',
    lease_expires_at INTEGER,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count >= 0),
    last_error TEXT NOT NULL DEFAULT '',
    projected_at INTEGER,
    created_at INTEGER NOT NULL CHECK(created_at > 0),
    FOREIGN KEY(event_id) REFERENCES orchestration_direct_peer_events(event_id),
    CHECK(
        (status IN ('pending', 'projected', 'poisoned') AND claimed_by = '' AND claim_token = '' AND lease_expires_at IS NULL) OR
        (status = 'claimed' AND claimed_by <> '' AND claim_token <> '' AND lease_expires_at IS NOT NULL)
    )
);

CREATE INDEX idx_direct_peer_event_outbox_claim
    ON orchestration_direct_peer_event_outbox(status, lease_expires_at, id)
    WHERE status IN ('pending', 'claimed');
