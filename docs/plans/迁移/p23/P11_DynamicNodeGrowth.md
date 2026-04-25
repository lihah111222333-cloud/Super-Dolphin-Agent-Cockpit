# P23.11: 自动节点伸缩 / 无限迭代（Dynamic Node Growth）

> 创建时间：2026-04-25 | 状态：**未开动（后段子任务，依赖 P0/P1/P2 + P9 backpressure）**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) §裁决 8
> 用户 2026-04-25 提出：「自动节点伸缩（无限迭代）」

## 目标

让 DAG 从「创建即固定结构」升级为「运行中可动态生长」：执行中的 agent 可声明"我需要再加 N 个 child node 继续迭代"，DAG runtime 在合规预算内 spawn 新 node 并接入依赖图。支持「自动迭代一个系统」直到收敛条件成立。**后段子任务**。

## 现状校准（事实层）

- 当前 DAG 是**静态结构**：`task_create_dag` 一次性 upsert 全部 node + 依赖（`cmd/mcp-orch/orchestration/dag.go:109-131`）；创建后 schema 不可改
- 当前**无** `spawn_child` 动作：node 只能 done / failed / observe_lost（P23 阶段 0 ②）
- 当前**无** 资源预算 / cap 字段：DAG 级只有 `schedule.max_concurrency`（控并发，**不**控 node 总数）
- 当前**无** 收敛条件 / 终止条件字段：DAG 永不终止由所有节点 done 触发（隐式）

## 推荐架构

### 核心机制：runtime spawn child node

agent 在执行中通过 hook（或专用 tool）声明 spawn 请求 → DAG runtime 在事务内：
1. 验证当前 node 仍 `running`、未越过 `growth_budget`
2. 在 `task_dag_node` 插入新 node，`depends_on` 指向当前 node 或其它已存在 node
3. 触发 `dagWatcherActor` 重新计算 ready 集合
4. 新 node 按 P0/P1/P2 标准路径推进

### 增长预算（防爆系统）

DAG schema 加：
- `dag.growth_budget.max_total_nodes`（DAG 级总 node 数硬上限）
- `dag.growth_budget.max_depth`（递归深度上限，避免无限链）
- `dag.growth_budget.max_spawn_per_node`（单 node fork 出的 child 上限）
- `dag.growth_budget.max_runtime_sec`（DAG 总运行时长上限）

任何超 budget 的 spawn → `ErrGrowthBudgetExceeded` + 触发 DAG `growth_capped` 事件（用户可调高 budget 后续跑）

### 收敛 / 终止条件

DAG schema 加：
- `dag.convergence.condition_template`（结构化条件，例如 `{kind: "all_done", filter: {...}}` / `{kind: "fixed_point", check_node: "..."}` / `{kind: "external_signal", channel: "..."}`）
- `dag.convergence.timeout_action`（达到 `max_runtime_sec` 时：`graceful_stop` 完成已 running / `hard_stop` 立即 abort / `mark_partial_success`）

### 入口：`task_spawn_child` 工具 + `dag/spawn` RPC

新增 MCP 工具 `task_spawn_child`（agent 调用）：
- 输入：`dag_key`、`from_node_key`、`new_nodes[]`（与 `task_create_dag` 的 nodes 子集格式一致）、`depends_on_extras`
- 输出：`{spawned_node_keys[], remaining_budget}`
- 鉴权：调用方必须是 `from_node_key.assigned_agent_id` 或 DAG owner

新增 RPC `dag/spawn(...)`：UI / 外部调度也能 spawn（P10 + P6 路径）

### 与 P9 协同：背压触发动态降速

`dagWatcherActor` 在 ready 计算时若发现 `pending_node_count + running_node_count > growth_budget * 0.8`，自动暂停接受新 spawn 请求（返 `ErrApproachingBudget`），让 agent 知道要"收敛"。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| schema | `cmd/mcp-orch/tools/task_tools.go` + `cmd/mcp-orch/orchestration/dag.go` | 新增 `dag.growth_budget` + `dag.convergence` schema |
| MCP 工具 | `cmd/mcp-orch/tools/task_tools.go` [扩展] | `task_spawn_child` 工具 + handler |
| RPC | `cmd/mcp-orch/orchestration/rpc.go` [扩展] | `dag/spawn` method |
| service | `cmd/mcp-orch/orchestration/dag.go` [扩展] | `SpawnChildNodes(ctx, ...)` 函数：事务内插入 + 预算校验 |
| state machine | `task_dag_node_runtime.sql` [扩展] | `growth_capped` DAG 级标记；node 级无新状态 |
| watcher | `cmd/mcp-orch/orchestration/runtime/watcher_actor.go` [扩展] | spawn 后重算 ready；预算预警 |
| convergence | `cmd/mcp-orch/orchestration/runtime/convergence_evaluator.go` [NEW] | 周期性评估 `convergence.condition_template`；触发 DAG 终态 |
| DDL | `0069_dag_dynamic_growth.sql` [NEW]（编号校准） | `task_dag` 加 `growth_budget JSONB` + `convergence JSONB`；统计列 `total_node_count` + `growth_depth` |
| archtest | `internal/archtest/dag_growth_test.go` [NEW] | spawn 必须经 `SpawnChildNodes` 入口；不允许直接 INSERT node |

## DDL / SQL

**`0069_dag_dynamic_growth.sql`** 草案（编号校准）：

```sql
ALTER TABLE public.task_dag ADD COLUMN growth_budget JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE public.task_dag ADD COLUMN convergence JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE public.task_dag ADD COLUMN total_node_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE public.task_dag ADD COLUMN growth_depth INTEGER NOT NULL DEFAULT 0;
ALTER TABLE public.task_dag ADD COLUMN growth_capped BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE public.task_dag_node ADD COLUMN spawned_by_node_key TEXT NOT NULL DEFAULT '';
ALTER TABLE public.task_dag_node ADD COLUMN spawned_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_task_dag_node_spawned_by
    ON public.task_dag_node (dag_key, spawned_by_node_key)
    WHERE spawned_by_node_key <> '';
```

## 依赖

- P0 / P1 / P2 全部合入（state machine + ready 计算 + reconcile）
- P9 backpressure 已就位（spawn × 增长 × 规模会形成倍数压力，必须复用 P9 的全局 token bucket + 队列退避）

## 风险

- **无限增长爆系统**：`growth_budget` 是硬约束，**必须**在 spawn 入口拦截；archtest 守
- **递归深度爆栈**：`max_depth` 防止 child 又生成 child 又生成 child 的无限链
- **agent 滥用 spawn**：恶意 / bug agent 可能频繁 spawn 把额度吃掉；建议 budget 默认值保守 + 用户明确提高才放宽
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
- 与 P12 swarm 协同：swarm verifier spawn 也吃 budget

## 输入材料

- README §"P11 自动节点伸缩 / 无限迭代"
- 用户原话：「自动节点伸缩（无限迭代）」（2026-04-25）
- 类比：Letta / autogen / babyagi 的迭代 agent 设计可作背景参考（owner 自调研）
