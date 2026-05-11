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

-- (CountActiveTaskDagRunsByKey query 已随 L3 根治删除。原作为 service.StartDAG
--  多 run 并发 reject 预检，有 TOCTOU race。L3 后该约束被 0076 partial
--  unique 下沉到 DB 兑底，应用层预检不再需要，删除避免未来再写 race。
--  历史代码 store/sqlc/task_dag_run.sql.go 中的生成代码作为 dead code、
--  留待未来 sqlc realignment 一并清理。不手工删生成代码以免下次
--  sqlc generate 反复。)

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

-- name: FinalizeTaskDagRunIfAllNodesTerminal :many
-- F6.2: run 终态判定 — 当 dag_key 下所有 task_dag_nodes.status 已进入终态
-- (done/failed/cancelled/skipped) 时，按优先级把 status='running' 的 run
-- 推进到对应终态并写 finished_at；否则 0 行受影响、run 保持 'running'。
--
-- 优先级（含义：什么 status 占主导）：
--   1. 任意节点 failed                 → run.status = 'failed'
--   2. 否则任意节点 cancelled          → run.status = 'cancelled'
--   3. 否则全部 done / skipped         → run.status = 'succeeded'
--   4. 还有非终态(pending/ready/running/retrying/waiting_human) → final_status=NULL
--      UPDATE WHERE 子句因此不命中，run 保持 'running'
--
-- F6.2: When every task_dag_node under dag_key has reached a terminal status
-- (done/failed/cancelled/skipped), flip the matching 'running' run row to the
-- aggregated terminal status with the priority above and set finished_at.
-- Otherwise the statement is a no-op and the run stays 'running'.
--
-- 与 0080 status CHECK 对齐：枚举锁定 running|succeeded|failed|cancelled，
-- final_status 取值都在白名单内、不会触发 CHECK 违例。
--
-- 单 DAG 单 run（0076 partial unique on (dag_key) WHERE status='running'）：
-- WHERE r.dag_key=$1 AND r.status='running' 命中至多 1 行；F6.5 升级
-- multi-run + 节点带 run_id 后，本 query 需带 run_id 参数定位目标 run。
WITH node_counts AS (
  SELECT
    COUNT(*) FILTER (WHERE status NOT IN ('done','failed','cancelled','skipped')) AS non_terminal,
    COUNT(*) FILTER (WHERE status = 'failed')    AS failed_cnt,
    COUNT(*) FILTER (WHERE status = 'cancelled') AS cancelled_cnt,
    COUNT(*)                                     AS total
  FROM task_dag_nodes
  WHERE dag_key = $1
),
final AS (
  SELECT CASE
    WHEN total = 0        THEN NULL
    WHEN non_terminal > 0 THEN NULL
    WHEN failed_cnt > 0   THEN 'failed'
    WHEN cancelled_cnt > 0 THEN 'cancelled'
    ELSE 'succeeded'
  END AS final_status
  FROM node_counts
)
UPDATE task_dag_runs r
SET status      = (SELECT final_status FROM final),
    finished_at = NOW(),
    updated_at  = NOW()
WHERE r.dag_key = $1
  AND r.status = 'running'
  AND (SELECT final_status FROM final) IS NOT NULL
RETURNING r.run_key, r.status;

-- name: AppendTaskDagRunEvent :one
-- F1.5: 把一个 JSON event append 到 task_dag_runs.events 数组。
--
-- 场景：AgentExecutor spawn 子 agent 时，如果是重试（旧 spawning_thread_id
-- 已存在），把 {kind: "node_spawn", node_key, prev_thread_id, thread_id, ts}
-- 落到 events 数组保留历史链。详 ADR-009 §3 / §5 Q4。
--
-- 当前实现：jsonb_build_array($2::jsonb) 包成单元素数组再 || 拼接，强制
-- 走 PG jsonb 的「array append」语义；events 列默认 '[]'::jsonb（参见
-- migration 0074）。然后通过 CASE 包一层环形截断（仅保留最近 50 条）。
--
-- 工艺修复（R1 P0 #1，W3）：早期写法 `events || $2::jsonb` 看起来是 array append，
-- 但当 bind 端误传 object（kind/node_key 等顶层 JSON object）时 PG `||` 走
-- 「object concat」(merge by key) 而非 append，导致历史链静默被合并/覆盖。
-- jsonb_build_array() 强制把右操作数包成数组，无论 bind 类型都保证 array
-- append 语义；同 store 层调用方也保持原 payload（单条 object），不需双重数组。
--
-- 环形截断（R1 P1 #5 + R2 P0 #2，W2）：events 列若不限长会无界增长，单条 run 高频重试
-- 时尾部能膨胀到几十 KB jsonb 数据，拖累 task_get_run 列表 / Detail 渲染。本 query
-- 在 append 后保留**最近 50 条**事件——CASE 内 jsonb_array_length 判断 + 截尾片段
-- 走 jsonb_array_elements WITH ORDINALITY 重组。50 是经验值，覆盖典型 retry 链
-- （单节点 7 次重试 × 多节点）仍留富余；阈值需要调整时改 50 字面常量即可。
--
-- WHERE 用 dag_key + status='running' 定位 0076 partial unique 保证的唯一 running run；
-- 0 行返回（无 running run）调用方静默吞，不视为错误。
UPDATE task_dag_runs
SET events     = CASE
        WHEN jsonb_array_length(events || jsonb_build_array($2::jsonb)) <= 50
            THEN events || jsonb_build_array($2::jsonb)
        ELSE COALESCE((
            SELECT jsonb_agg(elem ORDER BY ord)
            FROM jsonb_array_elements(events || jsonb_build_array($2::jsonb)) WITH ORDINALITY AS t(elem, ord)
            WHERE ord > jsonb_array_length(events || jsonb_build_array($2::jsonb)) - 50
        ), '[]'::jsonb)
    END,
    updated_at = NOW()
WHERE dag_key = $1 AND status = 'running'
RETURNING run_key;

