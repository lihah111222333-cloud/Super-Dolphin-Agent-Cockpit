-- name: CreateOrchestrationEventSourceState :one
INSERT INTO orchestration_event_source_states (
    source_id, source_epoch, lane, next_sequence, materialized_sequence,
    dedup_floor, revision, status, gap_reason, writer_epoch
)
VALUES (
    sqlc.arg(source_id), sqlc.arg(source_epoch), sqlc.arg(lane),
    1, 0, 0, 1, 'active', '', sqlc.arg(writer_epoch)
)
RETURNING
    source_id,
    source_epoch,
    lane,
    next_sequence,
    materialized_sequence,
    dedup_floor,
    revision,
    status,
    gap_reason,
    writer_epoch,
    created_at,
    updated_at;

-- name: GetOrchestrationEventSourceState :one
SELECT
    source_id,
    source_epoch,
    lane,
    next_sequence,
    materialized_sequence,
    dedup_floor,
    revision,
    status,
    gap_reason,
    writer_epoch,
    created_at,
    updated_at
FROM orchestration_event_source_states
WHERE source_id = sqlc.arg(source_id)
  AND source_epoch = sqlc.arg(source_epoch)
  AND lane = sqlc.arg(lane);

-- name: ListOrchestrationEventSourceStatesBySource :many
SELECT
    source_id,
    source_epoch,
    lane,
    next_sequence,
    materialized_sequence,
    dedup_floor,
    revision,
    status,
    gap_reason,
    writer_epoch,
    created_at,
    updated_at
FROM orchestration_event_source_states
WHERE source_id = sqlc.arg(source_id)
ORDER BY source_epoch ASC, lane ASC;

-- name: UpdateOrchestrationEventSourceState :execrows
UPDATE orchestration_event_source_states
SET next_sequence = sqlc.arg(next_sequence),
    materialized_sequence = sqlc.arg(materialized_sequence),
    dedup_floor = sqlc.arg(dedup_floor),
    revision = sqlc.arg(next_revision),
    status = sqlc.arg(status),
    gap_reason = sqlc.arg(gap_reason),
    writer_epoch = sqlc.arg(writer_epoch),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE source_id = sqlc.arg(source_id)
  AND source_epoch = sqlc.arg(source_epoch)
  AND lane = sqlc.arg(lane)
  AND revision = sqlc.arg(expected_revision);

-- name: GetOrchestrationEventIngressReceipt :one
SELECT
    source_id,
    source_epoch,
    lane,
    source_sequence,
    event_id,
    envelope_hash,
    status,
    event_type,
    schema_version,
    root_agent_id,
    subject_agent_id,
    reservation_id,
    incarnation_id,
    external_epoch,
    incarnation_generation,
    identity_digest,
    binding_version,
    binding_digest,
    source_fence_ref,
    provenance,
    trust_class,
    content_role,
    content_ref,
    content_size,
    media_type,
    provider_at,
    observed_at,
    writer_epoch,
    canonical_cursor,
    error_ref
FROM orchestration_event_ingress_receipts
WHERE source_id = sqlc.arg(source_id)
  AND source_epoch = sqlc.arg(source_epoch)
  AND lane = sqlc.arg(lane)
  AND source_sequence = sqlc.arg(source_sequence);

-- name: GetOrchestrationEventIngressReceiptAtSequence :one
SELECT
    source_id,
    source_epoch,
    lane,
    source_sequence,
    event_id,
    envelope_hash,
    status,
    event_type,
    schema_version,
    root_agent_id,
    subject_agent_id,
    reservation_id,
    incarnation_id,
    external_epoch,
    incarnation_generation,
    identity_digest,
    binding_version,
    binding_digest,
    source_fence_ref,
    provenance,
    trust_class,
    content_role,
    content_ref,
    content_size,
    media_type,
    provider_at,
    observed_at,
    writer_epoch,
    canonical_cursor,
    error_ref
FROM orchestration_event_ingress_receipts
WHERE source_id = sqlc.arg(source_id)
  AND source_epoch = sqlc.arg(source_epoch)
  AND lane = sqlc.arg(lane)
  AND source_sequence = sqlc.arg(source_sequence)
  AND status = 'staged';

-- name: HasOrchestrationEventIngressStagedAfter :one
SELECT EXISTS(
    SELECT 1
    FROM orchestration_event_ingress_receipts
    WHERE source_id = sqlc.arg(source_id)
      AND source_epoch = sqlc.arg(source_epoch)
      AND lane = sqlc.arg(lane)
      AND source_sequence >= sqlc.arg(from_sequence)
      AND status = 'staged'
);

-- name: InsertOrchestrationEventIngressReceipt :one
INSERT INTO orchestration_event_ingress_receipts (
    source_id, source_epoch, lane, source_sequence, event_id, envelope_hash,
    status, event_type, schema_version, root_agent_id, subject_agent_id,
    reservation_id, incarnation_id, external_epoch, incarnation_generation,
    identity_digest, binding_version, binding_digest, source_fence_ref,
    provenance, trust_class, content_role, content_ref, content_size, media_type,
    provider_at, observed_at, writer_epoch, canonical_cursor, error_ref
)
VALUES (
    sqlc.arg(source_id), sqlc.arg(source_epoch), sqlc.arg(lane), sqlc.arg(source_sequence),
    sqlc.arg(event_id), sqlc.arg(envelope_hash), sqlc.arg(status), sqlc.arg(event_type),
    sqlc.arg(schema_version), sqlc.arg(root_agent_id), sqlc.arg(subject_agent_id),
    sqlc.arg(reservation_id), sqlc.narg(incarnation_id), sqlc.arg(external_epoch),
    sqlc.arg(incarnation_generation), sqlc.arg(identity_digest), sqlc.arg(binding_version),
    sqlc.arg(binding_digest), sqlc.arg(source_fence_ref), sqlc.arg(provenance),
    sqlc.arg(trust_class), sqlc.arg(content_role), sqlc.arg(content_ref), sqlc.arg(content_size),
    sqlc.arg(media_type), sqlc.narg(provider_at), sqlc.arg(observed_at), sqlc.arg(writer_epoch),
    sqlc.arg(canonical_cursor), sqlc.arg(error_ref)
)
RETURNING
    source_id,
    source_epoch,
    lane,
    source_sequence,
    event_id,
    envelope_hash,
    status,
    event_type,
    schema_version,
    root_agent_id,
    subject_agent_id,
    reservation_id,
    incarnation_id,
    external_epoch,
    incarnation_generation,
    identity_digest,
    binding_version,
    binding_digest,
    source_fence_ref,
    provenance,
    trust_class,
    content_role,
    content_ref,
    content_size,
    media_type,
    provider_at,
    observed_at,
    writer_epoch,
    canonical_cursor,
    error_ref;

-- name: UpdateOrchestrationEventIngressReceiptStatus :execrows
UPDATE orchestration_event_ingress_receipts
SET status = sqlc.arg(next_status), error_ref = sqlc.arg(error_ref)
WHERE source_id = sqlc.arg(source_id)
  AND source_epoch = sqlc.arg(source_epoch)
  AND lane = sqlc.arg(lane)
  AND source_sequence = sqlc.arg(source_sequence)
  AND status = sqlc.arg(expected_status);

-- name: GetOrchestrationAgentEvent :one
SELECT
    event_id,
    source_id,
    source_epoch,
    lane,
    source_sequence,
    canonical_cursor,
    envelope_hash,
    status,
    event_type,
    schema_version,
    root_agent_id,
    subject_agent_id,
    reservation_id,
    incarnation_id,
    external_epoch,
    incarnation_generation,
    identity_digest,
    binding_version,
    binding_digest,
    source_fence_ref,
    provenance,
    trust_class,
    content_role,
    content_ref,
    content_size,
    media_type,
    provider_at,
    observed_at,
    writer_epoch
FROM orchestration_agent_events
WHERE event_id = sqlc.arg(event_id);

-- name: GetOrchestrationAgentEventBySourceSequence :one
SELECT
    event_id,
    source_id,
    source_epoch,
    lane,
    source_sequence,
    canonical_cursor,
    envelope_hash,
    status,
    event_type,
    schema_version,
    root_agent_id,
    subject_agent_id,
    reservation_id,
    incarnation_id,
    external_epoch,
    incarnation_generation,
    identity_digest,
    binding_version,
    binding_digest,
    source_fence_ref,
    provenance,
    trust_class,
    content_role,
    content_ref,
    content_size,
    media_type,
    provider_at,
    observed_at,
    writer_epoch
FROM orchestration_agent_events
WHERE source_id = sqlc.arg(source_id)
  AND source_epoch = sqlc.arg(source_epoch)
  AND lane = sqlc.arg(lane)
  AND source_sequence = sqlc.arg(source_sequence);

-- name: InsertOrchestrationAgentEvent :one
INSERT INTO orchestration_agent_events (
    event_id, source_id, source_epoch, lane, source_sequence, canonical_cursor,
    envelope_hash, status, event_type, schema_version, root_agent_id,
    subject_agent_id, reservation_id, incarnation_id, external_epoch,
    incarnation_generation, identity_digest, binding_version, binding_digest,
    source_fence_ref, provenance, trust_class, content_role, content_ref,
    content_size, media_type, provider_at, observed_at, writer_epoch
)
VALUES (
    sqlc.arg(event_id), sqlc.arg(source_id), sqlc.arg(source_epoch), sqlc.arg(lane),
    sqlc.arg(source_sequence), sqlc.arg(canonical_cursor), sqlc.arg(envelope_hash),
    'materialized', sqlc.arg(event_type), sqlc.arg(schema_version), sqlc.arg(root_agent_id),
    sqlc.arg(subject_agent_id), sqlc.arg(reservation_id), sqlc.narg(incarnation_id),
    sqlc.arg(external_epoch), sqlc.arg(incarnation_generation), sqlc.arg(identity_digest),
    sqlc.arg(binding_version), sqlc.arg(binding_digest), sqlc.arg(source_fence_ref),
    sqlc.arg(provenance), sqlc.arg(trust_class), sqlc.arg(content_role), sqlc.arg(content_ref),
    sqlc.arg(content_size), sqlc.arg(media_type), sqlc.narg(provider_at),
    sqlc.arg(observed_at), sqlc.arg(writer_epoch)
)
RETURNING
    event_id,
    source_id,
    source_epoch,
    lane,
    source_sequence,
    canonical_cursor,
    envelope_hash,
    status,
    event_type,
    schema_version,
    root_agent_id,
    subject_agent_id,
    reservation_id,
    incarnation_id,
    external_epoch,
    incarnation_generation,
    identity_digest,
    binding_version,
    binding_digest,
    source_fence_ref,
    provenance,
    trust_class,
    content_role,
    content_ref,
    content_size,
    media_type,
    provider_at,
    observed_at,
    writer_epoch;

-- name: ListOrchestrationAgentEvents :many
SELECT
    event_id,
    source_id,
    source_epoch,
    lane,
    source_sequence,
    canonical_cursor,
    envelope_hash,
    status,
    event_type,
    schema_version,
    root_agent_id,
    subject_agent_id,
    reservation_id,
    incarnation_id,
    external_epoch,
    incarnation_generation,
    identity_digest,
    binding_version,
    binding_digest,
    source_fence_ref,
    provenance,
    trust_class,
    content_role,
    content_ref,
    content_size,
    media_type,
    provider_at,
    observed_at,
    writer_epoch
FROM orchestration_agent_events
WHERE source_id = sqlc.arg(source_id)
  AND source_epoch = sqlc.arg(source_epoch)
  AND lane = sqlc.arg(lane)
  AND source_sequence > sqlc.arg(after_sequence)
ORDER BY source_sequence ASC
LIMIT sqlc.arg(limit);

-- name: GetOrchestrationEventTombstone :one
SELECT
    source_id,
    source_epoch,
    lane,
    source_sequence,
    envelope_hash,
    event_id,
    canonical_cursor,
    dedup_floor,
    status,
    created_at
FROM orchestration_event_tombstones
WHERE source_id = sqlc.arg(source_id)
  AND source_epoch = sqlc.arg(source_epoch)
  AND lane = sqlc.arg(lane)
  AND source_sequence = sqlc.arg(source_sequence);

-- name: InsertOrchestrationEventTombstone :one
INSERT INTO orchestration_event_tombstones (
    source_id, source_epoch, lane, source_sequence, envelope_hash, event_id,
    canonical_cursor, dedup_floor, status
)
VALUES (
    sqlc.arg(source_id), sqlc.arg(source_epoch), sqlc.arg(lane), sqlc.arg(source_sequence),
    sqlc.arg(envelope_hash), sqlc.arg(event_id), sqlc.arg(canonical_cursor),
    sqlc.arg(dedup_floor), sqlc.arg(status)
)
RETURNING
    source_id,
    source_epoch,
    lane,
    source_sequence,
    envelope_hash,
    event_id,
    canonical_cursor,
    dedup_floor,
    status,
    created_at;

-- name: GetOrchestrationEventMaterializationCursor :one
SELECT
    projection_id,
    source_id,
    source_epoch,
    lane,
    materialized_sequence,
    materialized_hash,
    revision,
    status,
    updated_at
FROM orchestration_event_materialization_cursors
WHERE projection_id = sqlc.arg(projection_id)
  AND source_id = sqlc.arg(source_id)
  AND source_epoch = sqlc.arg(source_epoch)
  AND lane = sqlc.arg(lane);

-- name: InsertOrchestrationEventMaterializationCursor :one
INSERT INTO orchestration_event_materialization_cursors (
    projection_id, source_id, source_epoch, lane, materialized_sequence,
    materialized_hash, revision, status
)
VALUES (
    sqlc.arg(projection_id), sqlc.arg(source_id), sqlc.arg(source_epoch),
    sqlc.arg(lane), sqlc.arg(materialized_sequence), sqlc.arg(materialized_hash),
    1, sqlc.arg(status)
)
RETURNING
    projection_id,
    source_id,
    source_epoch,
    lane,
    materialized_sequence,
    materialized_hash,
    revision,
    status,
    updated_at;

-- name: UpdateOrchestrationEventMaterializationCursor :execrows
UPDATE orchestration_event_materialization_cursors
SET materialized_sequence = sqlc.arg(materialized_sequence),
    materialized_hash = sqlc.arg(materialized_hash),
    revision = sqlc.arg(next_revision),
    status = sqlc.arg(status),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE projection_id = sqlc.arg(projection_id)
  AND source_id = sqlc.arg(source_id)
  AND source_epoch = sqlc.arg(source_epoch)
  AND lane = sqlc.arg(lane)
  AND revision = sqlc.arg(expected_revision);
