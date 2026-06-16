-- name: UpsertTaskDag :one
INSERT INTO task_dags (dag_key, title, description, status, created_by, metadata, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000))
RETURNING id, dag_key, title, description, status, created_by, CAST(metadata AS BLOB) AS metadata,
          started_at, finished_at, created_at, updated_at,
          trigger, owner_id, cron_expr, next_run_at, version;

-- name: ListTaskDags :many
SELECT id, dag_key, title, description, status, created_by, CAST('{}' AS BLOB) AS metadata,
       started_at, finished_at, created_at, updated_at,
       trigger, owner_id, cron_expr, next_run_at, version
FROM task_dags
WHERE (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
  AND (sqlc.arg(keyword) = ''
    OR dag_key LIKE '%' || sqlc.arg(keyword) || '%'
    OR title LIKE '%' || sqlc.arg(keyword) || '%'
    OR description LIKE '%' || sqlc.arg(keyword) || '%')
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(limit_count);

-- name: GetTaskDag :one
SELECT id, dag_key, title, description, status, created_by, CAST(metadata AS BLOB) AS metadata,
       started_at, finished_at, created_at, updated_at,
       trigger, owner_id, cron_expr, next_run_at, version
FROM task_dags
WHERE dag_key = ?;

-- name: GetTaskDagForUpdate :one
SELECT id, dag_key, title, description, status, created_by, CAST(metadata AS BLOB) AS metadata,
       started_at, finished_at, created_at, updated_at,
       trigger, owner_id, cron_expr, next_run_at, version
FROM task_dags
WHERE dag_key = ?;

-- name: GetTaskDagVersionForUpdate :one
SELECT version
FROM task_dags
WHERE dag_key = ?;

-- name: GetTaskDagVersion :one
SELECT version
FROM task_dags
WHERE dag_key = ?;

-- name: GetTaskDagSchedule :one
SELECT trigger, cron_expr
FROM task_dags
WHERE dag_key = ?;

-- name: UpdateTaskDagPatch :execrows
UPDATE task_dags
SET title = COALESCE(sqlc.narg('title'), title),
    description = COALESCE(sqlc.narg('description'), description),
    trigger = COALESCE(sqlc.narg('trigger'), trigger),
    cron_expr = COALESCE(sqlc.narg('cron_expr'), cron_expr),
    owner_id = COALESCE(sqlc.narg('owner_id'), owner_id),
    next_run_at = CASE
      WHEN sqlc.narg('schedule_enabled') IS NOT NULL
        AND sqlc.narg('schedule_enabled') = 0 THEN NULL
      WHEN COALESCE(sqlc.narg('trigger'), trigger) = 'scheduled'
        AND COALESCE(sqlc.narg('cron_expr'), cron_expr) <> ''
      THEN CASE
        WHEN sqlc.narg('schedule_enabled') IS NOT NULL
          AND sqlc.narg('schedule_enabled') = 1 THEN COALESCE(sqlc.narg('next_run_at'), next_run_at)
        WHEN sqlc.narg('trigger') IS NOT NULL
          OR sqlc.narg('cron_expr') IS NOT NULL
          OR next_run_at IS NULL THEN COALESCE(sqlc.narg('next_run_at'), next_run_at)
        ELSE next_run_at
      END
      ELSE NULL
    END,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = sqlc.arg('dag_key');

-- name: BumpTaskDagVersion :one
UPDATE task_dags
SET version = version + 1,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = sqlc.arg('dag_key') AND version = sqlc.arg('expected_version')
RETURNING version;

-- name: LockTaskDagForDelete :one
SELECT id
FROM task_dags
WHERE dag_key = ?;

-- name: DeleteTaskDagWakeupsByDAG :execrows
DELETE FROM task_dag_wakeups
WHERE dag_key = ?;

-- name: DeleteTaskDagNodesByDAG :execrows
DELETE FROM task_dag_nodes
WHERE dag_key = ?;

-- name: DeleteTaskDagRunsByDAG :execrows
DELETE FROM task_dag_runs
WHERE dag_key = ?;

-- name: DeleteTaskDagRow :execrows
DELETE FROM task_dags
WHERE dag_key = ?;

-- name: ListDueScheduledTaskDags :many
SELECT dag_key, cron_expr, next_run_at
FROM task_dags
WHERE trigger = 'scheduled'
  AND cron_expr <> ''
  AND next_run_at IS NOT NULL
  AND next_run_at <= ?
ORDER BY next_run_at ASC, id ASC;

-- name: UpdateTaskDagNextRun :execrows
UPDATE task_dags
SET next_run_at = sqlc.arg(next_run_at),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = sqlc.arg(dag_key)
  AND trigger = 'scheduled'
  AND cron_expr <> ''
  AND next_run_at = sqlc.arg(due_at);
