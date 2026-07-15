-- name: InsertWorkspaceRun :execrows
INSERT INTO workspace_runs (
    run_key, dag_key, source_root, workspace_path, status,
    created_by, updated_by, metadata, created_at, updated_at, finished_at
) VALUES (
    :run_key, :dag_key, :source_root, :workspace_path, :status,
    :created_by, :updated_by, :metadata,
    (CAST(strftime('%s','now') AS INTEGER) * 1000),
    (CAST(strftime('%s','now') AS INTEGER) * 1000), :finished_at
);

-- name: UpdateWorkspaceRun :execrows
UPDATE workspace_runs
SET dag_key = :dag_key,
    source_root = :source_root,
    workspace_path = :workspace_path,
    status = :status,
    updated_by = :updated_by,
    metadata = :metadata,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    finished_at = :finished_at
WHERE
    run_key = :run_key;

-- name: GetWorkspaceRun :one
SELECT id, run_key, dag_key, source_root, workspace_path, status, created_by, updated_by, CAST(metadata AS BLOB) AS metadata, created_at, updated_at, finished_at
FROM workspace_runs
WHERE
    run_key = :run_key;

-- name: ListWorkspaceRuns :many
SELECT id, run_key, dag_key, source_root, workspace_path, status, created_by, updated_by, CAST('{}' AS BLOB) AS metadata, created_at, updated_at, finished_at
FROM workspace_runs
WHERE (:status_filter = '' OR status = :status_filter)
  AND (:dag_key_filter = '' OR dag_key = :dag_key_filter)
ORDER BY updated_at DESC, id DESC
LIMIT :limit_count;

-- name: UpdateWorkspaceRunStatus :execrows
UPDATE workspace_runs
SET status = :new_status,
    updated_by = :updated_by,
    metadata = :metadata,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    finished_at = :finished_at
WHERE run_key = :run_key;

-- name: TransitionWorkspaceRunStatus :execrows
UPDATE workspace_runs
SET status = :new_status,
    updated_by = :updated_by,
    metadata = :metadata,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    finished_at = :finished_at
WHERE
    run_key = :run_key AND status = :expected_status;

-- name: InsertWorkspaceRunFile :execrows
INSERT INTO workspace_run_files (
    run_key, relative_path, baseline_sha256, workspace_sha256,
    source_sha256_before, source_sha256_after, state, last_error, created_at, updated_at
) VALUES (
    :run_key, :relative_path, :baseline_sha256, :workspace_sha256,
    :source_sha256_before, :source_sha256_after, :state, :last_error,
    (CAST(strftime('%s','now') AS INTEGER) * 1000),
    (CAST(strftime('%s','now') AS INTEGER) * 1000)
);

-- name: UpdateWorkspaceRunFile :execrows
UPDATE workspace_run_files
SET baseline_sha256 = :baseline_sha256,
    workspace_sha256 = :workspace_sha256,
    source_sha256_before = :source_sha256_before,
    source_sha256_after = :source_sha256_after,
    state = :state,
    last_error = :last_error,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE
    run_key = :run_key AND relative_path = :relative_path;

-- name: GetWorkspaceRunFile :one
SELECT id, run_key, relative_path, baseline_sha256, workspace_sha256, source_sha256_before, source_sha256_after, state, last_error, created_at, updated_at
FROM workspace_run_files
WHERE run_key = :run_key AND relative_path = :relative_path;

-- name: ListWorkspaceRunFiles :many
SELECT id, run_key, relative_path, baseline_sha256, workspace_sha256, source_sha256_before, source_sha256_after, state, last_error, created_at, updated_at
FROM workspace_run_files
WHERE (:run_key_filter = '' OR run_key = :run_key_filter)
  AND (:state_filter = '' OR state = :state_filter)
ORDER BY updated_at DESC, id DESC
LIMIT :limit_count;
