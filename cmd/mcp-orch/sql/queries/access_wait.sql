-- Access grants are durable authorization snapshots. Lifecycle writes below
-- are all compare-and-swap operations; metadata is protected by migration
-- triggers and never rewritten in place.

-- name: CreateOrchestrationAccessGrant :one
INSERT INTO orchestration_access_grants (
    grant_id, caller_principal_ref, scope_ref, source_agent_id, target_agent_id,
    source_reservation_id, source_reservation_generation, source_incarnation_id,
    source_incarnation_generation, source_binding_version, source_binding_digest,
    source_fence_ref, target_reservation_id, target_reservation_generation,
    target_incarnation_id, target_incarnation_generation, target_binding_version,
    target_binding_digest, target_fence_ref, can_read, can_send,
    idempotency_namespace, idempotency_key_digest, request_digest, revision,
    status, issued_at, expires_at, schema_version
)
VALUES (
    sqlc.arg(grant_id), sqlc.arg(caller_principal_ref), sqlc.arg(scope_ref),
    sqlc.arg(source_agent_id), sqlc.arg(target_agent_id), sqlc.arg(source_reservation_id),
    sqlc.arg(source_reservation_generation), sqlc.arg(source_incarnation_id),
    sqlc.arg(source_incarnation_generation), sqlc.arg(source_binding_version),
    sqlc.arg(source_binding_digest), sqlc.arg(source_fence_ref), sqlc.arg(target_reservation_id),
    sqlc.arg(target_reservation_generation), sqlc.arg(target_incarnation_id),
    sqlc.arg(target_incarnation_generation), sqlc.arg(target_binding_version),
    sqlc.arg(target_binding_digest), sqlc.arg(target_fence_ref), sqlc.arg(can_read),
    sqlc.arg(can_send), sqlc.arg(idempotency_namespace), sqlc.arg(idempotency_key_digest),
    sqlc.arg(request_digest), 1, 'active', sqlc.arg(issued_at), sqlc.arg(expires_at), 1
)
ON CONFLICT(idempotency_namespace, idempotency_key_digest) DO NOTHING
RETURNING
    grant_id,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    target_agent_id,
    source_reservation_id,
    source_reservation_generation,
    source_incarnation_id,
    source_incarnation_generation,
    source_binding_version,
    source_binding_digest,
    source_fence_ref,
    target_reservation_id,
    target_reservation_generation,
    target_incarnation_id,
    target_incarnation_generation,
    target_binding_version,
    target_binding_digest,
    target_fence_ref,
    can_read,
    can_send,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    revision,
    status,
    issued_at,
    expires_at,
    created_at,
    updated_at,
    schema_version;

-- name: GetOrchestrationAccessGrant :one
SELECT
    grant_id,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    target_agent_id,
    source_reservation_id,
    source_reservation_generation,
    source_incarnation_id,
    source_incarnation_generation,
    source_binding_version,
    source_binding_digest,
    source_fence_ref,
    target_reservation_id,
    target_reservation_generation,
    target_incarnation_id,
    target_incarnation_generation,
    target_binding_version,
    target_binding_digest,
    target_fence_ref,
    can_read,
    can_send,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    revision,
    status,
    issued_at,
    expires_at,
    created_at,
    updated_at,
    schema_version
FROM orchestration_access_grants
WHERE grant_id = sqlc.arg(grant_id);

-- name: GetOrchestrationAccessGrantByIdempotencyKey :one
SELECT
    grant_id,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    target_agent_id,
    source_reservation_id,
    source_reservation_generation,
    source_incarnation_id,
    source_incarnation_generation,
    source_binding_version,
    source_binding_digest,
    source_fence_ref,
    target_reservation_id,
    target_reservation_generation,
    target_incarnation_id,
    target_incarnation_generation,
    target_binding_version,
    target_binding_digest,
    target_fence_ref,
    can_read,
    can_send,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    revision,
    status,
    issued_at,
    expires_at,
    created_at,
    updated_at,
    schema_version
FROM orchestration_access_grants
WHERE idempotency_namespace = sqlc.arg(idempotency_namespace)
  AND idempotency_key_digest = sqlc.arg(idempotency_key_digest);

-- name: ListOrchestrationAccessGrantsForCaller :many
SELECT
    grant_id,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    target_agent_id,
    source_reservation_id,
    source_reservation_generation,
    source_incarnation_id,
    source_incarnation_generation,
    source_binding_version,
    source_binding_digest,
    source_fence_ref,
    target_reservation_id,
    target_reservation_generation,
    target_incarnation_id,
    target_incarnation_generation,
    target_binding_version,
    target_binding_digest,
    target_fence_ref,
    can_read,
    can_send,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    revision,
    status,
    issued_at,
    expires_at,
    created_at,
    updated_at,
    schema_version
FROM orchestration_access_grants
WHERE caller_principal_ref = sqlc.arg(caller_principal_ref)
ORDER BY issued_at DESC, grant_id ASC;

-- name: ListOrchestrationAccessGrantExpiryCandidates :many
-- This query is intentionally narrow: expiry ownership only needs the grant
-- id, current revision, and immutable expiry instant.  The caller supplies
-- the security-time snapshot and a hard row bound; SQLite wall clock is never
-- consulted for candidate selection.
SELECT grant_id, revision, expires_at
FROM orchestration_access_grants
WHERE status = 'active'
  AND (julianday(expires_at) IS NULL OR julianday(expires_at) <= julianday(CAST(sqlc.arg(now_at) AS TEXT)))
ORDER BY julianday(expires_at) ASC, grant_id ASC
LIMIT sqlc.arg(limit);

-- name: RevokeOrchestrationAccessGrant :execrows
UPDATE orchestration_access_grants
SET status = 'revoked', revision = revision + 1,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE grant_id = sqlc.arg(grant_id)
  AND revision = sqlc.arg(expected_revision)
  AND status = 'active';

-- name: ExpireOrchestrationAccessGrant :execrows
UPDATE orchestration_access_grants
SET status = 'expired', revision = revision + 1,
    updated_at = sqlc.arg(now_at)
WHERE grant_id = sqlc.arg(grant_id)
  AND revision = sqlc.arg(expected_revision)
  AND status = 'active'
  AND julianday(expires_at) <= julianday(CAST(sqlc.arg(now_at) AS TEXT));

-- name: CreateOrchestrationEventWaitHandle :one
INSERT INTO orchestration_event_wait_handles (
    wait_handle_id, grant_id, grant_revision, caller_principal_ref, scope_ref,
    source_agent_id, target_agent_id, target_reservation_id, target_reservation_generation,
    target_incarnation_id, target_incarnation_generation, target_binding_version,
    target_binding_digest, target_fence_ref, source_id, source_epoch, lane,
    selector_version, selector_json, selector_digest, event_types_json,
    after_source_sequence, after_cursor, deadline_at, idempotency_namespace,
    idempotency_key_digest, request_digest, revision, status,
    matched_event_id, matched_source_sequence, matched_canonical_cursor,
    resolved_at, schema_version
)
VALUES (
    sqlc.arg(wait_handle_id), sqlc.arg(grant_id), sqlc.arg(grant_revision),
    sqlc.arg(caller_principal_ref), sqlc.arg(scope_ref), sqlc.arg(source_agent_id),
    sqlc.arg(target_agent_id), sqlc.arg(target_reservation_id), sqlc.arg(target_reservation_generation),
    sqlc.arg(target_incarnation_id), sqlc.arg(target_incarnation_generation),
    sqlc.arg(target_binding_version), sqlc.arg(target_binding_digest), sqlc.arg(target_fence_ref),
    sqlc.arg(source_id), sqlc.arg(source_epoch), sqlc.arg(lane), sqlc.arg(selector_version),
    sqlc.arg(selector_json), sqlc.arg(selector_digest), sqlc.arg(event_types_json),
    sqlc.arg(after_source_sequence), sqlc.arg(after_cursor), sqlc.arg(deadline_at),
    sqlc.arg(idempotency_namespace), sqlc.arg(idempotency_key_digest), sqlc.arg(request_digest),
    1, 'pending', '', 0, '', '', 1
)
ON CONFLICT(idempotency_namespace, idempotency_key_digest) DO NOTHING
RETURNING
    wait_handle_id,
    grant_id,
    grant_revision,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    target_agent_id,
    target_reservation_id,
    target_reservation_generation,
    target_incarnation_id,
    target_incarnation_generation,
    target_binding_version,
    target_binding_digest,
    target_fence_ref,
    source_id,
    source_epoch,
    lane,
    selector_version,
    selector_json,
    selector_digest,
    event_types_json,
    after_source_sequence,
    after_cursor,
    deadline_at,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    revision,
    status,
    matched_event_id,
    matched_source_sequence,
    matched_canonical_cursor,
    resolved_at,
    created_at,
    updated_at,
    schema_version;

-- name: GetOrchestrationEventWaitHandle :one
SELECT
    wait_handle_id,
    grant_id,
    grant_revision,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    target_agent_id,
    target_reservation_id,
    target_reservation_generation,
    target_incarnation_id,
    target_incarnation_generation,
    target_binding_version,
    target_binding_digest,
    target_fence_ref,
    source_id,
    source_epoch,
    lane,
    selector_version,
    selector_json,
    selector_digest,
    event_types_json,
    after_source_sequence,
    after_cursor,
    deadline_at,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    revision,
    status,
    matched_event_id,
    matched_source_sequence,
    matched_canonical_cursor,
    resolved_at,
    created_at,
    updated_at,
    schema_version
FROM orchestration_event_wait_handles
WHERE wait_handle_id = sqlc.arg(wait_handle_id);

-- name: GetOrchestrationEventWaitHandleByIdempotencyKey :one
SELECT
    wait_handle_id,
    grant_id,
    grant_revision,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    target_agent_id,
    target_reservation_id,
    target_reservation_generation,
    target_incarnation_id,
    target_incarnation_generation,
    target_binding_version,
    target_binding_digest,
    target_fence_ref,
    source_id,
    source_epoch,
    lane,
    selector_version,
    selector_json,
    selector_digest,
    event_types_json,
    after_source_sequence,
    after_cursor,
    deadline_at,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    revision,
    status,
    matched_event_id,
    matched_source_sequence,
    matched_canonical_cursor,
    resolved_at,
    created_at,
    updated_at,
    schema_version
FROM orchestration_event_wait_handles
WHERE idempotency_namespace = sqlc.arg(idempotency_namespace)
  AND idempotency_key_digest = sqlc.arg(idempotency_key_digest);

-- name: ListOrchestrationEventWaitHandlesByGrant :many
SELECT
    wait_handle_id,
    grant_id,
    grant_revision,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    target_agent_id,
    target_reservation_id,
    target_reservation_generation,
    target_incarnation_id,
    target_incarnation_generation,
    target_binding_version,
    target_binding_digest,
    target_fence_ref,
    source_id,
    source_epoch,
    lane,
    selector_version,
    selector_json,
    selector_digest,
    event_types_json,
    after_source_sequence,
    after_cursor,
    deadline_at,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    revision,
    status,
    matched_event_id,
    matched_source_sequence,
    matched_canonical_cursor,
    resolved_at,
    created_at,
    updated_at,
    schema_version
FROM orchestration_event_wait_handles
WHERE grant_id = sqlc.arg(grant_id)
ORDER BY created_at ASC, wait_handle_id ASC;

-- name: SatisfyOrchestrationEventWaitHandle :execrows
UPDATE orchestration_event_wait_handles
SET status = 'satisfied', revision = revision + 1,
    matched_event_id = sqlc.arg(matched_event_id),
    matched_source_sequence = sqlc.arg(matched_source_sequence),
    matched_canonical_cursor = sqlc.arg(matched_canonical_cursor),
    resolved_at = sqlc.arg(resolved_at),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE wait_handle_id = sqlc.arg(wait_handle_id)
  AND grant_id = sqlc.arg(grant_id)
  AND revision = sqlc.arg(expected_revision)
  AND status = 'pending';

-- name: TimeoutOrchestrationEventWaitHandle :execrows
UPDATE orchestration_event_wait_handles
SET status = 'timed_out', revision = revision + 1,
    resolved_at = sqlc.arg(resolved_at),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE wait_handle_id = sqlc.arg(wait_handle_id)
  AND revision = sqlc.arg(expected_revision)
  AND status = 'pending'
  AND deadline_at <= sqlc.arg(now_at);

-- name: CancelOrchestrationEventWaitHandle :execrows
UPDATE orchestration_event_wait_handles
SET status = 'cancelled', revision = revision + 1,
    resolved_at = sqlc.arg(resolved_at),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE wait_handle_id = sqlc.arg(wait_handle_id)
  AND revision = sqlc.arg(expected_revision)
  AND status = 'pending';

-- name: RevokeOrchestrationEventWaitHandle :execrows
UPDATE orchestration_event_wait_handles
SET status = 'revoked', revision = revision + 1,
    resolved_at = sqlc.arg(resolved_at),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE wait_handle_id = sqlc.arg(wait_handle_id)
  AND grant_id = sqlc.arg(grant_id)
  AND revision = sqlc.arg(expected_revision)
  AND status = 'pending';

-- name: ExpireOrchestrationEventWaitHandle :execrows
UPDATE orchestration_event_wait_handles
SET status = 'expired', revision = revision + 1,
    resolved_at = sqlc.arg(resolved_at),
    updated_at = sqlc.arg(resolved_at)
WHERE wait_handle_id = sqlc.arg(wait_handle_id)
  AND revision = sqlc.arg(expected_revision)
  AND status = 'pending'
  AND deadline_at <= sqlc.arg(now_at);

-- name: ExpireOrchestrationEventWaitHandleByGrant :execrows
UPDATE orchestration_event_wait_handles
SET status = 'expired', revision = revision + 1,
    resolved_at = sqlc.arg(resolved_at),
    updated_at = sqlc.arg(resolved_at)
WHERE wait_handle_id = sqlc.arg(wait_handle_id)
  AND grant_id = sqlc.arg(grant_id)
  AND revision = sqlc.arg(expected_revision)
  AND status = 'pending';
