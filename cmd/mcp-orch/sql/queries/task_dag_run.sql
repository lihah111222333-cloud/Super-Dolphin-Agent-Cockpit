-- name: CreateTaskDagRun :execrows
INSERT INTO task_dag_runs (
    run_key, dag_key, dag_version_snapshot, trigger_source, status,
    started_at, metadata, budget_limit, created_at, updated_at
)
VALUES (
    :run_key, :dag_key, :dag_version_snapshot, :trigger_source, 'running',
    (CAST(strftime('%s','now') AS INTEGER) * 1000),
    :metadata, :budget_limit,
    (CAST(strftime('%s','now') AS INTEGER) * 1000),
    (CAST(strftime('%s','now') AS INTEGER) * 1000)
);

-- name: GetTaskDagRun :one
SELECT
  id,
  run_key,
  dag_key,
  dag_version_snapshot,
  trigger_source,
  status,
  started_at,
  finished_at,
  events,
  budget_used,
  budget_limit,
  metadata,
  created_at,
  updated_at
FROM
  task_dag_runs
WHERE
  run_key = :run_key;

-- name: ListTaskDagRunsByKey :many
SELECT id, run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, finished_at, budget_used, budget_limit, created_at, updated_at
FROM task_dag_runs
WHERE dag_key = :dag_key
  AND (:status_filter = '' OR status = :status_filter)
ORDER BY started_at DESC, id DESC
LIMIT :limit_count;

-- name: LockTaskDagRunForCompletionForUpdate :one
SELECT id
FROM task_dag_runs
WHERE dag_key = :dag_key
  AND id = :run_id
  AND status = 'running';

-- name: CountActiveTaskDagRunsByKey :one
SELECT COUNT(*) AS active
FROM task_dag_runs
WHERE dag_key = :dag_key AND status = 'running';

-- name: InsertTaskDagRunNode :execrows
INSERT INTO task_dag_nodes (
  dag_key, node_key, title, node_type, assigned_to, depends_on,
  status, command_ref, config, result, run_id, reads, writes,
  created_at, updated_at
)
VALUES (
  :dag_key, :node_key, :title, :node_type, :assigned_to, :depends_on,
  'pending', :command_ref, :config, '{}', :run_id, :reads, :writes,
  (CAST(strftime('%s','now') AS INTEGER) * 1000),
  (CAST(strftime('%s','now') AS INTEGER) * 1000)
);

-- name: PromoteRootNodesToReady :execrows
UPDATE task_dag_nodes
SET status = 'ready',
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = :dag_key
  AND run_id = :run_id
  AND status = 'pending'
  AND json_array_length(depends_on) = 0;

-- name: FinalizeTaskDagRun :execrows
UPDATE task_dag_runs
SET status = :status,
    finished_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE task_dag_runs.id = :run_id
  AND task_dag_runs.dag_key = :dag_key
  AND task_dag_runs.status = 'running';

-- name: ListTaskDagRunNodeStatuses :many
SELECT status
FROM task_dag_nodes
WHERE dag_key = :dag_key
  AND run_id = :run_id
ORDER BY id;

-- name: GetTaskDagRunIdentityByID :one
SELECT run_key, status
FROM task_dag_runs
WHERE dag_key = :dag_key
  AND id = :run_id;

-- name: ListCancelableTaskDagRunSpawningThreads :many
SELECT spawning_thread_id
FROM task_dag_nodes
WHERE dag_key = :dag_key
  AND run_id = :run_id
  AND status NOT IN ('done','failed','cancelled','skipped')
ORDER BY id;

-- name: CancelTaskDagRunNodes :execrows
UPDATE task_dag_nodes
SET status = 'cancelled',
    result = json_object('kind', 'run_cancelled', 'reason', :reason),
    active_turn_id = NULL,
    active_wakeup_id = NULL,
    finished_at = COALESCE(finished_at, (CAST(strftime('%s','now') AS INTEGER) * 1000)),
    last_event_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = :dag_key
  AND run_id = :run_id
  AND status NOT IN ('done','failed','cancelled','skipped');

-- name: CancelTaskDagRunWakeups :execrows
UPDATE task_dag_wakeups
SET status = 'failed',
    last_error = :reason,
    claimed_at = NULL,
    claimed_by = '',
    lease_expires_at = NULL,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = :dag_key
  AND run_id = :run_id
  AND status IN ('pending','dispatching','sent');

-- name: CancelTaskDagRun :execrows
UPDATE task_dag_runs
SET status = 'cancelled',
    finished_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    events = :events
WHERE dag_key = :dag_key
  AND id = :run_id
  AND run_key = :run_key
  AND status = 'running';

-- name: LoadTaskDagRunEventsForAppend :one
SELECT run_key, CAST(events AS BLOB) AS events
FROM task_dag_runs
WHERE dag_key = :dag_key
  AND status = 'running'
  AND id = :run_id;

-- name: UpdateTaskDagRunEventsAfterAppend :execrows
UPDATE task_dag_runs
SET events = :events,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = :dag_key
  AND status = 'running'
  AND id = :run_id;
