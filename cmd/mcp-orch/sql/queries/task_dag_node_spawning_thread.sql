-- name: UpdateTaskDagNodeSpawningThread :one
UPDATE task_dag_nodes
SET spawning_thread_id = ?,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = ?
  AND node_key = ?
  AND run_id = sqlc.arg(run_id)
  AND sqlc.arg(run_id) > 0
  AND status NOT IN ('done', 'failed', 'cancelled', 'skipped')
RETURNING id, dag_key, node_key, run_id, title,
          node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
          status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result,
          started_at, finished_at, created_at,
          updated_at, active_turn_id, active_wakeup_id,
          last_event_at, spawning_thread_id,
          '' AS previous_spawning_thread_id;
