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
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id;

-- name: UpdateTaskDagNodeStatusFlexible :one
-- 名字 "Flexible" 表示不附带 status 前置约束——调用方负责检查状态机合法转移。
-- 历史上曾与 UpdateTaskDagNodeStatus 两份 SQL 并存，但后者在 F4.2 / F6 后变成
-- 与本查询逻辑上完全重复的 dead code。R1 dead code 清理：仅保留 Flexible
-- 一项权威版本，store.UpdateNodeStatus 同样外调本 query。
UPDATE task_dag_nodes
SET status = $1, result = $2::jsonb, updated_at = NOW()
WHERE dag_key = $3 AND node_key = $4
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id;

-- name: ClaimTaskDagNodeOutputMaterialization :one
-- A2 turn.completed sharedfile writes have an external side effect. Claim the
-- node through a SQL status fence before writing sharedfile so stale duplicate
-- completions cannot write after another path already reached a terminal state.
-- awaiting_verify is accepted so a redelivered turn.completed can recover if
-- the prior attempt wrote sharedfile but hit a transient CompleteNode failure.
UPDATE task_dag_nodes
SET status = 'awaiting_verify', result = $1::jsonb, updated_at = NOW()
WHERE dag_key = $2
  AND node_key = $3
  AND status IN ('ready', 'running', 'awaiting_verify')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id;

-- name: FailTaskDagNodeIfNonTerminal :one
-- FailNodeAndCancelDownstream 的 primary-node fence：只允许非终态节点被改成 failed。
-- subscriber / dispatcher 都可能在 lookup 或 retry 决策后遇到并发终态推进；此处用
-- status 谓词作为最后一道原子护栏，避免 done/failed/cancelled/skipped 被迟到失败覆盖。
UPDATE task_dag_nodes
SET status = $1, result = $2::jsonb, updated_at = NOW()
WHERE dag_key = $3
  AND node_key = $4
  AND status NOT IN ('done', 'failed', 'cancelled', 'skipped')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id;

-- name: CascadeFailPendingTaskDagNode :execrows
-- fail_fast cascade 只认领仍处 pending 的下游。若并发路径已把下游推进到
-- done/failed/cancelled/skipped/running，本语句返回 0 rows，由调用方当作幂等 skip。
UPDATE task_dag_nodes
SET status = 'failed', result = $1::jsonb, updated_at = NOW()
WHERE dag_key = $2
  AND node_key = $3
  AND status = 'pending';

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
