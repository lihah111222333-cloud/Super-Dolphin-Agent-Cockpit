-- name: BindRunningTaskDagNodeTurn :execrows
UPDATE task_dag_nodes
SET active_turn_id = :active_turn_id,
    last_event_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = :dag_key
  AND node_key = :node_key
  AND run_id = :run_id
  AND :run_id > 0
  AND status = 'running'
  AND active_turn_id IS NULL
  AND active_wakeup_id = :active_wakeup_id;

-- name: TouchRunningTaskDagNodeEvent :execrows
UPDATE task_dag_nodes
SET last_event_at = :last_event_at,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = :dag_key
  AND node_key = :node_key
  AND run_id = :run_id
  AND :run_id > 0
  AND status = 'running'
  AND active_turn_id = :active_turn_id
  AND (last_event_at IS NULL OR last_event_at < :last_event_at);

-- name: UpdateRunningTaskDagNodeStatus :execrows
UPDATE task_dag_nodes
SET status = :status, result = :result, active_turn_id = NULL, active_wakeup_id = :active_wakeup_id,
    last_event_at = NULL, started_at = COALESCE(started_at, (CAST(strftime('%s','now') AS INTEGER) * 1000)), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = :dag_key AND node_key = :node_key
  AND run_id = :run_id
  AND :run_id > 0
  AND status IN ('pending', 'ready');

-- name: CompleteTaskDagNode :execrows
UPDATE task_dag_nodes
SET status = :status, result = :result, active_turn_id = NULL, active_wakeup_id = NULL,
    finished_at = COALESCE(finished_at, (CAST(strftime('%s','now') AS INTEGER) * 1000)), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE task_dag_nodes.dag_key = :dag_key AND task_dag_nodes.node_key = :node_key
  AND task_dag_nodes.run_id = :run_id
  AND :run_id > 0
  AND status IN ('ready', 'running')
  AND (
    CAST(:wakeup_id AS INTEGER) = 0
    OR (
      task_dag_nodes.active_wakeup_id = CAST(:wakeup_id AS INTEGER)
      AND task_dag_nodes.active_wakeup_id IN (
        SELECT w.id
        FROM task_dag_wakeups w
        WHERE w.id = CAST(:wakeup_id AS INTEGER)
          AND w.run_id = task_dag_nodes.run_id
          AND w.dag_key = task_dag_nodes.dag_key
          AND w.node_key = task_dag_nodes.node_key
          AND w.attempt_count = :wakeup_attempt
      )
    )
  );

-- name: MarkDispatchIncompleteNodesWithoutActiveWakeup :execrows
UPDATE task_dag_nodes
SET status = 'dispatch_incomplete',
    result = '{"kind":"dispatch_incomplete","reason":"assigned_without_active_wakeup"}',
    active_turn_id = NULL,
    active_wakeup_id = NULL,
    updated_at = :updated_at
WHERE run_id IS NOT NULL
  AND status IN ('pending', 'ready')
  AND trim(assigned_to) <> ''
  AND id NOT IN (
      SELECT n.id
      FROM task_dag_nodes n
      JOIN task_dag_wakeups w
        ON w.run_id = n.run_id
       AND w.dag_key = n.dag_key
       AND w.node_key = n.node_key
      WHERE w.status IN ('pending', 'dispatching')
         OR (w.status = 'sent' AND w.sent_at IS NOT NULL AND w.bound_turn_id IS NULL)
  );

-- name: ListDispatchIncompleteNodesByUpdatedAt :many
SELECT id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
       status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
       created_at, updated_at, active_turn_id, active_wakeup_id,
       last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id
FROM task_dag_nodes
WHERE status = 'dispatch_incomplete'
  AND updated_at = :updated_at
ORDER BY id;
