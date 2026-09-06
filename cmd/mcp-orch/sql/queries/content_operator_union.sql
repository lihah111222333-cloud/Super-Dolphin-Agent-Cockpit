-- name: BindOperatorSource :one
INSERT INTO orchestration_content_source_union (
    ingest_reservation_id,
    source_kind,
    task_snapshot_digest,
    task_binding_version,
    operator_source_id,
    operator_source_fence_ref,
    operator_principal_ref,
    operator_scope_ref,
    operator_auth_context,
    operator_generation,
    schema_version,
    created_at
) VALUES (?, 'operator', NULL, NULL, ?, ?, ?, ?, ?, ?, 1, ?)
RETURNING ingest_reservation_id, source_kind, task_snapshot_digest, task_binding_version,
          operator_source_id, operator_source_fence_ref, operator_principal_ref,
          operator_scope_ref, operator_auth_context, operator_generation,
          schema_version, created_at;

-- name: ResolveSource :one
SELECT ingest_reservation_id, source_kind, task_snapshot_digest, task_binding_version,
       operator_source_id, operator_source_fence_ref, operator_principal_ref,
       operator_scope_ref, operator_auth_context, operator_generation,
       schema_version, created_at
FROM orchestration_content_source_union
WHERE ingest_reservation_id = ?;
