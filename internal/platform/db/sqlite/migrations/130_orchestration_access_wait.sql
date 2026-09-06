-- Canonical authorization grants and non-authorizing event wait handles.
-- Grant/wait metadata is immutable after creation; lifecycle changes use
-- compare-and-swap (CAS) updates and retain all historical rows.

CREATE TABLE orchestration_access_grants (
    grant_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(trim(grant_id)) > 0 AND length(grant_id) <= 128),
    caller_principal_ref TEXT NOT NULL
        CHECK(length(trim(caller_principal_ref)) > 0 AND length(caller_principal_ref) <= 512),
    scope_ref TEXT NOT NULL
        CHECK(length(trim(scope_ref)) > 0 AND length(scope_ref) <= 512),
    source_agent_id TEXT NOT NULL
        CHECK(length(trim(source_agent_id)) > 0 AND length(source_agent_id) <= 128),
    target_agent_id TEXT NOT NULL
        CHECK(length(trim(target_agent_id)) > 0 AND length(target_agent_id) <= 128),
    source_reservation_id TEXT NOT NULL
        CHECK(length(trim(source_reservation_id)) > 0 AND length(source_reservation_id) <= 128),
    source_reservation_generation INTEGER NOT NULL CHECK(source_reservation_generation > 0),
    source_incarnation_id TEXT NOT NULL
        CHECK(length(trim(source_incarnation_id)) > 0 AND length(source_incarnation_id) <= 128),
    source_incarnation_generation INTEGER NOT NULL CHECK(source_incarnation_generation > 0),
    source_binding_version INTEGER NOT NULL CHECK(source_binding_version > 0),
    source_binding_digest TEXT NOT NULL CHECK(
        length(source_binding_digest) = 64 AND source_binding_digest NOT GLOB '*[^0-9a-f]*'
    ),
    source_fence_ref TEXT NOT NULL
        CHECK(length(trim(source_fence_ref)) > 0 AND length(source_fence_ref) <= 512),
    target_reservation_id TEXT NOT NULL
        CHECK(length(trim(target_reservation_id)) > 0 AND length(target_reservation_id) <= 128),
    target_reservation_generation INTEGER NOT NULL CHECK(target_reservation_generation > 0),
    target_incarnation_id TEXT NOT NULL
        CHECK(length(trim(target_incarnation_id)) > 0 AND length(target_incarnation_id) <= 128),
    target_incarnation_generation INTEGER NOT NULL CHECK(target_incarnation_generation > 0),
    target_binding_version INTEGER NOT NULL CHECK(target_binding_version > 0),
    target_binding_digest TEXT NOT NULL CHECK(
        length(target_binding_digest) = 64 AND target_binding_digest NOT GLOB '*[^0-9a-f]*'
    ),
    target_fence_ref TEXT NOT NULL
        CHECK(length(trim(target_fence_ref)) > 0 AND length(target_fence_ref) <= 512),
    can_read INTEGER NOT NULL CHECK(can_read IN (0, 1)),
    can_send INTEGER NOT NULL CHECK(can_send IN (0, 1)),
    idempotency_namespace TEXT NOT NULL
        CHECK(length(trim(idempotency_namespace)) > 0 AND length(idempotency_namespace) <= 256),
    idempotency_key_digest TEXT NOT NULL CHECK(
        length(idempotency_key_digest) = 64 AND idempotency_key_digest NOT GLOB '*[^0-9a-f]*'
    ),
    request_digest TEXT NOT NULL CHECK(
        length(request_digest) = 64 AND request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    revision INTEGER NOT NULL CHECK(revision > 0),
    status TEXT NOT NULL CHECK(status IN ('active', 'revoked', 'expired')),
    issued_at TEXT NOT NULL CHECK(length(trim(issued_at)) > 0 AND length(issued_at) <= 64),
    expires_at TEXT NOT NULL CHECK(length(trim(expires_at)) > 0 AND length(expires_at) <= 64),
    created_at TEXT NOT NULL
        CHECK(length(trim(created_at)) > 0 AND length(created_at) <= 64)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL
        CHECK(length(trim(updated_at)) > 0 AND length(updated_at) <= 64)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    UNIQUE(idempotency_namespace, idempotency_key_digest),
    CHECK(source_agent_id <> target_agent_id),
    CHECK(can_read = 1 OR can_send = 1),
    CHECK(expires_at > issued_at),
    FOREIGN KEY(source_reservation_id, source_agent_id)
        REFERENCES orchestration_identity_reservations(reservation_id, agent_id)
        ON DELETE RESTRICT,
    FOREIGN KEY(target_reservation_id, target_agent_id)
        REFERENCES orchestration_identity_reservations(reservation_id, agent_id)
        ON DELETE RESTRICT,
    FOREIGN KEY(source_incarnation_id)
        REFERENCES orchestration_execution_incarnations(incarnation_id)
        ON DELETE RESTRICT,
    FOREIGN KEY(target_incarnation_id)
        REFERENCES orchestration_execution_incarnations(incarnation_id)
        ON DELETE RESTRICT
) STRICT;

CREATE TABLE orchestration_event_wait_handles (
    wait_handle_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(trim(wait_handle_id)) > 0 AND length(wait_handle_id) <= 128),
    grant_id TEXT NOT NULL
        REFERENCES orchestration_access_grants(grant_id) ON DELETE RESTRICT,
    grant_revision INTEGER NOT NULL CHECK(grant_revision > 0),
    caller_principal_ref TEXT NOT NULL
        CHECK(length(trim(caller_principal_ref)) > 0 AND length(caller_principal_ref) <= 512),
    scope_ref TEXT NOT NULL
        CHECK(length(trim(scope_ref)) > 0 AND length(scope_ref) <= 512),
    source_agent_id TEXT NOT NULL
        CHECK(length(trim(source_agent_id)) > 0 AND length(source_agent_id) <= 128),
    target_agent_id TEXT NOT NULL
        CHECK(length(trim(target_agent_id)) > 0 AND length(target_agent_id) <= 128),
    target_reservation_id TEXT NOT NULL
        CHECK(length(trim(target_reservation_id)) > 0 AND length(target_reservation_id) <= 128),
    target_reservation_generation INTEGER NOT NULL CHECK(target_reservation_generation > 0),
    target_incarnation_id TEXT NOT NULL
        CHECK(length(trim(target_incarnation_id)) > 0 AND length(target_incarnation_id) <= 128),
    target_incarnation_generation INTEGER NOT NULL CHECK(target_incarnation_generation > 0),
    target_binding_version INTEGER NOT NULL CHECK(target_binding_version > 0),
    target_binding_digest TEXT NOT NULL CHECK(
        length(target_binding_digest) = 64 AND target_binding_digest NOT GLOB '*[^0-9a-f]*'
    ),
    target_fence_ref TEXT NOT NULL
        CHECK(length(trim(target_fence_ref)) > 0 AND length(target_fence_ref) <= 512),
    source_id TEXT NOT NULL
        CHECK(length(trim(source_id)) > 0 AND length(source_id) <= 256),
    source_epoch TEXT NOT NULL
        CHECK(length(trim(source_epoch)) > 0 AND length(source_epoch) <= 128),
    lane TEXT NOT NULL CHECK(lane IN ('control', 'output', 'content')),
    selector_version INTEGER NOT NULL CHECK(selector_version = 1),
    selector_json TEXT NOT NULL
        CHECK(length(trim(selector_json)) > 0 AND length(selector_json) <= 4096 AND json_valid(selector_json)),
    selector_digest TEXT NOT NULL CHECK(
        length(selector_digest) = 64 AND selector_digest NOT GLOB '*[^0-9a-f]*'
    ),
    event_types_json TEXT NOT NULL
        CHECK(length(trim(event_types_json)) > 0 AND length(event_types_json) <= 4096 AND json_valid(event_types_json)),
    after_source_sequence INTEGER NOT NULL CHECK(after_source_sequence >= 0),
    after_cursor TEXT NOT NULL CHECK(length(after_cursor) <= 512),
    deadline_at TEXT NOT NULL CHECK(length(trim(deadline_at)) > 0 AND length(deadline_at) <= 64),
    idempotency_namespace TEXT NOT NULL
        CHECK(length(trim(idempotency_namespace)) > 0 AND length(idempotency_namespace) <= 256),
    idempotency_key_digest TEXT NOT NULL CHECK(
        length(idempotency_key_digest) = 64 AND idempotency_key_digest NOT GLOB '*[^0-9a-f]*'
    ),
    request_digest TEXT NOT NULL CHECK(
        length(request_digest) = 64 AND request_digest NOT GLOB '*[^0-9a-f]*'
    ),
    revision INTEGER NOT NULL CHECK(revision > 0),
    status TEXT NOT NULL CHECK(status IN ('pending', 'satisfied', 'timed_out', 'cancelled', 'revoked', 'expired')),
    matched_event_id TEXT NOT NULL DEFAULT ''
        CHECK(length(matched_event_id) <= 128),
    matched_source_sequence INTEGER NOT NULL DEFAULT 0 CHECK(matched_source_sequence >= 0),
    matched_canonical_cursor TEXT NOT NULL DEFAULT ''
        CHECK(length(matched_canonical_cursor) <= 512),
    resolved_at TEXT NOT NULL DEFAULT '' CHECK(length(resolved_at) <= 64),
    created_at TEXT NOT NULL
        CHECK(length(trim(created_at)) > 0 AND length(created_at) <= 64)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL
        CHECK(length(trim(updated_at)) > 0 AND length(updated_at) <= 64)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    UNIQUE(idempotency_namespace, idempotency_key_digest),
    CHECK(source_agent_id <> target_agent_id),
    CHECK(
        (status = 'pending' AND matched_event_id = '' AND matched_source_sequence = 0 AND matched_canonical_cursor = '' AND resolved_at = '')
        OR
        (status = 'satisfied' AND matched_event_id <> '' AND matched_source_sequence > 0 AND matched_canonical_cursor <> '' AND resolved_at <> '')
        OR
        (status IN ('timed_out', 'cancelled', 'revoked', 'expired') AND matched_event_id = '' AND matched_source_sequence = 0 AND matched_canonical_cursor = '' AND resolved_at <> '')
    ),
    FOREIGN KEY(target_reservation_id, target_agent_id)
        REFERENCES orchestration_identity_reservations(reservation_id, agent_id)
        ON DELETE RESTRICT,
    FOREIGN KEY(target_incarnation_id)
        REFERENCES orchestration_execution_incarnations(incarnation_id)
        ON DELETE RESTRICT
) STRICT;

CREATE INDEX idx_orchestration_access_grants_caller_status
    ON orchestration_access_grants(caller_principal_ref, status, expires_at);
CREATE INDEX idx_orchestration_access_grants_target
    ON orchestration_access_grants(target_agent_id, status, revision);
CREATE INDEX idx_orchestration_event_wait_handles_grant_status
    ON orchestration_event_wait_handles(grant_id, status, deadline_at);
CREATE INDEX idx_orchestration_event_wait_handles_target_status
    ON orchestration_event_wait_handles(target_agent_id, status, deadline_at);

CREATE TRIGGER trg_orchestration_access_grants_immutable_metadata
BEFORE UPDATE ON orchestration_access_grants
WHEN NEW.grant_id <> OLD.grant_id
  OR NEW.caller_principal_ref <> OLD.caller_principal_ref
  OR NEW.scope_ref <> OLD.scope_ref
  OR NEW.source_agent_id <> OLD.source_agent_id
  OR NEW.target_agent_id <> OLD.target_agent_id
  OR NEW.source_reservation_id <> OLD.source_reservation_id
  OR NEW.source_reservation_generation <> OLD.source_reservation_generation
  OR NEW.source_incarnation_id <> OLD.source_incarnation_id
  OR NEW.source_incarnation_generation <> OLD.source_incarnation_generation
  OR NEW.source_binding_version <> OLD.source_binding_version
  OR NEW.source_binding_digest <> OLD.source_binding_digest
  OR NEW.source_fence_ref <> OLD.source_fence_ref
  OR NEW.target_reservation_id <> OLD.target_reservation_id
  OR NEW.target_reservation_generation <> OLD.target_reservation_generation
  OR NEW.target_incarnation_id <> OLD.target_incarnation_id
  OR NEW.target_incarnation_generation <> OLD.target_incarnation_generation
  OR NEW.target_binding_version <> OLD.target_binding_version
  OR NEW.target_binding_digest <> OLD.target_binding_digest
  OR NEW.target_fence_ref <> OLD.target_fence_ref
  OR NEW.can_read <> OLD.can_read
  OR NEW.can_send <> OLD.can_send
  OR NEW.idempotency_namespace <> OLD.idempotency_namespace
  OR NEW.idempotency_key_digest <> OLD.idempotency_key_digest
  OR NEW.request_digest <> OLD.request_digest
  OR NEW.issued_at <> OLD.issued_at
  OR NEW.expires_at <> OLD.expires_at
  OR NEW.created_at <> OLD.created_at
  OR NEW.schema_version <> OLD.schema_version
BEGIN
    SELECT RAISE(ABORT, 'access grant metadata is immutable');
END;

CREATE TRIGGER trg_orchestration_access_grants_cas
BEFORE UPDATE ON orchestration_access_grants
WHEN NEW.revision <> OLD.revision + 1
  OR (OLD.status <> 'active')
  OR NEW.status NOT IN ('revoked', 'expired')
BEGIN
    SELECT RAISE(ABORT, 'access grant lifecycle requires active CAS');
END;

CREATE TRIGGER trg_orchestration_access_grants_no_delete
BEFORE DELETE ON orchestration_access_grants
BEGIN
    SELECT RAISE(ABORT, 'access grants are immutable history');
END;

CREATE TRIGGER trg_orchestration_event_wait_handles_immutable_metadata
BEFORE UPDATE ON orchestration_event_wait_handles
WHEN NEW.wait_handle_id <> OLD.wait_handle_id
  OR NEW.grant_id <> OLD.grant_id
  OR NEW.grant_revision <> OLD.grant_revision
  OR NEW.caller_principal_ref <> OLD.caller_principal_ref
  OR NEW.scope_ref <> OLD.scope_ref
  OR NEW.source_agent_id <> OLD.source_agent_id
  OR NEW.target_agent_id <> OLD.target_agent_id
  OR NEW.target_reservation_id <> OLD.target_reservation_id
  OR NEW.target_reservation_generation <> OLD.target_reservation_generation
  OR NEW.target_incarnation_id <> OLD.target_incarnation_id
  OR NEW.target_incarnation_generation <> OLD.target_incarnation_generation
  OR NEW.target_binding_version <> OLD.target_binding_version
  OR NEW.target_binding_digest <> OLD.target_binding_digest
  OR NEW.target_fence_ref <> OLD.target_fence_ref
  OR NEW.source_id <> OLD.source_id
  OR NEW.source_epoch <> OLD.source_epoch
  OR NEW.lane <> OLD.lane
  OR NEW.selector_version <> OLD.selector_version
  OR NEW.selector_json <> OLD.selector_json
  OR NEW.selector_digest <> OLD.selector_digest
  OR NEW.event_types_json <> OLD.event_types_json
  OR NEW.after_source_sequence <> OLD.after_source_sequence
  OR NEW.after_cursor <> OLD.after_cursor
  OR NEW.deadline_at <> OLD.deadline_at
  OR NEW.idempotency_namespace <> OLD.idempotency_namespace
  OR NEW.idempotency_key_digest <> OLD.idempotency_key_digest
  OR NEW.request_digest <> OLD.request_digest
  OR NEW.created_at <> OLD.created_at
  OR NEW.schema_version <> OLD.schema_version
BEGIN
    SELECT RAISE(ABORT, 'event wait handle metadata is immutable');
END;

CREATE TRIGGER trg_orchestration_event_wait_handles_cas
BEFORE UPDATE ON orchestration_event_wait_handles
WHEN NEW.revision <> OLD.revision + 1
  OR OLD.status <> 'pending'
  OR NEW.status NOT IN ('satisfied', 'timed_out', 'cancelled', 'revoked', 'expired')
BEGIN
    SELECT RAISE(ABORT, 'event wait lifecycle requires pending CAS');
END;

CREATE TRIGGER trg_orchestration_event_wait_handles_no_delete
BEFORE DELETE ON orchestration_event_wait_handles
BEGIN
    SELECT RAISE(ABORT, 'event wait handles are immutable history');
END;

-- Independent foreign keys prove row existence only. These relation guards
-- additionally bind each incarnation to the exact reservation and agent so a
-- malformed direct SQL writer cannot splice fences from different identities.
CREATE TRIGGER trg_orchestration_access_grants_source_identity_relation
BEFORE INSERT ON orchestration_access_grants
WHEN NOT EXISTS (
    SELECT 1
    FROM orchestration_execution_incarnations i
    WHERE i.incarnation_id = NEW.source_incarnation_id
      AND i.reservation_id = NEW.source_reservation_id
      AND i.agent_id = NEW.source_agent_id
      AND i.incarnation_generation = NEW.source_incarnation_generation
)
BEGIN
    SELECT RAISE(ABORT, 'access grant source incarnation relation mismatch');
END;

CREATE TRIGGER trg_orchestration_access_grants_target_identity_relation
BEFORE INSERT ON orchestration_access_grants
WHEN NOT EXISTS (
    SELECT 1
    FROM orchestration_execution_incarnations i
    WHERE i.incarnation_id = NEW.target_incarnation_id
      AND i.reservation_id = NEW.target_reservation_id
      AND i.agent_id = NEW.target_agent_id
      AND i.incarnation_generation = NEW.target_incarnation_generation
)
BEGIN
    SELECT RAISE(ABORT, 'access grant target incarnation relation mismatch');
END;

CREATE TRIGGER trg_orchestration_event_wait_handles_target_identity_relation
BEFORE INSERT ON orchestration_event_wait_handles
WHEN NOT EXISTS (
    SELECT 1
    FROM orchestration_execution_incarnations i
    WHERE i.incarnation_id = NEW.target_incarnation_id
      AND i.reservation_id = NEW.target_reservation_id
      AND i.agent_id = NEW.target_agent_id
      AND i.incarnation_generation = NEW.target_incarnation_generation
)
BEGIN
    SELECT RAISE(ABORT, 'event wait target incarnation relation mismatch');
END;
