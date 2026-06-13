-- name: BindRunningTaskDagNodeTurn :one
UPDATE task_dag_nodes
SET active_turn_id = ?,
    last_event_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = ?
  AND node_key = ?
  AND run_id = sqlc.arg(run_id)
  AND sqlc.arg(run_id) > 0
  AND status = 'running'
  AND active_turn_id IS NULL
  AND active_wakeup_id = ?
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on,
          status, command_ref, config, result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, reads, writes, spawning_thread_id;

-- name: TouchRunningTaskDagNodeEvent :one
UPDATE task_dag_nodes
SET last_event_at = ?,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = ?
  AND node_key = ?
  AND run_id = sqlc.arg(run_id)
  AND sqlc.arg(run_id) > 0
  AND status = 'running'
  AND active_turn_id = ?
  AND (last_event_at IS NULL OR last_event_at < sqlc.arg(last_event_at))
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on,
          status, command_ref, config, result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, reads, writes, spawning_thread_id;

-- name: UpdateRunningTaskDagNodeStatus :one
UPDATE task_dag_nodes
SET status = sqlc.arg('status'), result = sqlc.arg('result'), active_turn_id = NULL, active_wakeup_id = sqlc.arg('active_wakeup_id'),
    last_event_at = NULL, started_at = COALESCE(started_at, (CAST(strftime('%s','now') AS INTEGER) * 1000)), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = sqlc.arg('dag_key') AND node_key = sqlc.arg('node_key')
  AND run_id = sqlc.arg('run_id')
  AND sqlc.arg('run_id') > 0
  AND status IN ('pending', 'ready')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on,
          status, command_ref, config, result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, reads, writes, spawning_thread_id;

-- name: UpdateAwaitingVerifyTaskDagNodeStatus :one
UPDATE task_dag_nodes
SET status = sqlc.arg('status'), result = sqlc.arg('result'), active_turn_id = NULL, active_wakeup_id = NULL, updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = sqlc.arg('dag_key') AND node_key = sqlc.arg('node_key')
  AND run_id = sqlc.arg('run_id')
  AND sqlc.arg('run_id') > 0
  AND status IN ('running')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on,
          status, command_ref, config, result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, reads, writes, spawning_thread_id;

-- name: CompleteTaskDagNode :one
UPDATE task_dag_nodes
SET status = sqlc.arg('status'), result = sqlc.arg('result'), active_turn_id = NULL, active_wakeup_id = NULL,
    finished_at = COALESCE(finished_at, (CAST(strftime('%s','now') AS INTEGER) * 1000)), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = sqlc.arg('dag_key') AND node_key = sqlc.arg('node_key')
  AND run_id = sqlc.arg('run_id')
  AND sqlc.arg('run_id') > 0
  AND status IN ('ready', 'running', 'awaiting_verify')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on,
          status, command_ref, config, result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, reads, writes, spawning_thread_id;
