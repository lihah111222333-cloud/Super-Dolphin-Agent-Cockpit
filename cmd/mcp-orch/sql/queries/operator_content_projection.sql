-- name: CreatePreparedObject :one
INSERT INTO orchestration_operator_content_objects (
 object_id, object_version, reservation_id, source_id, source_generation,
 aead_algorithm, key_wrap_profile, key_handle_ref, key_receipt_ref, key_receipt_digest,
 nonce_hex, aad_profile, aad_digest, media_type, size_bytes, ciphertext_sha256,
 content_state, key_state, ref_version, revision, object_receipt_ref, created_at, updated_at
) VALUES (sqlc.arg(object_id), 1, sqlc.arg(reservation_id), sqlc.arg(source_id), sqlc.arg(source_generation),
 sqlc.arg(aead_algorithm), sqlc.arg(key_wrap_profile), sqlc.arg(key_handle_ref), sqlc.arg(key_receipt_ref), sqlc.arg(key_receipt_digest),
 sqlc.arg(nonce_hex), sqlc.arg(aad_profile), sqlc.arg(aad_digest), sqlc.arg(media_type), sqlc.arg(size_bytes), sqlc.arg(ciphertext_sha256),
 'prepared', 'active', 0, 1, sqlc.arg(object_receipt_ref), sqlc.arg(created_at), sqlc.arg(updated_at))
RETURNING object_id, object_version, reservation_id, source_id, source_generation, aead_algorithm, key_wrap_profile, key_handle_ref, key_receipt_ref, key_receipt_digest, nonce_hex, aad_profile, aad_digest, media_type, size_bytes, ciphertext_sha256, content_state, key_state, ref_version, revision, object_receipt_ref, created_at, updated_at;

-- name: PromoteObject :one
UPDATE orchestration_operator_content_objects
SET content_state='active', ref_version=1, revision=2, updated_at=sqlc.arg(updated_at)
WHERE object_id=sqlc.arg(object_id) AND object_version=1 AND content_state='prepared'
RETURNING object_id, object_version, reservation_id, source_id, source_generation, aead_algorithm, key_wrap_profile, key_handle_ref, key_receipt_ref, key_receipt_digest, nonce_hex, aad_profile, aad_digest, media_type, size_bytes, ciphertext_sha256, content_state, key_state, ref_version, revision, object_receipt_ref, created_at, updated_at;

-- name: CreateActiveRef :one
INSERT INTO orchestration_operator_content_refs (
 reference_id, canonical_ref, object_id, object_version, reservation_id, source_id, source_generation,
 ref_version, caller_principal_ref, scope_ref, proof_digest, valid_until, reference_state, created_at
) VALUES (sqlc.arg(reference_id), sqlc.arg(canonical_ref), sqlc.arg(object_id), 1, sqlc.arg(reservation_id), sqlc.arg(source_id), sqlc.arg(source_generation), 1, sqlc.arg(caller_principal_ref), sqlc.arg(scope_ref), sqlc.arg(proof_digest), sqlc.arg(valid_until), 'active', sqlc.arg(created_at))
RETURNING reference_id, canonical_ref, object_id, object_version, reservation_id, source_id, source_generation, ref_version, caller_principal_ref, scope_ref, proof_digest, valid_until, reference_state, created_at;

-- name: ResolveActiveRef :one
SELECT reference_id, canonical_ref, object_id, object_version, reservation_id, source_id, source_generation, ref_version, caller_principal_ref, scope_ref, proof_digest, valid_until, reference_state, created_at
FROM orchestration_operator_content_refs
WHERE canonical_ref=sqlc.arg(canonical_ref) AND reference_state='active';
