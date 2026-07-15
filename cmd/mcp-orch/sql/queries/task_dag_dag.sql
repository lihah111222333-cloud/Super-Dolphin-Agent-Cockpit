-- name: InsertTaskDag :execrows
INSERT INTO task_dags (dag_key, title, description, status, created_by, metadata, created_at, updated_at)
VALUES (:dag_key, :title, :description, :status, :created_by, :metadata, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000));

-- name: UpdateTaskDag :execrows
UPDATE task_dags
SET title = :title,
    description = :description,
    status = :status,
    created_by = :created_by,
    metadata = :metadata,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE
    dag_key = :dag_key;

-- name: ListTaskDags :many
SELECT id, dag_key, title, description, status, created_by, CAST('{}' AS BLOB) AS metadata,
       started_at, finished_at, created_at, updated_at,
       trigger, owner_id, cron_expr, next_run_at, version
FROM task_dags
WHERE
  (:status_filter = '' OR status = :status_filter)
  AND (:keyword = ''
    OR dag_key LIKE :keyword_pattern
    OR title LIKE :keyword_pattern
    OR description LIKE :keyword_pattern)
ORDER BY updated_at DESC, id DESC
LIMIT :limit_count;

-- name: GetTaskDag :one
SELECT id, dag_key, title, description, status, created_by, CAST(metadata AS BLOB) AS metadata,
       started_at, finished_at, created_at, updated_at,
       trigger, owner_id, cron_expr, next_run_at, version
FROM task_dags
WHERE
    dag_key = :dag_key;

-- name: GetTaskDagForUpdate :one
SELECT id, dag_key, title, description, status, created_by, CAST(metadata AS BLOB) AS metadata,
       started_at, finished_at, created_at, updated_at,
       trigger, owner_id, cron_expr, next_run_at, version
FROM task_dags
WHERE
    dag_key = :dag_key;

-- name: GetTaskDagVersionForUpdate :one
SELECT version
FROM task_dags
WHERE
    dag_key = :dag_key;

-- name: GetTaskDagVersion :one
SELECT version
FROM task_dags
WHERE
    dag_key = :dag_key;

-- name: GetTaskDagSchedule :one
SELECT trigger, cron_expr
FROM task_dags
WHERE
    dag_key = :dag_key;

-- name: UpdateTaskDagPatch :execrows
UPDATE task_dags
SET title = COALESCE(:title, title),
    description = COALESCE(:description, description),
    trigger = COALESCE(:trigger, trigger),
    cron_expr = COALESCE(:cron_expr, cron_expr),
    owner_id = COALESCE(:owner_id, owner_id),
    next_run_at = :resolved_next_run_at,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE
    dag_key = :dag_key;

-- name: BumpTaskDagVersion :execrows
UPDATE task_dags
SET version = version + 1,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE
    dag_key = :dag_key AND version = :expected_version;

-- name: LockTaskDagForDelete :one
SELECT id
FROM task_dags
WHERE
    dag_key = :dag_key;

-- name: DeleteTaskDagWakeupsByDAG :execrows
DELETE FROM task_dag_wakeups
WHERE
    dag_key = :dag_key;

-- name: DeleteTaskDagNodesByDAG :execrows
DELETE FROM task_dag_nodes
WHERE
    dag_key = :dag_key;

-- name: DeleteTaskDagRunsByDAG :execrows
DELETE FROM task_dag_runs
WHERE
    dag_key = :dag_key;

-- name: DeleteTaskDagRow :execrows
DELETE FROM task_dags
WHERE
    dag_key = :dag_key;

-- name: ListDueScheduledTaskDags :many
SELECT dag_key, cron_expr, next_run_at
FROM task_dags
WHERE
  trigger = 'scheduled'
  AND cron_expr <> ''
  AND next_run_at IS NOT NULL
  AND next_run_at <= :due_at
ORDER BY next_run_at ASC, id ASC;

-- name: UpdateTaskDagNextRun :execrows
UPDATE task_dags
SET next_run_at = :next_run_at,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE
  dag_key = :dag_key
  AND trigger = 'scheduled'
  AND cron_expr <> ''
  AND next_run_at = :due_at;
