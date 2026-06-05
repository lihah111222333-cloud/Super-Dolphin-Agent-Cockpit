-- name: UpsertTaskDag :one
INSERT INTO task_dags (dag_key, title, description, status, created_by, metadata)
VALUES ($1, $2, $3, $4, $5, $6::jsonb)
RETURNING id, dag_key, title, description, status, created_by, metadata,
          started_at, finished_at, created_at, updated_at,
          trigger, owner_id, cron_expr, next_run_at, version;

-- name: ListTaskDags :many
SELECT id, dag_key, title, description, status, created_by, metadata,
       started_at, finished_at, created_at, updated_at,
       trigger, owner_id, cron_expr, next_run_at, version
FROM task_dags
WHERE ($1::text = '' OR status = $1)
  AND ($2::text = ''
    OR dag_key ILIKE '%' || $2 || '%'
    OR title ILIKE '%' || $2 || '%'
    OR description ILIKE '%' || $2 || '%')
ORDER BY updated_at DESC, id DESC
LIMIT $3;

-- name: GetTaskDag :one
SELECT id, dag_key, title, description, status, created_by, metadata,
       started_at, finished_at, created_at, updated_at,
       trigger, owner_id, cron_expr, next_run_at, version
FROM task_dags
WHERE dag_key = $1;

-- name: GetTaskDagForUpdate :one
SELECT id, dag_key, title, description, status, created_by, metadata,
       started_at, finished_at, created_at, updated_at,
       trigger, owner_id, cron_expr, next_run_at, version
FROM task_dags
WHERE dag_key = $1
FOR UPDATE;

-- name: GetTaskDagVersionForUpdate :one
SELECT version
FROM task_dags
WHERE dag_key = $1
FOR UPDATE;

-- name: GetTaskDagVersion :one
SELECT version
FROM task_dags
WHERE dag_key = $1;

-- name: GetTaskDagSchedule :one
SELECT trigger, cron_expr
FROM task_dags
WHERE dag_key = $1;

-- name: UpdateTaskDagPatch :execrows
UPDATE task_dags
SET title = COALESCE(sqlc.narg('title'), title),
    description = COALESCE(sqlc.narg('description'), description),
    trigger = COALESCE(sqlc.narg('trigger'), trigger),
    cron_expr = COALESCE(sqlc.narg('cron_expr'), cron_expr),
    owner_id = COALESCE(sqlc.narg('owner_id'), owner_id),
    next_run_at = CASE
      WHEN sqlc.narg('schedule_enabled')::boolean IS NOT NULL
        AND sqlc.narg('schedule_enabled')::boolean = FALSE THEN NULL
      WHEN COALESCE(sqlc.narg('trigger'), trigger) = 'scheduled'
        AND COALESCE(sqlc.narg('cron_expr'), cron_expr) <> ''
      THEN CASE
        WHEN sqlc.narg('schedule_enabled')::boolean IS NOT NULL
          AND sqlc.narg('schedule_enabled')::boolean = TRUE THEN COALESCE(sqlc.narg('next_run_at'), next_run_at)
        WHEN sqlc.narg('trigger') IS NOT NULL
          OR sqlc.narg('cron_expr') IS NOT NULL
          OR next_run_at IS NULL THEN COALESCE(sqlc.narg('next_run_at'), next_run_at)
        ELSE next_run_at
      END
      ELSE NULL
    END,
    updated_at = NOW()
WHERE dag_key = sqlc.arg('dag_key');

-- name: BumpTaskDagVersion :one
UPDATE task_dags
SET version = version + 1,
    updated_at = NOW()
WHERE dag_key = sqlc.arg('dag_key') AND version = sqlc.arg('expected_version')
RETURNING version;

-- name: LockTaskDagForDelete :one
SELECT id
FROM task_dags
WHERE dag_key = $1
FOR UPDATE;

-- name: DeleteTaskDagWakeupsByDAG :execrows
DELETE FROM task_dag_wakeups
WHERE dag_key = $1;

-- name: DeleteTaskDagNodesByDAG :execrows
DELETE FROM task_dag_nodes
WHERE dag_key = $1;

-- name: DeleteTaskDagRunsByDAG :execrows
DELETE FROM task_dag_runs
WHERE dag_key = $1;

-- name: DeleteTaskDagRow :execrows
DELETE FROM task_dags
WHERE dag_key = $1;

-- name: ListDueScheduledTaskDags :many
SELECT dag_key, cron_expr, next_run_at
FROM task_dags
WHERE trigger = 'scheduled'
  AND cron_expr <> ''
  AND next_run_at IS NOT NULL
  AND next_run_at <= $1
ORDER BY next_run_at ASC, id ASC;

-- name: UpdateTaskDagNextRun :execrows
UPDATE task_dags
SET next_run_at = sqlc.arg(next_run_at),
    updated_at = NOW()
WHERE dag_key = sqlc.arg(dag_key)
  AND trigger = 'scheduled'
  AND cron_expr <> ''
  AND next_run_at = sqlc.arg(due_at);

-- name: TryTaskDagAdvisoryLock :one
SELECT pg_try_advisory_lock($1);

-- name: UnlockTaskDagAdvisoryLock :one
SELECT pg_advisory_unlock($1);
