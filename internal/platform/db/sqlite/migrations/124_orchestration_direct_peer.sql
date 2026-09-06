CREATE TABLE orchestration_direct_peer_grants (
    grant_id TEXT PRIMARY KEY CHECK(length(trim(grant_id)) > 0),
    workspace_root TEXT NOT NULL CHECK(length(trim(workspace_root)) > 0),
    sender_agent_id TEXT NOT NULL CHECK(length(trim(sender_agent_id)) > 0),
    receiver_agent_id TEXT NOT NULL CHECK(length(trim(receiver_agent_id)) > 0),
    idempotency_key TEXT NOT NULL CHECK(length(trim(idempotency_key)) > 0),
    request_digest TEXT NOT NULL CHECK(length(request_digest) = 64),
    can_send INTEGER NOT NULL CHECK(can_send IN (0, 1)),
    can_read INTEGER NOT NULL CHECK(can_read IN (0, 1)),
    status TEXT NOT NULL CHECK(status IN ('active', 'revoked')),
    revision INTEGER NOT NULL CHECK(revision > 0),
    created_at INTEGER NOT NULL CHECK(created_at > 0),
    updated_at INTEGER NOT NULL CHECK(updated_at > 0),
    UNIQUE(workspace_root, sender_agent_id, receiver_agent_id),
    UNIQUE(workspace_root, idempotency_key),
    CHECK(sender_agent_id <> receiver_agent_id)
);

CREATE TABLE orchestration_direct_peer_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id TEXT NOT NULL UNIQUE CHECK(length(trim(message_id)) > 0),
    idempotency_key TEXT NOT NULL CHECK(length(trim(idempotency_key)) > 0),
    request_digest TEXT NOT NULL CHECK(length(request_digest) = 64),
    workspace_root TEXT NOT NULL CHECK(length(trim(workspace_root)) > 0),
    sender_agent_id TEXT NOT NULL CHECK(length(trim(sender_agent_id)) > 0),
    receiver_agent_id TEXT NOT NULL CHECK(length(trim(receiver_agent_id)) > 0),
    grant_revision INTEGER NOT NULL CHECK(grant_revision > 0),
    body TEXT NOT NULL CHECK(length(body) > 0),
    status TEXT NOT NULL CHECK(status IN ('accepted', 'submitted', 'delivery_failed')),
    submission_id TEXT NOT NULL DEFAULT '',
    delivery_error TEXT NOT NULL DEFAULT '',
    delivered_at INTEGER,
    created_at INTEGER NOT NULL CHECK(created_at > 0),
    UNIQUE(workspace_root, sender_agent_id, idempotency_key),
    FOREIGN KEY(workspace_root, sender_agent_id, receiver_agent_id)
      REFERENCES orchestration_direct_peer_grants(workspace_root, sender_agent_id, receiver_agent_id)
      ON DELETE RESTRICT,
    CHECK(
      (status = 'accepted' AND submission_id = '' AND delivery_error = '' AND delivered_at IS NULL) OR
      (status = 'submitted' AND submission_id <> '' AND delivery_error = '' AND delivered_at IS NOT NULL) OR
      (status = 'delivery_failed' AND submission_id = '' AND delivery_error <> '' AND delivered_at IS NULL)
    )
);

CREATE INDEX idx_direct_peer_messages_receiver_cursor
    ON orchestration_direct_peer_messages(workspace_root, receiver_agent_id, id);

CREATE INDEX idx_direct_peer_messages_sender_cursor
    ON orchestration_direct_peer_messages(workspace_root, sender_agent_id, id);
