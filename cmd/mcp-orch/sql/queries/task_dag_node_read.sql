-- name: ListTaskDagNodes :many
SELECT id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at
FROM task_dag_nodes
WHERE dag_key = $1
ORDER BY created_at;

-- name: ListRunningTaskDagNodesByAssignee :many
SELECT id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at
FROM task_dag_nodes
WHERE assigned_to = $1 AND status = 'running'
ORDER BY created_at;

-- name: GetTaskDagNodesForUpdate :many
SELECT id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at
FROM task_dag_nodes
WHERE dag_key = $1
ORDER BY created_at, id
FOR UPDATE;
