-- Durable wait-operation owner queries.  These statements only create/read the
-- operation plan and append target journal facts; they do not provision grants,
-- wait handles, provider effects, or public tool responses.

-- name: CreateOrchestrationWaitOperation :one
INSERT INTO orchestration_wait_operations (
    operation_key, operation_digest, target_set_digest,
    caller_principal_ref, scope_ref,
    source_agent_id, source_reservation_id, source_reservation_generation,
    source_incarnation_id, source_incarnation_generation,
    source_binding_version, source_binding_digest, source_fence_ref,
    issued_at, deadline_at, target_count, revision, status,
    uncertain_phase, cleanup_request_digest, cleanup_reason_ref, resolved_at,
    created_at, updated_at, schema_version
) VALUES (
    sqlc.arg(operation_key), sqlc.arg(operation_digest), sqlc.arg(target_set_digest),
    sqlc.arg(caller_principal_ref), sqlc.arg(scope_ref),
    sqlc.arg(source_agent_id), sqlc.arg(source_reservation_id), sqlc.arg(source_reservation_generation),
    sqlc.arg(source_incarnation_id), sqlc.arg(source_incarnation_generation),
    sqlc.arg(source_binding_version), sqlc.arg(source_binding_digest), sqlc.arg(source_fence_ref),
    sqlc.arg(issued_at), sqlc.arg(deadline_at), sqlc.arg(target_count), 1, 'provisioning',
    '', '', '', '', sqlc.arg(created_at), sqlc.arg(updated_at), 1
)
ON CONFLICT(operation_key) DO NOTHING
RETURNING
    operation_key, operation_digest, target_set_digest,
    caller_principal_ref, scope_ref,
    source_agent_id, source_reservation_id, source_reservation_generation,
    source_incarnation_id, source_incarnation_generation,
    source_binding_version, source_binding_digest, source_fence_ref,
    issued_at, deadline_at, target_count, revision, status,
    uncertain_phase, cleanup_request_digest, cleanup_reason_ref, resolved_at,
    created_at, updated_at, schema_version;

-- name: CreateOrchestrationWaitTargetPlan :one
INSERT INTO orchestration_wait_operation_target_journal (
    operation_key, operation_digest, target_key, target_index, version,
    target_digest, record_digest, previous_record_digest,
    target_agent_id, target_reservation_id, target_reservation_generation,
    target_incarnation_id, target_incarnation_generation,
    target_binding_version, target_binding_digest, target_fence_ref,
    source_id, source_epoch, lane, selector_version, selector_json,
    selector_digest, event_types_json, after_source_sequence, after_cursor,
    deadline_at, original_grant_id, grant_namespace, grant_key_digest, grant_request_digest,
    grant_revision, grant_status, wait_namespace, wait_key_digest, wait_request_digest,
    wait_revision, wait_status, grant_cleanup_state, wait_cleanup_state,
    phase, uncertain_phase, error_code, error_ref, resolved_at,
    recorded_at, schema_version
) VALUES (
    sqlc.arg(operation_key), sqlc.arg(operation_digest), sqlc.arg(target_key), sqlc.arg(target_index), 1,
    sqlc.arg(target_digest), sqlc.arg(record_digest), '',
    sqlc.arg(target_agent_id), sqlc.arg(target_reservation_id), sqlc.arg(target_reservation_generation),
    sqlc.arg(target_incarnation_id), sqlc.arg(target_incarnation_generation),
    sqlc.arg(target_binding_version), sqlc.arg(target_binding_digest), sqlc.arg(target_fence_ref),
    sqlc.arg(source_id), sqlc.arg(source_epoch), sqlc.arg(lane), sqlc.arg(selector_version),
    sqlc.arg(selector_json), sqlc.arg(selector_digest), sqlc.arg(event_types_json),
    sqlc.arg(after_source_sequence), sqlc.arg(after_cursor), sqlc.arg(deadline_at),
    sqlc.arg(original_grant_id), sqlc.arg(grant_namespace), sqlc.arg(grant_key_digest), sqlc.arg(grant_request_digest),
    0, 'planned', sqlc.arg(wait_namespace), sqlc.arg(wait_key_digest), sqlc.arg(wait_request_digest),
    0, 'absent', 'none', 'none', 'planned', '', '', '', '',
    sqlc.arg(recorded_at), 1
)
RETURNING
    operation_key, operation_digest, target_key, target_index, version,
    target_digest, record_digest, previous_record_digest,
    target_agent_id, target_reservation_id, target_reservation_generation,
    target_incarnation_id, target_incarnation_generation,
    target_binding_version, target_binding_digest, target_fence_ref,
    source_id, source_epoch, lane, selector_version, selector_json,
    selector_digest, event_types_json, after_source_sequence, after_cursor,
    deadline_at, original_grant_id, grant_namespace, grant_key_digest, grant_request_digest,
    resolved_grant_id, grant_revision, grant_status,
    wait_namespace, wait_key_digest, wait_request_digest, resolved_wait_handle_id,
    wait_revision, wait_status, grant_cleanup_state, wait_cleanup_state,
    phase, uncertain_phase, error_code, error_ref, resolved_at,
    recorded_at, schema_version;

-- name: GetOrchestrationWaitOperation :one
SELECT
    operation_key, operation_digest, target_set_digest,
    caller_principal_ref, scope_ref,
    source_agent_id, source_reservation_id, source_reservation_generation,
    source_incarnation_id, source_incarnation_generation,
    source_binding_version, source_binding_digest, source_fence_ref,
    issued_at, deadline_at, target_count, revision, status,
    uncertain_phase, cleanup_request_digest, cleanup_reason_ref, resolved_at,
    created_at, updated_at, schema_version
FROM orchestration_wait_operations
WHERE operation_key = sqlc.arg(operation_key);

-- name: GetOrchestrationWaitOperationByDigest :one
SELECT
    operation_key, operation_digest, target_set_digest,
    caller_principal_ref, scope_ref,
    source_agent_id, source_reservation_id, source_reservation_generation,
    source_incarnation_id, source_incarnation_generation,
    source_binding_version, source_binding_digest, source_fence_ref,
    issued_at, deadline_at, target_count, revision, status,
    uncertain_phase, cleanup_request_digest, cleanup_reason_ref, resolved_at,
    created_at, updated_at, schema_version
FROM orchestration_wait_operations
WHERE operation_key = sqlc.arg(operation_key)
  AND operation_digest = sqlc.arg(operation_digest);

-- name: GetOrchestrationWaitTargetHead :one
SELECT
    operation_key, operation_digest, target_key, target_index, version,
    target_digest, record_digest, previous_record_digest,
    target_agent_id, target_reservation_id, target_reservation_generation,
    target_incarnation_id, target_incarnation_generation,
    target_binding_version, target_binding_digest, target_fence_ref,
    source_id, source_epoch, lane, selector_version, selector_json,
    selector_digest, event_types_json, after_source_sequence, after_cursor,
    deadline_at, original_grant_id, grant_namespace, grant_key_digest, grant_request_digest,
    resolved_grant_id, grant_revision, grant_status,
    wait_namespace, wait_key_digest, wait_request_digest, resolved_wait_handle_id,
    wait_revision, wait_status, grant_cleanup_state, wait_cleanup_state,
    phase, uncertain_phase, error_code, error_ref, resolved_at,
    recorded_at, schema_version
FROM orchestration_wait_operation_target_journal
WHERE operation_key = sqlc.arg(operation_key)
  AND target_key = sqlc.arg(target_key)
ORDER BY version DESC
LIMIT 1;

-- name: ListOrchestrationWaitTargetHeads :many
SELECT
    j.operation_key, j.operation_digest, j.target_key, j.target_index, j.version,
    j.target_digest, j.record_digest, j.previous_record_digest,
    j.target_agent_id, j.target_reservation_id, j.target_reservation_generation,
    j.target_incarnation_id, j.target_incarnation_generation,
    j.target_binding_version, j.target_binding_digest, j.target_fence_ref,
    j.source_id, j.source_epoch, j.lane, j.selector_version, j.selector_json,
    j.selector_digest, j.event_types_json, j.after_source_sequence, j.after_cursor,
    j.deadline_at, j.original_grant_id, j.grant_namespace, j.grant_key_digest, j.grant_request_digest,
    j.resolved_grant_id, j.grant_revision, j.grant_status,
    j.wait_namespace, j.wait_key_digest, j.wait_request_digest, j.resolved_wait_handle_id,
    j.wait_revision, j.wait_status, j.grant_cleanup_state, j.wait_cleanup_state,
    j.phase, j.uncertain_phase, j.error_code, j.error_ref, j.resolved_at,
    j.recorded_at, j.schema_version
FROM orchestration_wait_operation_target_journal AS j
WHERE j.operation_key = sqlc.arg(operation_key)
  AND j.version = (
      SELECT MAX(newer.version)
      FROM orchestration_wait_operation_target_journal AS newer
      WHERE newer.operation_key = j.operation_key
        AND newer.target_key = j.target_key
  )
ORDER BY j.target_index ASC, j.target_key ASC;

-- name: ListOrchestrationWaitTargetJournal :many
SELECT
    operation_key, operation_digest, target_key, target_index, version,
    target_digest, record_digest, previous_record_digest,
    target_agent_id, target_reservation_id, target_reservation_generation,
    target_incarnation_id, target_incarnation_generation,
    target_binding_version, target_binding_digest, target_fence_ref,
    source_id, source_epoch, lane, selector_version, selector_json,
    selector_digest, event_types_json, after_source_sequence, after_cursor,
    deadline_at, original_grant_id, grant_namespace, grant_key_digest, grant_request_digest,
    resolved_grant_id, grant_revision, grant_status,
    wait_namespace, wait_key_digest, wait_request_digest, resolved_wait_handle_id,
    wait_revision, wait_status, grant_cleanup_state, wait_cleanup_state,
    phase, uncertain_phase, error_code, error_ref, resolved_at,
    recorded_at, schema_version
FROM orchestration_wait_operation_target_journal
WHERE operation_key = sqlc.arg(operation_key)
ORDER BY target_index ASC, target_key ASC, version ASC;

-- name: AppendOrchestrationWaitTargetJournal :one
INSERT INTO orchestration_wait_operation_target_journal (
    operation_key, operation_digest, target_key, target_index, version,
    target_digest, record_digest, previous_record_digest,
    target_agent_id, target_reservation_id, target_reservation_generation,
    target_incarnation_id, target_incarnation_generation,
    target_binding_version, target_binding_digest, target_fence_ref,
    source_id, source_epoch, lane, selector_version, selector_json,
    selector_digest, event_types_json, after_source_sequence, after_cursor,
    deadline_at, original_grant_id, grant_namespace, grant_key_digest, grant_request_digest,
    resolved_grant_id, grant_revision, grant_status,
    wait_namespace, wait_key_digest, wait_request_digest, resolved_wait_handle_id,
    wait_revision, wait_status, grant_cleanup_state, wait_cleanup_state,
    phase, uncertain_phase, error_code, error_ref, resolved_at,
    recorded_at, schema_version
)
SELECT
    current.operation_key, current.operation_digest, current.target_key,
    current.target_index, current.version + 1,
    current.target_digest, sqlc.arg(record_digest), current.record_digest,
    current.target_agent_id, current.target_reservation_id, current.target_reservation_generation,
    current.target_incarnation_id, current.target_incarnation_generation,
    current.target_binding_version, current.target_binding_digest, current.target_fence_ref,
    current.source_id, current.source_epoch, current.lane, current.selector_version,
    current.selector_json, current.selector_digest, current.event_types_json,
    current.after_source_sequence, current.after_cursor, current.deadline_at,
    current.original_grant_id, current.grant_namespace, current.grant_key_digest, current.grant_request_digest,
    sqlc.narg(resolved_grant_id), sqlc.arg(grant_revision), sqlc.arg(grant_status),
    current.wait_namespace, current.wait_key_digest, current.wait_request_digest, sqlc.narg(resolved_wait_handle_id),
    sqlc.arg(wait_revision), sqlc.arg(wait_status), sqlc.arg(grant_cleanup_state),
    sqlc.arg(wait_cleanup_state), sqlc.arg(next_phase), sqlc.arg(uncertain_phase),
    sqlc.arg(error_code), sqlc.arg(error_ref), sqlc.arg(resolved_at),
    sqlc.arg(recorded_at), 1
FROM orchestration_wait_operation_target_journal AS current
WHERE current.operation_key = sqlc.arg(operation_key)
  AND current.operation_digest = sqlc.arg(operation_digest)
  AND current.target_key = sqlc.arg(target_key)
  AND current.version = sqlc.arg(expected_version)
  AND current.phase = sqlc.arg(expected_phase)
  AND current.record_digest = sqlc.arg(expected_record_digest)
  AND NOT EXISTS (
      SELECT 1
      FROM orchestration_wait_operation_target_journal AS newer
      WHERE newer.operation_key = current.operation_key
        AND newer.target_key = current.target_key
        AND newer.version > current.version
  )
RETURNING
    operation_key, operation_digest, target_key, target_index, version,
    target_digest, record_digest, previous_record_digest,
    target_agent_id, target_reservation_id, target_reservation_generation,
    target_incarnation_id, target_incarnation_generation,
    target_binding_version, target_binding_digest, target_fence_ref,
    source_id, source_epoch, lane, selector_version, selector_json,
    selector_digest, event_types_json, after_source_sequence, after_cursor,
    deadline_at, original_grant_id, grant_namespace, grant_key_digest, grant_request_digest,
    resolved_grant_id, grant_revision, grant_status,
    wait_namespace, wait_key_digest, wait_request_digest, resolved_wait_handle_id,
    wait_revision, wait_status, grant_cleanup_state, wait_cleanup_state,
    phase, uncertain_phase, error_code, error_ref, resolved_at,
    recorded_at, schema_version;

-- name: UpdateOrchestrationWaitOperationState :one
UPDATE orchestration_wait_operations
SET status = sqlc.arg(next_status),
    uncertain_phase = sqlc.arg(next_uncertain_phase),
    cleanup_request_digest = sqlc.arg(next_cleanup_request_digest),
    cleanup_reason_ref = sqlc.arg(next_cleanup_reason_ref),
    resolved_at = sqlc.arg(next_resolved_at),
    updated_at = sqlc.arg(updated_at),
    revision = revision + 1
WHERE operation_key = sqlc.arg(operation_key)
  AND operation_digest = sqlc.arg(operation_digest)
  AND revision = sqlc.arg(expected_revision)
  AND status = sqlc.arg(expected_status)
  AND uncertain_phase = sqlc.arg(expected_uncertain_phase)
  AND cleanup_request_digest = sqlc.arg(expected_cleanup_request_digest)
  AND cleanup_reason_ref = sqlc.arg(expected_cleanup_reason_ref)
  AND resolved_at = sqlc.arg(expected_resolved_at)
RETURNING
    operation_key, operation_digest, target_set_digest,
    caller_principal_ref, scope_ref,
    source_agent_id, source_reservation_id, source_reservation_generation,
    source_incarnation_id, source_incarnation_generation,
    source_binding_version, source_binding_digest, source_fence_ref,
    issued_at, deadline_at, target_count, revision, status,
    uncertain_phase, cleanup_request_digest, cleanup_reason_ref, resolved_at,
    created_at, updated_at, schema_version;

-- name: CheckOrchestrationWaitOperationSchema :one
SELECT version AS migration_version
FROM schema_migrations
WHERE version = 136
LIMIT 1;

-- name: CountOrchestrationWaitOperations :one
SELECT COUNT(*) AS operation_count
FROM orchestration_wait_operations;

-- name: CountOrchestrationWaitOperationTargets :one
SELECT COUNT(*) AS target_count
FROM orchestration_wait_operation_target_journal;
