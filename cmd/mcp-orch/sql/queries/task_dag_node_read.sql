-- name: ListTaskDagNodes :many
SELECT id, dag_key, node_key, run_id, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id
FROM task_dag_nodes
WHERE dag_key = $1
  AND run_id IS NULL
ORDER BY created_at;

-- name: ListTaskDagRunNodes :many
SELECT id, dag_key, node_key, run_id, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id
FROM task_dag_nodes
WHERE dag_key = $1
  AND run_id = $2
ORDER BY created_at;

-- name: ListRunningTaskDagNodesByAssignee :many
SELECT id, dag_key, node_key, run_id, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id
FROM task_dag_nodes
WHERE assigned_to = $1 AND status = 'running'
ORDER BY created_at;

-- name: GetTaskDagNodesForUpdate :many
SELECT id, dag_key, node_key, run_id, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id
FROM task_dag_nodes
WHERE dag_key = $1
  AND run_id IS NULL
ORDER BY created_at, id
FOR UPDATE;

-- name: LookupNodesBySpawningThread :many
-- ADR-017 v1.2 §2.2 反查端口：DAG turn.completed subscriber 用 ev.ThreadID 反查
-- task_dag_nodes.spawning_thread_id；migration 0083 partial index
-- idx_task_dag_nodes_spawning_thread_id (WHERE spawning_thread_id IS NOT NULL)
-- 命中。
--
-- 返回 []TaskDagNode（不是 *TaskDagNode）— N>1 在重试/recovery 链下是常态
-- （partial index 无 UNIQUE + F1.5 写入端口非 single-writer），调用方逐条尝试推进。
SELECT id, dag_key, node_key, run_id, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id
FROM task_dag_nodes
WHERE spawning_thread_id = $1
  AND spawning_thread_id IS NOT NULL
ORDER BY updated_at DESC, id DESC;
