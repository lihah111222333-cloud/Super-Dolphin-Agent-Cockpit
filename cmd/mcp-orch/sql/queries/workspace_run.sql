-- name: UpsertWorkspaceRun :one
INSERT INTO workspace_runs (
    run_key, dag_key, source_root, workspace_path, status,
    created_by, updated_by, metadata, created_at, updated_at, finished_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000), ?)
ON CONFLICT (run_key) DO UPDATE
SET dag_key = EXCLUDED.dag_key,
    source_root = EXCLUDED.source_root,
    workspace_path = EXCLUDED.workspace_path,
    status = EXCLUDED.status,
    updated_by = EXCLUDED.updated_by,
    metadata = EXCLUDED.metadata,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    finished_at = EXCLUDED.finished_at
RETURNING id, run_key, dag_key, source_root, workspace_path, status, created_by, updated_by, CAST(metadata AS BLOB) AS metadata, created_at, updated_at, finished_at;

-- name: GetWorkspaceRun :one
SELECT id, run_key, dag_key, source_root, workspace_path, status, created_by, updated_by, CAST(metadata AS BLOB) AS metadata, created_at, updated_at, finished_at
FROM workspace_runs
WHERE run_key = ?;

-- name: ListWorkspaceRuns :many
SELECT id, run_key, dag_key, source_root, workspace_path, status, created_by, updated_by, CAST('{}' AS BLOB) AS metadata, created_at, updated_at, finished_at
FROM workspace_runs
WHERE (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (sqlc.arg(dag_key_filter) = '' OR dag_key = sqlc.arg(dag_key_filter))
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(limit_count);

-- name: UpdateWorkspaceRunStatus :one
UPDATE workspace_runs
SET status = sqlc.arg(new_status),
    updated_by = sqlc.arg(updated_by),
    metadata = sqlc.arg(metadata),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    finished_at = CASE
        WHEN sqlc.arg(new_status) = 'merged'
          OR sqlc.arg(new_status) = 'aborted'
          OR sqlc.arg(new_status) = 'failed' THEN (CAST(strftime('%s','now') AS INTEGER) * 1000)
        WHEN sqlc.arg(new_status) = 'active' THEN NULL
        ELSE finished_at
    END
WHERE run_key = sqlc.arg(run_key)
RETURNING id, run_key, dag_key, source_root, workspace_path, status, created_by, updated_by, CAST(metadata AS BLOB) AS metadata, created_at, updated_at, finished_at;

-- name: TransitionWorkspaceRunStatus :one
UPDATE workspace_runs
SET status = sqlc.arg(new_status),
    updated_by = sqlc.arg(updated_by),
    metadata = sqlc.arg(metadata),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    finished_at = CASE
        WHEN sqlc.arg(new_status) = 'merged'
          OR sqlc.arg(new_status) = 'aborted'
          OR sqlc.arg(new_status) = 'failed' THEN (CAST(strftime('%s','now') AS INTEGER) * 1000)
        WHEN sqlc.arg(new_status) = 'active' THEN NULL
        ELSE finished_at
    END
WHERE run_key = sqlc.arg(run_key) AND status = sqlc.arg(expected_status)
RETURNING id, run_key, dag_key, source_root, workspace_path, status, created_by, updated_by, CAST(metadata AS BLOB) AS metadata, created_at, updated_at, finished_at;

-- name: UpsertWorkspaceRunFile :one
INSERT INTO workspace_run_files (
    run_key, relative_path, baseline_sha256, workspace_sha256,
    source_sha256_before, source_sha256_after, state, last_error, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000))
ON CONFLICT (run_key, relative_path) DO UPDATE
SET baseline_sha256 = EXCLUDED.baseline_sha256,
    workspace_sha256 = EXCLUDED.workspace_sha256,
    source_sha256_before = EXCLUDED.source_sha256_before,
    source_sha256_after = EXCLUDED.source_sha256_after,
    state = EXCLUDED.state,
    last_error = EXCLUDED.last_error,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
RETURNING id, run_key, relative_path, baseline_sha256, workspace_sha256, source_sha256_before, source_sha256_after, state, last_error, created_at, updated_at;

-- name: GetWorkspaceRunFile :one
SELECT id, run_key, relative_path, baseline_sha256, workspace_sha256, source_sha256_before, source_sha256_after, state, last_error, created_at, updated_at
FROM workspace_run_files
WHERE run_key = ? AND relative_path = ?;

-- name: ListWorkspaceRunFiles :many
SELECT id, run_key, relative_path, baseline_sha256, workspace_sha256, source_sha256_before, source_sha256_after, state, last_error, created_at, updated_at
FROM workspace_run_files
WHERE (sqlc.arg(run_key_filter) = '' OR run_key = sqlc.arg(run_key_filter))
  AND (sqlc.arg(state_filter) = '' OR state = sqlc.arg(state_filter))
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(limit_count);
