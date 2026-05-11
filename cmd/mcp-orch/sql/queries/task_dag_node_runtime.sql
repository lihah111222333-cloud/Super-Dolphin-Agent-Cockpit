-- name: BindRunningTaskDagNodeTurn :one
UPDATE task_dag_nodes
SET active_turn_id = $1,
    last_event_at = NOW(),
    updated_at = NOW()
WHERE dag_key = $2
  AND node_key = $3
  AND status = 'running'
  AND active_turn_id IS NULL
  AND active_wakeup_id = $4
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id;

-- name: TouchRunningTaskDagNodeEvent :one
UPDATE task_dag_nodes
SET last_event_at = $1,
    updated_at = NOW()
WHERE dag_key = $2
  AND node_key = $3
  AND status = 'running'
  AND active_turn_id = $4
  AND (last_event_at IS NULL OR last_event_at < $1)
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id;

-- name: UpdateRunningTaskDagNodeStatus :one
UPDATE task_dag_nodes
SET status = $1, result = $2::jsonb, active_turn_id = NULL, active_wakeup_id = $3,
    last_event_at = NULL, started_at = COALESCE(started_at, NOW()), updated_at = NOW()
WHERE dag_key = $4 AND node_key = $5 AND status IN ('pending')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id;

-- name: UpdateAwaitingVerifyTaskDagNodeStatus :one
UPDATE task_dag_nodes
SET status = $1, result = $2::jsonb, active_turn_id = NULL, active_wakeup_id = NULL, updated_at = NOW()
WHERE dag_key = $3 AND node_key = $4 AND status IN ('running')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id;

-- name: CompleteTaskDagNode :one
UPDATE task_dag_nodes
SET status = $1, result = $2::jsonb, active_turn_id = NULL, active_wakeup_id = NULL,
    finished_at = COALESCE(finished_at, NOW()), updated_at = NOW()
WHERE dag_key = $3 AND node_key = $4 AND status IN ('running', 'awaiting_verify')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id;
