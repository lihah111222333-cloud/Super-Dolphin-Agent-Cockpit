-- name: GetOperatorReservation :one
SELECT reservation_id, source_kind, source_id, source_fence_ref, principal_ref,
       scope_ref, auth_context, generation, workspace_ref, caller_principal_ref,
       idempotency_namespace, idempotency_key_digest, content_intent, content_proof,
       content_candidate, content_digest_key, size_bytes, media_type, profile_ref,
       quota_ref, retention_seconds, deadline, state, revision, schema_version,
       created_at, updated_at
FROM orchestration_operator_reservations
WHERE reservation_id = ?;

-- name: CreateOperatorReservation :one
INSERT INTO orchestration_operator_reservations (
    reservation_id, source_kind, source_id, source_fence_ref, principal_ref,
    scope_ref, auth_context, generation, workspace_ref, caller_principal_ref,
    idempotency_namespace, idempotency_key_digest, content_intent, content_proof,
    content_candidate, content_digest_key, size_bytes, media_type, profile_ref,
    quota_ref, retention_seconds, deadline, state, revision, schema_version,
    created_at, updated_at
) VALUES (
    sqlc.arg(reservation_id), 'operator', sqlc.arg(source_id), sqlc.arg(source_fence_ref),
    sqlc.arg(principal_ref), sqlc.arg(scope_ref), sqlc.arg(auth_context), sqlc.arg(generation),
    sqlc.arg(workspace_ref), sqlc.arg(caller_principal_ref), sqlc.arg(idempotency_namespace),
    sqlc.arg(idempotency_key_digest), sqlc.arg(content_intent), sqlc.arg(content_proof),
    sqlc.arg(content_candidate), sqlc.arg(content_digest_key), sqlc.arg(size_bytes),
    sqlc.arg(media_type), sqlc.arg(profile_ref), sqlc.arg(quota_ref), sqlc.arg(retention_seconds),
    sqlc.arg(deadline), 'reserved', 1, 1, sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING reservation_id, source_kind, source_id, source_fence_ref, principal_ref,
          scope_ref, auth_context, generation, workspace_ref, caller_principal_ref,
          idempotency_namespace, idempotency_key_digest, content_intent, content_proof,
          content_candidate, content_digest_key, size_bytes, media_type, profile_ref,
          quota_ref, retention_seconds, deadline, state, revision, schema_version,
          created_at, updated_at;

-- name: ResolveOperatorReservation :one
SELECT reservation_id, source_kind, source_id, source_fence_ref, principal_ref,
       scope_ref, auth_context, generation, workspace_ref, caller_principal_ref,
       idempotency_namespace, idempotency_key_digest, content_intent, content_proof,
       content_candidate, content_digest_key, size_bytes, media_type, profile_ref,
       quota_ref, retention_seconds, deadline, state, revision, schema_version,
       created_at, updated_at
FROM orchestration_operator_reservations
WHERE idempotency_namespace = sqlc.arg(idempotency_namespace)
  AND idempotency_key_digest = sqlc.arg(idempotency_key_digest);
