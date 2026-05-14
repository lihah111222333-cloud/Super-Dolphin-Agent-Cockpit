-- name: BindRunningTaskDagNodeTurn :one
UPDATE task_dag_nodes
SET active_turn_id = $1,
    last_event_at = NOW(),
    updated_at = NOW()
WHERE dag_key = $2
  AND node_key = $3
  AND run_id = $5
  AND $5::bigint > 0
  AND status = 'running'
  AND active_turn_id IS NULL
  AND active_wakeup_id = $4
RETURNING id, dag_key, node_key, run_id, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id;

-- name: TouchRunningTaskDagNodeEvent :one
UPDATE task_dag_nodes
SET last_event_at = $1,
    updated_at = NOW()
WHERE dag_key = $2
  AND node_key = $3
  AND run_id = $5
  AND $5::bigint > 0
  AND status = 'running'
  AND active_turn_id = $4
  AND (last_event_at IS NULL OR last_event_at < $1)
RETURNING id, dag_key, node_key, run_id, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id;

-- name: UpdateRunningTaskDagNodeStatus :one
UPDATE task_dag_nodes
SET status = $1, result = $2::jsonb, active_turn_id = NULL, active_wakeup_id = $3,
    last_event_at = NULL, started_at = COALESCE(started_at, NOW()), updated_at = NOW()
WHERE dag_key = $4 AND node_key = $5
  AND run_id = $6
  AND $6::bigint > 0
  AND status IN ('pending', 'ready')
RETURNING id, dag_key, node_key, run_id, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id;

-- name: UpdateAwaitingVerifyTaskDagNodeStatus :one
UPDATE task_dag_nodes
SET status = $1, result = $2::jsonb, active_turn_id = NULL, active_wakeup_id = NULL, updated_at = NOW()
WHERE dag_key = $3 AND node_key = $4
  AND run_id = $5
  AND $5::bigint > 0
  AND status IN ('running')
RETURNING id, dag_key, node_key, run_id, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id;

-- name: CompleteTaskDagNode :one
-- ADR-017 v1.2 §2.3 白名单扩 'ready'：DAG turn.completed subscriber
-- 可能在 dispatchAgent 写 running 之前报到 done（race window A，§2.6）。
-- 原白名单 IN ('running','awaiting_verify') 会让 subscriber 0 rows；
-- 扩后接受 ready→done 路径，是 race A 根本处理手段。
-- 其它调用者全是 running/awaiting_verify 状态过来，扩白名单仅允许更多路径进入，
-- 不破坏旧调用。
UPDATE task_dag_nodes
SET status = $1, result = $2::jsonb, active_turn_id = NULL, active_wakeup_id = NULL,
    finished_at = COALESCE(finished_at, NOW()), updated_at = NOW()
WHERE dag_key = $3 AND node_key = $4
  AND run_id = $5
  AND $5::bigint > 0
  AND status IN ('ready', 'running', 'awaiting_verify')
RETURNING id, dag_key, node_key, run_id, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id;
