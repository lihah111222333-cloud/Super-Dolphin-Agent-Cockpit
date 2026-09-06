-- C1 source union: 132 remains the task-field SSOT; this table stores only the
-- immutable source-kind binding and its canonical task digest/operator selector.
CREATE TABLE orchestration_content_source_union (
    ingest_reservation_id TEXT PRIMARY KEY NOT NULL
        REFERENCES orchestration_content_ingest_reservations(ingest_reservation_id) ON DELETE RESTRICT,
    source_kind TEXT NOT NULL CHECK(source_kind IN ('task', 'operator')),
    task_snapshot_digest TEXT CHECK(
        task_snapshot_digest IS NULL OR
        (length(task_snapshot_digest) = 64 AND task_snapshot_digest NOT GLOB '*[^0-9a-f]*')
    ),
    task_binding_version INTEGER CHECK(task_binding_version IS NULL OR task_binding_version > 0),
    operator_source_id TEXT CHECK(operator_source_id IS NULL OR
        (length(operator_source_id) BETWEEN 1 AND 128 AND trim(operator_source_id) = operator_source_id AND instr(operator_source_id, char(0)) = 0)),
    operator_source_fence_ref TEXT CHECK(operator_source_fence_ref IS NULL OR
        (length(operator_source_fence_ref) BETWEEN 1 AND 512 AND trim(operator_source_fence_ref) = operator_source_fence_ref AND instr(operator_source_fence_ref, char(0)) = 0)),
    operator_principal_ref TEXT CHECK(operator_principal_ref IS NULL OR
        (length(operator_principal_ref) BETWEEN 1 AND 512 AND trim(operator_principal_ref) = operator_principal_ref AND instr(operator_principal_ref, char(0)) = 0)),
    operator_scope_ref TEXT CHECK(operator_scope_ref IS NULL OR
        (length(operator_scope_ref) BETWEEN 1 AND 512 AND trim(operator_scope_ref) = operator_scope_ref AND instr(operator_scope_ref, char(0)) = 0)),
    operator_auth_context TEXT CHECK(operator_auth_context IS NULL OR
        (length(operator_auth_context) BETWEEN 1 AND 512 AND trim(operator_auth_context) = operator_auth_context AND instr(operator_auth_context, char(0)) = 0)),
    operator_generation INTEGER CHECK(operator_generation IS NULL OR operator_generation > 0),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    created_at TEXT NOT NULL CHECK(
        length(created_at) = 30 AND created_at GLOB '????-??-??T??:??:??.?????????Z'
        AND substr(created_at, 21, 9) NOT GLOB '*[^0-9]*'
        AND julianday(substr(created_at, 1, 19) || 'Z') IS NOT NULL
    ),
    CHECK(
        (source_kind = 'task'
         AND task_snapshot_digest IS NOT NULL AND task_binding_version IS NOT NULL
         AND operator_source_id IS NULL AND operator_source_fence_ref IS NULL
         AND operator_principal_ref IS NULL AND operator_scope_ref IS NULL
         AND operator_auth_context IS NULL AND operator_generation IS NULL)
        OR
        (source_kind = 'operator'
         AND task_snapshot_digest IS NULL AND task_binding_version IS NULL
         AND operator_source_id IS NOT NULL AND operator_source_fence_ref IS NOT NULL
         AND operator_principal_ref IS NOT NULL AND operator_scope_ref IS NOT NULL
         AND operator_auth_context IS NOT NULL AND operator_generation IS NOT NULL)
    )
) STRICT;

CREATE INDEX orchestration_content_source_union_operator_idx
    ON orchestration_content_source_union(operator_source_id, operator_generation);

CREATE TRIGGER orchestration_content_source_union_operator_current
BEFORE INSERT ON orchestration_content_source_union
WHEN NEW.source_kind = 'operator'
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM orchestration_operator_sources AS source
        WHERE source.source_id = NEW.operator_source_id
          AND source.source_fence_ref = NEW.operator_source_fence_ref
          AND source.principal_ref = NEW.operator_principal_ref
          AND source.scope_ref = NEW.operator_scope_ref
          AND source.auth_context = NEW.operator_auth_context
          AND source.generation = NEW.operator_generation
          AND source.state = 'active'
          AND source.generation = (SELECT MAX(generation) FROM orchestration_operator_sources)
    ) THEN RAISE(ABORT, 'operator source selector is not current') END;
END;

CREATE TRIGGER orchestration_content_source_union_no_update
BEFORE UPDATE ON orchestration_content_source_union
BEGIN SELECT RAISE(ABORT, 'content source union is immutable'); END;

CREATE TRIGGER orchestration_content_source_union_no_delete
BEFORE DELETE ON orchestration_content_source_union
BEGIN SELECT RAISE(ABORT, 'content source union is immutable'); END;
