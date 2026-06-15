-- name: ListTaskDagNodes :many
SELECT id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
       status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
       created_at, updated_at, active_turn_id, active_wakeup_id,
       last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id
FROM task_dag_nodes
WHERE dag_key = ?
  AND run_id IS NULL
ORDER BY created_at;

-- name: ListTaskDagRunNodes :many
SELECT id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
       status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
       created_at, updated_at, active_turn_id, active_wakeup_id,
       last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id
FROM task_dag_nodes
WHERE dag_key = ?
  AND run_id = ?
ORDER BY created_at;

-- name: GetTaskDagRunNodeForUpdate :one
SELECT id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
       status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
       created_at, updated_at, active_turn_id, active_wakeup_id,
       last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id
FROM task_dag_nodes
WHERE dag_key = sqlc.arg('dag_key')
  AND node_key = sqlc.arg('node_key')
  AND run_id = sqlc.arg('run_id')
  AND sqlc.arg('run_id') > 0;

-- name: ListRunningTaskDagNodesByAssignee :many
SELECT id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
       status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
       created_at, updated_at, active_turn_id, active_wakeup_id,
       last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id
FROM task_dag_nodes
WHERE assigned_to = ? AND status = 'running'
ORDER BY created_at;

-- name: GetTaskDagNodesForUpdate :many
SELECT id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
       status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
       created_at, updated_at, active_turn_id, active_wakeup_id,
       last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id
FROM task_dag_nodes
WHERE dag_key = ?
  AND run_id IS NULL
ORDER BY created_at, id;

-- name: LookupNodesBySpawningThread :many
SELECT id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
       status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
       created_at, updated_at, active_turn_id, active_wakeup_id,
       last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id
FROM task_dag_nodes
WHERE spawning_thread_id = ?
  AND spawning_thread_id IS NOT NULL
  AND run_id IS NOT NULL
ORDER BY updated_at DESC, id DESC;
