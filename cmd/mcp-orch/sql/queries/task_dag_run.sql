-- name: CreateTaskDagRun :one
INSERT INTO task_dag_runs (
    run_key, dag_key, dag_version_snapshot, trigger_source, status,
    started_at, metadata, budget_limit, created_at, updated_at
)
VALUES (
    ?, ?, ?, ?, 'running',
    (CAST(strftime('%s','now') AS INTEGER) * 1000),
    ?, ?,
    (CAST(strftime('%s','now') AS INTEGER) * 1000),
    (CAST(strftime('%s','now') AS INTEGER) * 1000)
)
RETURNING id, run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, finished_at, events, budget_used, budget_limit, metadata, created_at, updated_at;

-- name: GetTaskDagRun :one
SELECT id, run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, finished_at, events, budget_used, budget_limit, metadata, created_at, updated_at
FROM task_dag_runs
WHERE run_key = ?;

-- name: ListTaskDagRunsByKey :many
SELECT id, run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, finished_at, events, budget_used, budget_limit, metadata, created_at, updated_at
FROM task_dag_runs
WHERE dag_key = ?
  AND (sqlc.arg(status_filter) = '' OR status = sqlc.arg(status_filter))
ORDER BY started_at DESC, id DESC
LIMIT sqlc.arg(limit_count);

-- name: LockTaskDagRunForCompletionForUpdate :one
SELECT id
FROM task_dag_runs
WHERE dag_key = ?
  AND id = ?
  AND status = 'running';

-- name: CountActiveTaskDagRunsByKey :one
SELECT COUNT(*) AS active
FROM task_dag_runs
WHERE dag_key = ? AND status = 'running';

-- name: CloneTaskDagNodesForRun :execrows
INSERT INTO task_dag_nodes (
  dag_key, node_key, title, node_type, assigned_to, depends_on,
  status, command_ref, config, result, run_id, reads, writes,
  created_at, updated_at
)
SELECT
  n.dag_key, n.node_key, n.title, n.node_type, n.assigned_to, n.depends_on,
  'pending', n.command_ref, n.config, '{}', sqlc.arg(run_id), n.reads, n.writes,
  (CAST(strftime('%s','now') AS INTEGER) * 1000),
  (CAST(strftime('%s','now') AS INTEGER) * 1000)
FROM task_dag_nodes n
WHERE n.dag_key = sqlc.arg(dag_key)
  AND n.run_id IS NULL
ON CONFLICT (dag_key, run_id, node_key) WHERE run_id IS NOT NULL DO NOTHING;

-- name: PromoteRootNodesToReady :execrows
UPDATE task_dag_nodes
SET status = 'ready',
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = ?
  AND run_id = ?
  AND status = 'pending'
  AND json_array_length(depends_on) = 0;

-- name: FinalizeTaskDagRunIfAllNodesTerminal :many
UPDATE task_dag_runs
SET status = CASE
      WHEN EXISTS (
        SELECT 1 FROM task_dag_nodes
        WHERE task_dag_nodes.dag_key = ?1
          AND task_dag_nodes.run_id = ?2
          AND task_dag_nodes.status = 'failed'
      ) THEN 'failed'
      WHEN EXISTS (
        SELECT 1 FROM task_dag_nodes
        WHERE task_dag_nodes.dag_key = ?1
          AND task_dag_nodes.run_id = ?2
          AND task_dag_nodes.status = 'cancelled'
      ) THEN 'cancelled'
      ELSE 'succeeded'
    END,
    finished_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE task_dag_runs.id = ?2
  AND task_dag_runs.dag_key = ?1
  AND task_dag_runs.status = 'running'
  AND EXISTS (
    SELECT 1 FROM task_dag_nodes
    WHERE task_dag_nodes.dag_key = ?1
      AND task_dag_nodes.run_id = ?2
  )
  AND NOT EXISTS (
    SELECT 1 FROM task_dag_nodes
    WHERE task_dag_nodes.dag_key = ?1
      AND task_dag_nodes.run_id = ?2
      AND task_dag_nodes.status NOT IN ('done','failed','cancelled','skipped')
  )
RETURNING run_key, status;

-- name: CancelTaskDagRunNodes :many
UPDATE task_dag_nodes
SET status = 'cancelled',
    result = json_object('kind', 'run_cancelled', 'reason', sqlc.arg(reason)),
    active_turn_id = NULL,
    active_wakeup_id = NULL,
    finished_at = COALESCE(finished_at, (CAST(strftime('%s','now') AS INTEGER) * 1000)),
    last_event_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = sqlc.arg(dag_key)
  AND run_id = sqlc.arg(run_id)
  AND status NOT IN ('done','failed','cancelled','skipped')
RETURNING spawning_thread_id;

-- name: CancelTaskDagRunWakeups :execrows
UPDATE task_dag_wakeups
SET status = 'failed',
    last_error = sqlc.arg(reason),
    claimed_at = NULL,
    claimed_by = '',
    lease_expires_at = NULL,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = sqlc.arg(dag_key)
  AND run_id = sqlc.arg(run_id)
  AND status IN ('pending','dispatching','sent');

-- name: CancelTaskDagRun :one
UPDATE task_dag_runs
SET status = 'cancelled',
    finished_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    events = json_insert(COALESCE(events, '[]'), '$[#]', json(sqlc.arg(event)))
WHERE dag_key = sqlc.arg(dag_key)
  AND id = sqlc.arg(run_id)
  AND run_key = sqlc.arg(run_key)
  AND status = 'running'
RETURNING id, run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, finished_at, events, budget_used, budget_limit, metadata, created_at, updated_at;

-- name: AppendTaskDagRunEvent :one
UPDATE task_dag_runs
SET events = json_insert(COALESCE(events, '[]'), '$[#]', json(sqlc.arg(event))),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = sqlc.arg(dag_key)
  AND status = 'running'
  AND id = sqlc.arg(run_id)
RETURNING run_key;
