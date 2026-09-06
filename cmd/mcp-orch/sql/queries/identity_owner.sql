-- name: CreateOrchestrationIdentityReservation :one
INSERT INTO orchestration_identity_reservations (
    reservation_id, command_id, agent_id, identity_kind, source_kind,
    caller_principal_ref, scope_ref, parent_agent_id,
    idempotency_namespace, idempotency_key_digest, request_digest,
    source_fence_ref, target_fence_ref, expected_binding_version,
    reservation_generation, state, schema_version
)
VALUES (
    sqlc.arg(reservation_id), sqlc.narg(command_id), sqlc.arg(agent_id),
    sqlc.arg(identity_kind), sqlc.arg(source_kind), sqlc.arg(caller_principal_ref),
    sqlc.arg(scope_ref), sqlc.arg(parent_agent_id), sqlc.arg(idempotency_namespace),
    sqlc.arg(idempotency_key_digest), sqlc.arg(request_digest),
    sqlc.arg(source_fence_ref), sqlc.arg(target_fence_ref),
    sqlc.arg(expected_binding_version), sqlc.arg(reservation_generation),
    'reserved', 1
)
ON CONFLICT(idempotency_namespace, idempotency_key_digest) DO NOTHING
RETURNING
    reservation_id,
    command_id,
    agent_id,
    identity_kind,
    source_kind,
    caller_principal_ref,
    scope_ref,
    parent_agent_id,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    source_fence_ref,
    target_fence_ref,
    expected_binding_version,
    reservation_generation,
    state,
    schema_version,
    created_at,
    updated_at;

-- name: GetOrchestrationIdentityReservation :one
SELECT
    reservation_id,
    command_id,
    agent_id,
    identity_kind,
    source_kind,
    caller_principal_ref,
    scope_ref,
    parent_agent_id,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    source_fence_ref,
    target_fence_ref,
    expected_binding_version,
    reservation_generation,
    state,
    schema_version,
    created_at,
    updated_at
FROM orchestration_identity_reservations
WHERE reservation_id = sqlc.arg(reservation_id);

-- name: GetOrchestrationIdentityReservationByKey :one
SELECT
    reservation_id,
    command_id,
    agent_id,
    identity_kind,
    source_kind,
    caller_principal_ref,
    scope_ref,
    parent_agent_id,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    source_fence_ref,
    target_fence_ref,
    expected_binding_version,
    reservation_generation,
    state,
    schema_version,
    created_at,
    updated_at
FROM orchestration_identity_reservations
WHERE idempotency_namespace = sqlc.arg(idempotency_namespace)
  AND idempotency_key_digest = sqlc.arg(idempotency_key_digest);

-- name: GetOrchestrationIdentityReservationByCommand :one
SELECT
    reservation_id,
    command_id,
    agent_id,
    identity_kind,
    source_kind,
    caller_principal_ref,
    scope_ref,
    parent_agent_id,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    source_fence_ref,
    target_fence_ref,
    expected_binding_version,
    reservation_generation,
    state,
    schema_version,
    created_at,
    updated_at
FROM orchestration_identity_reservations
WHERE command_id = sqlc.arg(command_id);

-- name: UpdateOrchestrationIdentityReservationState :execrows
UPDATE orchestration_identity_reservations
SET state = sqlc.arg(next_state), updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE reservation_id = sqlc.arg(reservation_id)
  AND state = sqlc.arg(expected_state);

-- name: CreateOrchestrationExecutionIncarnation :one
INSERT INTO orchestration_execution_incarnations (
    incarnation_id, reservation_id, agent_id, identity_kind,
    external_identity_ref, external_epoch, source_fence_ref,
    identity_digest, incarnation_generation, schema_version
)
VALUES (
    sqlc.arg(incarnation_id), sqlc.arg(reservation_id), sqlc.arg(agent_id),
    sqlc.arg(identity_kind), sqlc.arg(external_identity_ref), sqlc.arg(external_epoch),
    sqlc.arg(source_fence_ref), sqlc.arg(identity_digest),
    sqlc.arg(incarnation_generation), 1
)
ON CONFLICT(reservation_id) DO NOTHING
RETURNING
    incarnation_id,
    reservation_id,
    agent_id,
    identity_kind,
    external_identity_ref,
    external_epoch,
    source_fence_ref,
    identity_digest,
    incarnation_generation,
    schema_version,
    created_at;

-- name: GetOrchestrationExecutionIncarnation :one
SELECT
    incarnation_id,
    reservation_id,
    agent_id,
    identity_kind,
    external_identity_ref,
    external_epoch,
    source_fence_ref,
    identity_digest,
    incarnation_generation,
    schema_version,
    created_at
FROM orchestration_execution_incarnations
WHERE incarnation_id = sqlc.arg(incarnation_id);

-- name: GetOrchestrationExecutionIncarnationByReservation :one
SELECT
    incarnation_id,
    reservation_id,
    agent_id,
    identity_kind,
    external_identity_ref,
    external_epoch,
    source_fence_ref,
    identity_digest,
    incarnation_generation,
    schema_version,
    created_at
FROM orchestration_execution_incarnations
WHERE reservation_id = sqlc.arg(reservation_id);

-- name: GetOrchestrationExecutionIncarnationByExternal :one
SELECT
    incarnation_id,
    reservation_id,
    agent_id,
    identity_kind,
    external_identity_ref,
    external_epoch,
    source_fence_ref,
    identity_digest,
    incarnation_generation,
    schema_version,
    created_at
FROM orchestration_execution_incarnations
WHERE identity_kind = sqlc.arg(identity_kind)
  AND external_identity_ref = sqlc.arg(external_identity_ref)
  AND external_epoch = sqlc.arg(external_epoch);

-- name: CreateOrchestrationBindingPreparation :one
INSERT INTO orchestration_binding_preparations (
    preparation_id, reservation_id, incarnation_id, agent_id, identity_kind,
    preparation_generation, expected_binding_version,
    expected_incarnation_generation, binding_digest, source_fence_ref,
    active_process_receipt_ref, state, schema_version, resolved_at
)
VALUES (
    sqlc.arg(preparation_id), sqlc.arg(reservation_id), sqlc.arg(incarnation_id),
    sqlc.arg(agent_id), sqlc.arg(identity_kind), sqlc.arg(preparation_generation),
    sqlc.arg(expected_binding_version), sqlc.arg(expected_incarnation_generation),
    sqlc.arg(binding_digest), sqlc.arg(source_fence_ref),
    sqlc.arg(active_process_receipt_ref), 'prepared_non_authorizing', 1, ''
)
ON CONFLICT(reservation_id, preparation_generation) DO NOTHING
RETURNING
    preparation_id,
    reservation_id,
    incarnation_id,
    agent_id,
    identity_kind,
    preparation_generation,
    expected_binding_version,
    expected_incarnation_generation,
    binding_digest,
    source_fence_ref,
    active_process_receipt_ref,
    state,
    schema_version,
    created_at,
    resolved_at;

-- name: GetOrchestrationBindingPreparation :one
SELECT
    preparation_id,
    reservation_id,
    incarnation_id,
    agent_id,
    identity_kind,
    preparation_generation,
    expected_binding_version,
    expected_incarnation_generation,
    binding_digest,
    source_fence_ref,
    active_process_receipt_ref,
    state,
    schema_version,
    created_at,
    resolved_at
FROM orchestration_binding_preparations
WHERE preparation_id = sqlc.arg(preparation_id);

-- name: GetOrchestrationBindingPreparationByReservationGeneration :one
SELECT
    preparation_id,
    reservation_id,
    incarnation_id,
    agent_id,
    identity_kind,
    preparation_generation,
    expected_binding_version,
    expected_incarnation_generation,
    binding_digest,
    source_fence_ref,
    active_process_receipt_ref,
    state,
    schema_version,
    created_at,
    resolved_at
FROM orchestration_binding_preparations
WHERE reservation_id = sqlc.arg(reservation_id)
  AND preparation_generation = sqlc.arg(preparation_generation);

-- name: GetOrchestrationBindingPreparationByIncarnationGeneration :one
SELECT
    preparation_id,
    reservation_id,
    incarnation_id,
    agent_id,
    identity_kind,
    preparation_generation,
    expected_binding_version,
    expected_incarnation_generation,
    binding_digest,
    source_fence_ref,
    active_process_receipt_ref,
    state,
    schema_version,
    created_at,
    resolved_at
FROM orchestration_binding_preparations
WHERE incarnation_id = sqlc.arg(incarnation_id)
  AND preparation_generation = sqlc.arg(preparation_generation);

-- name: UpdateOrchestrationBindingPreparationState :execrows
UPDATE orchestration_binding_preparations
SET state = sqlc.arg(next_state),
    active_process_receipt_ref = sqlc.arg(active_process_receipt_ref),
    resolved_at = sqlc.arg(resolved_at)
WHERE preparation_id = sqlc.arg(preparation_id)
  AND agent_id = sqlc.arg(agent_id)
  AND preparation_generation = sqlc.arg(expected_preparation_generation)
  AND state = sqlc.arg(expected_state);

-- name: GetOrchestrationCurrentBinding :one
SELECT
    agent_id,
    binding_version,
    reservation_id,
    incarnation_id,
    identity_kind,
    reservation_generation,
    incarnation_generation,
    external_identity_ref,
    source_fence_ref,
    active_process_receipt_ref,
    binding_digest,
    state,
    schema_version,
    updated_at
FROM orchestration_current_bindings
WHERE agent_id = sqlc.arg(agent_id)
ORDER BY binding_version DESC
LIMIT 1;

-- name: GetOrchestrationCurrentBindingVersion :one
SELECT
    agent_id,
    binding_version,
    reservation_id,
    incarnation_id,
    identity_kind,
    reservation_generation,
    incarnation_generation,
    external_identity_ref,
    source_fence_ref,
    active_process_receipt_ref,
    binding_digest,
    state,
    schema_version,
    updated_at
FROM orchestration_current_bindings
WHERE agent_id = sqlc.arg(agent_id)
  AND binding_version = sqlc.arg(binding_version);

-- name: AppendOrchestrationCurrentBinding :one
INSERT INTO orchestration_current_bindings (
    agent_id, binding_version, reservation_id, incarnation_id, identity_kind,
    reservation_generation, incarnation_generation, external_identity_ref,
    source_fence_ref, active_process_receipt_ref, binding_digest, state,
    schema_version
)
VALUES (
    sqlc.arg(agent_id), sqlc.arg(binding_version), sqlc.arg(reservation_id),
    sqlc.arg(incarnation_id), sqlc.arg(identity_kind),
    sqlc.arg(reservation_generation), sqlc.arg(incarnation_generation),
    sqlc.arg(external_identity_ref), sqlc.arg(source_fence_ref),
    sqlc.arg(active_process_receipt_ref), sqlc.arg(binding_digest), 'active', 1
)
RETURNING
    agent_id,
    binding_version,
    reservation_id,
    incarnation_id,
    identity_kind,
    reservation_generation,
    incarnation_generation,
    external_identity_ref,
    source_fence_ref,
    active_process_receipt_ref,
    binding_digest,
    state,
    schema_version,
    updated_at;

-- name: AppendOrchestrationCurrentBindingRevoked :one
INSERT INTO orchestration_current_bindings (
    agent_id, binding_version, reservation_id, incarnation_id, identity_kind,
    reservation_generation, incarnation_generation, external_identity_ref,
    source_fence_ref, active_process_receipt_ref, binding_digest, state,
    schema_version
)
VALUES (
    sqlc.arg(agent_id), sqlc.arg(binding_version), sqlc.arg(reservation_id),
    sqlc.arg(incarnation_id), sqlc.arg(identity_kind),
    sqlc.arg(reservation_generation), sqlc.arg(incarnation_generation),
    sqlc.arg(external_identity_ref), sqlc.arg(source_fence_ref),
    sqlc.arg(active_process_receipt_ref), sqlc.arg(binding_digest), 'revoked',
    sqlc.arg(schema_version)
)
RETURNING
    agent_id,
    binding_version,
    reservation_id,
    incarnation_id,
    identity_kind,
    reservation_generation,
    incarnation_generation,
    external_identity_ref,
    source_fence_ref,
    active_process_receipt_ref,
    binding_digest,
    state,
    schema_version,
    updated_at;
