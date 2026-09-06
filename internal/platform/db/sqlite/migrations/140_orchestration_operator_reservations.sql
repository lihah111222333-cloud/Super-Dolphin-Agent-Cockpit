-- C1a operator reservation metadata. Independent namespace; no task/agent identity.
CREATE TABLE orchestration_operator_reservations (
 reservation_id TEXT PRIMARY KEY NOT NULL CHECK(length(reservation_id) BETWEEN 1 AND 128 AND trim(reservation_id)=reservation_id AND instr(reservation_id,char(0))=0),
 source_kind TEXT NOT NULL CHECK(source_kind='operator'),
 source_id TEXT NOT NULL CHECK(length(source_id) BETWEEN 1 AND 128 AND trim(source_id)=source_id AND instr(source_id,char(0))=0),
 source_fence_ref TEXT NOT NULL CHECK(length(source_fence_ref) BETWEEN 1 AND 128 AND trim(source_fence_ref)=source_fence_ref AND instr(source_fence_ref,char(0))=0),
 principal_ref TEXT NOT NULL CHECK(length(principal_ref) BETWEEN 1 AND 512 AND trim(principal_ref)=principal_ref AND instr(principal_ref,char(0))=0),
 scope_ref TEXT NOT NULL CHECK(length(scope_ref) BETWEEN 1 AND 512 AND trim(scope_ref)=scope_ref AND instr(scope_ref,char(0))=0),
 auth_context TEXT NOT NULL CHECK(length(auth_context) BETWEEN 1 AND 512 AND trim(auth_context)=auth_context AND instr(auth_context,char(0))=0),
 generation INTEGER NOT NULL CHECK(typeof(generation)='integer' AND generation>0),
 workspace_ref TEXT NOT NULL CHECK(length(workspace_ref) BETWEEN 1 AND 512 AND trim(workspace_ref)=workspace_ref),
 caller_principal_ref TEXT NOT NULL CHECK(length(caller_principal_ref) BETWEEN 1 AND 512 AND trim(caller_principal_ref)=caller_principal_ref),
 idempotency_namespace TEXT NOT NULL CHECK(length(idempotency_namespace) BETWEEN 1 AND 128 AND trim(idempotency_namespace)=idempotency_namespace),
 idempotency_key_digest TEXT NOT NULL CHECK(length(idempotency_key_digest)=64 AND idempotency_key_digest NOT GLOB '*[^0-9a-f]*'),
 content_intent TEXT NOT NULL CHECK(length(content_intent) BETWEEN 1 AND 128 AND trim(content_intent)=content_intent),
 content_proof TEXT NOT NULL CHECK(length(content_proof) BETWEEN 1 AND 128 AND trim(content_proof)=content_proof),
 content_candidate TEXT NOT NULL CHECK(length(content_candidate) BETWEEN 1 AND 128 AND trim(content_candidate)=content_candidate),
 content_digest_key TEXT NOT NULL CHECK(length(content_digest_key)=64 AND content_digest_key NOT GLOB '*[^0-9a-f]*'),
 size_bytes INTEGER NOT NULL CHECK(typeof(size_bytes)='integer' AND size_bytes>0),
 media_type TEXT NOT NULL CHECK(length(media_type) BETWEEN 1 AND 255 AND trim(media_type)=media_type),
 profile_ref TEXT NOT NULL CHECK(length(profile_ref) BETWEEN 1 AND 128 AND trim(profile_ref)=profile_ref),
 quota_ref TEXT NOT NULL CHECK(length(quota_ref) BETWEEN 1 AND 128 AND trim(quota_ref)=quota_ref),
 retention_seconds INTEGER NOT NULL CHECK(typeof(retention_seconds)='integer' AND retention_seconds>=0),
 deadline TEXT NOT NULL CHECK(length(deadline)=30 AND deadline GLOB '????-??-??T??:??:??.?????????Z' AND julianday(substr(deadline,1,19)||'Z') IS NOT NULL),
 state TEXT NOT NULL CHECK(state IN ('reserved','sealed','promoted')),
 revision INTEGER NOT NULL CHECK((state='reserved' AND revision=1) OR (state='sealed' AND revision=2) OR (state='promoted' AND revision=3)),
 schema_version INTEGER NOT NULL CHECK(schema_version=1),
 created_at TEXT NOT NULL CHECK(length(created_at)=30 AND created_at GLOB '????-??-??T??:??:??.?????????Z' AND julianday(substr(created_at,1,19)||'Z') IS NOT NULL),
 updated_at TEXT NOT NULL CHECK(length(updated_at)=30 AND updated_at GLOB '????-??-??T??:??:??.?????????Z' AND julianday(substr(updated_at,1,19)||'Z') IS NOT NULL),
 UNIQUE(idempotency_namespace,idempotency_key_digest), UNIQUE(reservation_id,generation),
 FOREIGN KEY(source_id) REFERENCES orchestration_operator_sources(source_id) ON DELETE RESTRICT
) STRICT;
CREATE TRIGGER orchestration_operator_reservations_source_guard BEFORE INSERT ON orchestration_operator_reservations BEGIN
 SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM orchestration_operator_sources s WHERE s.source_id=NEW.source_id AND s.source_fence_ref=NEW.source_fence_ref AND s.principal_ref=NEW.principal_ref AND s.scope_ref=NEW.scope_ref AND s.auth_context=NEW.auth_context AND s.generation=NEW.generation AND s.state='active' AND s.generation=(SELECT MAX(generation) FROM orchestration_operator_sources)) THEN RAISE(ABORT,'operator source selector is not current') END;
END;
CREATE TRIGGER orchestration_operator_reservations_no_update BEFORE UPDATE ON orchestration_operator_reservations BEGIN SELECT RAISE(ABORT,'operator reservation metadata is immutable'); END;
CREATE TRIGGER orchestration_operator_reservations_no_delete BEFORE DELETE ON orchestration_operator_reservations BEGIN SELECT RAISE(ABORT,'operator reservation metadata is append-only'); END;
