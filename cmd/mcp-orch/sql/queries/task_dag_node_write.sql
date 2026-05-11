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

-- name: PromoteSingleNodePendingToReady :execrows
-- F6.3: 节点完成后自动 promote 单个下游 pending 节点到 ready。
-- 调用方（store_complete_downstream.scheduleDownstreamWakeupsTx）已在 Go 侧
-- 计算过依赖满足条件（dependsOnIncludes + allDependenciesSatisfied）；本 SQL
-- 用 status='pending' 作为最后一道幂等护栏，避免并发场景下重复 promote
-- 或对已经 running/done 的节点误改。
--
-- 状态机：pending → ready 合法（见 nodeexec/status.go legalTransitions）。
-- 与 PromoteRootNodesToReady 的区别：那条是 StartDAG 时按 depends_on=[] 批量
-- promote 根节点；本条是 CompleteNode 后按 (dag_key, node_key) 精准 promote。
UPDATE task_dag_nodes
SET status = 'ready',
    updated_at = NOW()
WHERE dag_key = $1
  AND node_key = $2
  AND status = 'pending';

