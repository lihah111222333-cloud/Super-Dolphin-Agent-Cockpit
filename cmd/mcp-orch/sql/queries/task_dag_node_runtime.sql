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
RETURNING id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
          status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id;

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
RETURNING id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
          status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id;

-- name: UpdateRunningTaskDagNodeStatus :one
UPDATE task_dag_nodes
SET status = sqlc.arg('status'), result = sqlc.arg('result'), active_turn_id = NULL, active_wakeup_id = sqlc.arg('active_wakeup_id'),
    last_event_at = NULL, started_at = COALESCE(started_at, (CAST(strftime('%s','now') AS INTEGER) * 1000)), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = sqlc.arg('dag_key') AND node_key = sqlc.arg('node_key')
  AND run_id = sqlc.arg('run_id')
  AND sqlc.arg('run_id') > 0
  AND status IN ('pending', 'ready')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
          status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id;

-- name: CompleteTaskDagNode :one
UPDATE task_dag_nodes
SET status = sqlc.arg('status'), result = sqlc.arg('result'), active_turn_id = NULL, active_wakeup_id = NULL,
    finished_at = COALESCE(finished_at, (CAST(strftime('%s','now') AS INTEGER) * 1000)), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE task_dag_nodes.dag_key = sqlc.arg('dag_key') AND task_dag_nodes.node_key = sqlc.arg('node_key')
  AND task_dag_nodes.run_id = sqlc.arg('run_id')
  AND sqlc.arg('run_id') > 0
  AND status IN ('ready', 'running')
  AND (
    CAST(sqlc.arg('wakeup_id') AS INTEGER) = 0
    OR (
      task_dag_nodes.active_wakeup_id = CAST(sqlc.arg('wakeup_id') AS INTEGER)
      AND EXISTS (
        SELECT 1
        FROM task_dag_wakeups w
        WHERE w.id = CAST(sqlc.arg('wakeup_id') AS INTEGER)
          AND w.run_id = task_dag_nodes.run_id
          AND w.dag_key = task_dag_nodes.dag_key
          AND w.node_key = task_dag_nodes.node_key
          AND w.attempt_count = sqlc.arg('wakeup_attempt')
      )
    )
  )
RETURNING id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
          status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id;

-- name: MarkDispatchIncompleteNodesWithoutActiveWakeup :many
UPDATE task_dag_nodes
SET status = 'dispatch_incomplete',
    result = '{"kind":"dispatch_incomplete","reason":"assigned_without_active_wakeup"}',
    active_turn_id = NULL,
    active_wakeup_id = NULL,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE run_id IS NOT NULL
  AND status IN ('pending', 'ready')
  AND trim(assigned_to) <> ''
  AND NOT EXISTS (
      SELECT 1
      FROM task_dag_wakeups w
      WHERE w.run_id = task_dag_nodes.run_id
        AND w.dag_key = task_dag_nodes.dag_key
        AND w.node_key = task_dag_nodes.node_key
        AND (
          w.status IN ('pending', 'dispatching')
          OR (w.status = 'sent' AND w.sent_at IS NOT NULL AND w.bound_turn_id IS NULL)
        )
  )
RETURNING id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
          status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id;
