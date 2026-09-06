-- name: CreateOrchestrationCommandIntent :one
INSERT INTO orchestration_command_intents (
    command_id,
    command_kind,
    caller_principal_ref,
    scope_ref,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    digest_key_id,
    request_content_ref,
    source_fence_ref,
    target_fence_ref,
    schema_version,
    command_version
) VALUES (
    sqlc.arg(command_id),
    sqlc.arg(command_kind),
    sqlc.arg(caller_principal_ref),
    sqlc.arg(scope_ref),
    sqlc.arg(idempotency_namespace),
    sqlc.arg(idempotency_key_digest),
    sqlc.arg(request_digest),
    sqlc.arg(digest_key_id),
    sqlc.arg(request_content_ref),
    sqlc.arg(source_fence_ref),
    sqlc.arg(target_fence_ref),
    1,
    1
)
ON CONFLICT(idempotency_namespace, idempotency_key_digest) DO NOTHING
RETURNING
    command_id,
    command_kind,
    caller_principal_ref,
    scope_ref,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    digest_key_id,
    request_content_ref,
    source_fence_ref,
    target_fence_ref,
    schema_version,
    command_version,
    recorded_at;

-- name: GetOrchestrationCommandIntent :one
SELECT
    command_id,
    command_kind,
    caller_principal_ref,
    scope_ref,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    digest_key_id,
    request_content_ref,
    source_fence_ref,
    target_fence_ref,
    schema_version,
    command_version,
    recorded_at
FROM orchestration_command_intents
WHERE command_id = sqlc.arg(command_id);

-- name: GetOrchestrationCommandIntentByKey :one
SELECT
    command_id,
    command_kind,
    caller_principal_ref,
    scope_ref,
    idempotency_namespace,
    idempotency_key_digest,
    request_digest,
    digest_key_id,
    request_content_ref,
    source_fence_ref,
    target_fence_ref,
    schema_version,
    command_version,
    recorded_at
FROM orchestration_command_intents
WHERE idempotency_namespace = sqlc.arg(idempotency_namespace)
  AND idempotency_key_digest = sqlc.arg(idempotency_key_digest);
