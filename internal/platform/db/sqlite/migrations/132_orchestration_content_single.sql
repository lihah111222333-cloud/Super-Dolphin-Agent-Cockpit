-- CONTENT-1 metadata-only encrypted-single owner.
-- No plaintext, ciphertext, tag, raw/wrapped key bytes, decrypt, destroy, delete, or dispatch fields are stored here.

-- Existing v131 profiles are preserved as history but can never be upgraded into CONTENT authority.
CREATE TABLE orchestration_legacy_command_content_quarantine (
    command_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(command_id) > 0 AND length(command_id) <= 128
          AND trim(command_id) = command_id AND instr(command_id, char(0)) = 0)
        REFERENCES orchestration_command_intent_profiles(command_id) ON DELETE RESTRICT,
    complete_intent_digest TEXT NOT NULL CHECK(
        length(complete_intent_digest) = 64 AND complete_intent_digest NOT GLOB '*[^0-9a-f]*'
          AND instr(complete_intent_digest, char(0)) = 0
    ),
    reason TEXT NOT NULL CHECK(reason = 'legacy_missing_content_owner_ref'),
    source_schema_version INTEGER NOT NULL CHECK(source_schema_version = 131),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    created_at TEXT NOT NULL CHECK(
        length(created_at) > 0 AND length(created_at) <= 64 AND trim(created_at) = created_at
          AND instr(created_at, char(0)) = 0 AND julianday(created_at) IS NOT NULL
    ) DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

INSERT INTO orchestration_legacy_command_content_quarantine (
    command_id, complete_intent_digest, reason, source_schema_version, schema_version
)
SELECT command_id, complete_intent_digest, 'legacy_missing_content_owner_ref', 131, 1
FROM orchestration_command_intent_profiles;

CREATE TRIGGER trg_orchestration_legacy_command_content_quarantine_no_update
BEFORE UPDATE ON orchestration_legacy_command_content_quarantine
BEGIN SELECT RAISE(ABORT, 'legacy command content quarantine is immutable'); END;

CREATE TRIGGER trg_orchestration_legacy_command_content_quarantine_no_delete
BEFORE DELETE ON orchestration_legacy_command_content_quarantine
BEGIN SELECT RAISE(ABORT, 'legacy command content quarantine is immutable'); END;

CREATE TABLE orchestration_content_ingest_reservations (
    ingest_reservation_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(ingest_reservation_id) > 0 AND length(ingest_reservation_id) <= 128
          AND trim(ingest_reservation_id) = ingest_reservation_id AND instr(ingest_reservation_id, char(0)) = 0),
    ingest_ticket_id TEXT NOT NULL UNIQUE
        CHECK(length(ingest_ticket_id) > 0 AND length(ingest_ticket_id) <= 128
          AND trim(ingest_ticket_id) = ingest_ticket_id AND instr(ingest_ticket_id, char(0)) = 0),
    object_id TEXT NOT NULL
        CHECK(length(object_id) > 0 AND length(object_id) <= 128
          AND trim(object_id) = object_id AND instr(object_id, char(0)) = 0),
    object_version INTEGER NOT NULL CHECK(object_version = 1),
    owner_id TEXT NOT NULL
        CHECK(length(owner_id) > 0 AND length(owner_id) <= 256
          AND trim(owner_id) = owner_id AND instr(owner_id, char(0)) = 0),
    owner_generation INTEGER NOT NULL CHECK(owner_generation > 0),
    writer_fence_ref TEXT NOT NULL
        CHECK(length(writer_fence_ref) > 0 AND length(writer_fence_ref) <= 512
          AND trim(writer_fence_ref) = writer_fence_ref AND instr(writer_fence_ref, char(0)) = 0),
    security_time_generation INTEGER NOT NULL CHECK(security_time_generation > 0),
    workspace_ref TEXT NOT NULL
        CHECK(length(workspace_ref) > 0 AND length(workspace_ref) <= 512
          AND trim(workspace_ref) = workspace_ref AND instr(workspace_ref, char(0)) = 0),
    caller_principal_ref TEXT NOT NULL
        CHECK(length(caller_principal_ref) > 0 AND length(caller_principal_ref) <= 512
          AND trim(caller_principal_ref) = caller_principal_ref AND instr(caller_principal_ref, char(0)) = 0),
    scope_ref TEXT NOT NULL
        CHECK(length(scope_ref) > 0 AND length(scope_ref) <= 512
          AND trim(scope_ref) = scope_ref AND instr(scope_ref, char(0)) = 0),
    source_agent_id TEXT NOT NULL
        CHECK(length(source_agent_id) > 0 AND length(source_agent_id) <= 128
          AND trim(source_agent_id) = source_agent_id AND instr(source_agent_id, char(0)) = 0),
    source_identity_kind TEXT NOT NULL CHECK(source_identity_kind IN (
        'provider_session', 'native_subagent', 'managed_agent', 'a2a_remote_agent'
    )),
    source_identity_reservation_id TEXT NOT NULL
        CHECK(length(source_identity_reservation_id) > 0 AND length(source_identity_reservation_id) <= 128
          AND trim(source_identity_reservation_id) = source_identity_reservation_id
          AND instr(source_identity_reservation_id, char(0)) = 0),
    source_reservation_generation INTEGER NOT NULL CHECK(source_reservation_generation > 0),
    source_incarnation_id TEXT NOT NULL
        CHECK(length(source_incarnation_id) > 0 AND length(source_incarnation_id) <= 128
          AND trim(source_incarnation_id) = source_incarnation_id AND instr(source_incarnation_id, char(0)) = 0),
    source_incarnation_generation INTEGER NOT NULL CHECK(source_incarnation_generation > 0),
    source_external_identity_ref TEXT NOT NULL
        CHECK(length(source_external_identity_ref) > 0 AND length(source_external_identity_ref) <= 512
          AND trim(source_external_identity_ref) = source_external_identity_ref
          AND instr(source_external_identity_ref, char(0)) = 0),
    source_binding_version INTEGER NOT NULL CHECK(source_binding_version > 0),
    source_binding_digest TEXT NOT NULL CHECK(
        length(source_binding_digest) = 64 AND source_binding_digest NOT GLOB '*[^0-9a-f]*'
    ),
    source_fence_ref TEXT NOT NULL
        CHECK(length(source_fence_ref) > 0 AND length(source_fence_ref) <= 512
          AND trim(source_fence_ref) = source_fence_ref AND instr(source_fence_ref, char(0)) = 0),
    source_active_process_receipt_ref TEXT NOT NULL
        CHECK(length(source_active_process_receipt_ref) > 0 AND length(source_active_process_receipt_ref) <= 512
          AND trim(source_active_process_receipt_ref) = source_active_process_receipt_ref
          AND instr(source_active_process_receipt_ref, char(0)) = 0),
    source_binding_updated_at TEXT NOT NULL CHECK(
        length(source_binding_updated_at) > 0 AND length(source_binding_updated_at) <= 64
          AND trim(source_binding_updated_at) = source_binding_updated_at
          AND instr(source_binding_updated_at, char(0)) = 0 AND julianday(source_binding_updated_at) IS NOT NULL
    ),
    idempotency_namespace TEXT NOT NULL
        CHECK(length(idempotency_namespace) > 0 AND length(idempotency_namespace) <= 256
          AND trim(idempotency_namespace) = idempotency_namespace AND instr(idempotency_namespace, char(0)) = 0),
    idempotency_key_digest TEXT NOT NULL CHECK(
        length(idempotency_key_digest) = 64 AND idempotency_key_digest NOT GLOB '*[^0-9a-f]*'
    ),
    proof_request_digest TEXT NOT NULL CHECK(
        length(proof_request_digest) = 64 AND proof_request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    candidate_ref_digest TEXT NOT NULL CHECK(
        length(candidate_ref_digest) = 64 AND candidate_ref_digest NOT GLOB '*[^0-9a-f]*'
    ),
    content_intent_digest TEXT NOT NULL CHECK(
        length(content_intent_digest) = 64 AND content_intent_digest NOT GLOB '*[^0-9a-f]*'
    ),
    digest_key_id TEXT NOT NULL
        CHECK(length(digest_key_id) > 0 AND length(digest_key_id) <= 128
          AND trim(digest_key_id) = digest_key_id AND instr(digest_key_id, char(0)) = 0),
    digest_key_version INTEGER NOT NULL CHECK(digest_key_version > 0),
    ingest_kind TEXT NOT NULL CHECK(ingest_kind = 'content.single'),
    size_bytes INTEGER NOT NULL CHECK(size_bytes BETWEEN 1 AND 65536),
    media_type TEXT NOT NULL
        CHECK(length(media_type) > 0 AND length(media_type) <= 128
          AND trim(media_type) = media_type AND instr(media_type, char(0)) = 0),
    permitted_sink_role TEXT NOT NULL CHECK(permitted_sink_role = 'command_request'),
    aead_algorithm TEXT NOT NULL CHECK(aead_algorithm = 'aes-256-gcm'),
    key_wrap_profile TEXT NOT NULL
        CHECK(length(key_wrap_profile) > 0 AND length(key_wrap_profile) <= 128
          AND trim(key_wrap_profile) = key_wrap_profile AND instr(key_wrap_profile, char(0)) = 0),
    aad_profile TEXT NOT NULL CHECK(aad_profile = 'orch.content.single.aad/v1'),
    quota_snapshot_ref TEXT NOT NULL
        CHECK(length(quota_snapshot_ref) > 0 AND length(quota_snapshot_ref) <= 512
          AND trim(quota_snapshot_ref) = quota_snapshot_ref AND instr(quota_snapshot_ref, char(0)) = 0),
    quota_snapshot_digest TEXT NOT NULL CHECK(
        length(quota_snapshot_digest) = 64 AND quota_snapshot_digest NOT GLOB '*[^0-9a-f]*'
    ),
    retention_policy_id TEXT NOT NULL
        CHECK(length(retention_policy_id) > 0 AND length(retention_policy_id) <= 128
          AND trim(retention_policy_id) = retention_policy_id AND instr(retention_policy_id, char(0)) = 0),
    retention_policy_version INTEGER NOT NULL CHECK(retention_policy_version > 0),
    retention_policy_digest TEXT NOT NULL CHECK(
        length(retention_policy_digest) = 64 AND retention_policy_digest NOT GLOB '*[^0-9a-f]*'
    ),
    assembly_deadline_at TEXT NOT NULL CHECK(
        length(assembly_deadline_at) > 0 AND length(assembly_deadline_at) <= 64
          AND trim(assembly_deadline_at) = assembly_deadline_at
          AND instr(assembly_deadline_at, char(0)) = 0 AND julianday(assembly_deadline_at) IS NOT NULL
    ),
    prepare_operation_id TEXT NOT NULL DEFAULT '' CHECK(
        prepare_operation_id = '' OR (
            length(prepare_operation_id) > 0 AND length(prepare_operation_id) <= 128
              AND trim(prepare_operation_id) = prepare_operation_id
              AND instr(prepare_operation_id, char(0)) = 0
        )
    ),
    prepare_started_at TEXT NOT NULL DEFAULT '' CHECK(
        prepare_started_at = '' OR (
            length(prepare_started_at) > 0 AND length(prepare_started_at) <= 64
              AND trim(prepare_started_at) = prepare_started_at
              AND instr(prepare_started_at, char(0)) = 0 AND julianday(prepare_started_at) IS NOT NULL
        )
    ),
    revision INTEGER NOT NULL CHECK(revision IN (1, 2, 3)),
    state TEXT NOT NULL CHECK(state IN ('reserved', 'sealed', 'promoted')),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    created_at TEXT NOT NULL CHECK(length(trim(created_at)) > 0 AND julianday(created_at) IS NOT NULL)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL CHECK(length(trim(updated_at)) > 0 AND julianday(updated_at) IS NOT NULL)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(object_id, object_version),
    UNIQUE(ingest_reservation_id, object_id, object_version),
    UNIQUE(idempotency_namespace, idempotency_key_digest),
    FOREIGN KEY(source_identity_reservation_id, source_agent_id, source_identity_kind)
        REFERENCES orchestration_identity_reservations(reservation_id, agent_id, identity_kind) ON DELETE RESTRICT,
    FOREIGN KEY(source_incarnation_id, source_identity_reservation_id, source_agent_id, source_identity_kind)
        REFERENCES orchestration_execution_incarnations(incarnation_id, reservation_id, agent_id, identity_kind) ON DELETE RESTRICT,
    FOREIGN KEY(source_agent_id, source_binding_version)
        REFERENCES orchestration_current_bindings(agent_id, binding_version) ON DELETE RESTRICT,
    CHECK(julianday(assembly_deadline_at) > julianday(created_at)),
    CHECK(
        (prepare_operation_id = '' AND prepare_started_at = '')
        OR (prepare_operation_id <> '' AND prepare_started_at <> ''
            AND julianday(prepare_started_at) <= julianday(assembly_deadline_at))
    ),
    CHECK(
        (state = 'reserved' AND revision = 1)
        OR (state = 'sealed' AND revision = 2 AND prepare_operation_id <> '')
        OR (state = 'promoted' AND revision = 3 AND prepare_operation_id <> '')
    )
) STRICT;

CREATE TABLE orchestration_content_objects (
    object_id TEXT NOT NULL
        CHECK(length(object_id) > 0 AND length(object_id) <= 128
          AND trim(object_id) = object_id AND instr(object_id, char(0)) = 0),
    object_version INTEGER NOT NULL CHECK(object_version = 1),
    ingest_reservation_id TEXT NOT NULL UNIQUE
        CHECK(length(ingest_reservation_id) > 0 AND length(ingest_reservation_id) <= 128
          AND trim(ingest_reservation_id) = ingest_reservation_id AND instr(ingest_reservation_id, char(0)) = 0),
    owner_id TEXT NOT NULL
        CHECK(length(owner_id) > 0 AND length(owner_id) <= 256
          AND trim(owner_id) = owner_id AND instr(owner_id, char(0)) = 0),
    owner_generation INTEGER NOT NULL CHECK(owner_generation > 0),
    writer_fence_ref TEXT NOT NULL
        CHECK(length(writer_fence_ref) > 0 AND length(writer_fence_ref) <= 512
          AND trim(writer_fence_ref) = writer_fence_ref AND instr(writer_fence_ref, char(0)) = 0),
    security_time_generation INTEGER NOT NULL CHECK(security_time_generation > 0),
    workspace_ref TEXT NOT NULL
        CHECK(length(workspace_ref) > 0 AND length(workspace_ref) <= 512
          AND trim(workspace_ref) = workspace_ref AND instr(workspace_ref, char(0)) = 0),
    source_agent_id TEXT NOT NULL
        CHECK(length(source_agent_id) > 0 AND length(source_agent_id) <= 128
          AND trim(source_agent_id) = source_agent_id AND instr(source_agent_id, char(0)) = 0),
    source_fence_ref TEXT NOT NULL
        CHECK(length(source_fence_ref) > 0 AND length(source_fence_ref) <= 512
          AND trim(source_fence_ref) = source_fence_ref AND instr(source_fence_ref, char(0)) = 0),
    aead_algorithm TEXT NOT NULL CHECK(aead_algorithm = 'aes-256-gcm'),
    key_wrap_profile TEXT NOT NULL
        CHECK(length(key_wrap_profile) > 0 AND length(key_wrap_profile) <= 128
          AND trim(key_wrap_profile) = key_wrap_profile AND instr(key_wrap_profile, char(0)) = 0),
    key_handle_ref TEXT NOT NULL
        CHECK(length(key_handle_ref) > 0 AND length(key_handle_ref) <= 512
          AND trim(key_handle_ref) = key_handle_ref AND instr(key_handle_ref, char(0)) = 0),
    key_receipt_ref TEXT NOT NULL
        CHECK(length(key_receipt_ref) > 0 AND length(key_receipt_ref) <= 512
          AND trim(key_receipt_ref) = key_receipt_ref AND instr(key_receipt_ref, char(0)) = 0),
    key_receipt_digest TEXT NOT NULL CHECK(
        length(key_receipt_digest) = 64 AND instr(key_receipt_digest, char(0)) = 0
          AND key_receipt_digest NOT GLOB '*[^0-9a-f]*'
    ),
    nonce_hex TEXT NOT NULL CHECK(
        length(nonce_hex) = 24 AND instr(nonce_hex, char(0)) = 0
          AND nonce_hex NOT GLOB '*[^0-9a-f]*'
    ),
    aad_profile TEXT NOT NULL CHECK(aad_profile = 'orch.content.single.aad/v1'),
    aad_digest TEXT NOT NULL CHECK(
        length(aad_digest) = 64 AND instr(aad_digest, char(0)) = 0
          AND aad_digest NOT GLOB '*[^0-9a-f]*'
    ),
    media_type TEXT NOT NULL
        CHECK(length(media_type) > 0 AND length(media_type) <= 128
          AND trim(media_type) = media_type AND instr(media_type, char(0)) = 0),
    size_bytes INTEGER NOT NULL CHECK(size_bytes BETWEEN 1 AND 65536),
    ciphertext_sha256 TEXT NOT NULL CHECK(
        length(ciphertext_sha256) = 64 AND instr(ciphertext_sha256, char(0)) = 0
          AND ciphertext_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    sealed_payload_size_bytes INTEGER NOT NULL CHECK(sealed_payload_size_bytes BETWEEN 17 AND 65552),
    retention_policy_digest TEXT NOT NULL CHECK(
        length(retention_policy_digest) = 64 AND instr(retention_policy_digest, char(0)) = 0
          AND retention_policy_digest NOT GLOB '*[^0-9a-f]*'
    ),
    content_state TEXT NOT NULL CHECK(content_state IN ('prepared', 'active')),
    key_state TEXT NOT NULL CHECK(key_state = 'active'),
    ref_version INTEGER NOT NULL CHECK(ref_version IN (0, 1)),
    revision INTEGER NOT NULL CHECK(revision IN (1, 2)),
    promoted_at TEXT NOT NULL DEFAULT '' CHECK(
        promoted_at = '' OR (
            length(promoted_at) > 0 AND length(promoted_at) <= 64
              AND trim(promoted_at) = promoted_at AND instr(promoted_at, char(0)) = 0
              AND julianday(promoted_at) IS NOT NULL
        )
    ),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    created_at TEXT NOT NULL CHECK(
        length(created_at) > 0 AND length(created_at) <= 64
          AND trim(created_at) = created_at AND instr(created_at, char(0)) = 0
          AND julianday(created_at) IS NOT NULL
    )
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL CHECK(
        length(updated_at) > 0 AND length(updated_at) <= 64
          AND trim(updated_at) = updated_at AND instr(updated_at, char(0)) = 0
          AND julianday(updated_at) IS NOT NULL
    )
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY(object_id, object_version),
    UNIQUE(object_id, object_version, ingest_reservation_id),
    UNIQUE(key_handle_ref),
    UNIQUE(owner_id, nonce_hex),
    FOREIGN KEY(ingest_reservation_id, object_id, object_version)
        REFERENCES orchestration_content_ingest_reservations(ingest_reservation_id, object_id, object_version) ON DELETE RESTRICT,
    CHECK(sealed_payload_size_bytes = size_bytes + 16),
    CHECK(
        (content_state = 'prepared' AND revision = 1 AND ref_version = 0 AND promoted_at = '')
        OR (content_state = 'active' AND revision = 2 AND ref_version = 1 AND promoted_at <> '')
    )
) STRICT;

CREATE TABLE orchestration_content_private_payload_receipts (
    object_id TEXT NOT NULL
        CHECK(length(object_id) > 0 AND length(object_id) <= 128
          AND trim(object_id) = object_id AND instr(object_id, char(0)) = 0),
    object_version INTEGER NOT NULL CHECK(object_version = 1),
    ingest_reservation_id TEXT NOT NULL UNIQUE
        CHECK(length(ingest_reservation_id) > 0 AND length(ingest_reservation_id) <= 128
          AND trim(ingest_reservation_id) = ingest_reservation_id AND instr(ingest_reservation_id, char(0)) = 0),
    backend_kind TEXT NOT NULL CHECK(backend_kind = 'private_state_object_v1'),
    object_store_id TEXT NOT NULL
        CHECK(length(object_store_id) > 0 AND length(object_store_id) <= 128
          AND trim(object_store_id) = object_store_id AND instr(object_store_id, char(0)) = 0),
    payload_format TEXT NOT NULL CHECK(payload_format = 'aes256gcm-ciphertext-tag-v1'),
    private_payload_ref TEXT NOT NULL UNIQUE
        CHECK(length(private_payload_ref) > 0 AND length(private_payload_ref) <= 512
          AND trim(private_payload_ref) = private_payload_ref AND instr(private_payload_ref, char(0)) = 0),
    sealed_payload_size_bytes INTEGER NOT NULL CHECK(sealed_payload_size_bytes BETWEEN 17 AND 65552),
    ciphertext_sha256 TEXT NOT NULL CHECK(
        length(ciphertext_sha256) = 64 AND instr(ciphertext_sha256, char(0)) = 0
          AND ciphertext_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    store_operation_id TEXT NOT NULL UNIQUE
        CHECK(length(store_operation_id) > 0 AND length(store_operation_id) <= 128
          AND trim(store_operation_id) = store_operation_id AND instr(store_operation_id, char(0)) = 0),
    store_request_digest TEXT NOT NULL CHECK(
        length(store_request_digest) = 64 AND instr(store_request_digest, char(0)) = 0
          AND store_request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    store_receipt_ref TEXT NOT NULL
        CHECK(length(store_receipt_ref) > 0 AND length(store_receipt_ref) <= 512
          AND trim(store_receipt_ref) = store_receipt_ref AND instr(store_receipt_ref, char(0)) = 0),
    store_receipt_digest TEXT NOT NULL CHECK(
        length(store_receipt_digest) = 64 AND instr(store_receipt_digest, char(0)) = 0
          AND store_receipt_digest NOT GLOB '*[^0-9a-f]*'
    ),
    stored_at TEXT NOT NULL CHECK(
        length(stored_at) > 0 AND length(stored_at) <= 64
          AND trim(stored_at) = stored_at AND instr(stored_at, char(0)) = 0
          AND julianday(stored_at) IS NOT NULL
    ),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    PRIMARY KEY(object_id, object_version),
    FOREIGN KEY(object_id, object_version, ingest_reservation_id)
        REFERENCES orchestration_content_objects(object_id, object_version, ingest_reservation_id) ON DELETE RESTRICT
) STRICT;

CREATE TABLE orchestration_content_refs (
    reference_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(reference_id) > 0 AND length(reference_id) <= 128
          AND trim(reference_id) = reference_id AND instr(reference_id, char(0)) = 0),
    canonical_ref TEXT NOT NULL UNIQUE
        CHECK(length(canonical_ref) > 0 AND length(canonical_ref) <= 512
          AND trim(canonical_ref) = canonical_ref AND instr(canonical_ref, char(0)) = 0),
    object_id TEXT NOT NULL
        CHECK(length(object_id) > 0 AND length(object_id) <= 128
          AND trim(object_id) = object_id AND instr(object_id, char(0)) = 0),
    object_version INTEGER NOT NULL CHECK(object_version = 1),
    ingest_reservation_id TEXT NOT NULL
        CHECK(length(ingest_reservation_id) > 0 AND length(ingest_reservation_id) <= 128
          AND trim(ingest_reservation_id) = ingest_reservation_id AND instr(ingest_reservation_id, char(0)) = 0),
    ref_version INTEGER NOT NULL CHECK(ref_version = 1),
    reference_kind TEXT NOT NULL CHECK(reference_kind = 'command_request'),
    reference_state TEXT NOT NULL CHECK(reference_state = 'live'),
    caller_principal_ref TEXT NOT NULL
        CHECK(length(caller_principal_ref) > 0 AND length(caller_principal_ref) <= 512
          AND trim(caller_principal_ref) = caller_principal_ref AND instr(caller_principal_ref, char(0)) = 0),
    scope_ref TEXT NOT NULL
        CHECK(length(scope_ref) > 0 AND length(scope_ref) <= 512
          AND trim(scope_ref) = scope_ref AND instr(scope_ref, char(0)) = 0),
    source_agent_id TEXT NOT NULL
        CHECK(length(source_agent_id) > 0 AND length(source_agent_id) <= 128
          AND trim(source_agent_id) = source_agent_id AND instr(source_agent_id, char(0)) = 0),
    source_fence_ref TEXT NOT NULL
        CHECK(length(source_fence_ref) > 0 AND length(source_fence_ref) <= 512
          AND trim(source_fence_ref) = source_fence_ref AND instr(source_fence_ref, char(0)) = 0),
    proof_request_digest TEXT NOT NULL CHECK(
        length(proof_request_digest) = 64 AND instr(proof_request_digest, char(0)) = 0
          AND proof_request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    proof_id TEXT NOT NULL UNIQUE
        CHECK(length(proof_id) > 0 AND length(proof_id) <= 128
          AND trim(proof_id) = proof_id AND instr(proof_id, char(0)) = 0),
    proof_revision INTEGER NOT NULL CHECK(proof_revision > 0),
    owner_id TEXT NOT NULL
        CHECK(length(owner_id) > 0 AND length(owner_id) <= 256
          AND trim(owner_id) = owner_id AND instr(owner_id, char(0)) = 0),
    owner_generation INTEGER NOT NULL CHECK(owner_generation > 0),
    writer_fence_ref TEXT NOT NULL
        CHECK(length(writer_fence_ref) > 0 AND length(writer_fence_ref) <= 512
          AND trim(writer_fence_ref) = writer_fence_ref AND instr(writer_fence_ref, char(0)) = 0),
    security_time_generation INTEGER NOT NULL CHECK(security_time_generation > 0),
    owner_proof_ref TEXT NOT NULL
        CHECK(length(owner_proof_ref) > 0 AND length(owner_proof_ref) <= 512
          AND trim(owner_proof_ref) = owner_proof_ref AND instr(owner_proof_ref, char(0)) = 0),
    proof_snapshot_digest TEXT NOT NULL CHECK(
        length(proof_snapshot_digest) = 64 AND instr(proof_snapshot_digest, char(0)) = 0
          AND proof_snapshot_digest NOT GLOB '*[^0-9a-f]*'
    ),
    proof_expires_at TEXT NOT NULL CHECK(
        length(proof_expires_at) > 0 AND length(proof_expires_at) <= 64
          AND trim(proof_expires_at) = proof_expires_at AND instr(proof_expires_at, char(0)) = 0
          AND julianday(proof_expires_at) IS NOT NULL
    ),
    media_type TEXT NOT NULL
        CHECK(length(media_type) > 0 AND length(media_type) <= 128
          AND trim(media_type) = media_type AND instr(media_type, char(0)) = 0),
    size_bytes INTEGER NOT NULL CHECK(size_bytes BETWEEN 1 AND 65536),
    retention_policy_digest TEXT NOT NULL CHECK(
        length(retention_policy_digest) = 64 AND instr(retention_policy_digest, char(0)) = 0
          AND retention_policy_digest NOT GLOB '*[^0-9a-f]*'
    ),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    created_at TEXT NOT NULL CHECK(
        length(created_at) > 0 AND length(created_at) <= 64
          AND trim(created_at) = created_at AND instr(created_at, char(0)) = 0
          AND julianday(created_at) IS NOT NULL
    )
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(reference_id, canonical_ref, object_id, object_version, ref_version),
    UNIQUE(object_id, object_version, ref_version),
    FOREIGN KEY(object_id, object_version, ingest_reservation_id)
        REFERENCES orchestration_content_objects(object_id, object_version, ingest_reservation_id) ON DELETE RESTRICT,
    CHECK(julianday(proof_expires_at) > julianday(created_at))
) STRICT;

CREATE TABLE orchestration_command_content_deletion_fences (
    command_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(command_id) > 0 AND length(command_id) <= 128
          AND trim(command_id) = command_id AND instr(command_id, char(0)) = 0)
        REFERENCES orchestration_command_intent_profiles(command_id) ON DELETE RESTRICT,
    reference_id TEXT NOT NULL UNIQUE
        CHECK(length(reference_id) > 0 AND length(reference_id) <= 128
          AND trim(reference_id) = reference_id AND instr(reference_id, char(0)) = 0),
    canonical_ref TEXT NOT NULL UNIQUE
        CHECK(length(canonical_ref) > 0 AND length(canonical_ref) <= 512
          AND trim(canonical_ref) = canonical_ref AND instr(canonical_ref, char(0)) = 0),
    object_id TEXT NOT NULL
        CHECK(length(object_id) > 0 AND length(object_id) <= 128
          AND trim(object_id) = object_id AND instr(object_id, char(0)) = 0),
    object_version INTEGER NOT NULL CHECK(object_version = 1),
    ref_version INTEGER NOT NULL CHECK(ref_version = 1),
    fence_kind TEXT NOT NULL CHECK(fence_kind = 'non_terminal_command'),
    fence_state TEXT NOT NULL CHECK(fence_state = 'held'),
    command_profile_digest TEXT NOT NULL CHECK(
        length(command_profile_digest) = 64 AND instr(command_profile_digest, char(0)) = 0
          AND command_profile_digest NOT GLOB '*[^0-9a-f]*'
    ),
    source_fence_ref TEXT NOT NULL
        CHECK(length(source_fence_ref) > 0 AND length(source_fence_ref) <= 512
          AND trim(source_fence_ref) = source_fence_ref AND instr(source_fence_ref, char(0)) = 0),
    target_fence_ref TEXT NOT NULL
        CHECK(length(target_fence_ref) > 0 AND length(target_fence_ref) <= 512
          AND trim(target_fence_ref) = target_fence_ref AND instr(target_fence_ref, char(0)) = 0),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    created_at TEXT NOT NULL CHECK(
        length(created_at) > 0 AND length(created_at) <= 64
          AND trim(created_at) = created_at AND instr(created_at, char(0)) = 0
          AND julianday(created_at) IS NOT NULL
    )
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    FOREIGN KEY(reference_id, canonical_ref, object_id, object_version, ref_version)
        REFERENCES orchestration_content_refs(reference_id, canonical_ref, object_id, object_version, ref_version)
        ON DELETE RESTRICT
) STRICT;

CREATE INDEX idx_orchestration_content_ingest_state_deadline
    ON orchestration_content_ingest_reservations(workspace_ref, state, assembly_deadline_at);
CREATE INDEX idx_orchestration_content_objects_state
    ON orchestration_content_objects(workspace_ref, content_state, key_state);
CREATE INDEX idx_orchestration_content_refs_object
    ON orchestration_content_refs(object_id, object_version, reference_state, ref_version);
CREATE INDEX idx_orchestration_content_fences_object
    ON orchestration_command_content_deletion_fences(object_id, object_version, fence_state);

CREATE TRIGGER trg_orchestration_content_ingest_exact_identity
BEFORE INSERT ON orchestration_content_ingest_reservations
BEGIN
    SELECT CASE WHEN NEW.state <> 'reserved' OR NEW.revision <> 1
      OR NEW.prepare_operation_id <> '' OR NEW.prepare_started_at <> '' OR NOT EXISTS (
        SELECT 1 FROM orchestration_current_bindings AS binding
        WHERE binding.agent_id = NEW.source_agent_id
          AND binding.binding_version = NEW.source_binding_version
          AND binding.reservation_id = NEW.source_identity_reservation_id
          AND binding.reservation_generation = NEW.source_reservation_generation
          AND binding.incarnation_id = NEW.source_incarnation_id
          AND binding.incarnation_generation = NEW.source_incarnation_generation
          AND binding.identity_kind = NEW.source_identity_kind
          AND binding.external_identity_ref = NEW.source_external_identity_ref
          AND binding.source_fence_ref = NEW.source_fence_ref
          AND binding.active_process_receipt_ref = NEW.source_active_process_receipt_ref
          AND binding.binding_digest = NEW.source_binding_digest
          AND julianday(binding.updated_at) = julianday(NEW.source_binding_updated_at)
          AND binding.state = 'active' AND binding.schema_version = 1
          AND NOT EXISTS (
              SELECT 1 FROM orchestration_current_bindings AS newer
              WHERE newer.agent_id = binding.agent_id AND newer.binding_version > binding.binding_version
          )
    ) THEN RAISE(ABORT, 'content ingest identity relation mismatch') END;
END;

CREATE TRIGGER trg_orchestration_content_ingest_immutable_metadata
BEFORE UPDATE ON orchestration_content_ingest_reservations
WHEN NEW.ingest_reservation_id <> OLD.ingest_reservation_id
  OR NEW.ingest_ticket_id <> OLD.ingest_ticket_id OR NEW.object_id <> OLD.object_id
  OR NEW.object_version <> OLD.object_version OR NEW.owner_id <> OLD.owner_id
  OR NEW.owner_generation <> OLD.owner_generation OR NEW.writer_fence_ref <> OLD.writer_fence_ref
  OR NEW.security_time_generation <> OLD.security_time_generation OR NEW.workspace_ref <> OLD.workspace_ref
  OR NEW.caller_principal_ref <> OLD.caller_principal_ref OR NEW.scope_ref <> OLD.scope_ref
  OR NEW.source_agent_id <> OLD.source_agent_id OR NEW.source_identity_kind <> OLD.source_identity_kind
  OR NEW.source_identity_reservation_id <> OLD.source_identity_reservation_id
  OR NEW.source_reservation_generation <> OLD.source_reservation_generation
  OR NEW.source_incarnation_id <> OLD.source_incarnation_id
  OR NEW.source_incarnation_generation <> OLD.source_incarnation_generation
  OR NEW.source_external_identity_ref <> OLD.source_external_identity_ref
  OR NEW.source_binding_version <> OLD.source_binding_version OR NEW.source_binding_digest <> OLD.source_binding_digest
  OR NEW.source_fence_ref <> OLD.source_fence_ref
  OR NEW.source_active_process_receipt_ref <> OLD.source_active_process_receipt_ref
  OR NEW.source_binding_updated_at <> OLD.source_binding_updated_at
  OR NEW.idempotency_namespace <> OLD.idempotency_namespace OR NEW.idempotency_key_digest <> OLD.idempotency_key_digest
  OR NEW.proof_request_digest <> OLD.proof_request_digest OR NEW.candidate_ref_digest <> OLD.candidate_ref_digest
  OR NEW.content_intent_digest <> OLD.content_intent_digest OR NEW.digest_key_id <> OLD.digest_key_id
  OR NEW.digest_key_version <> OLD.digest_key_version OR NEW.ingest_kind <> OLD.ingest_kind
  OR NEW.size_bytes <> OLD.size_bytes OR NEW.media_type <> OLD.media_type
  OR NEW.permitted_sink_role <> OLD.permitted_sink_role OR NEW.aead_algorithm <> OLD.aead_algorithm
  OR NEW.key_wrap_profile <> OLD.key_wrap_profile OR NEW.aad_profile <> OLD.aad_profile
  OR NEW.quota_snapshot_ref <> OLD.quota_snapshot_ref OR NEW.quota_snapshot_digest <> OLD.quota_snapshot_digest
  OR NEW.retention_policy_id <> OLD.retention_policy_id
  OR NEW.retention_policy_version <> OLD.retention_policy_version
  OR NEW.retention_policy_digest <> OLD.retention_policy_digest
  OR NEW.assembly_deadline_at <> OLD.assembly_deadline_at OR NEW.schema_version <> OLD.schema_version
  OR NEW.created_at <> OLD.created_at
BEGIN
    SELECT RAISE(ABORT, 'content ingest metadata is immutable');
END;

CREATE TRIGGER trg_orchestration_content_ingest_state_machine
BEFORE UPDATE ON orchestration_content_ingest_reservations
WHEN NOT (
    (OLD.state = 'reserved' AND OLD.revision = 1 AND NEW.state = 'reserved' AND NEW.revision = 1
      AND OLD.prepare_operation_id = '' AND OLD.prepare_started_at = ''
      AND NEW.prepare_operation_id <> '' AND NEW.prepare_started_at <> ''
      AND julianday(NEW.prepare_started_at) <= julianday(OLD.assembly_deadline_at))
    OR
    (OLD.state = 'reserved' AND OLD.revision = 1 AND NEW.state = 'sealed' AND NEW.revision = 2
      AND OLD.prepare_operation_id <> '' AND NEW.prepare_operation_id = OLD.prepare_operation_id
      AND NEW.prepare_started_at = OLD.prepare_started_at
      AND EXISTS (SELECT 1 FROM orchestration_content_private_payload_receipts AS receipt
                  WHERE receipt.ingest_reservation_id = OLD.ingest_reservation_id))
    OR
    (OLD.state = 'sealed' AND OLD.revision = 2 AND NEW.state = 'promoted' AND NEW.revision = 3
      AND NEW.prepare_operation_id = OLD.prepare_operation_id AND NEW.prepare_started_at = OLD.prepare_started_at
      AND EXISTS (SELECT 1 FROM orchestration_content_objects AS object
                  WHERE object.ingest_reservation_id = OLD.ingest_reservation_id AND object.content_state = 'active')
      AND EXISTS (SELECT 1 FROM orchestration_content_refs AS ref
                  WHERE ref.ingest_reservation_id = OLD.ingest_reservation_id AND ref.reference_state = 'live'))
)
BEGIN
    SELECT RAISE(ABORT, 'content ingest state transition is invalid');
END;

CREATE TRIGGER trg_orchestration_content_ingest_no_delete
BEFORE DELETE ON orchestration_content_ingest_reservations
BEGIN SELECT RAISE(ABORT, 'content ingest reservations are immutable'); END;

CREATE TRIGGER trg_orchestration_content_objects_exact_reservation
BEFORE INSERT ON orchestration_content_objects
BEGIN
    SELECT CASE WHEN NEW.content_state <> 'prepared' OR NEW.revision <> 1 OR NEW.ref_version <> 0 OR NOT EXISTS (
        SELECT 1 FROM orchestration_content_ingest_reservations AS reservation
        WHERE reservation.ingest_reservation_id = NEW.ingest_reservation_id
          AND reservation.object_id = NEW.object_id AND reservation.object_version = NEW.object_version
          AND reservation.state = 'reserved' AND reservation.prepare_operation_id <> ''
          AND reservation.owner_id = NEW.owner_id
          AND reservation.owner_generation = NEW.owner_generation
          AND reservation.writer_fence_ref = NEW.writer_fence_ref
          AND reservation.security_time_generation = NEW.security_time_generation
          AND reservation.workspace_ref = NEW.workspace_ref AND reservation.source_agent_id = NEW.source_agent_id
          AND reservation.source_fence_ref = NEW.source_fence_ref
          AND reservation.aead_algorithm = NEW.aead_algorithm
          AND reservation.key_wrap_profile = NEW.key_wrap_profile
          AND reservation.aad_profile = NEW.aad_profile AND reservation.media_type = NEW.media_type
          AND reservation.size_bytes = NEW.size_bytes
          AND reservation.retention_policy_digest = NEW.retention_policy_digest
    ) THEN RAISE(ABORT, 'content object reservation relation mismatch') END;
END;

CREATE TRIGGER trg_orchestration_content_objects_immutable_metadata
BEFORE UPDATE ON orchestration_content_objects
WHEN NEW.object_id <> OLD.object_id OR NEW.object_version <> OLD.object_version
  OR NEW.ingest_reservation_id <> OLD.ingest_reservation_id OR NEW.owner_id <> OLD.owner_id
  OR NEW.owner_generation <> OLD.owner_generation OR NEW.writer_fence_ref <> OLD.writer_fence_ref
  OR NEW.security_time_generation <> OLD.security_time_generation OR NEW.workspace_ref <> OLD.workspace_ref
  OR NEW.source_agent_id <> OLD.source_agent_id OR NEW.source_fence_ref <> OLD.source_fence_ref
  OR NEW.aead_algorithm <> OLD.aead_algorithm OR NEW.key_wrap_profile <> OLD.key_wrap_profile
  OR NEW.key_handle_ref <> OLD.key_handle_ref OR NEW.key_receipt_ref <> OLD.key_receipt_ref
  OR NEW.key_receipt_digest <> OLD.key_receipt_digest OR NEW.nonce_hex <> OLD.nonce_hex
  OR NEW.aad_profile <> OLD.aad_profile OR NEW.aad_digest <> OLD.aad_digest
  OR NEW.media_type <> OLD.media_type OR NEW.size_bytes <> OLD.size_bytes
  OR NEW.ciphertext_sha256 <> OLD.ciphertext_sha256
  OR NEW.sealed_payload_size_bytes <> OLD.sealed_payload_size_bytes
  OR NEW.retention_policy_digest <> OLD.retention_policy_digest OR NEW.key_state <> OLD.key_state
  OR NEW.schema_version <> OLD.schema_version OR NEW.created_at <> OLD.created_at
BEGIN SELECT RAISE(ABORT, 'content object metadata is immutable'); END;

CREATE TRIGGER trg_orchestration_content_objects_prepared_to_active
BEFORE UPDATE ON orchestration_content_objects
WHEN NOT (
    OLD.content_state = 'prepared' AND OLD.revision = 1 AND OLD.ref_version = 0
    AND NEW.content_state = 'active' AND NEW.revision = 2 AND NEW.ref_version = 1
    AND julianday(NEW.promoted_at) IS NOT NULL
    AND EXISTS (SELECT 1 FROM orchestration_content_ingest_reservations AS reservation
                WHERE reservation.ingest_reservation_id = OLD.ingest_reservation_id AND reservation.state = 'sealed')
    AND EXISTS (SELECT 1 FROM orchestration_content_private_payload_receipts AS receipt
                WHERE receipt.ingest_reservation_id = OLD.ingest_reservation_id
                  AND receipt.ciphertext_sha256 = OLD.ciphertext_sha256
                  AND receipt.sealed_payload_size_bytes = OLD.sealed_payload_size_bytes)
)
BEGIN SELECT RAISE(ABORT, 'content object promotion is invalid'); END;

CREATE TRIGGER trg_orchestration_content_objects_no_delete
BEFORE DELETE ON orchestration_content_objects
BEGIN SELECT RAISE(ABORT, 'content objects are immutable'); END;

CREATE TRIGGER trg_orchestration_content_payload_exact_relation
BEFORE INSERT ON orchestration_content_private_payload_receipts
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM orchestration_content_objects AS object
        JOIN orchestration_content_ingest_reservations AS reservation
          ON reservation.ingest_reservation_id = object.ingest_reservation_id
        WHERE object.object_id = NEW.object_id AND object.object_version = NEW.object_version
          AND object.ingest_reservation_id = NEW.ingest_reservation_id
          AND object.content_state = 'prepared' AND reservation.state = 'reserved'
          AND object.ciphertext_sha256 = NEW.ciphertext_sha256
          AND object.sealed_payload_size_bytes = NEW.sealed_payload_size_bytes
          AND NEW.sealed_payload_size_bytes = reservation.size_bytes + 16
    ) THEN RAISE(ABORT, 'content payload receipt relation mismatch') END;
END;

CREATE TRIGGER trg_orchestration_content_payload_no_update
BEFORE UPDATE ON orchestration_content_private_payload_receipts
BEGIN SELECT RAISE(ABORT, 'content payload receipts are immutable'); END;
CREATE TRIGGER trg_orchestration_content_payload_no_delete
BEFORE DELETE ON orchestration_content_private_payload_receipts
BEGIN SELECT RAISE(ABORT, 'content payload receipts are immutable'); END;

CREATE TRIGGER trg_orchestration_content_refs_exact_active_relation
BEFORE INSERT ON orchestration_content_refs
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM orchestration_content_objects AS object
        JOIN orchestration_content_ingest_reservations AS reservation
          ON reservation.ingest_reservation_id = object.ingest_reservation_id
        WHERE object.object_id = NEW.object_id AND object.object_version = NEW.object_version
          AND object.ingest_reservation_id = NEW.ingest_reservation_id
          AND object.content_state = 'active' AND object.key_state = 'active' AND object.ref_version = NEW.ref_version
          AND reservation.state = 'sealed' AND reservation.caller_principal_ref = NEW.caller_principal_ref
          AND reservation.scope_ref = NEW.scope_ref AND reservation.source_agent_id = NEW.source_agent_id
          AND reservation.source_fence_ref = NEW.source_fence_ref
          AND reservation.proof_request_digest = NEW.proof_request_digest
          AND reservation.owner_id = NEW.owner_id AND reservation.owner_generation = NEW.owner_generation
          AND reservation.writer_fence_ref = NEW.writer_fence_ref
          AND reservation.security_time_generation = NEW.security_time_generation
          AND object.media_type = NEW.media_type AND object.size_bytes = NEW.size_bytes
          AND object.retention_policy_digest = NEW.retention_policy_digest
    ) THEN RAISE(ABORT, 'content ref relation mismatch') END;
END;

CREATE TRIGGER trg_orchestration_content_refs_no_update
BEFORE UPDATE ON orchestration_content_refs
BEGIN SELECT RAISE(ABORT, 'content refs are immutable'); END;
CREATE TRIGGER trg_orchestration_content_refs_no_delete
BEFORE DELETE ON orchestration_content_refs
BEGIN SELECT RAISE(ABORT, 'content refs are immutable'); END;

CREATE TRIGGER trg_orchestration_command_profiles_content_relation
BEFORE INSERT ON orchestration_command_intent_profiles
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM orchestration_command_intents AS command
        JOIN orchestration_content_refs AS ref ON ref.canonical_ref = command.request_content_ref
        JOIN orchestration_content_objects AS object
          ON object.object_id = ref.object_id AND object.object_version = ref.object_version
        JOIN orchestration_content_ingest_reservations AS reservation
          ON reservation.ingest_reservation_id = ref.ingest_reservation_id
        WHERE command.command_id = NEW.command_id AND command.command_kind = 'send_message'
          AND command.caller_principal_ref = ref.caller_principal_ref AND command.scope_ref = ref.scope_ref
          AND command.source_fence_ref = ref.source_fence_ref AND ref.source_agent_id = NEW.source_agent_id
          AND ref.source_fence_ref = NEW.source_fence_ref AND ref.reference_state = 'live'
          AND object.content_state = 'active' AND object.key_state = 'active' AND reservation.state = 'promoted'
          AND julianday(ref.proof_expires_at) > julianday(NEW.authorization_decided_at)
          AND julianday(ref.proof_expires_at) <= julianday(NEW.authorization_expires_at)
          AND julianday(ref.proof_expires_at) <= julianday(NEW.caller_auth_expires_at)
    ) THEN RAISE(ABORT, 'orchestration command profile content relation mismatch') END;
END;

CREATE TRIGGER trg_orchestration_command_profiles_create_content_fence
AFTER INSERT ON orchestration_command_intent_profiles
BEGIN
    INSERT INTO orchestration_command_content_deletion_fences (
        command_id, reference_id, canonical_ref, object_id, object_version, ref_version,
        fence_kind, fence_state, command_profile_digest, source_fence_ref, target_fence_ref, schema_version
    )
    SELECT NEW.command_id, ref.reference_id, ref.canonical_ref, ref.object_id, ref.object_version, ref.ref_version,
           'non_terminal_command', 'held', NEW.complete_intent_digest, NEW.source_fence_ref, NEW.target_fence_ref, 1
    FROM orchestration_command_intents AS command
    JOIN orchestration_content_refs AS ref ON ref.canonical_ref = command.request_content_ref
    WHERE command.command_id = NEW.command_id;
END;

CREATE TRIGGER trg_orchestration_command_content_fences_exact_relation
BEFORE INSERT ON orchestration_command_content_deletion_fences
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM orchestration_command_intent_profiles AS profile
        JOIN orchestration_command_intents AS command ON command.command_id = profile.command_id
        JOIN orchestration_content_refs AS ref ON ref.canonical_ref = command.request_content_ref
        WHERE profile.command_id = NEW.command_id AND profile.complete_intent_digest = NEW.command_profile_digest
          AND profile.source_fence_ref = NEW.source_fence_ref AND profile.target_fence_ref = NEW.target_fence_ref
          AND ref.reference_id = NEW.reference_id AND ref.canonical_ref = NEW.canonical_ref
          AND ref.object_id = NEW.object_id AND ref.object_version = NEW.object_version
          AND ref.ref_version = NEW.ref_version AND ref.reference_state = 'live'
    ) THEN RAISE(ABORT, 'command content deletion fence relation mismatch') END;
END;

CREATE TRIGGER trg_orchestration_command_content_fences_no_update
BEFORE UPDATE ON orchestration_command_content_deletion_fences
BEGIN SELECT RAISE(ABORT, 'command content deletion fences are immutable'); END;
CREATE TRIGGER trg_orchestration_command_content_fences_no_delete
BEFORE DELETE ON orchestration_command_content_deletion_fences
BEGIN SELECT RAISE(ABORT, 'command content deletion fences are immutable'); END;
