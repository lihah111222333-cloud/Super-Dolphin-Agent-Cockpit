-- Canonical logical-agent identity owner. Legacy agent_* tables remain projections.
-- This migration stores opaque fence/receipt references only; it does not grant
-- provider execution, issue a permit, or persist prompt/credential material.
CREATE TABLE orchestration_identity_reservations (
    reservation_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(trim(reservation_id)) > 0 AND length(reservation_id) <= 128),
    command_id TEXT
        REFERENCES orchestration_command_intents(command_id) ON DELETE RESTRICT,
    agent_id TEXT NOT NULL
        CHECK(length(trim(agent_id)) > 0 AND length(agent_id) <= 128),
    identity_kind TEXT NOT NULL CHECK(identity_kind IN (
        'provider_session', 'native_subagent', 'managed_agent', 'a2a_remote_agent'
    )),
    source_kind TEXT NOT NULL CHECK(source_kind IN (
        'first_launch', 'recover', 'replace', 'provider_discovery', 'a2a_observation'
    )),
    caller_principal_ref TEXT NOT NULL
        CHECK(length(trim(caller_principal_ref)) > 0 AND length(caller_principal_ref) <= 512),
    scope_ref TEXT NOT NULL
        CHECK(length(trim(scope_ref)) > 0 AND length(scope_ref) <= 512),
    parent_agent_id TEXT NOT NULL CHECK(length(parent_agent_id) <= 128),
    idempotency_namespace TEXT NOT NULL
        CHECK(length(trim(idempotency_namespace)) > 0 AND length(idempotency_namespace) <= 256),
    idempotency_key_digest TEXT NOT NULL CHECK(
        length(idempotency_key_digest) = 64
        AND idempotency_key_digest NOT GLOB '*[^0-9a-f]*'
    ),
    request_digest TEXT NOT NULL CHECK(
        length(request_digest) = 64
        AND request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    source_fence_ref TEXT NOT NULL
        CHECK(length(trim(source_fence_ref)) > 0 AND length(source_fence_ref) <= 512),
    target_fence_ref TEXT NOT NULL
        CHECK(length(trim(target_fence_ref)) > 0 AND length(target_fence_ref) <= 512),
    expected_binding_version INTEGER NOT NULL CHECK(expected_binding_version >= 0),
    reservation_generation INTEGER NOT NULL CHECK(reservation_generation > 0),
    state TEXT NOT NULL CHECK(state IN (
        'reserved', 'claimed', 'identity_pending', 'activation_pending',
        'outcome_unknown', 'activated', 'rejected', 'failed', 'expired'
    )),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    created_at TEXT NOT NULL
        CHECK(length(trim(created_at)) > 0)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL
        CHECK(length(trim(updated_at)) > 0)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(idempotency_namespace, idempotency_key_digest),
    UNIQUE(agent_id, reservation_generation),
    UNIQUE(command_id),
    UNIQUE(reservation_id, agent_id, identity_kind),
    UNIQUE(reservation_id, agent_id),
    CHECK(
        (source_kind IN ('first_launch', 'recover', 'replace') AND command_id IS NOT NULL)
        OR (source_kind IN ('provider_discovery', 'a2a_observation') AND command_id IS NULL)
    )
) STRICT;

CREATE TABLE orchestration_execution_incarnations (
    incarnation_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(trim(incarnation_id)) > 0 AND length(incarnation_id) <= 128),
    reservation_id TEXT NOT NULL UNIQUE
        REFERENCES orchestration_identity_reservations(reservation_id) ON DELETE RESTRICT,
    agent_id TEXT NOT NULL
        CHECK(length(trim(agent_id)) > 0 AND length(agent_id) <= 128),
    identity_kind TEXT NOT NULL CHECK(identity_kind IN (
        'provider_session', 'native_subagent', 'managed_agent', 'a2a_remote_agent'
    )),
    external_identity_ref TEXT NOT NULL
        CHECK(length(trim(external_identity_ref)) > 0 AND length(external_identity_ref) <= 512),
    external_epoch TEXT NOT NULL
        CHECK(length(trim(external_epoch)) > 0 AND length(external_epoch) <= 128),
    source_fence_ref TEXT NOT NULL
        CHECK(length(trim(source_fence_ref)) > 0 AND length(source_fence_ref) <= 512),
    identity_digest TEXT NOT NULL CHECK(
        length(identity_digest) = 64
        AND identity_digest NOT GLOB '*[^0-9a-f]*'
    ),
    incarnation_generation INTEGER NOT NULL CHECK(incarnation_generation > 0),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    created_at TEXT NOT NULL
        CHECK(length(trim(created_at)) > 0)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(agent_id, incarnation_generation),
    UNIQUE(identity_kind, external_identity_ref, external_epoch),
    UNIQUE(incarnation_id, reservation_id, agent_id, identity_kind),
    FOREIGN KEY(reservation_id, agent_id, identity_kind)
        REFERENCES orchestration_identity_reservations(reservation_id, agent_id, identity_kind)
        ON DELETE RESTRICT
) STRICT;

CREATE TABLE orchestration_binding_preparations (
    preparation_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(trim(preparation_id)) > 0 AND length(preparation_id) <= 128),
    reservation_id TEXT NOT NULL
        REFERENCES orchestration_identity_reservations(reservation_id) ON DELETE RESTRICT,
    incarnation_id TEXT NOT NULL
        REFERENCES orchestration_execution_incarnations(incarnation_id) ON DELETE RESTRICT,
    agent_id TEXT NOT NULL
        CHECK(length(trim(agent_id)) > 0 AND length(agent_id) <= 128),
    identity_kind TEXT NOT NULL CHECK(identity_kind IN (
        'provider_session', 'native_subagent', 'managed_agent', 'a2a_remote_agent'
    )),
    preparation_generation INTEGER NOT NULL CHECK(preparation_generation > 0),
    expected_binding_version INTEGER NOT NULL CHECK(expected_binding_version >= 0),
    expected_incarnation_generation INTEGER NOT NULL CHECK(expected_incarnation_generation > 0),
    binding_digest TEXT NOT NULL CHECK(
        length(binding_digest) = 64
        AND binding_digest NOT GLOB '*[^0-9a-f]*'
    ),
    source_fence_ref TEXT NOT NULL
        CHECK(length(trim(source_fence_ref)) > 0 AND length(source_fence_ref) <= 512),
    active_process_receipt_ref TEXT NOT NULL CHECK(length(active_process_receipt_ref) <= 512),
    state TEXT NOT NULL CHECK(state IN (
        'prepared_non_authorizing', 'finalized', 'aborted', 'outcome_unknown'
    )),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    created_at TEXT NOT NULL
        CHECK(length(trim(created_at)) > 0)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    resolved_at TEXT NOT NULL CHECK(length(resolved_at) <= 64),
    UNIQUE(reservation_id, preparation_generation),
    UNIQUE(incarnation_id, preparation_generation),
    FOREIGN KEY(reservation_id, agent_id, identity_kind)
        REFERENCES orchestration_identity_reservations(reservation_id, agent_id, identity_kind)
        ON DELETE RESTRICT,
    FOREIGN KEY(incarnation_id, reservation_id, agent_id, identity_kind)
        REFERENCES orchestration_execution_incarnations(incarnation_id, reservation_id, agent_id, identity_kind)
        ON DELETE RESTRICT
) STRICT;

CREATE TABLE orchestration_current_bindings (
    agent_id TEXT NOT NULL
        CHECK(length(trim(agent_id)) > 0 AND length(agent_id) <= 128),
    binding_version INTEGER NOT NULL CHECK(binding_version > 0),
    reservation_id TEXT NOT NULL
        REFERENCES orchestration_identity_reservations(reservation_id) ON DELETE RESTRICT,
    incarnation_id TEXT NOT NULL
        REFERENCES orchestration_execution_incarnations(incarnation_id) ON DELETE RESTRICT,
    identity_kind TEXT NOT NULL CHECK(identity_kind IN (
        'provider_session', 'native_subagent', 'managed_agent', 'a2a_remote_agent'
    )),
    reservation_generation INTEGER NOT NULL CHECK(reservation_generation > 0),
    incarnation_generation INTEGER NOT NULL CHECK(incarnation_generation > 0),
    external_identity_ref TEXT NOT NULL
        CHECK(length(trim(external_identity_ref)) > 0 AND length(external_identity_ref) <= 512),
    source_fence_ref TEXT NOT NULL
        CHECK(length(trim(source_fence_ref)) > 0 AND length(source_fence_ref) <= 512),
    active_process_receipt_ref TEXT NOT NULL
        CHECK(length(trim(active_process_receipt_ref)) > 0 AND length(active_process_receipt_ref) <= 512),
    binding_digest TEXT NOT NULL CHECK(
        length(binding_digest) = 64
        AND binding_digest NOT GLOB '*[^0-9a-f]*'
    ),
    state TEXT NOT NULL CHECK(state IN ('active', 'revoking', 'revoked')),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    updated_at TEXT NOT NULL
        CHECK(length(trim(updated_at)) > 0)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY(agent_id, binding_version),
    FOREIGN KEY(reservation_id, agent_id, identity_kind)
        REFERENCES orchestration_identity_reservations(reservation_id, agent_id, identity_kind)
        ON DELETE RESTRICT,
    FOREIGN KEY(incarnation_id, reservation_id, agent_id, identity_kind)
        REFERENCES orchestration_execution_incarnations(incarnation_id, reservation_id, agent_id, identity_kind)
        ON DELETE RESTRICT,
    CHECK(state = 'revoked' OR binding_version > 0)
) STRICT;

CREATE INDEX idx_orchestration_identity_reservations_agent
    ON orchestration_identity_reservations(agent_id, reservation_generation);
CREATE INDEX idx_orchestration_execution_incarnations_agent
    ON orchestration_execution_incarnations(agent_id, incarnation_generation);
CREATE INDEX idx_orchestration_binding_preparations_agent
    ON orchestration_binding_preparations(agent_id, preparation_generation);
CREATE INDEX idx_orchestration_current_bindings_latest
    ON orchestration_current_bindings(agent_id, binding_version DESC);

-- Identity metadata is immutable. Lifecycle heads may change only through the
-- repository's fenced CAS methods; direct metadata rewrites are always blocked.
CREATE TRIGGER trg_orchestration_identity_reservations_immutable_fields
BEFORE UPDATE ON orchestration_identity_reservations
WHEN NEW.reservation_id <> OLD.reservation_id
  OR NEW.command_id IS NOT OLD.command_id
  OR NEW.agent_id <> OLD.agent_id
  OR NEW.identity_kind <> OLD.identity_kind
  OR NEW.source_kind <> OLD.source_kind
  OR NEW.caller_principal_ref <> OLD.caller_principal_ref
  OR NEW.scope_ref <> OLD.scope_ref
  OR NEW.parent_agent_id <> OLD.parent_agent_id
  OR NEW.idempotency_namespace <> OLD.idempotency_namespace
  OR NEW.idempotency_key_digest <> OLD.idempotency_key_digest
  OR NEW.request_digest <> OLD.request_digest
  OR NEW.source_fence_ref <> OLD.source_fence_ref
  OR NEW.target_fence_ref <> OLD.target_fence_ref
  OR NEW.expected_binding_version <> OLD.expected_binding_version
  OR NEW.reservation_generation <> OLD.reservation_generation
  OR NEW.schema_version <> OLD.schema_version
  OR NEW.created_at <> OLD.created_at
BEGIN
    SELECT RAISE(ABORT, 'identity reservation metadata is immutable');
END;

CREATE TRIGGER trg_orchestration_identity_reservations_no_delete
BEFORE DELETE ON orchestration_identity_reservations
BEGIN
    SELECT RAISE(ABORT, 'identity reservations are immutable');
END;

CREATE TRIGGER trg_orchestration_execution_incarnations_no_update
BEFORE UPDATE ON orchestration_execution_incarnations
BEGIN
    SELECT RAISE(ABORT, 'execution incarnations are immutable');
END;

CREATE TRIGGER trg_orchestration_execution_incarnations_no_delete
BEFORE DELETE ON orchestration_execution_incarnations
BEGIN
    SELECT RAISE(ABORT, 'execution incarnations are immutable');
END;

CREATE TRIGGER trg_orchestration_binding_preparations_immutable_fields
BEFORE UPDATE ON orchestration_binding_preparations
WHEN NEW.preparation_id <> OLD.preparation_id
  OR NEW.reservation_id <> OLD.reservation_id
  OR NEW.incarnation_id <> OLD.incarnation_id
  OR NEW.agent_id <> OLD.agent_id
  OR NEW.preparation_generation <> OLD.preparation_generation
  OR NEW.expected_binding_version <> OLD.expected_binding_version
  OR NEW.expected_incarnation_generation <> OLD.expected_incarnation_generation
  OR NEW.binding_digest <> OLD.binding_digest
  OR NEW.source_fence_ref <> OLD.source_fence_ref
  OR (NEW.active_process_receipt_ref <> OLD.active_process_receipt_ref
      AND NOT (OLD.active_process_receipt_ref = ''
               AND NEW.active_process_receipt_ref <> ''
               AND OLD.state = 'prepared_non_authorizing'
               AND NEW.state = 'finalized'))
  OR NEW.schema_version <> OLD.schema_version
  OR NEW.created_at <> OLD.created_at
BEGIN
    SELECT RAISE(ABORT, 'binding preparation metadata is immutable');
END;

CREATE TRIGGER trg_orchestration_binding_preparations_no_delete
BEFORE DELETE ON orchestration_binding_preparations
BEGIN
    SELECT RAISE(ABORT, 'binding preparations are immutable');
END;

CREATE TRIGGER trg_orchestration_identity_reservations_state_machine
BEFORE UPDATE OF state ON orchestration_identity_reservations
WHEN NOT (
    (OLD.state = 'reserved' AND NEW.state IN ('claimed', 'identity_pending', 'rejected', 'expired')) OR
    (OLD.state = 'claimed' AND NEW.state IN ('identity_pending', 'outcome_unknown', 'rejected', 'failed')) OR
    (OLD.state = 'identity_pending' AND NEW.state IN ('activation_pending', 'outcome_unknown', 'rejected', 'failed')) OR
    (OLD.state = 'activation_pending' AND NEW.state IN ('activated', 'outcome_unknown', 'rejected', 'failed')) OR
    (OLD.state = 'outcome_unknown' AND NEW.state IN ('identity_pending', 'activation_pending', 'activated', 'rejected', 'failed', 'expired')) OR
    (OLD.state = NEW.state)
)
BEGIN
    SELECT RAISE(ABORT, 'identity reservation state transition is invalid');
END;

CREATE TRIGGER trg_orchestration_binding_preparations_state_machine
BEFORE UPDATE OF state ON orchestration_binding_preparations
WHEN NOT (
    (OLD.state = 'prepared_non_authorizing' AND NEW.state IN ('finalized', 'aborted', 'outcome_unknown')) OR
    (OLD.state = 'outcome_unknown' AND NEW.state IN ('finalized', 'aborted')) OR
    (OLD.state = NEW.state)
)
BEGIN
    SELECT RAISE(ABORT, 'binding preparation state transition is invalid');
END;

CREATE TRIGGER trg_orchestration_binding_preparations_finalize_receipt
BEFORE UPDATE OF state ON orchestration_binding_preparations
WHEN NEW.state = 'finalized' AND length(trim(NEW.active_process_receipt_ref)) = 0
BEGIN
    SELECT RAISE(ABORT, 'finalized binding requires active process receipt');
END;

CREATE TRIGGER trg_orchestration_current_bindings_no_update
BEFORE UPDATE ON orchestration_current_bindings
BEGIN
    SELECT RAISE(ABORT, 'current binding history is immutable');
END;

CREATE TRIGGER trg_orchestration_current_bindings_no_delete
BEFORE DELETE ON orchestration_current_bindings
BEGIN
    SELECT RAISE(ABORT, 'current binding history is immutable');
END;

CREATE TRIGGER trg_orchestration_current_bindings_version_contiguous
BEFORE INSERT ON orchestration_current_bindings
WHEN NEW.binding_version <> COALESCE((
    SELECT MAX(binding_version) + 1
    FROM orchestration_current_bindings
    WHERE agent_id = NEW.agent_id
), 1)
BEGIN
    SELECT RAISE(ABORT, 'current binding version must advance contiguously');
END;

CREATE TRIGGER trg_orchestration_current_bindings_state_machine
BEFORE INSERT ON orchestration_current_bindings
WHEN (
    NEW.binding_version = 1 AND NEW.state <> 'active'
) OR (
    NEW.binding_version > 1 AND NEW.state NOT IN ('active', 'revoking', 'revoked')
) OR (
    NEW.binding_version > 1 AND NOT EXISTS (
        SELECT 1 FROM orchestration_current_bindings AS previous
        WHERE previous.agent_id = NEW.agent_id
          AND previous.binding_version = NEW.binding_version - 1
          AND (
              (previous.state = 'active' AND NEW.state IN ('revoking', 'revoked')) OR
              (previous.state = 'revoking' AND NEW.state = 'revoked') OR
              (previous.state = 'revoked' AND NEW.state = 'active')
          )
    )
)
BEGIN
    SELECT RAISE(ABORT, 'current binding state transition is invalid');
END;

CREATE TRIGGER trg_orchestration_execution_incarnation_generation_monotonic
BEFORE INSERT ON orchestration_execution_incarnations
WHEN NEW.incarnation_generation <= COALESCE((
    SELECT MAX(incarnation_generation)
    FROM orchestration_execution_incarnations
    WHERE agent_id = NEW.agent_id
), 0)
  AND NOT EXISTS (
      SELECT 1 FROM orchestration_execution_incarnations
      WHERE reservation_id = NEW.reservation_id
  )
BEGIN
    SELECT RAISE(ABORT, 'execution incarnation generation must increase');
END;

CREATE TRIGGER trg_orchestration_reservation_generation_monotonic
BEFORE INSERT ON orchestration_identity_reservations
WHEN NEW.reservation_generation <= COALESCE((
    SELECT MAX(reservation_generation)
    FROM orchestration_identity_reservations
    WHERE agent_id = NEW.agent_id
), 0)
  AND NOT EXISTS (
      SELECT 1 FROM orchestration_identity_reservations
      WHERE idempotency_namespace = NEW.idempotency_namespace
        AND idempotency_key_digest = NEW.idempotency_key_digest
  )
BEGIN
    SELECT RAISE(ABORT, 'identity reservation generation must increase');
END;
