-- Operator source generations prove provenance only, never authorization or custody.
CREATE TABLE orchestration_operator_sources (
    source_id TEXT NOT NULL CHECK(length(source_id) BETWEEN 1 AND 128 AND trim(source_id, char(9)||char(10)||char(11)||char(12)||char(13)||' ') = source_id AND instr(source_id, char(0)) = 0),
    source_fence_ref TEXT NOT NULL CHECK(length(source_fence_ref) BETWEEN 1 AND 128 AND trim(source_fence_ref, char(9)||char(10)||char(11)||char(12)||char(13)||' ') = source_fence_ref AND instr(source_fence_ref, char(0)) = 0),
    principal_ref TEXT NOT NULL CHECK(length(principal_ref) BETWEEN 1 AND 512 AND trim(principal_ref, char(9)||char(10)||char(11)||char(12)||char(13)||' ') = principal_ref AND instr(principal_ref, char(0)) = 0),
    scope_ref TEXT NOT NULL CHECK(length(scope_ref) BETWEEN 1 AND 512 AND trim(scope_ref, char(9)||char(10)||char(11)||char(12)||char(13)||' ') = scope_ref AND instr(scope_ref, char(0)) = 0),
    auth_context TEXT NOT NULL CHECK(length(auth_context) BETWEEN 1 AND 512 AND trim(auth_context, char(9)||char(10)||char(11)||char(12)||char(13)||' ') = auth_context AND instr(auth_context, char(0)) = 0),
    generation INTEGER NOT NULL CHECK(typeof(generation) = 'integer' AND generation > 0),
    state TEXT NOT NULL CHECK(state IN ('active', 'revoked')),
    created_at TEXT NOT NULL CHECK(length(created_at) = 30 AND created_at GLOB '????-??-??T??:??:??.?????????Z' AND substr(created_at,21,9) NOT GLOB '*[^0-9]*' AND julianday(substr(created_at,1,19)||'Z') IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', substr(created_at,1,19)||'Z', '+0 seconds') = substr(created_at,1,19)),
    revoked_at TEXT CHECK(revoked_at IS NULL OR (length(revoked_at) = 30 AND revoked_at GLOB '????-??-??T??:??:??.?????????Z' AND substr(revoked_at,21,9) NOT GLOB '*[^0-9]*' AND julianday(substr(revoked_at,1,19)||'Z') IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', substr(revoked_at,1,19)||'Z', '+0 seconds') = substr(revoked_at,1,19) AND revoked_at >= created_at)),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    PRIMARY KEY(source_id),
    UNIQUE(source_fence_ref),
    UNIQUE(generation),
    UNIQUE(principal_ref, scope_ref, auth_context),
    CHECK((state = 'active' AND revoked_at IS NULL) OR (state = 'revoked' AND revoked_at IS NOT NULL))
) STRICT;
CREATE UNIQUE INDEX orchestration_operator_sources_active ON orchestration_operator_sources((1)) WHERE state = 'active';
CREATE TRIGGER orchestration_operator_sources_insert BEFORE INSERT ON orchestration_operator_sources
BEGIN
    SELECT CASE WHEN NEW.state != 'active' OR NEW.revoked_at IS NOT NULL OR NEW.generation != COALESCE((SELECT MAX(generation) FROM orchestration_operator_sources),0)+1
        OR EXISTS(SELECT 1 FROM orchestration_operator_sources WHERE state='active')
        THEN RAISE(ABORT,'invalid operator source generation') END;
END;
CREATE TRIGGER orchestration_operator_sources_update BEFORE UPDATE ON orchestration_operator_sources
BEGIN
    SELECT CASE WHEN OLD.state != 'active' OR NEW.state != 'revoked' OR NEW.revoked_at IS NULL
        OR NEW.source_id IS NOT OLD.source_id
        OR NEW.source_fence_ref IS NOT OLD.source_fence_ref
        OR NEW.principal_ref IS NOT OLD.principal_ref
        OR NEW.scope_ref IS NOT OLD.scope_ref
        OR NEW.auth_context IS NOT OLD.auth_context
        OR NEW.generation IS NOT OLD.generation
        OR NEW.created_at IS NOT OLD.created_at
        OR NEW.schema_version IS NOT OLD.schema_version
        THEN RAISE(ABORT,'immutable operator source') END;
END;
CREATE TRIGGER orchestration_operator_sources_delete BEFORE DELETE ON orchestration_operator_sources
BEGIN SELECT RAISE(ABORT,'operator source history is append-only'); END;
