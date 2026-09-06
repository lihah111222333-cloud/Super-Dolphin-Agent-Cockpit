-- C1b operator CONTENT metadata projection. It does not contain plaintext.
CREATE TABLE orchestration_operator_content_objects (
    object_id TEXT NOT NULL CHECK(length(object_id) BETWEEN 1 AND 128 AND trim(object_id)=object_id AND instr(object_id,char(0))=0),
    object_version INTEGER NOT NULL CHECK(object_version=1),
    reservation_id TEXT NOT NULL REFERENCES orchestration_operator_reservations(reservation_id) ON DELETE RESTRICT,
    source_id TEXT NOT NULL REFERENCES orchestration_operator_sources(source_id) ON DELETE RESTRICT,
    source_generation INTEGER NOT NULL CHECK(source_generation>0),
    aead_algorithm TEXT NOT NULL CHECK(aead_algorithm='aes-256-gcm'),
    key_wrap_profile TEXT NOT NULL CHECK(length(key_wrap_profile)>0 AND length(key_wrap_profile)<=128 AND trim(key_wrap_profile)=key_wrap_profile),
    key_handle_ref TEXT NOT NULL CHECK(length(key_handle_ref)>0 AND length(key_handle_ref)<=512 AND trim(key_handle_ref)=key_handle_ref),
    key_receipt_ref TEXT NOT NULL CHECK(length(key_receipt_ref)>0 AND length(key_receipt_ref)<=512 AND trim(key_receipt_ref)=key_receipt_ref),
    key_receipt_digest TEXT NOT NULL CHECK(length(key_receipt_digest)=64 AND key_receipt_digest NOT GLOB '*[^0-9a-f]*'),
    nonce_hex TEXT NOT NULL CHECK(length(nonce_hex)=24 AND nonce_hex NOT GLOB '*[^0-9a-f]*'),
    aad_profile TEXT NOT NULL CHECK(aad_profile='orch.content.operator.single.aad/v1'),
    aad_digest TEXT NOT NULL CHECK(length(aad_digest)=64 AND aad_digest NOT GLOB '*[^0-9a-f]*'),
    media_type TEXT NOT NULL CHECK(length(media_type)>0 AND length(media_type)<=255 AND trim(media_type)=media_type),
    size_bytes INTEGER NOT NULL CHECK(size_bytes BETWEEN 1 AND 65536),
    ciphertext_sha256 TEXT NOT NULL CHECK(length(ciphertext_sha256)=64 AND ciphertext_sha256 NOT GLOB '*[^0-9a-f]*'),
    content_state TEXT NOT NULL CHECK(content_state IN ('prepared','active')),
    key_state TEXT NOT NULL CHECK(key_state='active'),
    ref_version INTEGER NOT NULL CHECK(ref_version IN (0,1)),
    revision INTEGER NOT NULL CHECK((content_state='prepared' AND revision=1) OR (content_state='active' AND revision=2)),
    object_receipt_ref TEXT NOT NULL CHECK(length(object_receipt_ref)>0 AND length(object_receipt_ref)<=512 AND trim(object_receipt_ref)=object_receipt_ref),
    created_at TEXT NOT NULL CHECK(length(created_at)=30 AND created_at GLOB '????-??-??T??:??:??.?????????Z' AND julianday(substr(created_at,1,19)||'Z') IS NOT NULL),
    updated_at TEXT NOT NULL CHECK(length(updated_at)=30 AND updated_at GLOB '????-??-??T??:??:??.?????????Z' AND julianday(substr(updated_at,1,19)||'Z') IS NOT NULL),
    PRIMARY KEY(object_id,object_version), UNIQUE(reservation_id,object_id,object_version)
) STRICT;

CREATE TABLE orchestration_operator_content_refs (
    reference_id TEXT PRIMARY KEY NOT NULL CHECK(length(reference_id)>0 AND length(reference_id)<=128 AND trim(reference_id)=reference_id AND instr(reference_id,char(0))=0),
    canonical_ref TEXT NOT NULL UNIQUE CHECK(length(canonical_ref)>0 AND length(canonical_ref)<=512 AND trim(canonical_ref)=canonical_ref AND instr(canonical_ref,char(0))=0),
    object_id TEXT NOT NULL,
    object_version INTEGER NOT NULL CHECK(object_version=1),
    reservation_id TEXT NOT NULL REFERENCES orchestration_operator_reservations(reservation_id) ON DELETE RESTRICT,
    source_id TEXT NOT NULL REFERENCES orchestration_operator_sources(source_id) ON DELETE RESTRICT,
    source_generation INTEGER NOT NULL CHECK(source_generation>0),
    ref_version INTEGER NOT NULL CHECK(ref_version=1),
    caller_principal_ref TEXT NOT NULL CHECK(length(caller_principal_ref)>0 AND length(caller_principal_ref)<=512 AND trim(caller_principal_ref)=caller_principal_ref),
    scope_ref TEXT NOT NULL CHECK(length(scope_ref)>0 AND length(scope_ref)<=512 AND trim(scope_ref)=scope_ref),
    proof_digest TEXT NOT NULL CHECK(length(proof_digest)=64 AND proof_digest NOT GLOB '*[^0-9a-f]*'),
    valid_until TEXT NOT NULL CHECK(length(valid_until)=30 AND valid_until GLOB '????-??-??T??:??:??.?????????Z' AND julianday(substr(valid_until,1,19)||'Z') IS NOT NULL),
    reference_state TEXT NOT NULL CHECK(reference_state='active'),
    created_at TEXT NOT NULL CHECK(length(created_at)=30 AND created_at GLOB '????-??-??T??:??:??.?????????Z' AND julianday(substr(created_at,1,19)||'Z') IS NOT NULL),
    FOREIGN KEY(object_id,object_version) REFERENCES orchestration_operator_content_objects(object_id,object_version) ON DELETE RESTRICT
) STRICT;

CREATE INDEX orchestration_operator_content_objects_source_idx ON orchestration_operator_content_objects(source_id,source_generation);
CREATE INDEX orchestration_operator_content_refs_source_idx ON orchestration_operator_content_refs(source_id,source_generation);

CREATE TRIGGER orchestration_operator_content_objects_current
BEFORE INSERT ON orchestration_operator_content_objects BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM orchestration_operator_reservations r JOIN orchestration_operator_sources s ON s.source_id=r.source_id
        WHERE r.reservation_id=NEW.reservation_id AND r.source_id=NEW.source_id AND r.generation=NEW.source_generation
          AND s.state='active' AND s.generation=(SELECT MAX(generation) FROM orchestration_operator_sources)
    ) THEN RAISE(ABORT,'operator object source is not current') END;
END;
CREATE TRIGGER orchestration_operator_content_objects_promote
BEFORE UPDATE ON orchestration_operator_content_objects
WHEN NOT (OLD.content_state='prepared' AND NEW.content_state='active' AND NEW.revision=2 AND NEW.ref_version=1
  AND NEW.object_id=OLD.object_id AND NEW.object_version=OLD.object_version AND NEW.reservation_id=OLD.reservation_id
  AND NEW.source_id=OLD.source_id AND NEW.source_generation=OLD.source_generation AND NEW.key_receipt_digest=OLD.key_receipt_digest
  AND NEW.ciphertext_sha256=OLD.ciphertext_sha256 AND NEW.created_at=OLD.created_at)
BEGIN SELECT RAISE(ABORT,'operator object metadata is immutable'); END;
CREATE TRIGGER orchestration_operator_content_objects_no_delete BEFORE DELETE ON orchestration_operator_content_objects BEGIN SELECT RAISE(ABORT,'operator object is append-only'); END;
CREATE TRIGGER orchestration_operator_content_refs_current
BEFORE INSERT ON orchestration_operator_content_refs BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM orchestration_operator_content_objects o JOIN orchestration_operator_sources s ON s.source_id=o.source_id
        WHERE o.object_id=NEW.object_id AND o.object_version=NEW.object_version AND o.reservation_id=NEW.reservation_id
          AND o.source_id=NEW.source_id AND o.source_generation=NEW.source_generation AND o.content_state='active'
          AND s.state='active' AND s.generation=(SELECT MAX(generation) FROM orchestration_operator_sources)
    ) THEN RAISE(ABORT,'operator ref object/source is not current') END;
END;
CREATE TRIGGER orchestration_operator_content_refs_no_update BEFORE UPDATE ON orchestration_operator_content_refs BEGIN SELECT RAISE(ABORT,'operator ref is immutable'); END;
CREATE TRIGGER orchestration_operator_content_refs_no_delete BEFORE DELETE ON orchestration_operator_content_refs BEGIN SELECT RAISE(ABORT,'operator ref is append-only'); END;
