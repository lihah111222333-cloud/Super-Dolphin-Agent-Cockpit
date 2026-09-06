-- Canonical dense event owner. Legacy direct-peer/terminal/DAG event tables
-- remain projections; this schema never stores prompt or provider credentials.
CREATE TABLE orchestration_event_source_states (
    source_id TEXT NOT NULL
        CHECK(length(trim(source_id)) > 0 AND length(source_id) <= 256),
    source_epoch TEXT NOT NULL
        CHECK(length(trim(source_epoch)) > 0 AND length(source_epoch) <= 128),
    lane TEXT NOT NULL CHECK(lane IN ('control', 'output', 'content')),
    next_sequence INTEGER NOT NULL CHECK(next_sequence >= 1),
    materialized_sequence INTEGER NOT NULL CHECK(materialized_sequence >= 0),
    dedup_floor INTEGER NOT NULL CHECK(dedup_floor >= 0),
    revision INTEGER NOT NULL CHECK(revision > 0),
    status TEXT NOT NULL CHECK(status IN ('active', 'gap', 'quarantined', 'closed')),
    gap_reason TEXT NOT NULL CHECK(length(gap_reason) <= 512),
    writer_epoch TEXT NOT NULL
        CHECK(length(trim(writer_epoch)) > 0 AND length(writer_epoch) <= 128),
    created_at TEXT NOT NULL
        CHECK(length(trim(created_at)) > 0)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL
        CHECK(length(trim(updated_at)) > 0)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY(source_id, source_epoch, lane),
    CHECK(materialized_sequence < next_sequence),
    CHECK(dedup_floor <= materialized_sequence),
    CHECK((status = 'gap' AND length(trim(gap_reason)) > 0) OR status <> 'gap')
) STRICT;

CREATE TABLE orchestration_event_ingress_receipts (
    source_id TEXT NOT NULL
        CHECK(length(trim(source_id)) > 0 AND length(source_id) <= 256),
    source_epoch TEXT NOT NULL
        CHECK(length(trim(source_epoch)) > 0 AND length(source_epoch) <= 128),
    lane TEXT NOT NULL CHECK(lane IN ('control', 'output', 'content')),
    source_sequence INTEGER NOT NULL CHECK(source_sequence >= 1),
    event_id TEXT NOT NULL
        CHECK(length(trim(event_id)) > 0 AND length(event_id) <= 128),
    envelope_hash TEXT NOT NULL CHECK(
        length(envelope_hash) = 64 AND envelope_hash NOT GLOB '*[^0-9a-f]*'
    ),
    status TEXT NOT NULL CHECK(status IN ('staged', 'materialized', 'quarantined', 'gap')),
    event_type TEXT NOT NULL
        CHECK(length(trim(event_type)) > 0 AND length(event_type) <= 128),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    root_agent_id TEXT NOT NULL
        CHECK(length(trim(root_agent_id)) > 0 AND length(root_agent_id) <= 128),
    subject_agent_id TEXT NOT NULL
        CHECK(length(trim(subject_agent_id)) > 0 AND length(subject_agent_id) <= 128),
    reservation_id TEXT NOT NULL
        ,
    incarnation_id TEXT
        REFERENCES orchestration_execution_incarnations(incarnation_id) ON DELETE RESTRICT,
    external_epoch TEXT NOT NULL CHECK(length(external_epoch) <= 128),
    incarnation_generation INTEGER NOT NULL CHECK(incarnation_generation >= 0),
    identity_digest TEXT NOT NULL CHECK(
        identity_digest = '' OR (length(identity_digest) = 64 AND identity_digest NOT GLOB '*[^0-9a-f]*')
    ),
    binding_version INTEGER NOT NULL CHECK(binding_version >= 0),
    binding_digest TEXT NOT NULL CHECK(
        binding_digest = '' OR (length(binding_digest) = 64 AND binding_digest NOT GLOB '*[^0-9a-f]*')
    ),
    source_fence_ref TEXT NOT NULL
        CHECK(length(trim(source_fence_ref)) > 0 AND length(source_fence_ref) <= 512),
    provenance TEXT NOT NULL
        CHECK(length(trim(provenance)) > 0 AND length(provenance) <= 256),
    trust_class TEXT NOT NULL
        CHECK(length(trim(trust_class)) > 0 AND length(trust_class) <= 64),
    content_role TEXT NOT NULL CHECK(content_role IN ('none', 'prompt', 'message', 'tool', 'output', 'event')),
    content_ref TEXT NOT NULL CHECK(length(content_ref) <= 512),
    content_size INTEGER NOT NULL CHECK(content_size >= 0),
    media_type TEXT NOT NULL CHECK(length(media_type) <= 128),
    provider_at TEXT,
    observed_at TEXT NOT NULL
        CHECK(length(trim(observed_at)) > 0 AND length(observed_at) <= 64),
    writer_epoch TEXT NOT NULL
        CHECK(length(trim(writer_epoch)) > 0 AND length(writer_epoch) <= 128),
    canonical_cursor TEXT NOT NULL
        CHECK(length(trim(canonical_cursor)) > 0 AND length(canonical_cursor) <= 512),
    error_ref TEXT NOT NULL CHECK(length(error_ref) <= 512),
    PRIMARY KEY(source_id, source_epoch, lane, source_sequence),
    UNIQUE(event_id),
    FOREIGN KEY(reservation_id, root_agent_id)
        REFERENCES orchestration_identity_reservations(reservation_id, agent_id)
        ON DELETE RESTRICT,
    CHECK(
        (incarnation_id IS NULL AND external_epoch = '' AND incarnation_generation = 0 AND identity_digest = '' AND binding_version = 0 AND binding_digest = '')
        OR
        (incarnation_id IS NOT NULL AND length(trim(external_epoch)) > 0 AND incarnation_generation > 0 AND identity_digest <> '' AND binding_version > 0 AND binding_digest <> '')
    ),
    CHECK(
        (content_role = 'none' AND content_ref = '' AND content_size = 0 AND media_type = '')
        OR (content_role <> 'none' AND length(trim(content_ref)) > 0 AND length(trim(media_type)) > 0)
    )
) STRICT;

CREATE TABLE orchestration_agent_events (
    event_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(trim(event_id)) > 0 AND length(event_id) <= 128),
    source_id TEXT NOT NULL
        CHECK(length(trim(source_id)) > 0 AND length(source_id) <= 256),
    source_epoch TEXT NOT NULL
        CHECK(length(trim(source_epoch)) > 0 AND length(source_epoch) <= 128),
    lane TEXT NOT NULL CHECK(lane IN ('control', 'output', 'content')),
    source_sequence INTEGER NOT NULL CHECK(source_sequence >= 1),
    canonical_cursor TEXT NOT NULL
        CHECK(length(trim(canonical_cursor)) > 0 AND length(canonical_cursor) <= 512),
    envelope_hash TEXT NOT NULL CHECK(
        length(envelope_hash) = 64 AND envelope_hash NOT GLOB '*[^0-9a-f]*'
    ),
    status TEXT NOT NULL CHECK(status = 'materialized'),
    event_type TEXT NOT NULL
        CHECK(length(trim(event_type)) > 0 AND length(event_type) <= 128),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    root_agent_id TEXT NOT NULL
        CHECK(length(trim(root_agent_id)) > 0 AND length(root_agent_id) <= 128),
    subject_agent_id TEXT NOT NULL
        CHECK(length(trim(subject_agent_id)) > 0 AND length(subject_agent_id) <= 128),
    reservation_id TEXT NOT NULL,
    incarnation_id TEXT
        REFERENCES orchestration_execution_incarnations(incarnation_id) ON DELETE RESTRICT,
    external_epoch TEXT NOT NULL CHECK(length(external_epoch) <= 128),
    incarnation_generation INTEGER NOT NULL CHECK(incarnation_generation >= 0),
    identity_digest TEXT NOT NULL CHECK(
        identity_digest = '' OR (length(identity_digest) = 64 AND identity_digest NOT GLOB '*[^0-9a-f]*')
    ),
    binding_version INTEGER NOT NULL CHECK(binding_version >= 0),
    binding_digest TEXT NOT NULL CHECK(
        binding_digest = '' OR (length(binding_digest) = 64 AND binding_digest NOT GLOB '*[^0-9a-f]*')
    ),
    source_fence_ref TEXT NOT NULL
        CHECK(length(trim(source_fence_ref)) > 0 AND length(source_fence_ref) <= 512),
    provenance TEXT NOT NULL
        CHECK(length(trim(provenance)) > 0 AND length(provenance) <= 256),
    trust_class TEXT NOT NULL
        CHECK(length(trim(trust_class)) > 0 AND length(trust_class) <= 64),
    content_role TEXT NOT NULL CHECK(content_role IN ('none', 'prompt', 'message', 'tool', 'output', 'event')),
    content_ref TEXT NOT NULL CHECK(length(content_ref) <= 512),
    content_size INTEGER NOT NULL CHECK(content_size >= 0),
    media_type TEXT NOT NULL CHECK(length(media_type) <= 128),
    provider_at TEXT,
    observed_at TEXT NOT NULL
        CHECK(length(trim(observed_at)) > 0 AND length(observed_at) <= 64),
    writer_epoch TEXT NOT NULL
        CHECK(length(trim(writer_epoch)) > 0 AND length(writer_epoch) <= 128),
    UNIQUE(source_id, source_epoch, lane, source_sequence),
    FOREIGN KEY(source_id, source_epoch, lane, source_sequence)
        REFERENCES orchestration_event_ingress_receipts(source_id, source_epoch, lane, source_sequence)
        ON DELETE RESTRICT,
    CHECK(
        (incarnation_id IS NULL AND external_epoch = '' AND incarnation_generation = 0 AND identity_digest = '' AND binding_version = 0 AND binding_digest = '')
        OR
        (incarnation_id IS NOT NULL AND length(trim(external_epoch)) > 0 AND incarnation_generation > 0 AND identity_digest <> '' AND binding_version > 0 AND binding_digest <> '')
    ),
    CHECK(
        (content_role = 'none' AND content_ref = '' AND content_size = 0 AND media_type = '')
        OR (content_role <> 'none' AND length(trim(content_ref)) > 0 AND length(trim(media_type)) > 0)
    ),
    FOREIGN KEY(reservation_id, root_agent_id)
        REFERENCES orchestration_identity_reservations(reservation_id, agent_id)
        ON DELETE RESTRICT
) STRICT;

CREATE TABLE orchestration_event_tombstones (
    source_id TEXT NOT NULL
        CHECK(length(trim(source_id)) > 0 AND length(source_id) <= 256),
    source_epoch TEXT NOT NULL
        CHECK(length(trim(source_epoch)) > 0 AND length(source_epoch) <= 128),
    lane TEXT NOT NULL CHECK(lane IN ('control', 'output', 'content')),
    source_sequence INTEGER NOT NULL CHECK(source_sequence >= 1),
    envelope_hash TEXT NOT NULL CHECK(
        length(envelope_hash) = 64 AND envelope_hash NOT GLOB '*[^0-9a-f]*'
    ),
    event_id TEXT NOT NULL
        CHECK(length(trim(event_id)) > 0 AND length(event_id) <= 128),
    canonical_cursor TEXT NOT NULL
        CHECK(length(trim(canonical_cursor)) > 0 AND length(canonical_cursor) <= 512),
    dedup_floor INTEGER NOT NULL CHECK(dedup_floor >= 0),
    status TEXT NOT NULL CHECK(status IN ('materialized', 'expired', 'quarantined')),
    created_at TEXT NOT NULL
        CHECK(length(trim(created_at)) > 0)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY(source_id, source_epoch, lane, source_sequence)
) STRICT;

CREATE TABLE orchestration_event_materialization_cursors (
    projection_id TEXT NOT NULL
        CHECK(length(trim(projection_id)) > 0 AND length(projection_id) <= 128),
    source_id TEXT NOT NULL
        CHECK(length(trim(source_id)) > 0 AND length(source_id) <= 256),
    source_epoch TEXT NOT NULL
        CHECK(length(trim(source_epoch)) > 0 AND length(source_epoch) <= 128),
    lane TEXT NOT NULL CHECK(lane IN ('control', 'output', 'content')),
    materialized_sequence INTEGER NOT NULL CHECK(materialized_sequence >= 0),
    materialized_hash TEXT NOT NULL CHECK(
        materialized_hash = '' OR (length(materialized_hash) = 64 AND materialized_hash NOT GLOB '*[^0-9a-f]*')
    ),
    revision INTEGER NOT NULL CHECK(revision > 0),
    status TEXT NOT NULL CHECK(status IN ('active', 'gap', 'quarantined', 'closed')),
    updated_at TEXT NOT NULL
        CHECK(length(trim(updated_at)) > 0)
        DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY(projection_id, source_id, source_epoch, lane),
    FOREIGN KEY(source_id, source_epoch, lane)
        REFERENCES orchestration_event_source_states(source_id, source_epoch, lane)
        ON DELETE RESTRICT,
    CHECK((materialized_sequence = 0 AND materialized_hash = '') OR materialized_hash <> '')
) STRICT;

CREATE INDEX idx_orchestration_event_receipts_status
    ON orchestration_event_ingress_receipts(source_id, source_epoch, lane, status, source_sequence);
CREATE INDEX idx_orchestration_agent_events_subject
    ON orchestration_agent_events(subject_agent_id, source_epoch, lane, source_sequence);
CREATE INDEX idx_orchestration_event_tombstones_floor
    ON orchestration_event_tombstones(source_id, source_epoch, lane, dedup_floor);

CREATE TRIGGER trg_orchestration_event_source_states_no_delete
BEFORE DELETE ON orchestration_event_source_states
BEGIN
    SELECT RAISE(ABORT, 'event source state is immutable history');
END;

CREATE TRIGGER trg_orchestration_event_source_states_revision
BEFORE UPDATE ON orchestration_event_source_states
WHEN NEW.revision <> OLD.revision + 1
BEGIN
    SELECT RAISE(ABORT, 'event source state revision must advance contiguously');
END;

CREATE TRIGGER trg_orchestration_event_source_states_dedup_floor
BEFORE UPDATE ON orchestration_event_source_states
WHEN NEW.dedup_floor < OLD.dedup_floor OR NEW.materialized_sequence < OLD.materialized_sequence OR NEW.next_sequence < OLD.next_sequence
BEGIN
    SELECT RAISE(ABORT, 'event source watermarks cannot move backwards');
END;

CREATE TRIGGER trg_orchestration_event_ingress_receipts_immutable
BEFORE UPDATE ON orchestration_event_ingress_receipts
WHEN NEW.source_id <> OLD.source_id OR NEW.source_epoch <> OLD.source_epoch OR NEW.lane <> OLD.lane
  OR NEW.source_sequence <> OLD.source_sequence OR NEW.event_id <> OLD.event_id OR NEW.envelope_hash <> OLD.envelope_hash
  OR NEW.event_type <> OLD.event_type OR NEW.schema_version <> OLD.schema_version OR NEW.reservation_id <> OLD.reservation_id
  OR NEW.incarnation_id IS NOT OLD.incarnation_id OR NEW.external_epoch <> OLD.external_epoch
  OR NEW.incarnation_generation <> OLD.incarnation_generation OR NEW.identity_digest <> OLD.identity_digest
  OR NEW.binding_version <> OLD.binding_version OR NEW.binding_digest <> OLD.binding_digest
  OR NEW.source_fence_ref <> OLD.source_fence_ref OR NEW.provenance <> OLD.provenance OR NEW.trust_class <> OLD.trust_class
  OR NEW.content_role <> OLD.content_role OR NEW.content_ref <> OLD.content_ref OR NEW.content_size <> OLD.content_size
  OR NEW.media_type <> OLD.media_type OR NEW.provider_at IS NOT OLD.provider_at OR NEW.observed_at <> OLD.observed_at
  OR NEW.writer_epoch <> OLD.writer_epoch OR NEW.canonical_cursor <> OLD.canonical_cursor
BEGIN
    SELECT RAISE(ABORT, 'event ingress receipt identity is immutable');
END;

CREATE TRIGGER trg_orchestration_event_ingress_receipts_no_delete
BEFORE DELETE ON orchestration_event_ingress_receipts
BEGIN
    SELECT RAISE(ABORT, 'event ingress receipts are immutable');
END;

CREATE TRIGGER trg_orchestration_agent_events_no_update
BEFORE UPDATE ON orchestration_agent_events
BEGIN
    SELECT RAISE(ABORT, 'canonical agent events are immutable');
END;

CREATE TRIGGER trg_orchestration_agent_events_no_delete
BEFORE DELETE ON orchestration_agent_events
BEGIN
    SELECT RAISE(ABORT, 'canonical agent events are immutable');
END;

CREATE TRIGGER trg_orchestration_event_tombstones_no_update
BEFORE UPDATE ON orchestration_event_tombstones
BEGIN
    SELECT RAISE(ABORT, 'event tombstones are immutable');
END;

CREATE TRIGGER trg_orchestration_event_tombstones_no_delete
BEFORE DELETE ON orchestration_event_tombstones
BEGIN
    SELECT RAISE(ABORT, 'event tombstones are immutable');
END;

CREATE TRIGGER trg_orchestration_event_materialization_cursors_no_delete
BEFORE DELETE ON orchestration_event_materialization_cursors
BEGIN
    SELECT RAISE(ABORT, 'event materialization cursor is immutable history');
END;

CREATE TRIGGER trg_orchestration_event_materialization_cursors_revision
BEFORE UPDATE ON orchestration_event_materialization_cursors
WHEN NEW.revision <> OLD.revision + 1 OR NEW.materialized_sequence < OLD.materialized_sequence
BEGIN
    SELECT RAISE(ABORT, 'event materialization cursor revision/watermark is invalid');
END;
