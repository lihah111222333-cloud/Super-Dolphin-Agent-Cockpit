-- RESOURCE reserved-only persistence skeleton.
-- This migration is non-authorizing: it does not implement claim, permit,
-- release, provider dispatch, telemetry ingestion, or any default budget.

CREATE TABLE orchestration_resource_effective_policy_snapshots (
    policy_snapshot_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(policy_snapshot_id) BETWEEN 1 AND 128
          AND trim(policy_snapshot_id) = policy_snapshot_id AND instr(policy_snapshot_id, char(0)) = 0),
    policy_lineage_id TEXT NOT NULL
        CHECK(length(policy_lineage_id) BETWEEN 1 AND 128
          AND trim(policy_lineage_id) = policy_lineage_id AND instr(policy_lineage_id, char(0)) = 0),
    policy_revision INTEGER NOT NULL CHECK(policy_revision > 0),
    requested_kind TEXT NOT NULL CHECK(requested_kind = 'send_message'),
    caller_principal_ref TEXT NOT NULL
        CHECK(length(caller_principal_ref) BETWEEN 1 AND 512
          AND trim(caller_principal_ref) = caller_principal_ref AND instr(caller_principal_ref, char(0)) = 0),
    scope_ref TEXT NOT NULL
        CHECK(length(scope_ref) BETWEEN 1 AND 512
          AND trim(scope_ref) = scope_ref AND instr(scope_ref, char(0)) = 0),
    policy_applicability_digest TEXT NOT NULL CHECK(
        length(policy_applicability_digest) = 64
          AND instr(policy_applicability_digest, char(0)) = 0
          AND policy_applicability_digest NOT GLOB '*[^0-9a-f]*'
    ),
    dimension_set TEXT NOT NULL CHECK(dimension_set = 'orch.resource.dimensions/v1'),
    source_policy_set_ref TEXT NOT NULL
        CHECK(length(source_policy_set_ref) BETWEEN 1 AND 512
          AND trim(source_policy_set_ref) = source_policy_set_ref AND instr(source_policy_set_ref, char(0)) = 0),
    source_policy_set_digest TEXT NOT NULL CHECK(
        length(source_policy_set_digest) = 64
          AND instr(source_policy_set_digest, char(0)) = 0
          AND source_policy_set_digest NOT GLOB '*[^0-9a-f]*'
    ),
    precedence_trace_ref TEXT NOT NULL
        CHECK(length(precedence_trace_ref) BETWEEN 1 AND 512
          AND trim(precedence_trace_ref) = precedence_trace_ref AND instr(precedence_trace_ref, char(0)) = 0),
    precedence_trace_digest TEXT NOT NULL CHECK(
        length(precedence_trace_digest) = 64
          AND instr(precedence_trace_digest, char(0)) = 0
          AND precedence_trace_digest NOT GLOB '*[^0-9a-f]*'
    ),
    canonicalization_profile TEXT NOT NULL
        CHECK(canonicalization_profile = 'orch.resource.effective-policy/v1'),
    canonical_policy_hash TEXT NOT NULL CHECK(
        length(canonical_policy_hash) = 64
          AND instr(canonical_policy_hash, char(0)) = 0
          AND canonical_policy_hash NOT GLOB '*[^0-9a-f]*'
    ),
    policy_snapshot_digest TEXT NOT NULL UNIQUE CHECK(
        length(policy_snapshot_digest) = 64
          AND instr(policy_snapshot_digest, char(0)) = 0
          AND policy_snapshot_digest NOT GLOB '*[^0-9a-f]*'
    ),
    effective_limits_digest TEXT NOT NULL CHECK(
        length(effective_limits_digest) = 64
          AND instr(effective_limits_digest, char(0)) = 0
          AND effective_limits_digest NOT GLOB '*[^0-9a-f]*'
    ),
    release_condition_digest TEXT NOT NULL CHECK(
        length(release_condition_digest) = 64
          AND instr(release_condition_digest, char(0)) = 0
          AND release_condition_digest NOT GLOB '*[^0-9a-f]*'
    ),
    limit_agent_count INTEGER NOT NULL CHECK(limit_agent_count > 0),
    limit_in_flight_count INTEGER NOT NULL CHECK(limit_in_flight_count > 0),
    limit_fanout_count INTEGER NOT NULL CHECK(limit_fanout_count > 0),
    limit_concurrency_count INTEGER NOT NULL CHECK(limit_concurrency_count > 0),
    limit_wall_time_ms INTEGER NOT NULL CHECK(limit_wall_time_ms > 0),
    limit_input_tokens INTEGER NOT NULL CHECK(limit_input_tokens > 0),
    limit_output_tokens INTEGER NOT NULL CHECK(limit_output_tokens > 0),
    limit_cost_microunit INTEGER NOT NULL CHECK(limit_cost_microunit > 0),
    limit_content_bytes INTEGER NOT NULL CHECK(limit_content_bytes > 0),
    limit_ingress_bytes INTEGER NOT NULL CHECK(limit_ingress_bytes > 0),
    idempotency_namespace TEXT NOT NULL
        CHECK(length(idempotency_namespace) BETWEEN 1 AND 256
          AND trim(idempotency_namespace) = idempotency_namespace AND instr(idempotency_namespace, char(0)) = 0),
    idempotency_key_digest TEXT NOT NULL CHECK(
        length(idempotency_key_digest) = 64
          AND instr(idempotency_key_digest, char(0)) = 0
          AND idempotency_key_digest NOT GLOB '*[^0-9a-f]*'
    ),
    publish_request_digest TEXT NOT NULL CHECK(
        length(publish_request_digest) = 64
          AND instr(publish_request_digest, char(0)) = 0
          AND publish_request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    publish_operation_id TEXT NOT NULL UNIQUE
        CHECK(length(publish_operation_id) BETWEEN 1 AND 128
          AND trim(publish_operation_id) = publish_operation_id
          AND instr(publish_operation_id, char(0)) = 0),
    publish_operation_digest TEXT NOT NULL CHECK(
        length(publish_operation_digest) = 64
          AND instr(publish_operation_digest, char(0)) = 0
          AND publish_operation_digest NOT GLOB '*[^0-9a-f]*'
    ),
    publish_receipt_ref TEXT NOT NULL
        CHECK(length(publish_receipt_ref) BETWEEN 1 AND 512
          AND trim(publish_receipt_ref) = publish_receipt_ref
          AND instr(publish_receipt_ref, char(0)) = 0),
    publish_receipt_digest TEXT NOT NULL UNIQUE CHECK(
        length(publish_receipt_digest) = 64
          AND instr(publish_receipt_digest, char(0)) = 0
          AND publish_receipt_digest NOT GLOB '*[^0-9a-f]*'
    ),
    policy_owner_id TEXT NOT NULL
        CHECK(length(policy_owner_id) BETWEEN 1 AND 256
          AND trim(policy_owner_id) = policy_owner_id AND instr(policy_owner_id, char(0)) = 0),
    policy_owner_generation INTEGER NOT NULL CHECK(policy_owner_generation > 0),
    policy_writer_fence_ref TEXT NOT NULL
        CHECK(length(policy_writer_fence_ref) BETWEEN 1 AND 512
          AND trim(policy_writer_fence_ref) = policy_writer_fence_ref
          AND instr(policy_writer_fence_ref, char(0)) = 0),
    policy_owner_proof_ref TEXT NOT NULL
        CHECK(length(policy_owner_proof_ref) BETWEEN 1 AND 512
          AND trim(policy_owner_proof_ref) = policy_owner_proof_ref
          AND instr(policy_owner_proof_ref, char(0)) = 0),
    deployment_fence_ref TEXT NOT NULL
        CHECK(length(deployment_fence_ref) BETWEEN 1 AND 512
          AND trim(deployment_fence_ref) = deployment_fence_ref AND instr(deployment_fence_ref, char(0)) = 0),
    security_time_generation INTEGER NOT NULL CHECK(security_time_generation > 0),
    effective_at TEXT NOT NULL CHECK(
        length(effective_at) BETWEEN 1 AND 64 AND trim(effective_at) = effective_at
          AND instr(effective_at, char(0)) = 0 AND julianday(effective_at) IS NOT NULL
    ),
    valid_until TEXT NOT NULL CHECK(
        length(valid_until) BETWEEN 1 AND 64 AND trim(valid_until) = valid_until
          AND instr(valid_until, char(0)) = 0 AND julianday(valid_until) IS NOT NULL
    ),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    created_at TEXT NOT NULL CHECK(
        length(created_at) BETWEEN 1 AND 64 AND trim(created_at) = created_at
          AND instr(created_at, char(0)) = 0 AND julianday(created_at) IS NOT NULL
    ),
    UNIQUE(policy_lineage_id, policy_revision),
    UNIQUE(caller_principal_ref, scope_ref, requested_kind, policy_revision),
    UNIQUE(idempotency_namespace, idempotency_key_digest),
    UNIQUE(
        policy_snapshot_id, policy_revision, policy_snapshot_digest,
        effective_limits_digest, release_condition_digest
    ),
    CHECK(julianday(effective_at) <= julianday(created_at)),
    CHECK(julianday(valid_until) > julianday(effective_at)),
    CHECK(julianday(valid_until) > julianday(created_at))
) STRICT;

CREATE TABLE orchestration_resource_reservations (
    reservation_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(reservation_id) BETWEEN 1 AND 128
          AND trim(reservation_id) = reservation_id AND instr(reservation_id, char(0)) = 0),
    reservation_version INTEGER NOT NULL CHECK(reservation_version = 1),
    resource_snapshot_id TEXT NOT NULL UNIQUE
        CHECK(length(resource_snapshot_id) BETWEEN 1 AND 128
          AND trim(resource_snapshot_id) = resource_snapshot_id AND instr(resource_snapshot_id, char(0)) = 0),
    resource_snapshot_digest TEXT NOT NULL UNIQUE CHECK(
        length(resource_snapshot_digest) = 64
          AND instr(resource_snapshot_digest, char(0)) = 0
          AND resource_snapshot_digest NOT GLOB '*[^0-9a-f]*'
    ),
    resource_status TEXT NOT NULL CHECK(resource_status = 'reserved'),
    requested_kind TEXT NOT NULL CHECK(requested_kind = 'send_message'),
    command_id TEXT NOT NULL UNIQUE
        REFERENCES orchestration_command_intent_profiles(command_id) ON DELETE RESTRICT,
    command_version INTEGER NOT NULL CHECK(command_version = 1),
    command_profile_digest TEXT NOT NULL CHECK(
        length(command_profile_digest) = 64
          AND instr(command_profile_digest, char(0)) = 0
          AND command_profile_digest NOT GLOB '*[^0-9a-f]*'
    ),
    caller_principal_ref TEXT NOT NULL
        CHECK(length(caller_principal_ref) BETWEEN 1 AND 512
          AND trim(caller_principal_ref) = caller_principal_ref AND instr(caller_principal_ref, char(0)) = 0),
    scope_ref TEXT NOT NULL
        CHECK(length(scope_ref) BETWEEN 1 AND 512
          AND trim(scope_ref) = scope_ref AND instr(scope_ref, char(0)) = 0),
    source_agent_id TEXT NOT NULL
        CHECK(length(source_agent_id) BETWEEN 1 AND 128
          AND trim(source_agent_id) = source_agent_id AND instr(source_agent_id, char(0)) = 0),
    source_binding_version INTEGER NOT NULL CHECK(source_binding_version > 0),
    source_fence_ref TEXT NOT NULL
        CHECK(length(source_fence_ref) BETWEEN 1 AND 512
          AND trim(source_fence_ref) = source_fence_ref AND instr(source_fence_ref, char(0)) = 0),
    target_agent_id TEXT NOT NULL
        CHECK(length(target_agent_id) BETWEEN 1 AND 128
          AND trim(target_agent_id) = target_agent_id AND instr(target_agent_id, char(0)) = 0),
    target_binding_version INTEGER NOT NULL CHECK(target_binding_version > 0),
    target_fence_ref TEXT NOT NULL
        CHECK(length(target_fence_ref) BETWEEN 1 AND 512
          AND trim(target_fence_ref) = target_fence_ref AND instr(target_fence_ref, char(0)) = 0),
    identity_fence_digest TEXT NOT NULL CHECK(
        length(identity_fence_digest) = 64
          AND instr(identity_fence_digest, char(0)) = 0
          AND identity_fence_digest NOT GLOB '*[^0-9a-f]*'
    ),
    content_reference_id TEXT NOT NULL
        CHECK(length(content_reference_id) BETWEEN 1 AND 128
          AND trim(content_reference_id) = content_reference_id AND instr(content_reference_id, char(0)) = 0),
    content_ref TEXT NOT NULL
        CHECK(length(content_ref) BETWEEN 1 AND 512
          AND trim(content_ref) = content_ref AND instr(content_ref, char(0)) = 0),
    content_object_id TEXT NOT NULL
        CHECK(length(content_object_id) BETWEEN 1 AND 128
          AND trim(content_object_id) = content_object_id AND instr(content_object_id, char(0)) = 0),
    content_object_version INTEGER NOT NULL CHECK(content_object_version = 1),
    content_ref_version INTEGER NOT NULL CHECK(content_ref_version = 1),
    content_owner_snapshot_digest TEXT NOT NULL CHECK(
        length(content_owner_snapshot_digest) = 64
          AND instr(content_owner_snapshot_digest, char(0)) = 0
          AND content_owner_snapshot_digest NOT GLOB '*[^0-9a-f]*'
    ),
    content_admission_snapshot_digest TEXT NOT NULL CHECK(
        length(content_admission_snapshot_digest) = 64
          AND instr(content_admission_snapshot_digest, char(0)) = 0
          AND content_admission_snapshot_digest NOT GLOB '*[^0-9a-f]*'
    ),
    policy_snapshot_id TEXT NOT NULL,
    policy_revision INTEGER NOT NULL CHECK(policy_revision > 0),
    policy_snapshot_digest TEXT NOT NULL CHECK(
        length(policy_snapshot_digest) = 64
          AND instr(policy_snapshot_digest, char(0)) = 0
          AND policy_snapshot_digest NOT GLOB '*[^0-9a-f]*'
    ),
    policy_canonical_hash TEXT NOT NULL CHECK(
        length(policy_canonical_hash) = 64
          AND instr(policy_canonical_hash, char(0)) = 0
          AND policy_canonical_hash NOT GLOB '*[^0-9a-f]*'
    ),
    effective_limits_digest TEXT NOT NULL CHECK(
        length(effective_limits_digest) = 64
          AND instr(effective_limits_digest, char(0)) = 0
          AND effective_limits_digest NOT GLOB '*[^0-9a-f]*'
    ),
    reserved_agent_count INTEGER NOT NULL CHECK(reserved_agent_count > 0),
    reserved_in_flight_count INTEGER NOT NULL CHECK(reserved_in_flight_count > 0),
    reserved_fanout_count INTEGER NOT NULL CHECK(reserved_fanout_count > 0),
    reserved_concurrency_count INTEGER NOT NULL CHECK(reserved_concurrency_count > 0),
    reserved_wall_time_ms INTEGER NOT NULL CHECK(reserved_wall_time_ms > 0),
    reserved_input_tokens INTEGER NOT NULL CHECK(reserved_input_tokens > 0),
    reserved_output_tokens INTEGER NOT NULL CHECK(reserved_output_tokens > 0),
    reserved_cost_microunit INTEGER NOT NULL CHECK(reserved_cost_microunit > 0),
    reserved_content_bytes INTEGER NOT NULL CHECK(reserved_content_bytes > 0),
    reserved_ingress_bytes INTEGER NOT NULL CHECK(reserved_ingress_bytes > 0),
    usage_digest_domain TEXT NOT NULL CHECK(usage_digest_domain = 'orch.resource.usage-ledger/v1'),
    usage_watermark INTEGER NOT NULL CHECK(usage_watermark = 0),
    usage_digest TEXT NOT NULL CHECK(
        usage_digest = 'c290c54b3c8466b643e128da65eda3cc1a43f85799c73b38e8dd862c7d4cbe31'
          AND instr(usage_digest, char(0)) = 0
    ),
    release_condition_digest TEXT NOT NULL CHECK(
        length(release_condition_digest) = 64
          AND instr(release_condition_digest, char(0)) = 0
          AND release_condition_digest NOT GLOB '*[^0-9a-f]*'
    ),
    idempotency_namespace TEXT NOT NULL
        CHECK(length(idempotency_namespace) BETWEEN 1 AND 256
          AND trim(idempotency_namespace) = idempotency_namespace AND instr(idempotency_namespace, char(0)) = 0),
    idempotency_key_digest TEXT NOT NULL CHECK(
        length(idempotency_key_digest) = 64
          AND instr(idempotency_key_digest, char(0)) = 0
          AND idempotency_key_digest NOT GLOB '*[^0-9a-f]*'
    ),
    reservation_request_digest TEXT NOT NULL CHECK(
        length(reservation_request_digest) = 64
          AND instr(reservation_request_digest, char(0)) = 0
          AND reservation_request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    reservation_operation_id TEXT NOT NULL UNIQUE
        CHECK(length(reservation_operation_id) BETWEEN 1 AND 128
          AND trim(reservation_operation_id) = reservation_operation_id
          AND instr(reservation_operation_id, char(0)) = 0),
    reservation_operation_digest TEXT NOT NULL CHECK(
        length(reservation_operation_digest) = 64
          AND instr(reservation_operation_digest, char(0)) = 0
          AND reservation_operation_digest NOT GLOB '*[^0-9a-f]*'
    ),
    reservation_receipt_ref TEXT NOT NULL
        CHECK(length(reservation_receipt_ref) BETWEEN 1 AND 512
          AND trim(reservation_receipt_ref) = reservation_receipt_ref
          AND instr(reservation_receipt_ref, char(0)) = 0),
    reservation_receipt_digest TEXT NOT NULL UNIQUE CHECK(
        length(reservation_receipt_digest) = 64
          AND instr(reservation_receipt_digest, char(0)) = 0
          AND reservation_receipt_digest NOT GLOB '*[^0-9a-f]*'
    ),
    admission_owner_id TEXT NOT NULL
        CHECK(length(admission_owner_id) BETWEEN 1 AND 256
          AND trim(admission_owner_id) = admission_owner_id AND instr(admission_owner_id, char(0)) = 0),
    admission_owner_generation INTEGER NOT NULL CHECK(admission_owner_generation > 0),
    admission_writer_fence_ref TEXT NOT NULL
        CHECK(length(admission_writer_fence_ref) BETWEEN 1 AND 512
          AND trim(admission_writer_fence_ref) = admission_writer_fence_ref
          AND instr(admission_writer_fence_ref, char(0)) = 0),
    admission_deployment_fence_ref TEXT NOT NULL
        CHECK(length(admission_deployment_fence_ref) BETWEEN 1 AND 512
          AND trim(admission_deployment_fence_ref) = admission_deployment_fence_ref
          AND instr(admission_deployment_fence_ref, char(0)) = 0),
    security_time_generation INTEGER NOT NULL CHECK(security_time_generation > 0),
    owner_proof_ref TEXT NOT NULL
        CHECK(length(owner_proof_ref) BETWEEN 1 AND 512
          AND trim(owner_proof_ref) = owner_proof_ref AND instr(owner_proof_ref, char(0)) = 0),
    valid_until TEXT NOT NULL CHECK(
        length(valid_until) BETWEEN 1 AND 64 AND trim(valid_until) = valid_until
          AND instr(valid_until, char(0)) = 0 AND julianday(valid_until) IS NOT NULL
    ),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    created_at TEXT NOT NULL CHECK(
        length(created_at) BETWEEN 1 AND 64 AND trim(created_at) = created_at
          AND instr(created_at, char(0)) = 0 AND julianday(created_at) IS NOT NULL
    ),
    UNIQUE(reservation_id, reservation_version),
    UNIQUE(idempotency_namespace, idempotency_key_digest),
    FOREIGN KEY(source_agent_id, source_binding_version)
        REFERENCES orchestration_current_bindings(agent_id, binding_version) ON DELETE RESTRICT,
    FOREIGN KEY(target_agent_id, target_binding_version)
        REFERENCES orchestration_current_bindings(agent_id, binding_version) ON DELETE RESTRICT,
    FOREIGN KEY(content_reference_id, content_ref, content_object_id, content_object_version, content_ref_version)
        REFERENCES orchestration_content_refs(reference_id, canonical_ref, object_id, object_version, ref_version)
        ON DELETE RESTRICT,
    FOREIGN KEY(
        policy_snapshot_id, policy_revision, policy_snapshot_digest,
        effective_limits_digest, release_condition_digest
    )
        REFERENCES orchestration_resource_effective_policy_snapshots(
            policy_snapshot_id, policy_revision, policy_snapshot_digest,
            effective_limits_digest, release_condition_digest
        ) ON DELETE RESTRICT,
    CHECK(julianday(valid_until) > julianday(created_at))
) STRICT;

CREATE TABLE orchestration_resource_usage_ledger (
    usage_entry_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(usage_entry_id) BETWEEN 1 AND 128
          AND trim(usage_entry_id) = usage_entry_id AND instr(usage_entry_id, char(0)) = 0),
    reservation_id TEXT NOT NULL,
    reservation_version INTEGER NOT NULL CHECK(reservation_version = 1),
    ledger_sequence INTEGER NOT NULL CHECK(ledger_sequence > 0),
    previous_usage_digest TEXT NOT NULL CHECK(
        length(previous_usage_digest) = 64
          AND instr(previous_usage_digest, char(0)) = 0
          AND previous_usage_digest NOT GLOB '*[^0-9a-f]*'
    ),
    usage_entry_digest TEXT NOT NULL UNIQUE CHECK(
        length(usage_entry_digest) = 64
          AND instr(usage_entry_digest, char(0)) = 0
          AND usage_entry_digest NOT GLOB '*[^0-9a-f]*'
    ),
    command_id TEXT NOT NULL
        CHECK(length(command_id) BETWEEN 1 AND 128
          AND trim(command_id) = command_id AND instr(command_id, char(0)) = 0),
    command_version INTEGER NOT NULL CHECK(command_version = 1),
    command_profile_digest TEXT NOT NULL CHECK(
        length(command_profile_digest) = 64
          AND instr(command_profile_digest, char(0)) = 0
          AND command_profile_digest NOT GLOB '*[^0-9a-f]*'
    ),
    identity_fence_digest TEXT NOT NULL CHECK(
        length(identity_fence_digest) = 64
          AND instr(identity_fence_digest, char(0)) = 0
          AND identity_fence_digest NOT GLOB '*[^0-9a-f]*'
    ),
    content_admission_snapshot_digest TEXT NOT NULL CHECK(
        length(content_admission_snapshot_digest) = 64
          AND instr(content_admission_snapshot_digest, char(0)) = 0
          AND content_admission_snapshot_digest NOT GLOB '*[^0-9a-f]*'
    ),
    policy_snapshot_id TEXT NOT NULL,
    policy_revision INTEGER NOT NULL CHECK(policy_revision > 0),
    policy_snapshot_digest TEXT NOT NULL CHECK(
        length(policy_snapshot_digest) = 64
          AND instr(policy_snapshot_digest, char(0)) = 0
          AND policy_snapshot_digest NOT GLOB '*[^0-9a-f]*'
    ),
    effective_limits_digest TEXT NOT NULL CHECK(
        length(effective_limits_digest) = 64
          AND instr(effective_limits_digest, char(0)) = 0
          AND effective_limits_digest NOT GLOB '*[^0-9a-f]*'
    ),
    release_condition_digest TEXT NOT NULL CHECK(
        length(release_condition_digest) = 64
          AND instr(release_condition_digest, char(0)) = 0
          AND release_condition_digest NOT GLOB '*[^0-9a-f]*'
    ),
    dimension_set TEXT NOT NULL CHECK(dimension_set = 'orch.resource.dimensions/v1'),
    dimension TEXT NOT NULL CHECK(dimension IN (
        'agent', 'in_flight', 'fanout', 'concurrency', 'wall_time',
        'input_tokens', 'output_tokens', 'cost', 'content_bytes', 'ingress_bytes'
    )),
    unit TEXT NOT NULL CHECK(unit IN ('count', 'ms', 'token', 'microunit', 'byte')),
    delta_value INTEGER NOT NULL CHECK(delta_value > 0),
    cumulative_value INTEGER NOT NULL CHECK(cumulative_value > 0),
    attempt_id TEXT NOT NULL
        CHECK(length(attempt_id) BETWEEN 1 AND 128
          AND trim(attempt_id) = attempt_id AND instr(attempt_id, char(0)) = 0),
    attempt_fence_ref TEXT NOT NULL
        CHECK(length(attempt_fence_ref) BETWEEN 1 AND 512
          AND trim(attempt_fence_ref) = attempt_fence_ref AND instr(attempt_fence_ref, char(0)) = 0),
    measurement_source_ref TEXT NOT NULL
        CHECK(length(measurement_source_ref) BETWEEN 1 AND 512
          AND trim(measurement_source_ref) = measurement_source_ref
          AND instr(measurement_source_ref, char(0)) = 0),
    measurement_id TEXT NOT NULL
        CHECK(length(measurement_id) BETWEEN 1 AND 128
          AND trim(measurement_id) = measurement_id AND instr(measurement_id, char(0)) = 0),
    source_sequence INTEGER NOT NULL CHECK(source_sequence > 0),
    observed_at TEXT NOT NULL CHECK(
        length(observed_at) BETWEEN 1 AND 64 AND trim(observed_at) = observed_at
          AND instr(observed_at, char(0)) = 0 AND julianday(observed_at) IS NOT NULL
    ),
    measurement_receipt_ref TEXT NOT NULL
        CHECK(length(measurement_receipt_ref) BETWEEN 1 AND 512
          AND trim(measurement_receipt_ref) = measurement_receipt_ref
          AND instr(measurement_receipt_ref, char(0)) = 0),
    measurement_receipt_digest TEXT NOT NULL CHECK(
        length(measurement_receipt_digest) = 64
          AND instr(measurement_receipt_digest, char(0)) = 0
          AND measurement_receipt_digest NOT GLOB '*[^0-9a-f]*'
    ),
    usage_operation_id TEXT NOT NULL UNIQUE
        CHECK(length(usage_operation_id) BETWEEN 1 AND 128
          AND trim(usage_operation_id) = usage_operation_id AND instr(usage_operation_id, char(0)) = 0),
    usage_request_digest TEXT NOT NULL CHECK(
        length(usage_request_digest) = 64
          AND instr(usage_request_digest, char(0)) = 0
          AND usage_request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    admission_owner_id TEXT NOT NULL
        CHECK(length(admission_owner_id) BETWEEN 1 AND 256
          AND trim(admission_owner_id) = admission_owner_id AND instr(admission_owner_id, char(0)) = 0),
    admission_owner_generation INTEGER NOT NULL CHECK(admission_owner_generation > 0),
    admission_writer_fence_ref TEXT NOT NULL
        CHECK(length(admission_writer_fence_ref) BETWEEN 1 AND 512
          AND trim(admission_writer_fence_ref) = admission_writer_fence_ref
          AND instr(admission_writer_fence_ref, char(0)) = 0),
    admission_deployment_fence_ref TEXT NOT NULL
        CHECK(length(admission_deployment_fence_ref) BETWEEN 1 AND 512
          AND trim(admission_deployment_fence_ref) = admission_deployment_fence_ref
          AND instr(admission_deployment_fence_ref, char(0)) = 0),
    security_time_generation INTEGER NOT NULL CHECK(security_time_generation > 0),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    recorded_at TEXT NOT NULL CHECK(
        length(recorded_at) BETWEEN 1 AND 64 AND trim(recorded_at) = recorded_at
          AND instr(recorded_at, char(0)) = 0 AND julianday(recorded_at) IS NOT NULL
    ),
    UNIQUE(reservation_id, reservation_version, ledger_sequence),
    UNIQUE(reservation_id, reservation_version, dimension, measurement_source_ref, measurement_id),
    FOREIGN KEY(reservation_id, reservation_version)
        REFERENCES orchestration_resource_reservations(reservation_id, reservation_version) ON DELETE RESTRICT,
    FOREIGN KEY(
        policy_snapshot_id, policy_revision, policy_snapshot_digest,
        effective_limits_digest, release_condition_digest
    )
        REFERENCES orchestration_resource_effective_policy_snapshots(
            policy_snapshot_id, policy_revision, policy_snapshot_digest,
            effective_limits_digest, release_condition_digest
        ) ON DELETE RESTRICT,
    CHECK(
        (dimension IN ('agent', 'in_flight', 'fanout', 'concurrency') AND unit = 'count')
        OR (dimension = 'wall_time' AND unit = 'ms')
        OR (dimension IN ('input_tokens', 'output_tokens') AND unit = 'token')
        OR (dimension = 'cost' AND unit = 'microunit')
        OR (dimension IN ('content_bytes', 'ingress_bytes') AND unit = 'byte')
    )
) STRICT;

CREATE INDEX idx_orchestration_resource_policy_scope
    ON orchestration_resource_effective_policy_snapshots(
        caller_principal_ref, scope_ref, requested_kind, policy_revision DESC
    );
CREATE INDEX idx_orchestration_resource_reservation_scope
    ON orchestration_resource_reservations(caller_principal_ref, scope_ref, resource_status, valid_until);
CREATE INDEX idx_orchestration_resource_reservation_policy
    ON orchestration_resource_reservations(policy_snapshot_id, policy_revision);
CREATE INDEX idx_orchestration_resource_reservation_agents
    ON orchestration_resource_reservations(source_agent_id, target_agent_id);
CREATE INDEX idx_orchestration_resource_usage_reservation
    ON orchestration_resource_usage_ledger(reservation_id, reservation_version, ledger_sequence);
CREATE INDEX idx_orchestration_resource_usage_dimension
    ON orchestration_resource_usage_ledger(dimension, observed_at);

CREATE TRIGGER trg_orchestration_resource_policy_revision_contiguous
BEFORE INSERT ON orchestration_resource_effective_policy_snapshots
WHEN NEW.policy_revision <> COALESCE((
    SELECT MAX(policy_revision) + 1
    FROM orchestration_resource_effective_policy_snapshots
    WHERE policy_lineage_id = NEW.policy_lineage_id
), 1)
  OR (NEW.policy_revision > 1 AND EXISTS (
      SELECT 1 FROM orchestration_resource_effective_policy_snapshots AS predecessor
      WHERE predecessor.policy_lineage_id = NEW.policy_lineage_id
        AND predecessor.policy_revision = NEW.policy_revision - 1
        AND julianday(NEW.effective_at) < julianday(predecessor.effective_at)
  ))
BEGIN
    SELECT RAISE(ABORT, 'resource policy revision or effective time is invalid');
END;

CREATE TRIGGER trg_orchestration_resource_policy_no_update
BEFORE UPDATE ON orchestration_resource_effective_policy_snapshots
BEGIN
    SELECT RAISE(ABORT, 'resource policy snapshots are immutable');
END;

CREATE TRIGGER trg_orchestration_resource_policy_no_delete
BEFORE DELETE ON orchestration_resource_effective_policy_snapshots
BEGIN
    SELECT RAISE(ABORT, 'resource policy snapshots are immutable');
END;

CREATE TRIGGER trg_orchestration_resource_reservation_exact_relation
BEFORE INSERT ON orchestration_resource_reservations
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1
        FROM orchestration_command_intents AS command
        JOIN orchestration_command_intent_profiles AS profile
          ON profile.command_id = command.command_id
        JOIN orchestration_current_bindings AS source_binding
          ON source_binding.agent_id = profile.source_agent_id
         AND source_binding.binding_version = profile.source_binding_version
        JOIN orchestration_current_bindings AS target_binding
          ON target_binding.agent_id = profile.target_agent_id
         AND target_binding.binding_version = profile.target_binding_version
        JOIN orchestration_command_content_deletion_fences AS content_fence
          ON content_fence.command_id = command.command_id
        JOIN orchestration_content_refs AS content_ref
          ON content_ref.reference_id = content_fence.reference_id
         AND content_ref.canonical_ref = content_fence.canonical_ref
         AND content_ref.object_id = content_fence.object_id
         AND content_ref.object_version = content_fence.object_version
         AND content_ref.ref_version = content_fence.ref_version
        JOIN orchestration_content_objects AS content_object
          ON content_object.object_id = content_ref.object_id
         AND content_object.object_version = content_ref.object_version
         AND content_object.ingest_reservation_id = content_ref.ingest_reservation_id
        JOIN orchestration_content_ingest_reservations AS content_ingest
          ON content_ingest.ingest_reservation_id = content_ref.ingest_reservation_id
        JOIN orchestration_resource_effective_policy_snapshots AS policy
          ON policy.policy_snapshot_id = NEW.policy_snapshot_id
         AND policy.policy_revision = NEW.policy_revision
         AND policy.policy_snapshot_digest = NEW.policy_snapshot_digest
         AND policy.effective_limits_digest = NEW.effective_limits_digest
        WHERE command.command_id = NEW.command_id
          AND command.command_version = NEW.command_version
          AND command.command_kind = NEW.requested_kind
          AND command.request_digest = NEW.command_profile_digest
          AND profile.complete_intent_digest = NEW.command_profile_digest
          AND command.caller_principal_ref = NEW.caller_principal_ref
          AND profile.caller_profile <> ''
          AND command.scope_ref = NEW.scope_ref
          AND profile.source_agent_id = NEW.source_agent_id
          AND profile.source_binding_version = NEW.source_binding_version
          AND profile.source_fence_ref = NEW.source_fence_ref
          AND profile.target_agent_id = NEW.target_agent_id
          AND profile.target_binding_version = NEW.target_binding_version
          AND profile.target_fence_ref = NEW.target_fence_ref
          AND source_binding.reservation_id = profile.source_reservation_id
          AND source_binding.reservation_generation = profile.source_reservation_generation
          AND source_binding.incarnation_id = profile.source_incarnation_id
          AND source_binding.incarnation_generation = profile.source_incarnation_generation
          AND source_binding.identity_kind = profile.source_identity_kind
          AND source_binding.external_identity_ref = profile.source_external_identity_ref
          AND source_binding.binding_digest = profile.source_binding_digest
          AND source_binding.source_fence_ref = profile.source_fence_ref
          AND source_binding.active_process_receipt_ref = profile.source_active_process_receipt_ref
          AND source_binding.state = profile.source_binding_state
          AND source_binding.schema_version = profile.source_binding_schema_version
          AND julianday(source_binding.updated_at) = julianday(profile.source_binding_updated_at)
          AND target_binding.reservation_id = profile.target_reservation_id
          AND target_binding.reservation_generation = profile.target_reservation_generation
          AND target_binding.incarnation_id = profile.target_incarnation_id
          AND target_binding.incarnation_generation = profile.target_incarnation_generation
          AND target_binding.identity_kind = profile.target_identity_kind
          AND target_binding.external_identity_ref = profile.target_external_identity_ref
          AND target_binding.binding_digest = profile.target_binding_digest
          AND target_binding.source_fence_ref = profile.target_fence_ref
          AND target_binding.active_process_receipt_ref = profile.target_active_process_receipt_ref
          AND target_binding.state = profile.target_binding_state
          AND target_binding.schema_version = profile.target_binding_schema_version
          AND julianday(target_binding.updated_at) = julianday(profile.target_binding_updated_at)
          AND source_binding.state = 'active'
          AND target_binding.state = 'active'
          AND NOT EXISTS (
              SELECT 1 FROM orchestration_current_bindings AS newer_source
              WHERE newer_source.agent_id = source_binding.agent_id
                AND newer_source.binding_version > source_binding.binding_version
          )
          AND NOT EXISTS (
              SELECT 1 FROM orchestration_current_bindings AS newer_target
              WHERE newer_target.agent_id = target_binding.agent_id
                AND newer_target.binding_version > target_binding.binding_version
          )
          AND command.request_content_ref = NEW.content_ref
          AND content_ref.reference_id = NEW.content_reference_id
          AND content_ref.canonical_ref = NEW.content_ref
          AND content_ref.object_id = NEW.content_object_id
          AND content_ref.object_version = NEW.content_object_version
          AND content_ref.ref_version = NEW.content_ref_version
          AND content_ref.caller_principal_ref = NEW.caller_principal_ref
          AND content_ref.scope_ref = NEW.scope_ref
          AND content_ref.source_agent_id = NEW.source_agent_id
          AND content_ref.source_fence_ref = NEW.source_fence_ref
          AND content_ref.proof_snapshot_digest = NEW.content_owner_snapshot_digest
          AND content_ref.reference_state = 'live'
          AND content_object.content_state = 'active'
          AND content_object.key_state = 'active'
          AND content_ingest.caller_principal_ref = NEW.caller_principal_ref
          AND content_ingest.scope_ref = NEW.scope_ref
          AND content_ingest.source_agent_id = NEW.source_agent_id
          AND content_ingest.source_fence_ref = NEW.source_fence_ref
          AND content_ingest.state = 'promoted'
          AND content_fence.fence_state = 'held'
          AND content_fence.command_profile_digest = NEW.command_profile_digest
          AND content_fence.source_fence_ref = NEW.source_fence_ref
          AND content_fence.target_fence_ref = NEW.target_fence_ref
          AND policy.requested_kind = NEW.requested_kind
          AND policy.caller_principal_ref = NEW.caller_principal_ref
          AND policy.scope_ref = NEW.scope_ref
          AND policy.canonical_policy_hash = NEW.policy_canonical_hash
          AND policy.release_condition_digest = NEW.release_condition_digest
          AND julianday(NEW.created_at) >= julianday(policy.effective_at)
          AND julianday(NEW.created_at) < julianday(policy.valid_until)
          AND julianday(NEW.created_at) < julianday(content_ref.proof_expires_at)
          AND julianday(NEW.valid_until) <= julianday(policy.valid_until)
          AND julianday(NEW.valid_until) <= julianday(content_ref.proof_expires_at)
          AND julianday(NEW.valid_until) <= julianday(profile.caller_auth_expires_at)
          AND julianday(NEW.valid_until) <= julianday(profile.authorization_expires_at)
          AND NEW.reserved_agent_count <= policy.limit_agent_count
          AND NEW.reserved_in_flight_count <= policy.limit_in_flight_count
          AND NEW.reserved_fanout_count <= policy.limit_fanout_count
          AND NEW.reserved_concurrency_count <= policy.limit_concurrency_count
          AND NEW.reserved_wall_time_ms <= policy.limit_wall_time_ms
          AND NEW.reserved_input_tokens <= policy.limit_input_tokens
          AND NEW.reserved_output_tokens <= policy.limit_output_tokens
          AND NEW.reserved_cost_microunit <= policy.limit_cost_microunit
          AND NEW.reserved_content_bytes <= policy.limit_content_bytes
          AND NEW.reserved_ingress_bytes <= policy.limit_ingress_bytes
          AND NOT EXISTS (
              SELECT 1
              FROM orchestration_resource_effective_policy_snapshots AS newer_policy
              WHERE newer_policy.caller_principal_ref = policy.caller_principal_ref
                AND newer_policy.scope_ref = policy.scope_ref
                AND newer_policy.requested_kind = policy.requested_kind
                AND newer_policy.policy_revision > policy.policy_revision
                AND julianday(newer_policy.effective_at) <= julianday(NEW.created_at)
                AND julianday(NEW.created_at) < julianday(newer_policy.valid_until)
          )
    ) THEN RAISE(ABORT, 'resource reservation relation mismatch') END;
END;

CREATE TRIGGER trg_orchestration_resource_reservation_no_update
BEFORE UPDATE ON orchestration_resource_reservations
BEGIN
    SELECT RAISE(ABORT, 'resource reservations are immutable');
END;

CREATE TRIGGER trg_orchestration_resource_reservation_no_delete
BEFORE DELETE ON orchestration_resource_reservations
BEGIN
    SELECT RAISE(ABORT, 'resource reservations are immutable');
END;

CREATE TRIGGER trg_orchestration_resource_usage_insert_disabled
BEFORE INSERT ON orchestration_resource_usage_ledger
BEGIN
    SELECT RAISE(ABORT, 'resource usage ledger writer is disabled in schema v133');
END;

CREATE TRIGGER trg_orchestration_resource_usage_no_update
BEFORE UPDATE ON orchestration_resource_usage_ledger
BEGIN
    SELECT RAISE(ABORT, 'resource usage ledger is append-only');
END;

CREATE TRIGGER trg_orchestration_resource_usage_no_delete
BEFORE DELETE ON orchestration_resource_usage_ledger
BEGIN
    SELECT RAISE(ABORT, 'resource usage ledger is append-only');
END;
