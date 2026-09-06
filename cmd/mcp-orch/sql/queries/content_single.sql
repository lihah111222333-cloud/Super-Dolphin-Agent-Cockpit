-- name: CreateContentIngestReservation :one
INSERT INTO orchestration_content_ingest_reservations (
    ingest_reservation_id, ingest_ticket_id, object_id, object_version,
    owner_id, owner_generation, writer_fence_ref, security_time_generation, workspace_ref,
    caller_principal_ref, scope_ref, source_agent_id, source_identity_kind,
    source_identity_reservation_id, source_reservation_generation, source_incarnation_id,
    source_incarnation_generation, source_external_identity_ref, source_binding_version,
    source_binding_digest, source_fence_ref, source_active_process_receipt_ref, source_binding_updated_at,
    idempotency_namespace, idempotency_key_digest, proof_request_digest, candidate_ref_digest,
    content_intent_digest, digest_key_id, digest_key_version, ingest_kind, size_bytes, media_type,
    permitted_sink_role, aead_algorithm, key_wrap_profile, aad_profile, quota_snapshot_ref,
    quota_snapshot_digest, retention_policy_id, retention_policy_version, retention_policy_digest,
    assembly_deadline_at, revision, state, schema_version
) VALUES (
    sqlc.arg(ingest_reservation_id), sqlc.arg(ingest_ticket_id), sqlc.arg(object_id), 1,
    sqlc.arg(owner_id), sqlc.arg(owner_generation), sqlc.arg(writer_fence_ref),
    sqlc.arg(security_time_generation), sqlc.arg(workspace_ref),
    sqlc.arg(caller_principal_ref), sqlc.arg(scope_ref), sqlc.arg(source_agent_id), sqlc.arg(source_identity_kind),
    sqlc.arg(source_identity_reservation_id), sqlc.arg(source_reservation_generation), sqlc.arg(source_incarnation_id),
    sqlc.arg(source_incarnation_generation), sqlc.arg(source_external_identity_ref), sqlc.arg(source_binding_version),
    sqlc.arg(source_binding_digest), sqlc.arg(source_fence_ref), sqlc.arg(source_active_process_receipt_ref),
    sqlc.arg(source_binding_updated_at), sqlc.arg(idempotency_namespace), sqlc.arg(idempotency_key_digest),
    sqlc.arg(proof_request_digest), sqlc.arg(candidate_ref_digest), sqlc.arg(content_intent_digest),
    sqlc.arg(digest_key_id), sqlc.arg(digest_key_version), 'content.single', sqlc.arg(size_bytes),
    sqlc.arg(media_type), 'command_request', 'aes-256-gcm', sqlc.arg(key_wrap_profile),
    'orch.content.single.aad/v1', sqlc.arg(quota_snapshot_ref), sqlc.arg(quota_snapshot_digest),
    sqlc.arg(retention_policy_id), sqlc.arg(retention_policy_version), sqlc.arg(retention_policy_digest),
    sqlc.arg(assembly_deadline_at), 1, 'reserved', 1
)
RETURNING
    ingest_reservation_id,
    ingest_ticket_id,
    object_id,
    object_version,
    owner_id,
    owner_generation,
    writer_fence_ref,
    security_time_generation,
    workspace_ref,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    source_identity_kind,
    source_identity_reservation_id,
    source_reservation_generation,
    source_incarnation_id,
    source_incarnation_generation,
    source_external_identity_ref,
    source_binding_version,
    source_binding_digest,
    source_fence_ref,
    source_active_process_receipt_ref,
    source_binding_updated_at,
    idempotency_namespace,
    idempotency_key_digest,
    proof_request_digest,
    candidate_ref_digest,
    content_intent_digest,
    digest_key_id,
    digest_key_version,
    ingest_kind,
    size_bytes,
    media_type,
    permitted_sink_role,
    aead_algorithm,
    key_wrap_profile,
    aad_profile,
    quota_snapshot_ref,
    quota_snapshot_digest,
    retention_policy_id,
    retention_policy_version,
    retention_policy_digest,
    assembly_deadline_at,
    prepare_operation_id,
    prepare_started_at,
    revision,
    state,
    schema_version,
    created_at,
    updated_at;

-- name: GetContentIngestReservationByTicket :one
SELECT
    ingest_reservation_id,
    ingest_ticket_id,
    object_id,
    object_version,
    owner_id,
    owner_generation,
    writer_fence_ref,
    security_time_generation,
    workspace_ref,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    source_identity_kind,
    source_identity_reservation_id,
    source_reservation_generation,
    source_incarnation_id,
    source_incarnation_generation,
    source_external_identity_ref,
    source_binding_version,
    source_binding_digest,
    source_fence_ref,
    source_active_process_receipt_ref,
    source_binding_updated_at,
    idempotency_namespace,
    idempotency_key_digest,
    proof_request_digest,
    candidate_ref_digest,
    content_intent_digest,
    digest_key_id,
    digest_key_version,
    ingest_kind,
    size_bytes,
    media_type,
    permitted_sink_role,
    aead_algorithm,
    key_wrap_profile,
    aad_profile,
    quota_snapshot_ref,
    quota_snapshot_digest,
    retention_policy_id,
    retention_policy_version,
    retention_policy_digest,
    assembly_deadline_at,
    prepare_operation_id,
    prepare_started_at,
    revision,
    state,
    schema_version,
    created_at,
    updated_at
FROM orchestration_content_ingest_reservations
WHERE ingest_ticket_id = sqlc.arg(ingest_ticket_id);

-- name: GetContentIngestReservationByKey :one
SELECT
    ingest_reservation_id,
    ingest_ticket_id,
    object_id,
    object_version,
    owner_id,
    owner_generation,
    writer_fence_ref,
    security_time_generation,
    workspace_ref,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    source_identity_kind,
    source_identity_reservation_id,
    source_reservation_generation,
    source_incarnation_id,
    source_incarnation_generation,
    source_external_identity_ref,
    source_binding_version,
    source_binding_digest,
    source_fence_ref,
    source_active_process_receipt_ref,
    source_binding_updated_at,
    idempotency_namespace,
    idempotency_key_digest,
    proof_request_digest,
    candidate_ref_digest,
    content_intent_digest,
    digest_key_id,
    digest_key_version,
    ingest_kind,
    size_bytes,
    media_type,
    permitted_sink_role,
    aead_algorithm,
    key_wrap_profile,
    aad_profile,
    quota_snapshot_ref,
    quota_snapshot_digest,
    retention_policy_id,
    retention_policy_version,
    retention_policy_digest,
    assembly_deadline_at,
    prepare_operation_id,
    prepare_started_at,
    revision,
    state,
    schema_version,
    created_at,
    updated_at
FROM orchestration_content_ingest_reservations
WHERE idempotency_namespace = sqlc.arg(idempotency_namespace)
  AND idempotency_key_digest = sqlc.arg(idempotency_key_digest);

-- name: GetContentIngestReservationByID :one
SELECT
    ingest_reservation_id,
    ingest_ticket_id,
    object_id,
    object_version,
    owner_id,
    owner_generation,
    writer_fence_ref,
    security_time_generation,
    workspace_ref,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    source_identity_kind,
    source_identity_reservation_id,
    source_reservation_generation,
    source_incarnation_id,
    source_incarnation_generation,
    source_external_identity_ref,
    source_binding_version,
    source_binding_digest,
    source_fence_ref,
    source_active_process_receipt_ref,
    source_binding_updated_at,
    idempotency_namespace,
    idempotency_key_digest,
    proof_request_digest,
    candidate_ref_digest,
    content_intent_digest,
    digest_key_id,
    digest_key_version,
    ingest_kind,
    size_bytes,
    media_type,
    permitted_sink_role,
    aead_algorithm,
    key_wrap_profile,
    aad_profile,
    quota_snapshot_ref,
    quota_snapshot_digest,
    retention_policy_id,
    retention_policy_version,
    retention_policy_digest,
    assembly_deadline_at,
    prepare_operation_id,
    prepare_started_at,
    revision,
    state,
    schema_version,
    created_at,
    updated_at
FROM orchestration_content_ingest_reservations
WHERE ingest_reservation_id = sqlc.arg(ingest_reservation_id);

-- name: StartContentPreparation :execrows
UPDATE orchestration_content_ingest_reservations
SET prepare_operation_id = sqlc.arg(prepare_operation_id),
    prepare_started_at = sqlc.arg(prepare_started_at),
    updated_at = sqlc.arg(updated_at)
WHERE ingest_reservation_id = sqlc.arg(ingest_reservation_id)
  AND state = 'reserved' AND revision = 1
  AND prepare_operation_id = '' AND prepare_started_at = '';

-- name: CreateContentPreparedObject :one
INSERT INTO orchestration_content_objects (
    object_id, object_version, ingest_reservation_id, owner_id, owner_generation, writer_fence_ref,
    security_time_generation, workspace_ref, source_agent_id, source_fence_ref, aead_algorithm,
    key_wrap_profile, key_handle_ref, key_receipt_ref, key_receipt_digest, nonce_hex,
    aad_profile, aad_digest, media_type, size_bytes, ciphertext_sha256, sealed_payload_size_bytes,
    retention_policy_digest, content_state, key_state, ref_version, revision, schema_version
) VALUES (
    sqlc.arg(object_id), 1, sqlc.arg(ingest_reservation_id), sqlc.arg(owner_id),
    sqlc.arg(owner_generation), sqlc.arg(writer_fence_ref), sqlc.arg(security_time_generation),
    sqlc.arg(workspace_ref), sqlc.arg(source_agent_id), sqlc.arg(source_fence_ref), 'aes-256-gcm',
    sqlc.arg(key_wrap_profile), sqlc.arg(key_handle_ref), sqlc.arg(key_receipt_ref),
    sqlc.arg(key_receipt_digest), sqlc.arg(nonce_hex), 'orch.content.single.aad/v1',
    sqlc.arg(aad_digest), sqlc.arg(media_type), sqlc.arg(size_bytes), sqlc.arg(ciphertext_sha256),
    sqlc.arg(sealed_payload_size_bytes), sqlc.arg(retention_policy_digest), 'prepared', 'active', 0, 1, 1
)
RETURNING
    object_id,
    object_version,
    ingest_reservation_id,
    owner_id,
    owner_generation,
    writer_fence_ref,
    security_time_generation,
    workspace_ref,
    source_agent_id,
    source_fence_ref,
    aead_algorithm,
    key_wrap_profile,
    key_handle_ref,
    key_receipt_ref,
    key_receipt_digest,
    nonce_hex,
    aad_profile,
    aad_digest,
    media_type,
    size_bytes,
    ciphertext_sha256,
    sealed_payload_size_bytes,
    retention_policy_digest,
    content_state,
    key_state,
    ref_version,
    revision,
    promoted_at,
    schema_version,
    created_at,
    updated_at;

-- name: CreateContentPrivatePayloadReceipt :one
INSERT INTO orchestration_content_private_payload_receipts (
    object_id, object_version, ingest_reservation_id, backend_kind, object_store_id, payload_format,
    private_payload_ref, sealed_payload_size_bytes, ciphertext_sha256, store_operation_id,
    store_request_digest, store_receipt_ref, store_receipt_digest, stored_at, schema_version
) VALUES (
    sqlc.arg(object_id), 1, sqlc.arg(ingest_reservation_id), 'private_state_object_v1',
    sqlc.arg(object_store_id), 'aes256gcm-ciphertext-tag-v1', sqlc.arg(private_payload_ref), sqlc.arg(sealed_payload_size_bytes),
    sqlc.arg(ciphertext_sha256), sqlc.arg(store_operation_id), sqlc.arg(store_request_digest),
    sqlc.arg(store_receipt_ref), sqlc.arg(store_receipt_digest), sqlc.arg(stored_at), 1
)
RETURNING
    object_id,
    object_version,
    ingest_reservation_id,
    backend_kind,
    object_store_id,
    payload_format,
    private_payload_ref,
    sealed_payload_size_bytes,
    ciphertext_sha256,
    store_operation_id,
    store_request_digest,
    store_receipt_ref,
    store_receipt_digest,
    stored_at,
    schema_version;

-- name: MarkContentIngestSealed :execrows
UPDATE orchestration_content_ingest_reservations
SET state = 'sealed', revision = 2, updated_at = sqlc.arg(updated_at)
WHERE ingest_reservation_id = sqlc.arg(ingest_reservation_id)
  AND state = 'reserved' AND revision = 1;

-- name: GetContentObjectByReservation :one
SELECT
    object_id,
    object_version,
    ingest_reservation_id,
    owner_id,
    owner_generation,
    writer_fence_ref,
    security_time_generation,
    workspace_ref,
    source_agent_id,
    source_fence_ref,
    aead_algorithm,
    key_wrap_profile,
    key_handle_ref,
    key_receipt_ref,
    key_receipt_digest,
    nonce_hex,
    aad_profile,
    aad_digest,
    media_type,
    size_bytes,
    ciphertext_sha256,
    sealed_payload_size_bytes,
    retention_policy_digest,
    content_state,
    key_state,
    ref_version,
    revision,
    promoted_at,
    schema_version,
    created_at,
    updated_at
FROM orchestration_content_objects
WHERE ingest_reservation_id = sqlc.arg(ingest_reservation_id);

-- name: GetContentPrivatePayloadReceiptByReservation :one
SELECT
    object_id,
    object_version,
    ingest_reservation_id,
    backend_kind,
    object_store_id,
    payload_format,
    private_payload_ref,
    sealed_payload_size_bytes,
    ciphertext_sha256,
    store_operation_id,
    store_request_digest,
    store_receipt_ref,
    store_receipt_digest,
    stored_at,
    schema_version
FROM orchestration_content_private_payload_receipts
WHERE ingest_reservation_id = sqlc.arg(ingest_reservation_id);

-- name: PromoteContentObject :execrows
UPDATE orchestration_content_objects
SET content_state = 'active', ref_version = 1, revision = 2,
    promoted_at = sqlc.arg(promoted_at), updated_at = sqlc.arg(updated_at)
WHERE ingest_reservation_id = sqlc.arg(ingest_reservation_id)
  AND content_state = 'prepared' AND revision = 1 AND ref_version = 0;

-- name: CreateContentActiveRef :one
INSERT INTO orchestration_content_refs (
    reference_id, canonical_ref, object_id, object_version, ingest_reservation_id, ref_version,
    reference_kind, reference_state, caller_principal_ref, scope_ref, source_agent_id,
    source_fence_ref, proof_request_digest, proof_id, proof_revision, owner_id, owner_generation,
    writer_fence_ref, security_time_generation, owner_proof_ref, proof_snapshot_digest,
    proof_expires_at, media_type, size_bytes, retention_policy_digest, schema_version
) VALUES (
    sqlc.arg(reference_id), sqlc.arg(canonical_ref), sqlc.arg(object_id), 1,
    sqlc.arg(ingest_reservation_id), 1, 'command_request', 'live', sqlc.arg(caller_principal_ref),
    sqlc.arg(scope_ref), sqlc.arg(source_agent_id), sqlc.arg(source_fence_ref),
    sqlc.arg(proof_request_digest), sqlc.arg(proof_id), sqlc.arg(proof_revision),
    sqlc.arg(owner_id), sqlc.arg(owner_generation), sqlc.arg(writer_fence_ref),
    sqlc.arg(security_time_generation), sqlc.arg(owner_proof_ref), sqlc.arg(proof_snapshot_digest),
    sqlc.arg(proof_expires_at), sqlc.arg(media_type), sqlc.arg(size_bytes),
    sqlc.arg(retention_policy_digest), 1
)
RETURNING
    reference_id,
    canonical_ref,
    object_id,
    object_version,
    ingest_reservation_id,
    ref_version,
    reference_kind,
    reference_state,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    source_fence_ref,
    proof_request_digest,
    proof_id,
    proof_revision,
    owner_id,
    owner_generation,
    writer_fence_ref,
    security_time_generation,
    owner_proof_ref,
    proof_snapshot_digest,
    proof_expires_at,
    media_type,
    size_bytes,
    retention_policy_digest,
    schema_version,
    created_at;

-- name: MarkContentIngestPromoted :execrows
UPDATE orchestration_content_ingest_reservations
SET state = 'promoted', revision = 3, updated_at = sqlc.arg(updated_at)
WHERE ingest_reservation_id = sqlc.arg(ingest_reservation_id)
  AND state = 'sealed' AND revision = 2;

-- name: GetContentRefByCanonical :one
SELECT
    reference_id,
    canonical_ref,
    object_id,
    object_version,
    ingest_reservation_id,
    ref_version,
    reference_kind,
    reference_state,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    source_fence_ref,
    proof_request_digest,
    proof_id,
    proof_revision,
    owner_id,
    owner_generation,
    writer_fence_ref,
    security_time_generation,
    owner_proof_ref,
    proof_snapshot_digest,
    proof_expires_at,
    media_type,
    size_bytes,
    retention_policy_digest,
    schema_version,
    created_at
FROM orchestration_content_refs
WHERE canonical_ref = sqlc.arg(canonical_ref);

-- name: GetContentRefByReservation :one
SELECT
    reference_id,
    canonical_ref,
    object_id,
    object_version,
    ingest_reservation_id,
    ref_version,
    reference_kind,
    reference_state,
    caller_principal_ref,
    scope_ref,
    source_agent_id,
    source_fence_ref,
    proof_request_digest,
    proof_id,
    proof_revision,
    owner_id,
    owner_generation,
    writer_fence_ref,
    security_time_generation,
    owner_proof_ref,
    proof_snapshot_digest,
    proof_expires_at,
    media_type,
    size_bytes,
    retention_policy_digest,
    schema_version,
    created_at
FROM orchestration_content_refs
WHERE ingest_reservation_id = sqlc.arg(ingest_reservation_id);

-- name: GetCommandContentDeletionFence :one
SELECT
    command_id,
    reference_id,
    canonical_ref,
    object_id,
    object_version,
    ref_version,
    fence_kind,
    fence_state,
    command_profile_digest,
    source_fence_ref,
    target_fence_ref,
    schema_version,
    created_at
FROM orchestration_command_content_deletion_fences
WHERE command_id = sqlc.arg(command_id);

-- name: GetLegacyCommandContentQuarantine :one
SELECT
    command_id,
    complete_intent_digest,
    reason,
    source_schema_version,
    schema_version,
    created_at
FROM orchestration_legacy_command_content_quarantine
WHERE command_id = sqlc.arg(command_id);

-- name: CheckCommandContentAdmissionRelation :one
SELECT EXISTS (
    SELECT 1
    FROM orchestration_command_content_deletion_fences AS fence
    JOIN orchestration_content_refs AS ref
      ON ref.reference_id = fence.reference_id
     AND ref.canonical_ref = fence.canonical_ref
     AND ref.object_id = fence.object_id
     AND ref.object_version = fence.object_version
     AND ref.ref_version = fence.ref_version
    JOIN orchestration_content_objects AS object
      ON object.object_id = ref.object_id AND object.object_version = ref.object_version
    JOIN orchestration_content_ingest_reservations AS reservation
      ON reservation.ingest_reservation_id = ref.ingest_reservation_id
    WHERE fence.command_id = sqlc.arg(command_id)
      AND fence.fence_kind = 'non_terminal_command' AND fence.fence_state = 'held'
      AND fence.command_profile_digest = sqlc.arg(command_profile_digest)
      AND fence.source_fence_ref = sqlc.arg(source_fence_ref)
      AND fence.target_fence_ref = sqlc.arg(target_fence_ref)
      AND ref.canonical_ref = sqlc.arg(canonical_ref) AND ref.reference_state = 'live'
      AND ref.caller_principal_ref = sqlc.arg(caller_principal_ref)
      AND ref.scope_ref = sqlc.arg(scope_ref)
      AND ref.source_agent_id = sqlc.arg(source_agent_id)
      AND ref.source_fence_ref = sqlc.arg(source_fence_ref)
      AND object.content_state = 'active' AND object.key_state = 'active'
      AND reservation.state = 'promoted'
      AND reservation.caller_principal_ref = ref.caller_principal_ref
      AND reservation.scope_ref = ref.scope_ref
      AND reservation.source_agent_id = ref.source_agent_id
      AND reservation.source_fence_ref = ref.source_fence_ref
      AND julianday(ref.proof_expires_at) > julianday(sqlc.arg(authorization_decided_at))
      AND julianday(ref.proof_expires_at) <= julianday(sqlc.arg(authorization_expires_at))
      AND julianday(ref.proof_expires_at) <= julianday(sqlc.arg(caller_auth_expires_at))
) AS relation_valid;
