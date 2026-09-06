-- name: CreateDirectPeerGrant :one
INSERT INTO orchestration_direct_peer_grants (
    grant_id, workspace_root, sender_agent_id, receiver_agent_id, idempotency_key, request_digest,
    can_send, can_read, status, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active', 1, ?, ?)
RETURNING grant_id, workspace_root, sender_agent_id, receiver_agent_id, idempotency_key, request_digest,
          can_send, can_read, status, revision, created_at, updated_at;

-- name: GetDirectPeerGrant :one
SELECT grant_id, workspace_root, sender_agent_id, receiver_agent_id, idempotency_key, request_digest,
       can_send, can_read, status, revision, created_at, updated_at
FROM orchestration_direct_peer_grants
WHERE workspace_root = ? AND sender_agent_id = ? AND receiver_agent_id = ?;

-- name: GetDirectPeerGrantByKey :one
SELECT grant_id, workspace_root, sender_agent_id, receiver_agent_id, idempotency_key, request_digest,
       can_send, can_read, status, revision, created_at, updated_at
FROM orchestration_direct_peer_grants
WHERE workspace_root = ? AND idempotency_key = ?;

-- name: RevokeDirectPeerGrant :one
UPDATE orchestration_direct_peer_grants
SET status = 'revoked', can_send = 0, can_read = 0,
    idempotency_key = ?, request_digest = ?, revision = revision + 1, updated_at = ?
WHERE workspace_root = ? AND sender_agent_id = ? AND receiver_agent_id = ? AND status = 'active' AND revision = ?
RETURNING grant_id, workspace_root, sender_agent_id, receiver_agent_id, idempotency_key, request_digest,
          can_send, can_read, status, revision, created_at, updated_at;

-- name: CreateDirectPeerMessage :one
INSERT INTO orchestration_direct_peer_messages (
    message_id, idempotency_key, request_digest, workspace_root,
    sender_agent_id, receiver_agent_id, grant_revision, body, status, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'accepted', ?)
RETURNING id, message_id, idempotency_key, request_digest, workspace_root,
          sender_agent_id, receiver_agent_id, grant_revision, body, status,
          submission_id, delivery_error, delivered_at, created_at;

-- name: GetDirectPeerMessageByKey :one
SELECT m.id, m.message_id, m.idempotency_key, m.request_digest, m.workspace_root,
       m.sender_agent_id, m.receiver_agent_id, m.grant_revision, m.body, m.status,
       m.submission_id, m.delivery_error, m.delivered_at, m.created_at
FROM orchestration_direct_peer_messages m
WHERE m.workspace_root = ? AND m.sender_agent_id = ? AND m.idempotency_key = ?;

-- name: ListDirectPeerMessagesForReceiver :many
SELECT m.id, m.message_id, m.idempotency_key, m.request_digest, m.workspace_root,
       m.sender_agent_id, m.receiver_agent_id, m.grant_revision, m.body, m.status,
       m.submission_id, m.delivery_error, m.delivered_at, m.created_at
FROM orchestration_direct_peer_messages m
JOIN orchestration_direct_peer_grants g
  ON g.workspace_root = m.workspace_root
 AND g.sender_agent_id = m.sender_agent_id
 AND g.receiver_agent_id = m.receiver_agent_id
WHERE m.workspace_root = ? AND m.receiver_agent_id = ? AND m.id > ?
  AND g.status = 'active' AND g.can_read = 1
ORDER BY m.id ASC
LIMIT ?;

-- name: MarkDirectPeerMessageSubmitted :one
UPDATE orchestration_direct_peer_messages
SET status = 'submitted', submission_id = ?, delivery_error = '', delivered_at = ?
WHERE message_id = ? AND status = 'accepted'
RETURNING id, message_id, idempotency_key, request_digest, workspace_root,
          sender_agent_id, receiver_agent_id, grant_revision, body, status,
          submission_id, delivery_error, delivered_at, created_at;

-- name: MarkDirectPeerMessageDeliveryFailed :one
UPDATE orchestration_direct_peer_messages
SET status = 'delivery_failed', submission_id = '', delivery_error = ?, delivered_at = NULL
WHERE message_id = ? AND status = 'accepted'
RETURNING id, message_id, idempotency_key, request_digest, workspace_root,
          sender_agent_id, receiver_agent_id, grant_revision, body, status,
          submission_id, delivery_error, delivered_at, created_at;

-- name: CreateDirectPeerEvent :one
INSERT INTO orchestration_direct_peer_events (
    event_id, event_type, workspace_root, grant_id, message_id,
    sender_agent_id, receiver_agent_id, grant_revision, payload_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, event_id, event_type, workspace_root, grant_id, message_id,
          sender_agent_id, receiver_agent_id, grant_revision, payload_json, created_at;

-- name: CreateDirectPeerEventOutbox :exec
INSERT INTO orchestration_direct_peer_event_outbox (event_id, status, created_at)
VALUES (?, 'pending', ?);

-- name: ListDirectPeerEventsForReceiver :many
SELECT e.id, e.event_id, e.event_type, e.workspace_root, e.grant_id, e.message_id,
       e.sender_agent_id, e.receiver_agent_id, e.grant_revision, e.payload_json, e.created_at
FROM orchestration_direct_peer_events e
JOIN orchestration_direct_peer_grants g
  ON g.workspace_root = e.workspace_root
 AND g.sender_agent_id = e.sender_agent_id
 AND g.receiver_agent_id = e.receiver_agent_id
WHERE e.workspace_root = ? AND e.receiver_agent_id = ? AND e.id > ?
  AND g.status = 'active' AND g.can_read = 1
ORDER BY e.id ASC
LIMIT ?;

-- name: ListDirectPeerEventOutboxCandidates :many
SELECT o.id, o.event_id, o.status, o.claimed_by, o.claim_token, o.lease_expires_at,
       o.attempt_count, o.last_error, o.projected_at, o.created_at,
       e.id, e.event_id, e.event_type, e.workspace_root, e.grant_id, e.message_id,
       e.sender_agent_id, e.receiver_agent_id, e.grant_revision, e.payload_json, e.created_at
FROM orchestration_direct_peer_event_outbox o
JOIN orchestration_direct_peer_events e ON e.event_id = o.event_id
WHERE o.status = 'pending' OR (o.status = 'claimed' AND o.lease_expires_at <= ?)
ORDER BY o.id ASC
LIMIT ?;

-- name: ClaimDirectPeerEventOutbox :execrows
UPDATE orchestration_direct_peer_event_outbox
SET status = 'claimed', claimed_by = ?, claim_token = ?, lease_expires_at = ?,
    attempt_count = attempt_count + 1, last_error = ''
WHERE id = ? AND (status = 'pending' OR (status = 'claimed' AND lease_expires_at <= ?));

-- name: RenewDirectPeerEventOutbox :execrows
UPDATE orchestration_direct_peer_event_outbox
SET lease_expires_at = ?
WHERE id = ? AND status = 'claimed' AND claimed_by = ? AND claim_token = ?
  AND lease_expires_at > ?;

-- name: MarkDirectPeerEventOutboxProjected :execrows
UPDATE orchestration_direct_peer_event_outbox
SET status = 'projected', claimed_by = '', claim_token = '', lease_expires_at = NULL,
    projected_at = ?
WHERE id = ? AND status = 'claimed' AND claimed_by = ? AND claim_token = ?
  AND lease_expires_at > ?;

-- name: PoisonDirectPeerEventOutbox :execrows
UPDATE orchestration_direct_peer_event_outbox
SET status = 'poisoned', claimed_by = '', claim_token = '', lease_expires_at = NULL,
    attempt_count = attempt_count + 1, last_error = ?
WHERE id = ? AND status IN ('pending', 'claimed');
