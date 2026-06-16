# P23.11: 自动节点伸缩 / 无限迭代（Dynamic Node Growth）

> 创建时间：2026-04-25 | 状态：**未开动（后段子任务，依赖 P0/P1/P2 + P9 backpressure）**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) §裁决 8
> 用户 2026-04-25 提出：「自动节点伸缩（无限迭代）」

## 目标

让 DAG 从「创建即固定结构」升级为「运行中可动态生长」：执行中的 agent 可声明"我需要再加 N 个 child node 继续迭代"，DAG runtime 在合规预算内 spawn 新 node 并接入依赖图。支持「自动迭代一个系统」直到收敛条件成立。**后段子任务**。

## 现状校准（事实层）

- 当前 DAG 是**静态结构**：`task_create_dag` 一次性 upsert 全部 node + 依赖（`internal/sidecar/orch/orchestration/dag.go:109-131`）；创建后 schema 不可改
- 当前**无** `spawn_child` 动作：node 只能 done / failed / observe_lost（P23 阶段 0 ②）
- 当前**无** 资源预算 / cap 字段：DAG 级只有 `schedule.max_concurrency`（控并发，**不**控 node 总数）
- 当前**无** 收敛条件 / 终止条件字段：DAG 永不终止由所有节点 done 触发（隐式）

## 推荐架构

### 核心机制：runtime spawn child node

agent 在执行中通过 hook（或专用 tool）声明 spawn 请求 → DAG runtime 在事务内：
1. 验证当前 node 仍 `running`、未越过 `growth_budget`
2. 在 `task_dag_nodes` 插入新 node，`depends_on` 指向当前 node 或其它已存在 node
3. 触发 `dagWatcherActor` 重新计算 ready 集合
4. 新 node 按 P0/P1/P2 标准路径推进

### 增长预算（防爆系统）

DAG schema 加：
- `dag.growth_budget.max_total_nodes`（DAG 级总 node 数硬上限）
- `dag.growth_budget.max_depth`（递归深度上限，避免无限链）
- `dag.growth_budget.max_spawn_per_node`（单 node fork 出的 child 上限）
- `dag.convergence.max_runtime_sec` 或 `dag.execution_budget.max_runtime_sec`（DAG 总运行时长上限；不放入 structural `growth_budget`）

任何超 budget 的 spawn → `ErrGrowthBudgetExceeded` + 触发 DAG `growth_capped` 事件（用户可调高 budget 后续跑）

### 收敛 / 终止条件

DAG schema 加：
- `dag.convergence.condition_template`（v1 只允许 `{kind: "all_done"}` / `{kind: "no_ready_and_no_running"}` / `{kind: "max_iterations"}` / `{kind: "external_signal"}`；`fixed_point` 属于后续 opt-in，需要 P8/P12 cost gate）
- `dag.convergence.timeout_action`（达到 `max_runtime_sec` 时：`graceful_stop` 完成已 running / `hard_stop` 立即 abort / `mark_partial_success`）

### 入口：`task_spawn_child` 工具 + `dag/spawn` RPC

新增 MCP 工具 `task_spawn_child`（agent 调用）：
- 输入：`dag_key`、`from_node_key`、`new_nodes[]`（与 `task_create_dag` 的 nodes 子集格式一致）、`depends_on_extras`
- 输出：`{spawned_node_keys[], remaining_budget}`
- 鉴权：调用方必须是 `from_node_key.assigned_agent_id` 或 DAG owner

新增 RPC `dag/spawn(...)`：UI / 外部调度也能 spawn（P10 + P6 路径）

### 与 P9 协同：背压触发动态降速

`dagWatcherActor` 在 ready 计算时若发现 `total_node_count / growth_budget.max_total_nodes >= 0.8`（或 ledger charged count 达 80%），自动暂停接受新 spawn 请求（返 `ErrApproachingBudget`），让 agent 知道要"收敛"。不得使用 `pending+running` 作为预算分母。动作对齐 P9 matrix：80% 只拒新 spawn，100% hard stop；owner 调高预算并 audit 后 watcher replay。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| schema provider | `internal/sidecar/orch/tools/dag_schema_registry.go` + `internal/sidecar/orch/tools/dag_schema_growth.go` [NEW] | 注册 `dag.growth_budget` + `dag.convergence` schema；`task_tools.go` / `dag.go` 只消费 registry |
| MCP 工具 | `internal/sidecar/orch/tools/task_tools.go`（薄 handler） + growth schema provider | `task_spawn_child` 工具；handler 只做 registry 校验并调用 `SpawnChildNodes` |
| RPC registrar | `internal/sidecar/orch/orchestration/rpc_growth.go` [NEW] | `registerDAGGrowthRPC` 注册 `dag/spawn`；`rpc.go` 只调用 registrar |
| service | `internal/sidecar/orch/orchestration/dag_spawn.go` [NEW] | `SpawnChildNodes(ctx, ...)` 函数：事务内 reservation + 预算校验 + 插入；不膨胀 `dag.go` |
| state machine | `task_dag_node_runtime.sql` [扩展] | `growth_capped` DAG 级标记；node 级无新状态 |
| watcher | `internal/sidecar/orch/orchestration/runtime/watcher_actor.go` [扩展] | spawn 后重算 ready；预算预警 |
| convergence actor | `internal/sidecar/orch/orchestration/runtime/convergence_actor.go` [NEW] | Runner actor 周期性评估 `convergence.condition_template`；触发 DAG 终态 |
| DDL | `0071_dag_dynamic_growth.sql` [NEW]（编号校准） | `task_dags` 加 structural `growth_budget JSONB` + `convergence JSONB`；统计列 `total_node_count` + `max_observed_growth_depth` |
| archtest | `internal/archtest/dag_growth_test.go` [NEW] | spawn 必须经 `SpawnChildNodes` 入口；不允许直接 INSERT node |

### schema / RPC write-set 拆分

P11 不直接并行改 `task_tools.go` 的 schema 段，也不直接追加 `rpc.go` case。growth 字段通过 `internal/sidecar/orch/tools/dag_schema_registry.go` 的 growth provider 注册；RPC 通过 `registerDAGGrowthRPC`；动态生长服务落 `dag_spawn.go`；长跑收敛逻辑落 `internal/sidecar/orch/orchestration/runtime/convergence_actor.go`。

## DDL / SQL

**`0071_dag_dynamic_growth.sql`** 草案（编号校准）：

```sql
ALTER TABLE public.task_dags ADD COLUMN growth_budget JSONB NOT NULL DEFAULT '{}'::jsonb; -- 空对象仅表示未启用 growth；spawn-enabled DAG 中 `{}` 必须 schema invalid
ALTER TABLE public.task_dags ADD COLUMN convergence JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE public.task_dags ADD COLUMN total_node_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE public.task_dags ADD COLUMN max_observed_growth_depth INTEGER NOT NULL DEFAULT 0;
ALTER TABLE public.task_dags ADD COLUMN growth_capped BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE public.task_dag_nodes ADD COLUMN spawned_by_node_key TEXT NOT NULL DEFAULT '';
ALTER TABLE public.task_dag_nodes ADD COLUMN spawned_at TIMESTAMPTZ;
ALTER TABLE public.task_dag_nodes ADD COLUMN growth_depth INTEGER NOT NULL DEFAULT 0;
ALTER TABLE public.task_dag_nodes ADD COLUMN spawned_child_count INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_task_dag_node_spawned_by
    ON public.task_dag_nodes (dag_key, spawned_by_node_key)
    WHERE spawned_by_node_key <> '';

CREATE TABLE IF NOT EXISTS public.dag_growth_reservations (
    reservation_id TEXT PRIMARY KEY,
    dag_key TEXT NOT NULL,
    from_node_key TEXT NOT NULL,
    requested_nodes INTEGER NOT NULL,
    reserved_nodes INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('reserved','committed','rolled_back','expired')),
    idempotency_key TEXT NOT NULL,
    spawned_node_keys JSONB NOT NULL DEFAULT '[]'::jsonb,
    parent_spawn_count_before INTEGER NOT NULL DEFAULT 0,
    parent_spawn_count_after INTEGER NOT NULL DEFAULT 0,
    total_node_count_before INTEGER NOT NULL DEFAULT 0,
    total_node_count_after INTEGER NOT NULL DEFAULT 0,
    created_by_agent_id TEXT NOT NULL,
    expires_at TIMESTAMPTZ,
    committed_at TIMESTAMPTZ,
    rolled_back_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (dag_key, idempotency_key),
    CHECK (requested_nodes > 0),
    CHECK (reserved_nodes >= 0 AND reserved_nodes <= requested_nodes)
);

CREATE INDEX IF NOT EXISTS idx_dag_growth_reservations_reconcile
    ON public.dag_growth_reservations (status, expires_at)
    WHERE status = 'reserved';
```

## 依赖

- P0 / P1 / P2 全部合入（state machine + ready 计算 + reconcile）
- P9 backpressure 已就位（spawn × 增长 × 规模会形成倍数压力，必须复用 P9 的全局 token bucket + 队列退避）

## 风险

- **无限增长爆系统**：`growth_budget` 是硬约束，**必须**在 spawn 入口拦截；空 `{}` 对 spawn-enabled DAG 视为 invalid，不得解释为 unlimited；默认 structural cap 必须保守（如 max_total_nodes/max_depth/max_spawn_per_node 均非空），运行时长上限必须在 convergence/execution budget 中非空
- **递归深度爆栈**：`max_depth` 防止 child 又生成 child 又生成 child 的无限链
- **agent 滥用 spawn**：恶意 / bug agent 可能频繁 spawn 把额度吃掉；必须有 budget ledger/reservation CAS（`UPDATE ... WHERE total_node_count + n <= max_total_nodes` 或等价 ledger）后再插 node，失败回滚；用户明确提高才放宽
- **依赖循环**：spawn 时新 node 的 `depends_on` 必须 archtest 守不能形成环（DAG 拓扑校验）
- **与 P10 模板**：模板里的 `growth_budget` / `convergence` 必须支持参数化；模板版本演进时已实例化任务 budget 不被覆盖
- **与 P12 蜂群涌现**：swarm verdict 也可能 spawn 新 verifier；spawn budget 必须包含 P12 的 swarm overhead
- **与 P9 规模**：N=1000 base + 动态增长可能到 N=10000；P9 的 partial index / token bucket 必须容量化设计
- **收敛条件错误导致死循环**：`convergence.condition_template` 必须有 `max_runtime_sec` 兜底，超时强制终止

## 必测项

- agent spawn child（`task_spawn_child` 工具，从 running node 起）
- spawn 超 `max_total_nodes` → `ErrGrowthBudgetExceeded`
- spawn 形成依赖环 → `ErrCircularDependency`
- 递归 spawn 到 `max_depth` → 拒绝
- DAG 总运行时超 `max_runtime_sec` → `convergence.timeout_action` 生效
- 收敛条件成立 → DAG 终态推进
- backpressure：80% budget 时返 `ErrApproachingBudget`
- 并发 spawn reservation：两个 agent 同时 spawn 时不得突破 `max_total_nodes/max_depth/max_spawn_per_node`
- spawn 插入 child 后同事务 enqueue wakeup/outbox；crash 后 watcher 可恢复
- 与 P12 swarm 协同：swarm verifier spawn 也吃 budget

## 输入材料

- README §"P11 自动节点伸缩 / 无限迭代"
- 用户原话：「自动节点伸缩（无限迭代）」（2026-04-25）
- 类比：Letta / autogen / babyagi 的迭代 agent 设计可作背景参考（owner 自调研）

## growth ledger / convergence 硬契约（需求补全仲裁）

### DDL / SQL 补充

`0071_dag_dynamic_growth.sql` 必须补 `dag_growth_reservations`（或等价 ledger）：`reservation_id`、`dag_key`、`from_node_key`、`requested_nodes`、`reserved_nodes`、`status`、`idempotency_key`、`created_by_agent_id`、`expires_at`、`committed_at`、`rolled_back_at`、`created_at`。`task_dag_nodes` 增 `growth_depth INTEGER NOT NULL DEFAULT 0` 或等价 lineage/path，否则 `max_depth` 不可事务内校验。

Spawn 流程：先用 CAS reservation 更新 `total_node_count`（或 ledger charged count），再插 nodes，再同事务 enqueue wakeup/outbox；重复 idempotency key 返回同一 `spawned_node_keys`。失败必须 rollback reservation 或进入 reconcile。

并发防爆细节：`max_spawn_per_node` 必须事务内校验，不能先 count 后 insert。实现可选 `SELECT ... FOR UPDATE` 锁定 parent node 统计列，或维护 `spawned_child_count` 并执行 `UPDATE task_dag_nodes SET spawned_child_count = spawned_child_count + $n WHERE dag_key=$dag AND node_key=$parent AND spawned_child_count + $n <= max_spawn_per_node AND active_turn_id=$turn_id RETURNING ...`；0 rows 即 `ErrGrowthBudgetExceeded` / stale。`dag_growth_reservations` 必须记录 `max_total_nodes_before/after`、`parent_spawn_count_before/after` 或等价审计字段，确保 crash reconcile 能判断 reservation 是否已 charged。child insert 与 parent count/ledger commit 必须同事务；不得用仅应用内 mutex 防并发。

### backpressure / budget 公式

80% backpressure 基于 `total_node_count / growth_budget.max_total_nodes` 或 ledger charged count，不用 `pending+running`。预算类型分层：structural node budget、execution/repair budget、LLM spend budget、storage/audit budget；token bucket 只控速率，budget ledger 控 hard stop。

### convergence DSL v1

只允许 `all_done`、`no_ready_and_no_running`、`max_iterations`、`external_signal` 四类确定性条件。`fixed_point` 若需要 LLM/自定义 evaluator，必须走 P8/P12 cost gate 并单独 opt-in。`dag/update_budget` RPC/UI 必须记录 owner/tenant 权限、approval、audit；调低预算不得破坏已运行节点，调高后可唤醒 watcher。
