-- name: GetLatestOperatorSource :one
SELECT * FROM orchestration_operator_sources ORDER BY generation DESC LIMIT 1;
-- name: GetOperatorSourceBoot :one
SELECT * FROM orchestration_operator_sources WHERE principal_ref=? AND scope_ref=? AND auth_context=?;
-- name: GetOperatorSourceID :one
SELECT * FROM orchestration_operator_sources WHERE source_id=?;
-- name: InsertOperatorSource :one
INSERT INTO orchestration_operator_sources(source_id,source_fence_ref,principal_ref,scope_ref,auth_context,generation,state,created_at,schema_version)
VALUES(?,?,?,?,?,?,'active',?,1) RETURNING *;
-- name: RevokeOperatorSource :one
UPDATE orchestration_operator_sources SET state='revoked', revoked_at=? WHERE source_id=? AND generation=? AND state='active' RETURNING *;
