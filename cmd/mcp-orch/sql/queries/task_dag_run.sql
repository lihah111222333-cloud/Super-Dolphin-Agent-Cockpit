-- DAG v2 T1.2-mid: task_dag_runs CRUD + 根节点 promote 入口。
-- 蓝图 v2 §5 决策 C 混合：每次 StartDAG 创建一条 run；T1.2-mid 阶段单 DAG
-- 单 run，多并发 reject。F6.5 升级为 multi-run + 节点复制带 run_id。

-- name: CreateTaskDagRun :one
-- StartDAG 调用：插入新 run；run_key 由 service 层生成（保证 UNIQUE）。
-- dag_version_snapshot 由调用方从 task_dags.version 当前值传入，保证
-- run 创建后 DAG 模板被改不影响这次 run（Temporal 风格）。
INSERT INTO task_dag_runs (run_key, dag_key, dag_version_snapshot, trigger_source, status, metadata, budget_limit)
VALUES ($1, $2, $3, $4, 'running', $5::jsonb, $6)
RETURNING id, run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, finished_at, events, budget_used, budget_limit, metadata, created_at, updated_at;

-- name: GetTaskDagRun :one
SELECT id, run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, finished_at, events, budget_used, budget_limit, metadata, created_at, updated_at
FROM task_dag_runs
WHERE run_key = $1;

-- name: ListTaskDagRunsByKey :many
-- 按 dag_key + 可选 status 过滤；空 status 表示全部状态。
-- 与 ListTaskDags 风格一致：$2='' 当作未过滤。
SELECT id, run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, finished_at, events, budget_used, budget_limit, metadata, created_at, updated_at
FROM task_dag_runs
WHERE dag_key = $1
  AND ($2::text = '' OR status = $2)
ORDER BY started_at DESC, id DESC
LIMIT $3;

-- name: CountActiveTaskDagRunsByKey :one
-- StartDAG 用于多 run 并发 reject：当且仅当 0 时才允许新 run。
-- T1.2-mid 限制；F6.5 升级 multi-run 后此 query 不再被 StartDAG 调用。
SELECT COUNT(*)::bigint AS active
FROM task_dag_runs
WHERE dag_key = $1 AND status = 'running';

-- name: PromoteRootNodesToReady :execrows
-- StartDAG 在新 run 创建后调用：把 dag_key 下所有 depends_on=[] 且 status='pending'
-- 的根节点提升为 'ready'。返回受影响行数（service 层用于断言至少一个根节点被
-- 提升，否则视为 DAG 无可执行起点）。
-- 状态机：pending → ready 是合法转移（见 nodeexec/types.go ValidTransitions）。
UPDATE task_dag_nodes
SET status = 'ready',
    updated_at = NOW()
WHERE dag_key = $1
  AND status = 'pending'
  AND jsonb_array_length(depends_on) = 0;
