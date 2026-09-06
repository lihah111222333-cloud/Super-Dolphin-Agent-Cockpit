-- Durable wait-operation planning head and append-only per-target journal.
-- This candidate is intentionally independent from accesswait provisioning:
-- plan rows contain opaque grant/wait references only.  A later owner may bind
-- those references in one outer transaction; this migration never writes
-- provider, direct-peer, event, or public-tool state.

CREATE TABLE orchestration_wait_operations (
    operation_key TEXT PRIMARY KEY NOT NULL
        CHECK(length(trim(operation_key)) > 0 AND length(operation_key) <= 256
            AND trim(operation_key) = operation_key AND instr(operation_key, char(0)) = 0),
    operation_digest TEXT NOT NULL CHECK(
        length(operation_digest) = 64
        AND operation_digest NOT GLOB '*[^0-9a-f]*'
    ),
    target_set_digest TEXT NOT NULL CHECK(
        length(target_set_digest) = 64
        AND target_set_digest NOT GLOB '*[^0-9a-f]*'
    ),
    caller_principal_ref TEXT NOT NULL
        CHECK(length(trim(caller_principal_ref)) > 0 AND length(caller_principal_ref) <= 512
            AND trim(caller_principal_ref) = caller_principal_ref
            AND instr(caller_principal_ref, char(0)) = 0),
    scope_ref TEXT NOT NULL
        CHECK(length(trim(scope_ref)) > 0 AND length(scope_ref) <= 512
            AND trim(scope_ref) = scope_ref AND instr(scope_ref, char(0)) = 0),

    source_agent_id TEXT NOT NULL
        CHECK(length(trim(source_agent_id)) > 0 AND length(source_agent_id) <= 128
            AND trim(source_agent_id) = source_agent_id AND instr(source_agent_id, char(0)) = 0),
    source_reservation_id TEXT NOT NULL
        CHECK(length(trim(source_reservation_id)) > 0 AND length(source_reservation_id) <= 128
            AND trim(source_reservation_id) = source_reservation_id AND instr(source_reservation_id, char(0)) = 0),
    source_reservation_generation INTEGER NOT NULL CHECK(source_reservation_generation > 0),
    source_incarnation_id TEXT NOT NULL
        CHECK(length(trim(source_incarnation_id)) > 0 AND length(source_incarnation_id) <= 128
            AND trim(source_incarnation_id) = source_incarnation_id AND instr(source_incarnation_id, char(0)) = 0),
    source_incarnation_generation INTEGER NOT NULL CHECK(source_incarnation_generation > 0),
    source_binding_version INTEGER NOT NULL CHECK(source_binding_version > 0),
    source_binding_digest TEXT NOT NULL CHECK(
        length(source_binding_digest) = 64
        AND source_binding_digest NOT GLOB '*[^0-9a-f]*'
    ),
    source_fence_ref TEXT NOT NULL
        CHECK(length(trim(source_fence_ref)) > 0 AND length(source_fence_ref) <= 512
            AND trim(source_fence_ref) = source_fence_ref AND instr(source_fence_ref, char(0)) = 0),

    issued_at TEXT NOT NULL CHECK(
        length(trim(issued_at)) > 0 AND length(issued_at) <= 64
        AND trim(issued_at) = issued_at AND instr(issued_at, char(0)) = 0
        AND julianday(issued_at) IS NOT NULL
    ),
    deadline_at TEXT NOT NULL CHECK(
        length(trim(deadline_at)) > 0 AND length(deadline_at) <= 64
        AND trim(deadline_at) = deadline_at AND instr(deadline_at, char(0)) = 0
        AND julianday(deadline_at) IS NOT NULL
    ),
    target_count INTEGER NOT NULL CHECK(target_count BETWEEN 1 AND 32),
    revision INTEGER NOT NULL CHECK(revision > 0),
    status TEXT NOT NULL CHECK(status IN (
        'provisioning', 'ready', 'partial', 'outcome_unknown',
        'cleanup_pending', 'cleanup_cleared'
    )),
    uncertain_phase TEXT NOT NULL DEFAULT '' CHECK(uncertain_phase IN (
        '', 'operation_create', 'target_transition', 'grant_create',
        'wait_create', 'cleanup'
    )),
    cleanup_request_digest TEXT NOT NULL DEFAULT '' CHECK(
        cleanup_request_digest = '' OR (
            length(cleanup_request_digest) = 64
            AND cleanup_request_digest NOT GLOB '*[^0-9a-f]*'
        )
    ),
    cleanup_reason_ref TEXT NOT NULL DEFAULT '' CHECK(
        length(cleanup_reason_ref) <= 512
        AND trim(cleanup_reason_ref) = cleanup_reason_ref
        AND instr(cleanup_reason_ref, char(0)) = 0
    ),
    resolved_at TEXT NOT NULL DEFAULT '' CHECK(
        length(resolved_at) <= 64 AND trim(resolved_at) = resolved_at
        AND instr(resolved_at, char(0)) = 0
        AND (resolved_at = '' OR julianday(resolved_at) IS NOT NULL)
    ),
    created_at TEXT NOT NULL CHECK(
        length(trim(created_at)) > 0 AND length(created_at) <= 64
        AND trim(created_at) = created_at AND instr(created_at, char(0)) = 0
        AND julianday(created_at) IS NOT NULL
    ),
    updated_at TEXT NOT NULL CHECK(
        length(trim(updated_at)) > 0 AND length(updated_at) <= 64
        AND trim(updated_at) = updated_at AND instr(updated_at, char(0)) = 0
        AND julianday(updated_at) IS NOT NULL
    ),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),

    UNIQUE(operation_key, operation_digest),
    CHECK(julianday(deadline_at) > julianday(issued_at)),
    CHECK(
        (status IN ('provisioning', 'ready', 'partial')
            AND uncertain_phase = '' AND cleanup_request_digest = ''
            AND cleanup_reason_ref = '' AND resolved_at = '')
        OR (status = 'outcome_unknown'
            AND uncertain_phase IN ('operation_create', 'target_transition', 'grant_create', 'wait_create')
            AND cleanup_request_digest = '' AND cleanup_reason_ref = '' AND resolved_at = '')
        OR (status = 'outcome_unknown'
            AND uncertain_phase = 'cleanup' AND cleanup_request_digest <> ''
            AND cleanup_reason_ref <> '' AND resolved_at = '')
        OR (status = 'cleanup_pending'
            AND uncertain_phase = 'cleanup' AND cleanup_request_digest <> ''
            AND cleanup_reason_ref <> '' AND resolved_at = '')
        OR (status = 'cleanup_cleared'
            AND uncertain_phase = 'cleanup' AND cleanup_request_digest <> ''
            AND cleanup_reason_ref <> '' AND resolved_at <> '')
    ),
    FOREIGN KEY(source_reservation_id, source_agent_id)
        REFERENCES orchestration_identity_reservations(reservation_id, agent_id)
        ON DELETE RESTRICT,
    FOREIGN KEY(source_incarnation_id)
        REFERENCES orchestration_execution_incarnations(incarnation_id)
        ON DELETE RESTRICT
) STRICT;

CREATE INDEX idx_orchestration_wait_operations_status
    ON orchestration_wait_operations(status, deadline_at, operation_key);

-- A target journal row is immutable; every lifecycle change appends a new
-- contiguous version for the same operation/target key.
CREATE TABLE orchestration_wait_operation_target_journal (
    operation_key TEXT NOT NULL,
    operation_digest TEXT NOT NULL,
    target_key TEXT NOT NULL
        CHECK(length(trim(target_key)) > 0 AND length(target_key) <= 128
            AND trim(target_key) = target_key AND instr(target_key, char(0)) = 0),
    target_index INTEGER NOT NULL CHECK(target_index BETWEEN 1 AND 32),
    version INTEGER NOT NULL CHECK(version > 0),
    target_digest TEXT NOT NULL CHECK(
        length(target_digest) = 64 AND target_digest NOT GLOB '*[^0-9a-f]*'
    ),
    record_digest TEXT NOT NULL CHECK(
        length(record_digest) = 64 AND record_digest NOT GLOB '*[^0-9a-f]*'
    ),
    previous_record_digest TEXT NOT NULL DEFAULT '' CHECK(
        previous_record_digest = '' OR (
            length(previous_record_digest) = 64
            AND previous_record_digest NOT GLOB '*[^0-9a-f]*'
        )
    ),

    target_agent_id TEXT NOT NULL
        CHECK(length(trim(target_agent_id)) > 0 AND length(target_agent_id) <= 128
            AND trim(target_agent_id) = target_agent_id AND instr(target_agent_id, char(0)) = 0),
    target_reservation_id TEXT NOT NULL
        CHECK(length(trim(target_reservation_id)) > 0 AND length(target_reservation_id) <= 128
            AND trim(target_reservation_id) = target_reservation_id AND instr(target_reservation_id, char(0)) = 0),
    target_reservation_generation INTEGER NOT NULL CHECK(target_reservation_generation > 0),
    target_incarnation_id TEXT NOT NULL
        CHECK(length(trim(target_incarnation_id)) > 0 AND length(target_incarnation_id) <= 128
            AND trim(target_incarnation_id) = target_incarnation_id AND instr(target_incarnation_id, char(0)) = 0),
    target_incarnation_generation INTEGER NOT NULL CHECK(target_incarnation_generation > 0),
    target_binding_version INTEGER NOT NULL CHECK(target_binding_version > 0),
    target_binding_digest TEXT NOT NULL CHECK(
        length(target_binding_digest) = 64
        AND target_binding_digest NOT GLOB '*[^0-9a-f]*'
    ),
    target_fence_ref TEXT NOT NULL
        CHECK(length(trim(target_fence_ref)) > 0 AND length(target_fence_ref) <= 512
            AND trim(target_fence_ref) = target_fence_ref AND instr(target_fence_ref, char(0)) = 0),

    source_id TEXT NOT NULL
        CHECK(length(trim(source_id)) > 0 AND length(source_id) <= 256
            AND trim(source_id) = source_id AND instr(source_id, char(0)) = 0),
    source_epoch TEXT NOT NULL
        CHECK(length(trim(source_epoch)) > 0 AND length(source_epoch) <= 128
            AND trim(source_epoch) = source_epoch AND instr(source_epoch, char(0)) = 0),
    lane TEXT NOT NULL CHECK(lane IN ('control', 'output', 'content')),
    selector_version INTEGER NOT NULL CHECK(selector_version = 1),
    selector_json TEXT NOT NULL CHECK(
        length(trim(selector_json)) > 0 AND length(selector_json) <= 4096
        AND trim(selector_json) = selector_json AND json_valid(selector_json)
    ),
    selector_digest TEXT NOT NULL CHECK(
        length(selector_digest) = 64 AND selector_digest NOT GLOB '*[^0-9a-f]*'
    ),
    event_types_json TEXT NOT NULL CHECK(
        length(trim(event_types_json)) > 0 AND length(event_types_json) <= 4096
        AND trim(event_types_json) = event_types_json AND json_valid(event_types_json)
    ),
    after_source_sequence INTEGER NOT NULL CHECK(after_source_sequence >= 0),
    after_cursor TEXT NOT NULL DEFAULT '' CHECK(length(after_cursor) <= 512),
    deadline_at TEXT NOT NULL CHECK(
        length(trim(deadline_at)) > 0 AND length(deadline_at) <= 64
        AND trim(deadline_at) = deadline_at AND instr(deadline_at, char(0)) = 0
        AND julianday(deadline_at) IS NOT NULL
    ),

    original_grant_id TEXT NOT NULL
        CHECK(length(trim(original_grant_id)) > 0 AND length(original_grant_id) <= 128
            AND trim(original_grant_id) = original_grant_id AND instr(original_grant_id, char(0)) = 0),
    grant_namespace TEXT NOT NULL
        CHECK(length(trim(grant_namespace)) > 0 AND length(grant_namespace) <= 256
            AND trim(grant_namespace) = grant_namespace AND instr(grant_namespace, char(0)) = 0),
    grant_key_digest TEXT NOT NULL CHECK(
        length(grant_key_digest) = 64 AND grant_key_digest NOT GLOB '*[^0-9a-f]*'
    ),
    grant_request_digest TEXT NOT NULL CHECK(
        length(grant_request_digest) = 64 AND grant_request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    resolved_grant_id TEXT CHECK(
        resolved_grant_id IS NULL OR (length(trim(resolved_grant_id)) > 0
            AND length(resolved_grant_id) <= 128 AND trim(resolved_grant_id) = resolved_grant_id
            AND instr(resolved_grant_id, char(0)) = 0)
    ),
    grant_revision INTEGER NOT NULL CHECK(grant_revision >= 0),
    grant_status TEXT NOT NULL CHECK(grant_status IN (
        'absent', 'planned', 'active', 'revoked', 'expired', 'unknown'
    )),
    wait_namespace TEXT NOT NULL
        CHECK(length(trim(wait_namespace)) > 0 AND length(wait_namespace) <= 256
            AND trim(wait_namespace) = wait_namespace AND instr(wait_namespace, char(0)) = 0),
    wait_key_digest TEXT NOT NULL CHECK(
        length(wait_key_digest) = 64 AND wait_key_digest NOT GLOB '*[^0-9a-f]*'
    ),
    wait_request_digest TEXT NOT NULL CHECK(
        length(wait_request_digest) = 64 AND wait_request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    resolved_wait_handle_id TEXT CHECK(
        resolved_wait_handle_id IS NULL OR (length(trim(resolved_wait_handle_id)) > 0
            AND length(resolved_wait_handle_id) <= 128
            AND trim(resolved_wait_handle_id) = resolved_wait_handle_id
            AND instr(resolved_wait_handle_id, char(0)) = 0)
    ),
    wait_revision INTEGER NOT NULL CHECK(wait_revision >= 0),
    wait_status TEXT NOT NULL CHECK(wait_status IN (
        'absent', 'planned', 'pending', 'satisfied', 'timed_out',
        'cancelled', 'revoked', 'expired', 'unknown'
    )),
    grant_cleanup_state TEXT NOT NULL CHECK(grant_cleanup_state IN ('none', 'pending', 'cleared')),
    wait_cleanup_state TEXT NOT NULL CHECK(wait_cleanup_state IN ('none', 'pending', 'cleared')),
    phase TEXT NOT NULL CHECK(phase IN (
        'planned', 'grant_pending', 'wait_pending', 'waiting',
        'satisfied', 'timed_out', 'cancelled', 'failed',
        'outcome_unknown', 'cleanup_pending', 'cleanup_cleared'
    )),
    uncertain_phase TEXT NOT NULL DEFAULT '' CHECK(uncertain_phase IN (
        '', 'grant_create', 'wait_create', 'target_transition', 'cleanup'
    )),
    error_code TEXT NOT NULL DEFAULT '' CHECK(
        length(error_code) <= 256 AND trim(error_code) = error_code
        AND instr(error_code, char(0)) = 0
    ),
    error_ref TEXT NOT NULL DEFAULT '' CHECK(
        length(error_ref) <= 512 AND trim(error_ref) = error_ref
        AND instr(error_ref, char(0)) = 0
    ),
    resolved_at TEXT NOT NULL DEFAULT '' CHECK(
        length(resolved_at) <= 64 AND trim(resolved_at) = resolved_at
        AND instr(resolved_at, char(0)) = 0
        AND (resolved_at = '' OR julianday(resolved_at) IS NOT NULL)
    ),
    recorded_at TEXT NOT NULL CHECK(
        length(trim(recorded_at)) > 0 AND length(recorded_at) <= 64
        AND trim(recorded_at) = recorded_at AND instr(recorded_at, char(0)) = 0
        AND julianday(recorded_at) IS NOT NULL
    ),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),

    PRIMARY KEY(operation_key, target_key, version),
    UNIQUE(operation_key, target_index, version),
    UNIQUE(operation_key, target_key, record_digest),
    CHECK(
        (version = 1 AND previous_record_digest = '')
        OR (version > 1 AND previous_record_digest <> '')
    ),
    CHECK(
        (after_source_sequence = 0 AND after_cursor = '')
        OR (after_source_sequence > 0 AND after_cursor <> '')
    ),
    CHECK(
        (phase IN ('planned', 'grant_pending')
            AND grant_status = 'planned' AND wait_status = 'absent'
            AND grant_revision = 0 AND wait_revision = 0
            AND resolved_grant_id IS NULL AND resolved_wait_handle_id IS NULL
            AND grant_cleanup_state = 'none' AND wait_cleanup_state = 'none'
            AND uncertain_phase = '' AND error_code = '' AND error_ref = '' AND resolved_at = '')
        OR (phase = 'wait_pending'
            AND grant_status = 'active' AND wait_status = 'planned'
            AND grant_revision > 0 AND wait_revision = 0
            AND resolved_grant_id IS NOT NULL AND resolved_wait_handle_id IS NULL
            AND grant_cleanup_state = 'none' AND wait_cleanup_state = 'none'
            AND uncertain_phase = '' AND error_code = '' AND error_ref = '' AND resolved_at = '')
        OR (phase = 'waiting'
            AND grant_status = 'active' AND wait_status = 'pending'
            AND grant_revision > 0 AND wait_revision > 0
            AND resolved_grant_id IS NOT NULL AND resolved_wait_handle_id IS NOT NULL
            AND grant_cleanup_state = 'none' AND wait_cleanup_state = 'none'
            AND uncertain_phase = '' AND error_code = '' AND error_ref = '' AND resolved_at = '')
        OR (phase = 'satisfied'
            AND grant_status = 'active' AND wait_status = 'satisfied'
            AND grant_revision > 0 AND wait_revision > 0
            AND resolved_grant_id IS NOT NULL AND resolved_wait_handle_id IS NOT NULL
            AND grant_cleanup_state = 'none' AND wait_cleanup_state = 'none'
            AND uncertain_phase = '' AND error_code = '' AND error_ref = '' AND resolved_at <> '')
        OR (phase = 'timed_out'
            AND grant_status = 'active' AND wait_status = 'timed_out'
            AND grant_revision > 0 AND wait_revision > 0
            AND resolved_grant_id IS NOT NULL AND resolved_wait_handle_id IS NOT NULL
            AND grant_cleanup_state = 'none' AND wait_cleanup_state = 'none'
            AND uncertain_phase = '' AND error_code = '' AND error_ref = '' AND resolved_at <> '')
        OR (phase = 'cancelled'
            AND grant_status IN ('active', 'revoked', 'expired')
            AND wait_status IN ('cancelled', 'revoked', 'expired')
            AND grant_revision > 0 AND wait_revision > 0
            AND resolved_grant_id IS NOT NULL AND resolved_wait_handle_id IS NOT NULL
            AND grant_cleanup_state = 'none' AND wait_cleanup_state = 'none'
            AND uncertain_phase = '' AND error_code = '' AND error_ref = '' AND resolved_at <> '')
        OR (phase = 'failed'
            AND grant_status = 'planned' AND wait_status = 'absent'
            AND grant_revision = 0 AND wait_revision = 0
            AND resolved_grant_id IS NULL AND resolved_wait_handle_id IS NULL
            AND grant_cleanup_state = 'none' AND wait_cleanup_state = 'none'
            AND error_code <> '' AND resolved_at <> '' AND uncertain_phase = '')
        OR (phase = 'failed'
            AND grant_status = 'active' AND wait_status = 'absent'
            AND grant_revision > 0 AND wait_revision = 0
            AND resolved_grant_id IS NOT NULL AND resolved_wait_handle_id IS NULL
            AND grant_cleanup_state = 'none' AND wait_cleanup_state = 'none'
            AND error_code <> '' AND resolved_at <> '' AND uncertain_phase = '')
        OR (phase = 'outcome_unknown'
            AND uncertain_phase = 'grant_create'
            AND grant_status = 'unknown' AND wait_status = 'absent'
            AND grant_revision = 0 AND wait_revision = 0
            AND resolved_grant_id IS NULL AND resolved_wait_handle_id IS NULL
            AND grant_cleanup_state = 'none' AND wait_cleanup_state = 'none'
            AND error_code = '' AND error_ref = '' AND resolved_at = '')
        OR (phase = 'outcome_unknown'
            AND uncertain_phase = 'wait_create'
            AND grant_status = 'active' AND wait_status = 'unknown'
            AND grant_revision > 0 AND wait_revision = 0
            AND resolved_grant_id IS NOT NULL AND resolved_wait_handle_id IS NULL
            AND grant_cleanup_state = 'none' AND wait_cleanup_state = 'none'
            AND error_code = '' AND error_ref = '' AND resolved_at = '')
        OR (phase = 'outcome_unknown'
            AND uncertain_phase = 'target_transition'
            AND grant_status = 'planned' AND wait_status = 'absent'
            AND grant_revision = 0 AND wait_revision = 0
            AND resolved_grant_id IS NULL AND resolved_wait_handle_id IS NULL
            AND grant_cleanup_state = 'none' AND wait_cleanup_state = 'none'
            AND error_code = '' AND error_ref = '' AND resolved_at = '')
        OR (phase = 'outcome_unknown'
            AND uncertain_phase = 'target_transition'
            AND grant_status = 'active' AND wait_status = 'planned'
            AND grant_revision > 0 AND wait_revision = 0
            AND resolved_grant_id IS NOT NULL AND resolved_wait_handle_id IS NULL
            AND grant_cleanup_state = 'none' AND wait_cleanup_state = 'none'
            AND error_code = '' AND error_ref = '' AND resolved_at = '')
        OR (phase = 'outcome_unknown'
            AND uncertain_phase = 'target_transition'
            AND grant_status = 'active' AND wait_status = 'pending'
            AND grant_revision > 0 AND wait_revision > 0
            AND resolved_grant_id IS NOT NULL AND resolved_wait_handle_id IS NOT NULL
            AND grant_cleanup_state = 'none' AND wait_cleanup_state = 'none'
            AND error_code = '' AND error_ref = '' AND resolved_at = '')
        OR (phase = 'outcome_unknown'
            AND uncertain_phase = 'cleanup'
            AND grant_status <> 'unknown' AND wait_status <> 'unknown'
            AND grant_cleanup_state = 'pending' AND wait_cleanup_state = 'pending'
            AND error_code = '' AND error_ref = '' AND resolved_at = '')
        OR (phase = 'cleanup_pending'
            AND grant_cleanup_state = 'pending' AND wait_cleanup_state = 'pending'
            AND uncertain_phase = 'cleanup' AND error_code = '' AND error_ref = ''
            AND resolved_at = '')
        OR (phase = 'cleanup_cleared'
            AND grant_cleanup_state = 'cleared' AND wait_cleanup_state = 'cleared'
            AND uncertain_phase = 'cleanup' AND error_code = '' AND error_ref = ''
            AND resolved_at <> ''
            AND ((resolved_grant_id IS NULL AND grant_status IN ('absent', 'planned') AND grant_revision = 0)
                 OR (resolved_grant_id IS NOT NULL AND grant_status IN ('revoked', 'expired') AND grant_revision > 0))
            AND ((resolved_wait_handle_id IS NULL AND wait_status = 'absent' AND wait_revision = 0)
                 OR (resolved_wait_handle_id IS NOT NULL AND wait_status IN ('satisfied', 'timed_out', 'cancelled', 'revoked', 'expired') AND wait_revision > 0)))
    ),
    FOREIGN KEY(operation_key, operation_digest)
        REFERENCES orchestration_wait_operations(operation_key, operation_digest)
        ON DELETE RESTRICT,
    FOREIGN KEY(target_reservation_id, target_agent_id)
        REFERENCES orchestration_identity_reservations(reservation_id, agent_id)
        ON DELETE RESTRICT,
    FOREIGN KEY(target_incarnation_id)
        REFERENCES orchestration_execution_incarnations(incarnation_id)
        ON DELETE RESTRICT,
    FOREIGN KEY(resolved_grant_id)
        REFERENCES orchestration_access_grants(grant_id)
        ON DELETE RESTRICT,
    FOREIGN KEY(resolved_wait_handle_id)
        REFERENCES orchestration_event_wait_handles(wait_handle_id)
        ON DELETE RESTRICT
) STRICT;

CREATE INDEX idx_orchestration_wait_operation_target_head
    ON orchestration_wait_operation_target_journal(operation_key, target_key, version DESC);
CREATE INDEX idx_orchestration_wait_operation_target_index
    ON orchestration_wait_operation_target_journal(operation_key, target_index, version DESC);

-- Every operation source fence must point at one exact reservation and its
-- matching execution incarnation. The scalar foreign keys above prove row
-- existence only; this trigger also binds both generation values and agent.
CREATE TRIGGER trg_orchestration_wait_operations_source_identity_relation
BEFORE INSERT ON orchestration_wait_operations
WHEN NOT EXISTS (
    SELECT 1
    FROM orchestration_identity_reservations AS reservation
    JOIN orchestration_execution_incarnations AS incarnation
      ON incarnation.incarnation_id = NEW.source_incarnation_id
     AND incarnation.reservation_id = reservation.reservation_id
     AND incarnation.agent_id = reservation.agent_id
     AND incarnation.incarnation_generation = NEW.source_incarnation_generation
    JOIN orchestration_current_bindings AS binding
      ON binding.agent_id = reservation.agent_id
     AND binding.binding_version = NEW.source_binding_version
     AND binding.reservation_id = reservation.reservation_id
     AND binding.reservation_generation = reservation.reservation_generation
     AND binding.incarnation_id = incarnation.incarnation_id
     AND binding.incarnation_generation = incarnation.incarnation_generation
     AND binding.binding_digest = NEW.source_binding_digest
     AND binding.source_fence_ref = NEW.source_fence_ref
     AND binding.state = 'active'
    WHERE reservation.reservation_id = NEW.source_reservation_id
      AND reservation.agent_id = NEW.source_agent_id
      AND reservation.reservation_generation = NEW.source_reservation_generation
      AND reservation.state = 'activated'
      AND NOT EXISTS (
          SELECT 1 FROM orchestration_current_bindings AS newer
          WHERE newer.agent_id = binding.agent_id
            AND newer.binding_version > binding.binding_version
      )
)
BEGIN
    SELECT RAISE(ABORT, 'wait operation source incarnation relation mismatch');
END;

CREATE TRIGGER trg_orchestration_wait_operations_complete_on_insert
BEFORE INSERT ON orchestration_wait_operations
WHEN NEW.status IN ('ready', 'cleanup_cleared')
BEGIN
    SELECT RAISE(ABORT, 'wait operation aggregate head cannot be complete at insert');
END;

-- The key/digest relation and the target index map are part of the SQL
-- contract, not merely Go validation.
CREATE TRIGGER trg_orchestration_wait_operation_target_relation
BEFORE INSERT ON orchestration_wait_operation_target_journal
WHEN NOT EXISTS (
    SELECT 1 FROM orchestration_wait_operations AS operation
    WHERE operation.operation_key = NEW.operation_key
      AND operation.operation_digest = NEW.operation_digest
      AND NEW.target_index <= operation.target_count
      AND NEW.deadline_at = operation.deadline_at
)
BEGIN
    SELECT RAISE(ABORT, 'wait operation target does not match operation head');
END;

CREATE TRIGGER trg_orchestration_wait_operation_target_identity_relation
BEFORE INSERT ON orchestration_wait_operation_target_journal
WHEN NOT EXISTS (
    SELECT 1
    FROM orchestration_identity_reservations AS reservation
    JOIN orchestration_wait_operations AS operation
      ON operation.operation_key = NEW.operation_key
     AND operation.operation_digest = NEW.operation_digest
    JOIN orchestration_execution_incarnations AS incarnation
      ON incarnation.incarnation_id = NEW.target_incarnation_id
     AND incarnation.reservation_id = reservation.reservation_id
     AND incarnation.agent_id = reservation.agent_id
     AND incarnation.incarnation_generation = NEW.target_incarnation_generation
    JOIN orchestration_current_bindings AS binding
      ON binding.agent_id = reservation.agent_id
     AND binding.binding_version = NEW.target_binding_version
     AND binding.reservation_id = reservation.reservation_id
     AND binding.reservation_generation = reservation.reservation_generation
     AND binding.incarnation_id = incarnation.incarnation_id
     AND binding.incarnation_generation = incarnation.incarnation_generation
     AND binding.binding_digest = NEW.target_binding_digest
     AND binding.source_fence_ref = NEW.target_fence_ref
    JOIN orchestration_current_bindings AS source_binding
      ON source_binding.agent_id = operation.source_agent_id
     AND source_binding.binding_version = operation.source_binding_version
     AND source_binding.reservation_id = operation.source_reservation_id
     AND source_binding.reservation_generation = operation.source_reservation_generation
     AND source_binding.incarnation_id = operation.source_incarnation_id
     AND source_binding.incarnation_generation = operation.source_incarnation_generation
     AND source_binding.binding_digest = operation.source_binding_digest
     AND source_binding.source_fence_ref = operation.source_fence_ref
    WHERE reservation.reservation_id = NEW.target_reservation_id
      AND reservation.agent_id = NEW.target_agent_id
      AND reservation.reservation_generation = NEW.target_reservation_generation
      AND reservation.state = 'activated'
      AND (
          NEW.phase IN ('cleanup_pending', 'cleanup_cleared')
          OR (NEW.phase = 'outcome_unknown' AND NEW.uncertain_phase = 'cleanup')
          OR (NOT (NEW.phase IN ('cleanup_pending', 'cleanup_cleared')
                   OR (NEW.phase = 'outcome_unknown' AND NEW.uncertain_phase = 'cleanup'))
              AND binding.state = 'active'
              AND NOT EXISTS (
                  SELECT 1 FROM orchestration_current_bindings AS newer
                  WHERE newer.agent_id = binding.agent_id
                    AND newer.binding_version > binding.binding_version
              ))
      )
      AND (
          NEW.phase IN ('cleanup_pending', 'cleanup_cleared')
          OR (NEW.phase = 'outcome_unknown' AND NEW.uncertain_phase = 'cleanup')
          OR (NOT (NEW.phase IN ('cleanup_pending', 'cleanup_cleared')
                   OR (NEW.phase = 'outcome_unknown' AND NEW.uncertain_phase = 'cleanup'))
              AND source_binding.state = 'active'
              AND NOT EXISTS (
                  SELECT 1 FROM orchestration_current_bindings AS newer_source
                  WHERE newer_source.agent_id = source_binding.agent_id
                    AND newer_source.binding_version > source_binding.binding_version
              ))
      )
)
BEGIN
    SELECT RAISE(ABORT, 'wait operation target incarnation relation mismatch');
END;

CREATE TRIGGER trg_orchestration_wait_operation_target_selector_relation
BEFORE INSERT ON orchestration_wait_operation_target_journal
WHEN NOT EXISTS (
    SELECT 1
    FROM orchestration_wait_operations AS operation
    WHERE operation.operation_key = NEW.operation_key
      AND operation.operation_digest = NEW.operation_digest
      AND (
          (NEW.source_id = 'task/' || operation.source_agent_id
           AND NEW.source_epoch = operation.source_incarnation_id)
          OR
          (NEW.source_id = 'task/' || NEW.target_agent_id
           AND NEW.source_epoch = NEW.target_incarnation_id)
      )
      AND CAST(json_extract(NEW.selector_json, '$.version') AS INTEGER) = NEW.selector_version
      AND json_extract(NEW.selector_json, '$.source_id') = NEW.source_id
      AND json_extract(NEW.selector_json, '$.source_epoch') = NEW.source_epoch
      AND json_extract(NEW.selector_json, '$.lane') = NEW.lane
      AND json_extract(NEW.selector_json, '$.target_agent_id') = NEW.target_agent_id
      AND (json_extract(NEW.selector_json, '$.event_types') IS NULL
           OR json(NEW.event_types_json) = json(json_extract(NEW.selector_json, '$.event_types')))
)
BEGIN
    SELECT RAISE(ABORT, 'wait operation target selector relation mismatch');
END;

CREATE TRIGGER trg_orchestration_wait_operation_target_contiguous
BEFORE INSERT ON orchestration_wait_operation_target_journal
WHEN NEW.version <> (
    SELECT COALESCE(MAX(version), 0) + 1
    FROM orchestration_wait_operation_target_journal
    WHERE operation_key = NEW.operation_key AND target_key = NEW.target_key
)
BEGIN
    SELECT RAISE(ABORT, 'wait operation target journal version must be contiguous');
END;

CREATE TRIGGER trg_orchestration_wait_operation_target_index_map
BEFORE INSERT ON orchestration_wait_operation_target_journal
WHEN NEW.version = 1 AND EXISTS (
    SELECT 1 FROM orchestration_wait_operation_target_journal AS prior
    WHERE prior.operation_key = NEW.operation_key
      AND prior.target_index = NEW.target_index
      AND prior.version = 1
      AND prior.target_key <> NEW.target_key
)
BEGIN
    SELECT RAISE(ABORT, 'wait operation target index is already bound to another key');
END;

CREATE TRIGGER trg_orchestration_wait_operation_target_identity
BEFORE INSERT ON orchestration_wait_operation_target_journal
WHEN NEW.version > 1 AND EXISTS (
    SELECT 1
    FROM orchestration_wait_operation_target_journal AS prior
    WHERE prior.operation_key = NEW.operation_key
      AND prior.target_key = NEW.target_key
      AND prior.version = NEW.version - 1
      AND (
          prior.operation_digest <> NEW.operation_digest
          OR prior.target_index <> NEW.target_index
          OR prior.target_digest <> NEW.target_digest
          OR prior.target_agent_id <> NEW.target_agent_id
          OR prior.target_reservation_id <> NEW.target_reservation_id
          OR prior.target_reservation_generation <> NEW.target_reservation_generation
          OR prior.target_incarnation_id <> NEW.target_incarnation_id
          OR prior.target_incarnation_generation <> NEW.target_incarnation_generation
          OR prior.target_binding_version <> NEW.target_binding_version
          OR prior.target_binding_digest <> NEW.target_binding_digest
          OR prior.target_fence_ref <> NEW.target_fence_ref
          OR prior.source_id <> NEW.source_id
          OR prior.source_epoch <> NEW.source_epoch
          OR prior.lane <> NEW.lane
          OR prior.selector_version <> NEW.selector_version
          OR prior.selector_json <> NEW.selector_json
          OR prior.selector_digest <> NEW.selector_digest
          OR prior.event_types_json <> NEW.event_types_json
          OR prior.after_source_sequence <> NEW.after_source_sequence
          OR prior.after_cursor <> NEW.after_cursor
          OR prior.deadline_at <> NEW.deadline_at
          OR prior.original_grant_id <> NEW.original_grant_id
          OR prior.grant_namespace <> NEW.grant_namespace
          OR prior.grant_key_digest <> NEW.grant_key_digest
          OR prior.grant_request_digest <> NEW.grant_request_digest
          OR prior.wait_namespace <> NEW.wait_namespace
          OR prior.wait_key_digest <> NEW.wait_key_digest
          OR prior.wait_request_digest <> NEW.wait_request_digest
      )
)
BEGIN
    SELECT RAISE(ABORT, 'wait operation target identity is immutable');
END;

CREATE TRIGGER trg_orchestration_wait_operation_target_previous_digest
BEFORE INSERT ON orchestration_wait_operation_target_journal
WHEN NEW.version > 1 AND NEW.previous_record_digest <> COALESCE((
    SELECT record_digest
    FROM orchestration_wait_operation_target_journal AS prior
    WHERE prior.operation_key = NEW.operation_key
      AND prior.target_key = NEW.target_key
      AND prior.version = NEW.version - 1
), '')
BEGIN
    SELECT RAISE(ABORT, 'wait operation target predecessor digest mismatch');
END;

-- A resolved grant is not merely an opaque id: it must be the exact grant
-- requested by this operation and target.  Every caller/scope, identity
-- generation, binding fence, idempotency/request digest and lifecycle revision
-- participates in the relation so a direct SQL writer cannot splice rows.
CREATE TRIGGER trg_orchestration_wait_operation_resolved_grant_relation
BEFORE INSERT ON orchestration_wait_operation_target_journal
WHEN NEW.resolved_grant_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1
    FROM orchestration_wait_operations AS operation
    JOIN orchestration_access_grants AS grant_record
      ON grant_record.grant_id = NEW.resolved_grant_id
    JOIN orchestration_current_bindings AS source_binding
      ON source_binding.agent_id = operation.source_agent_id
     AND source_binding.binding_version = operation.source_binding_version
     AND source_binding.reservation_id = operation.source_reservation_id
     AND source_binding.reservation_generation = operation.source_reservation_generation
     AND source_binding.incarnation_id = operation.source_incarnation_id
     AND source_binding.incarnation_generation = operation.source_incarnation_generation
     AND source_binding.binding_digest = operation.source_binding_digest
     AND source_binding.source_fence_ref = operation.source_fence_ref
    JOIN orchestration_current_bindings AS target_binding
      ON target_binding.agent_id = NEW.target_agent_id
     AND target_binding.binding_version = NEW.target_binding_version
     AND target_binding.reservation_id = NEW.target_reservation_id
     AND target_binding.reservation_generation = NEW.target_reservation_generation
     AND target_binding.incarnation_id = NEW.target_incarnation_id
     AND target_binding.incarnation_generation = NEW.target_incarnation_generation
     AND target_binding.binding_digest = NEW.target_binding_digest
     AND target_binding.source_fence_ref = NEW.target_fence_ref
    WHERE operation.operation_key = NEW.operation_key
      AND operation.operation_digest = NEW.operation_digest
      AND NEW.resolved_grant_id = NEW.original_grant_id
      AND grant_record.caller_principal_ref = operation.caller_principal_ref
      AND grant_record.scope_ref = operation.scope_ref
      AND grant_record.source_agent_id = operation.source_agent_id
      AND grant_record.source_reservation_id = operation.source_reservation_id
      AND grant_record.source_reservation_generation = operation.source_reservation_generation
      AND grant_record.source_incarnation_id = operation.source_incarnation_id
      AND grant_record.source_incarnation_generation = operation.source_incarnation_generation
      AND grant_record.source_binding_version = operation.source_binding_version
      AND grant_record.source_binding_digest = operation.source_binding_digest
      AND grant_record.source_fence_ref = operation.source_fence_ref
      AND grant_record.target_agent_id = NEW.target_agent_id
      AND grant_record.target_reservation_id = NEW.target_reservation_id
      AND grant_record.target_reservation_generation = NEW.target_reservation_generation
      AND grant_record.target_incarnation_id = NEW.target_incarnation_id
      AND grant_record.target_incarnation_generation = NEW.target_incarnation_generation
      AND grant_record.target_binding_version = NEW.target_binding_version
      AND grant_record.target_binding_digest = NEW.target_binding_digest
      AND grant_record.target_fence_ref = NEW.target_fence_ref
      AND grant_record.idempotency_namespace = NEW.grant_namespace
      AND grant_record.idempotency_key_digest = NEW.grant_key_digest
      AND grant_record.request_digest = NEW.grant_request_digest
      AND grant_record.can_read = 1
      AND grant_record.can_send = 0
      AND grant_record.issued_at = operation.issued_at
      AND grant_record.expires_at = operation.deadline_at
      AND grant_record.revision = NEW.grant_revision
      AND grant_record.status = NEW.grant_status
      AND (
          (NEW.phase IN ('cleanup_pending', 'cleanup_cleared')
           OR (NEW.phase = 'outcome_unknown' AND NEW.uncertain_phase = 'cleanup'))
          OR (NOT (NEW.phase IN ('cleanup_pending', 'cleanup_cleared')
                   OR (NEW.phase = 'outcome_unknown' AND NEW.uncertain_phase = 'cleanup'))
              AND source_binding.state = 'active' AND target_binding.state = 'active'
              AND NOT EXISTS (
          SELECT 1 FROM orchestration_current_bindings AS newer_source
          WHERE newer_source.agent_id = source_binding.agent_id
            AND newer_source.binding_version > source_binding.binding_version
              )
              AND NOT EXISTS (
          SELECT 1 FROM orchestration_current_bindings AS newer_target
          WHERE newer_target.agent_id = target_binding.agent_id
            AND newer_target.binding_version > target_binding.binding_version
              ))
      )
 )
BEGIN
    SELECT RAISE(ABORT, 'wait operation resolved grant relation mismatch');
END;

-- A resolved wait handle must remain bound to that exact grant and selector
-- tuple.  This includes caller/scope, target fence, selector/cursor/deadline,
-- wait idempotency/request digest and the current wait revision/status.
CREATE TRIGGER trg_orchestration_wait_operation_resolved_wait_relation
BEFORE INSERT ON orchestration_wait_operation_target_journal
WHEN NEW.resolved_wait_handle_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1
    FROM orchestration_wait_operations AS operation
    JOIN orchestration_event_wait_handles AS wait_handle
      ON wait_handle.wait_handle_id = NEW.resolved_wait_handle_id
    JOIN orchestration_access_grants AS grant_record
      ON grant_record.grant_id = wait_handle.grant_id
    JOIN orchestration_current_bindings AS source_binding
      ON source_binding.agent_id = operation.source_agent_id
     AND source_binding.binding_version = operation.source_binding_version
     AND source_binding.reservation_id = operation.source_reservation_id
     AND source_binding.reservation_generation = operation.source_reservation_generation
     AND source_binding.incarnation_id = operation.source_incarnation_id
     AND source_binding.incarnation_generation = operation.source_incarnation_generation
     AND source_binding.binding_digest = operation.source_binding_digest
     AND source_binding.source_fence_ref = operation.source_fence_ref
    JOIN orchestration_current_bindings AS target_binding
      ON target_binding.agent_id = NEW.target_agent_id
     AND target_binding.binding_version = NEW.target_binding_version
     AND target_binding.reservation_id = NEW.target_reservation_id
     AND target_binding.reservation_generation = NEW.target_reservation_generation
     AND target_binding.incarnation_id = NEW.target_incarnation_id
     AND target_binding.incarnation_generation = NEW.target_incarnation_generation
     AND target_binding.binding_digest = NEW.target_binding_digest
     AND target_binding.source_fence_ref = NEW.target_fence_ref
    WHERE operation.operation_key = NEW.operation_key
      AND operation.operation_digest = NEW.operation_digest
      AND NEW.resolved_grant_id IS NOT NULL
      AND wait_handle.grant_id = NEW.resolved_grant_id
      AND wait_handle.grant_revision <= grant_record.revision
      -- A cleanup row records the current grant revision after revoke/expire;
      -- the immutable wait handle retains the revision it authorized against.
      AND (wait_handle.grant_revision = NEW.grant_revision
           OR NEW.phase IN ('cleanup_pending', 'cleanup_cleared')
           OR (NEW.phase = 'outcome_unknown' AND NEW.uncertain_phase = 'cleanup'))
      AND wait_handle.caller_principal_ref = operation.caller_principal_ref
      AND wait_handle.scope_ref = operation.scope_ref
      AND wait_handle.source_agent_id = operation.source_agent_id
      AND wait_handle.target_agent_id = NEW.target_agent_id
      AND wait_handle.target_reservation_id = NEW.target_reservation_id
      AND wait_handle.target_reservation_generation = NEW.target_reservation_generation
      AND wait_handle.target_incarnation_id = NEW.target_incarnation_id
      AND wait_handle.target_incarnation_generation = NEW.target_incarnation_generation
      AND wait_handle.target_binding_version = NEW.target_binding_version
      AND wait_handle.target_binding_digest = NEW.target_binding_digest
      AND wait_handle.target_fence_ref = NEW.target_fence_ref
      AND wait_handle.source_id = NEW.source_id
      AND wait_handle.source_epoch = NEW.source_epoch
      AND wait_handle.lane = NEW.lane
      AND wait_handle.selector_version = NEW.selector_version
      AND wait_handle.selector_json = NEW.selector_json
      AND wait_handle.selector_digest = NEW.selector_digest
      AND wait_handle.event_types_json = NEW.event_types_json
      AND wait_handle.after_source_sequence = NEW.after_source_sequence
      AND wait_handle.after_cursor = NEW.after_cursor
      AND wait_handle.deadline_at = NEW.deadline_at
      AND wait_handle.idempotency_namespace = NEW.wait_namespace
      AND wait_handle.idempotency_key_digest = NEW.wait_key_digest
      AND wait_handle.request_digest = NEW.wait_request_digest
      AND wait_handle.deadline_at = grant_record.expires_at
      AND wait_handle.revision = NEW.wait_revision
      AND wait_handle.status = NEW.wait_status
      AND (
          (NEW.phase IN ('cleanup_pending', 'cleanup_cleared')
           OR (NEW.phase = 'outcome_unknown' AND NEW.uncertain_phase = 'cleanup'))
          OR (NOT (NEW.phase IN ('cleanup_pending', 'cleanup_cleared')
                   OR (NEW.phase = 'outcome_unknown' AND NEW.uncertain_phase = 'cleanup'))
              AND source_binding.state = 'active' AND target_binding.state = 'active'
              AND NOT EXISTS (
          SELECT 1 FROM orchestration_current_bindings AS newer_source
          WHERE newer_source.agent_id = source_binding.agent_id
            AND newer_source.binding_version > source_binding.binding_version
              )
              AND NOT EXISTS (
          SELECT 1 FROM orchestration_current_bindings AS newer_target
          WHERE newer_target.agent_id = target_binding.agent_id
            AND newer_target.binding_version > target_binding.binding_version
              ))
      )
 )
BEGIN
    SELECT RAISE(ABORT, 'wait operation resolved wait relation mismatch');
END;

-- Cleanup is a release-only phase: it may revoke/expire existing resources,
-- but it cannot erase or invent their resolved identities. This continuity
-- check also prevents a forged cleanup-cleared row from dropping a real grant
-- or wait handle before its owner has produced a receipt.
CREATE TRIGGER trg_orchestration_wait_operation_cleanup_continuity
BEFORE INSERT ON orchestration_wait_operation_target_journal
WHEN (NEW.phase IN ('cleanup_pending', 'cleanup_cleared')
      OR (NEW.phase = 'outcome_unknown' AND NEW.uncertain_phase = 'cleanup'))
 AND NEW.version > 1
 AND EXISTS (
    SELECT 1
    FROM orchestration_wait_operation_target_journal AS prior
    WHERE prior.operation_key = NEW.operation_key
      AND prior.target_key = NEW.target_key
      AND prior.version = NEW.version - 1
      AND (
          prior.resolved_grant_id IS NOT NEW.resolved_grant_id
          OR prior.resolved_wait_handle_id IS NOT NEW.resolved_wait_handle_id
          OR (prior.resolved_grant_id IS NOT NULL AND NEW.grant_revision < prior.grant_revision)
          OR (prior.resolved_wait_handle_id IS NOT NULL AND NEW.wait_revision < prior.wait_revision)
          OR (prior.resolved_grant_id IS NOT NULL AND NEW.grant_status IN ('absent', 'planned', 'unknown'))
          OR (prior.resolved_wait_handle_id IS NOT NULL AND NEW.wait_status IN ('absent', 'planned', 'unknown'))
      )
 )
BEGIN
    SELECT RAISE(ABORT, 'wait operation cleanup continuity is invalid');
END;

CREATE TRIGGER trg_orchestration_wait_operation_unknown_resource_continuity
BEFORE INSERT ON orchestration_wait_operation_target_journal
WHEN NEW.phase = 'outcome_unknown'
 AND NEW.uncertain_phase <> 'cleanup'
 AND NEW.version > 1
 AND EXISTS (
    SELECT 1
    FROM orchestration_wait_operation_target_journal AS prior
    WHERE prior.operation_key = NEW.operation_key
      AND prior.target_key = NEW.target_key
      AND prior.version = NEW.version - 1
      AND (
          prior.resolved_grant_id IS NOT NEW.resolved_grant_id
          OR prior.resolved_wait_handle_id IS NOT NEW.resolved_wait_handle_id
          OR (prior.resolved_grant_id IS NOT NULL AND NEW.grant_status IN ('absent', 'planned', 'unknown'))
          OR (prior.resolved_wait_handle_id IS NOT NULL AND NEW.wait_status IN ('absent', 'planned', 'unknown'))
          OR (prior.grant_status IN ('revoked', 'expired') AND NEW.grant_status NOT IN ('revoked', 'expired'))
          OR (prior.wait_status IN ('satisfied', 'timed_out', 'cancelled', 'revoked', 'expired')
              AND NEW.wait_status NOT IN ('satisfied', 'timed_out', 'cancelled', 'revoked', 'expired'))
      )
 )
BEGIN
    SELECT RAISE(ABORT, 'wait operation unknown state cannot drop resolved resources');
END;

-- Once an aggregate advertises READY or CLEANUP_CLEARED, its target set is
-- frozen. A writer must first perform the operation-level fenced transition
-- (and only then append a successor), never mutate a completed head directly.
CREATE TRIGGER trg_orchestration_wait_operation_target_head_stability
BEFORE INSERT ON orchestration_wait_operation_target_journal
WHEN EXISTS (
    SELECT 1
    FROM orchestration_wait_operations AS operation
    WHERE operation.operation_key = NEW.operation_key
      AND operation.operation_digest = NEW.operation_digest
      AND operation.status IN ('ready', 'cleanup_cleared')
      AND NOT (
          operation.status = 'ready'
          AND NEW.phase IN ('satisfied', 'timed_out', 'cancelled')
          AND NEW.uncertain_phase = ''
          AND EXISTS (
              SELECT 1
              FROM orchestration_wait_operation_target_journal AS current
              WHERE current.operation_key = NEW.operation_key
                AND current.target_key = NEW.target_key
                AND current.version = (
                    SELECT MAX(prior.version)
                    FROM orchestration_wait_operation_target_journal AS prior
                    WHERE prior.operation_key = current.operation_key
                      AND prior.target_key = current.target_key
                )
                AND current.phase = 'waiting'
          )
      )
)
BEGIN
    SELECT RAISE(ABORT, 'wait operation target head is stable');
END;

CREATE TRIGGER trg_orchestration_wait_operations_immutable
BEFORE UPDATE ON orchestration_wait_operations
WHEN NEW.operation_key <> OLD.operation_key
  OR NEW.operation_digest <> OLD.operation_digest
  OR NEW.target_set_digest <> OLD.target_set_digest
  OR NEW.caller_principal_ref <> OLD.caller_principal_ref
  OR NEW.scope_ref <> OLD.scope_ref
  OR NEW.source_agent_id <> OLD.source_agent_id
  OR NEW.source_reservation_id <> OLD.source_reservation_id
  OR NEW.source_reservation_generation <> OLD.source_reservation_generation
  OR NEW.source_incarnation_id <> OLD.source_incarnation_id
  OR NEW.source_incarnation_generation <> OLD.source_incarnation_generation
  OR NEW.source_binding_version <> OLD.source_binding_version
  OR NEW.source_binding_digest <> OLD.source_binding_digest
  OR NEW.source_fence_ref <> OLD.source_fence_ref
  OR NEW.issued_at <> OLD.issued_at
  OR NEW.deadline_at <> OLD.deadline_at
  OR NEW.target_count <> OLD.target_count
  OR NEW.created_at <> OLD.created_at
  OR NEW.schema_version <> OLD.schema_version
  OR NEW.revision <> OLD.revision + 1
BEGIN
    SELECT RAISE(ABORT, 'wait operation immutable fields or revision are invalid');
END;

CREATE TRIGGER trg_orchestration_wait_operations_cas
BEFORE UPDATE ON orchestration_wait_operations
WHEN NOT (
    (OLD.status = 'provisioning' AND NEW.status IN ('provisioning', 'ready', 'partial', 'outcome_unknown', 'cleanup_pending'))
    OR (OLD.status = 'ready' AND NEW.status IN ('ready', 'outcome_unknown', 'cleanup_pending'))
    OR (OLD.status = 'partial' AND NEW.status IN ('partial', 'ready', 'outcome_unknown', 'cleanup_pending'))
    OR (OLD.status = 'outcome_unknown' AND NEW.status IN ('outcome_unknown', 'provisioning', 'partial', 'ready', 'cleanup_pending'))
    OR (OLD.status = 'cleanup_pending' AND NEW.status IN ('cleanup_pending', 'cleanup_cleared'))
    OR (OLD.status = 'cleanup_cleared' AND NEW.status = 'cleanup_cleared')
)
BEGIN
    SELECT RAISE(ABORT, 'wait operation status transition is invalid');
END;

CREATE TRIGGER trg_orchestration_wait_operations_unknown_continuity
BEFORE UPDATE ON orchestration_wait_operations
WHEN OLD.status = 'outcome_unknown'
 AND NEW.status = 'outcome_unknown'
 AND (
     NEW.uncertain_phase <> OLD.uncertain_phase
     OR NEW.cleanup_request_digest <> OLD.cleanup_request_digest
     OR NEW.cleanup_reason_ref <> OLD.cleanup_reason_ref
     OR NEW.resolved_at <> OLD.resolved_at
 )
BEGIN
    SELECT RAISE(ABORT, 'wait operation outcome_unknown subtype cannot change');
END;

CREATE TRIGGER trg_orchestration_wait_operations_cleanup_metadata_continuity
BEFORE UPDATE ON orchestration_wait_operations
WHEN ((OLD.status = 'outcome_unknown' AND OLD.uncertain_phase = 'cleanup' AND NEW.status = 'cleanup_pending')
      OR (OLD.status = 'cleanup_pending' AND NEW.status IN ('cleanup_pending', 'cleanup_cleared'))
      OR (OLD.status = 'cleanup_cleared' AND NEW.status = 'cleanup_cleared'))
 AND (NEW.cleanup_request_digest <> OLD.cleanup_request_digest
      OR NEW.cleanup_reason_ref <> OLD.cleanup_reason_ref
      OR (OLD.status = 'cleanup_cleared' AND NEW.resolved_at <> OLD.resolved_at))
BEGIN
    SELECT RAISE(ABORT, 'wait operation cleanup metadata cannot change');
END;

CREATE TRIGGER trg_orchestration_wait_operations_cleanup_unknown_no_reopen
BEFORE UPDATE ON orchestration_wait_operations
WHEN OLD.status = 'outcome_unknown'
 AND OLD.uncertain_phase = 'cleanup'
 AND NEW.status IN ('provisioning', 'partial', 'ready')
BEGIN
    SELECT RAISE(ABORT, 'wait operation cleanup outcome_unknown cannot reopen');
END;

-- READY and CLEANUP_CLEARED are aggregate heads.  They may only be written
-- after every planned target has a latest journal head; a partial target set
-- must remain provisioning/partial instead of being advertised as complete.
CREATE TRIGGER trg_orchestration_wait_operations_complete_head_count
BEFORE UPDATE OF status ON orchestration_wait_operations
WHEN NEW.status IN ('ready', 'cleanup_pending', 'cleanup_cleared')
 AND (
    (SELECT COUNT(*)
       FROM orchestration_wait_operation_target_journal AS head
      WHERE head.operation_key = NEW.operation_key
        AND head.operation_digest = NEW.operation_digest
        AND head.version = (
            SELECT MAX(newer.version)
            FROM orchestration_wait_operation_target_journal AS newer
            WHERE newer.operation_key = head.operation_key
              AND newer.target_key = head.target_key
        )) <> NEW.target_count
 OR EXISTS (
    SELECT 1
    FROM orchestration_wait_operation_target_journal AS incomplete
    WHERE incomplete.operation_key = NEW.operation_key
      AND incomplete.operation_digest = NEW.operation_digest
      AND incomplete.version = (
          SELECT MAX(newer.version)
          FROM orchestration_wait_operation_target_journal AS newer
          WHERE newer.operation_key = incomplete.operation_key
            AND newer.target_key = incomplete.target_key
      )
      AND ((NEW.status = 'ready' AND incomplete.phase NOT IN ('waiting', 'satisfied', 'timed_out', 'cancelled'))
       OR (NEW.status = 'cleanup_pending' AND incomplete.phase NOT IN ('satisfied', 'timed_out', 'cancelled', 'failed'))
       OR (NEW.status = 'cleanup_cleared' AND incomplete.phase <> 'cleanup_cleared'))
 )
)
BEGIN
    SELECT RAISE(ABORT, 'wait operation aggregate head count is incomplete');
END;

-- Target journal rows form one append-only state machine.  A CAS may keep the
-- same phase, but it cannot skip backwards or jump across an unrelated phase.
CREATE TRIGGER trg_orchestration_wait_operation_target_phase_transition
BEFORE INSERT ON orchestration_wait_operation_target_journal
WHEN NEW.version > 1
 AND NOT (
    ((SELECT prior.phase FROM orchestration_wait_operation_target_journal AS prior
       WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
         AND prior.version = NEW.version - 1) = NEW.phase
     AND (SELECT prior.uncertain_phase FROM orchestration_wait_operation_target_journal AS prior
       WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
         AND prior.version = NEW.version - 1) = NEW.uncertain_phase
     AND (SELECT prior.resolved_grant_id FROM orchestration_wait_operation_target_journal AS prior
       WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
         AND prior.version = NEW.version - 1) IS NEW.resolved_grant_id
     AND (SELECT prior.grant_revision FROM orchestration_wait_operation_target_journal AS prior
       WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
         AND prior.version = NEW.version - 1) = NEW.grant_revision
     AND (SELECT prior.grant_status FROM orchestration_wait_operation_target_journal AS prior
       WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
         AND prior.version = NEW.version - 1) = NEW.grant_status
     AND (SELECT prior.resolved_wait_handle_id FROM orchestration_wait_operation_target_journal AS prior
       WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
         AND prior.version = NEW.version - 1) IS NEW.resolved_wait_handle_id
     AND (SELECT prior.wait_revision FROM orchestration_wait_operation_target_journal AS prior
       WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
         AND prior.version = NEW.version - 1) = NEW.wait_revision
     AND (SELECT prior.wait_status FROM orchestration_wait_operation_target_journal AS prior
       WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
         AND prior.version = NEW.version - 1) = NEW.wait_status
     AND (SELECT prior.grant_cleanup_state FROM orchestration_wait_operation_target_journal AS prior
       WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
         AND prior.version = NEW.version - 1) = NEW.grant_cleanup_state
     AND (SELECT prior.wait_cleanup_state FROM orchestration_wait_operation_target_journal AS prior
       WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
         AND prior.version = NEW.version - 1) = NEW.wait_cleanup_state
     AND (SELECT prior.error_code FROM orchestration_wait_operation_target_journal AS prior
       WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
         AND prior.version = NEW.version - 1) = NEW.error_code
     AND (SELECT prior.error_ref FROM orchestration_wait_operation_target_journal AS prior
       WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
         AND prior.version = NEW.version - 1) = NEW.error_ref
     AND (SELECT prior.resolved_at FROM orchestration_wait_operation_target_journal AS prior
       WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
         AND prior.version = NEW.version - 1) = NEW.resolved_at)
    OR ((SELECT prior.phase FROM orchestration_wait_operation_target_journal AS prior
          WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
            AND prior.version = NEW.version - 1) = 'planned'
        AND NEW.phase IN ('grant_pending', 'outcome_unknown', 'failed'))
    OR ((SELECT prior.phase FROM orchestration_wait_operation_target_journal AS prior
          WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
            AND prior.version = NEW.version - 1) = 'grant_pending'
        AND NEW.phase IN ('wait_pending', 'outcome_unknown', 'failed'))
    OR ((SELECT prior.phase FROM orchestration_wait_operation_target_journal AS prior
          WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
            AND prior.version = NEW.version - 1) = 'wait_pending'
        AND NEW.phase IN ('waiting', 'outcome_unknown', 'failed'))
    OR ((SELECT prior.phase FROM orchestration_wait_operation_target_journal AS prior
          WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
            AND prior.version = NEW.version - 1) = 'waiting'
        AND NEW.phase IN ('satisfied', 'timed_out', 'cancelled', 'outcome_unknown', 'cleanup_pending'))
    OR ((SELECT prior.phase FROM orchestration_wait_operation_target_journal AS prior
          WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
            AND prior.version = NEW.version - 1) IN ('satisfied', 'timed_out', 'cancelled', 'failed')
        AND NEW.phase IN ('cleanup_pending', 'outcome_unknown'))
    OR ((SELECT prior.phase FROM orchestration_wait_operation_target_journal AS prior
          WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
            AND prior.version = NEW.version - 1) = 'outcome_unknown'
        AND (SELECT prior.uncertain_phase FROM orchestration_wait_operation_target_journal AS prior
             WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
               AND prior.version = NEW.version - 1) = 'grant_create'
        AND NEW.phase IN ('grant_pending', 'wait_pending', 'failed'))
    OR ((SELECT prior.phase FROM orchestration_wait_operation_target_journal AS prior
          WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
            AND prior.version = NEW.version - 1) = 'outcome_unknown'
        AND (SELECT prior.uncertain_phase FROM orchestration_wait_operation_target_journal AS prior
             WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
               AND prior.version = NEW.version - 1) = 'wait_create'
        AND NEW.phase IN ('wait_pending', 'waiting', 'failed'))
    OR ((SELECT prior.phase FROM orchestration_wait_operation_target_journal AS prior
          WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
            AND prior.version = NEW.version - 1) = 'outcome_unknown'
        AND (SELECT prior.uncertain_phase FROM orchestration_wait_operation_target_journal AS prior
             WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
               AND prior.version = NEW.version - 1) = 'target_transition'
        AND (SELECT prior.grant_status FROM orchestration_wait_operation_target_journal AS prior
             WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
               AND prior.version = NEW.version - 1) = 'planned'
        AND (SELECT prior.wait_status FROM orchestration_wait_operation_target_journal AS prior
             WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
               AND prior.version = NEW.version - 1) = 'absent'
        AND NEW.phase IN ('grant_pending', 'failed'))
    OR ((SELECT prior.phase FROM orchestration_wait_operation_target_journal AS prior
          WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
            AND prior.version = NEW.version - 1) = 'outcome_unknown'
        AND (SELECT prior.uncertain_phase FROM orchestration_wait_operation_target_journal AS prior
             WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
               AND prior.version = NEW.version - 1) = 'target_transition'
        AND (SELECT prior.grant_status FROM orchestration_wait_operation_target_journal AS prior
             WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
               AND prior.version = NEW.version - 1) = 'active'
        AND (SELECT prior.wait_status FROM orchestration_wait_operation_target_journal AS prior
             WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
               AND prior.version = NEW.version - 1) = 'planned'
        AND NEW.phase IN ('wait_pending', 'waiting', 'failed', 'cleanup_pending'))
    OR ((SELECT prior.phase FROM orchestration_wait_operation_target_journal AS prior
          WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
            AND prior.version = NEW.version - 1) = 'outcome_unknown'
        AND (SELECT prior.uncertain_phase FROM orchestration_wait_operation_target_journal AS prior
             WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
               AND prior.version = NEW.version - 1) = 'target_transition'
        AND (SELECT prior.grant_status FROM orchestration_wait_operation_target_journal AS prior
             WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
               AND prior.version = NEW.version - 1) = 'active'
        AND (SELECT prior.wait_status FROM orchestration_wait_operation_target_journal AS prior
             WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
               AND prior.version = NEW.version - 1) = 'pending'
        AND NEW.phase IN ('waiting', 'satisfied', 'timed_out', 'cancelled', 'cleanup_pending'))
    OR ((SELECT prior.phase FROM orchestration_wait_operation_target_journal AS prior
          WHERE prior.operation_key = NEW.operation_key AND prior.target_key = NEW.target_key
            AND prior.version = NEW.version - 1) = 'cleanup_pending'
        AND NEW.phase = 'cleanup_cleared')
 )
BEGIN
    SELECT RAISE(ABORT, 'wait operation target phase transition is invalid');
END;

CREATE TRIGGER trg_orchestration_wait_operations_no_delete
BEFORE DELETE ON orchestration_wait_operations
BEGIN
    SELECT RAISE(ABORT, 'wait operations are immutable history');
END;

CREATE TRIGGER trg_orchestration_wait_operation_target_no_update
BEFORE UPDATE ON orchestration_wait_operation_target_journal
BEGIN
    SELECT RAISE(ABORT, 'wait operation target journal is append-only');
END;

CREATE TRIGGER trg_orchestration_wait_operation_target_no_delete
BEFORE DELETE ON orchestration_wait_operation_target_journal
BEGIN
    SELECT RAISE(ABORT, 'wait operation target journal is immutable history');
END;

CREATE TABLE orchestration_wait_operation_commit_markers (
    marker_key TEXT PRIMARY KEY NOT NULL CHECK(
        length(marker_key) = 64 AND marker_key NOT GLOB '*[^0-9a-f]*'
        AND trim(marker_key) = marker_key AND instr(marker_key, char(0)) = 0
    ),
    operation_key TEXT NOT NULL CHECK(
        length(trim(operation_key)) > 0 AND length(operation_key) <= 256
        AND trim(operation_key) = operation_key AND instr(operation_key, char(0)) = 0
    ),
    operation_digest TEXT NOT NULL CHECK(
        length(operation_digest) = 64 AND operation_digest NOT GLOB '*[^0-9a-f]*'
    ),
    target_key TEXT NOT NULL DEFAULT '' CHECK(
        length(target_key) <= 128 AND trim(target_key) = target_key
        AND instr(target_key, char(0)) = 0
    ),
    mutation_kind TEXT NOT NULL CHECK(mutation_kind IN (
        'plan', 'target_append', 'state_advance', 'target_provision',
        'target_cleanup', 'target_resume'
    )),
    request_digest TEXT NOT NULL CHECK(
        length(request_digest) = 64 AND request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    pre_state_digest TEXT NOT NULL CHECK(
        pre_state_digest = '' OR (
            length(pre_state_digest) = 64 AND pre_state_digest NOT GLOB '*[^0-9a-f]*'
        )
    ),
    post_state_digest TEXT NOT NULL DEFAULT '' CHECK(
        post_state_digest = '' OR (
            length(post_state_digest) = 64 AND post_state_digest NOT GLOB '*[^0-9a-f]*'
        )
    ),
    state TEXT NOT NULL CHECK(state IN ('pending', 'resolved')),
    created_at TEXT NOT NULL CHECK(
        length(trim(created_at)) > 0 AND length(created_at) <= 64
        AND trim(created_at) = created_at AND instr(created_at, char(0)) = 0
        AND julianday(created_at) IS NOT NULL
    ),
    resolved_at TEXT NOT NULL DEFAULT '' CHECK(
        length(resolved_at) <= 64 AND trim(resolved_at) = resolved_at
        AND instr(resolved_at, char(0)) = 0
        AND (resolved_at = '' OR julianday(resolved_at) IS NOT NULL)
    ),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    CHECK(mutation_kind = 'plan' OR pre_state_digest <> ''),
    CHECK(
        (mutation_kind IN ('plan', 'state_advance') AND target_key = '')
        OR (mutation_kind IN ('target_append', 'target_provision', 'target_cleanup', 'target_resume')
            AND target_key <> '')
    ),
    CHECK(
        (state = 'pending' AND resolved_at = '')
        OR (state = 'resolved' AND post_state_digest <> '' AND resolved_at <> ''
            AND julianday(resolved_at) >= julianday(created_at))
    )
) STRICT;

CREATE INDEX idx_orchestration_wait_operation_commit_markers_unresolved
    ON orchestration_wait_operation_commit_markers(state, operation_key, marker_key);

CREATE TRIGGER trg_orchestration_wait_operation_commit_marker_pending_insert
BEFORE INSERT ON orchestration_wait_operation_commit_markers
WHEN NEW.state <> 'pending' OR NEW.post_state_digest <> '' OR NEW.resolved_at <> ''
BEGIN
    SELECT RAISE(ABORT, 'wait operation commit marker must start pending without post-state');
END;

CREATE TRIGGER trg_orchestration_wait_operation_commit_marker_immutable
BEFORE UPDATE OF marker_key, operation_key, operation_digest, target_key,
    mutation_kind, request_digest, pre_state_digest, created_at, schema_version
ON orchestration_wait_operation_commit_markers
BEGIN
    SELECT RAISE(ABORT, 'wait operation commit marker identity is immutable');
END;

CREATE TRIGGER trg_orchestration_wait_operation_commit_marker_transition
BEFORE UPDATE OF post_state_digest, state, resolved_at
ON orchestration_wait_operation_commit_markers
WHEN NOT (
    (OLD.state = 'pending' AND NEW.state = 'pending'
        AND OLD.post_state_digest = '' AND NEW.post_state_digest <> ''
        AND NEW.resolved_at = '')
    OR (OLD.state = 'pending' AND NEW.state = 'resolved'
        AND OLD.post_state_digest <> '' AND NEW.post_state_digest = OLD.post_state_digest
        AND NEW.resolved_at <> '')
)
BEGIN
    SELECT RAISE(ABORT, 'wait operation commit marker transition is invalid');
END;

CREATE TRIGGER trg_orchestration_wait_operation_commit_marker_no_delete
BEFORE DELETE ON orchestration_wait_operation_commit_markers
BEGIN
    SELECT RAISE(ABORT, 'wait operation commit markers cannot be deleted');
END;
