-- DAG v2 F1.5: task_dag_nodes.spawning_thread_id 写入入口（ADR-009）。
--
-- 设计要点：
--   - UPDATE 语义：覆盖最新一次 spawn 出来的 child thread id。
--     重试场景下旧值进 task_dag_runs.events（调用方负责追加 node_spawn 事件，
--     见 store_run_event.go AppendNodeSpawnEvent），本 query 只负责覆盖。
--   - RETURNING 同时带出新旧两份 spawning_thread_id：用 CTE 先 SELECT 旧值，
--     再 UPDATE 拿新值，最后 LEFT JOIN 合并返回。省一次 round-trip：
--       - 旧值（previous_spawning_thread_id）进 task_dag_runs.events 形成历史链；
--       - 新值（spawning_thread_id）由调用方 sanity check。
--   - WHERE 只排除终态：F1.4 智能重试可能把节点先置为 'retrying' 再 spawn，
--     不应被窄 status 闸门挡住；但 DAG run 终止后，迟到的 spawn 写回必须
--     fence miss，让调用方停止刚启动的 child thread。
--   - 与 migration 0083 partial index (WHERE spawning_thread_id IS NOT NULL)
--     协同：UPDATE 覆盖时索引自动维护，无需额外操作。

-- name: UpdateTaskDagNodeSpawningThread :one
WITH old AS (
  SELECT spawning_thread_id AS previous_spawning_thread_id
  FROM task_dag_nodes AS n
  WHERE n.dag_key = $2 AND n.node_key = $3
    AND n.run_id = $4
    AND $4::bigint > 0
),
updated AS (
  UPDATE task_dag_nodes AS n
  SET spawning_thread_id = $1,
      updated_at = NOW()
  WHERE n.dag_key = $2 AND n.node_key = $3
    AND n.run_id = $4
    AND $4::bigint > 0
    AND n.status NOT IN ('done', 'failed', 'cancelled', 'skipped')
  RETURNING n.id, n.dag_key, n.node_key, n.run_id, n.title, n.node_type, n.assigned_to, n.depends_on,
            n.status, n.command_ref, n.config, n.result, n.started_at, n.finished_at,
            n.created_at, n.updated_at, n.active_turn_id, n.active_wakeup_id,
            n.last_event_at, n.spawning_thread_id
)
SELECT updated.id, updated.dag_key, updated.node_key, updated.run_id, updated.title,
       updated.node_type, updated.assigned_to, updated.depends_on,
       updated.status, updated.command_ref, updated.config, updated.result,
       updated.started_at, updated.finished_at, updated.created_at,
       updated.updated_at, updated.active_turn_id, updated.active_wakeup_id,
       updated.last_event_at, updated.spawning_thread_id,
       old.previous_spawning_thread_id
FROM updated LEFT JOIN old ON TRUE;
