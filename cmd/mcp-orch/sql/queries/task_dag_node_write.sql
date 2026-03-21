-- name: UpsertTaskDagNode :one
INSERT INTO task_dag_nodes (dag_key, node_key, title, node_type, assigned_to, depends_on, command_ref, config)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8::jsonb)
ON CONFLICT (dag_key, node_key) DO UPDATE
SET title = EXCLUDED.title,
    node_type = EXCLUDED.node_type,
    assigned_to = EXCLUDED.assigned_to,
    depends_on = EXCLUDED.depends_on,
    command_ref = EXCLUDED.command_ref,
    config = EXCLUDED.config,
    updated_at = NOW()
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;

-- name: UpdateTaskDagNodeStatus :one
UPDATE task_dag_nodes
SET status = $1,
    result = $2::jsonb,
    updated_at = NOW()
WHERE dag_key = $3 AND node_key = $4
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;

-- name: UpdateTaskDagNodeStatusFlexible :one
UPDATE task_dag_nodes
SET status = $1, result = $2::jsonb, updated_at = NOW()
WHERE dag_key = $3 AND node_key = $4
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;
