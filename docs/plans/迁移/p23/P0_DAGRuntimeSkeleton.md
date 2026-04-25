# P23.0: DAG 自驱运行时骨架

> 创建时间：2026-04-25 | 状态：**未开动**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)
> 本文件是 P23 子任务 stub；具体实现细节由 owner 在开工前补全。

## 目标

把 DAG 模块从「只存不跑」升级为「自驱执行」：watcher / ready 计算器 / `pending → running` CAS 调度器 + 冻结 4 actor 角色（`dagWatcherActor` / `dagDispatcherActor` / `dagLeaseActor` / `dagReconcileActor`）。

## 现状校准（事实层）

- watcher 进程不存在：`cmd/mcp-orch/tools/task_tools.go:23,231-235`、`cmd/mcp-orch/orchestration/dag.go:109-131`
- 节点状态机原语（半成品）：`cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql:24-29,37-42`、`cmd/mcp-orch/store/taskdag/store.go:145-155,168-177`
- wakeup 表 + fence（半成品）：`migrations/0023_dag_watcher_phase1.sql:9-30`、`cmd/mcp-orch/store/taskdag/store_wakeup.go:9-104`
- worker lease（半成品）：`migrations/0023_dag_watcher_phase1.sql:38-43`、`cmd/mcp-orch/store/taskdag/store_lease.go:9-45`
- ready 判定逻辑不存在：`cmd/mcp-orch/orchestration/dag.go:230,323-331`（只存/读 JSON）

## 推荐架构

> 待 owner 在开工前补全；约束以 [`README.md`](README.md) §"当前基线约束" / §"默认值安全原则" / §"收口口径" 为准。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| _待补_ | _待补_ | _待补_ |

**已知关键改动方向**：
- 新增 `cmd/mcp-orch/orchestration/runtime/<actor>.go` 4 个 actor，挂入 `runner.actors`（active Fx tag: `group:"runners"`）
- `0063_dag_state_machine.sql`：加 `assigned_agent_id` 列、`last_activity_at` 列（P7 预留）、状态机 CHECK 约束
- `task_dag_node_runtime.sql` 加 CAS 形 SQL（`WHERE current_status = $expected`）
- archtest：`dag_watcher_no_lifecycle_loop` / `dag_runner_actors_present` / `dag_status_cas_only`

## DDL / SQL

**0063_dag_state_machine.sql** 草案（待 owner 细化）：
- `task_dag_node` 加 `assigned_agent_id TEXT NOT NULL DEFAULT ''`
- `task_dag_node` 加 `last_activity_at TIMESTAMPTZ`（P23 阶段 0 ⑤ 预留）
- `task_dag_node.status` 加 CHECK 约束 `('pending','running','done','failed','observe_lost')`

## 依赖

- 阶段 0 三件冻结全部完成（migration 编号 / state machine 契约 / RunnerModule 角色）
- P22 archtest 守卫已落地（runtime ownership 不允许 callback 内长跑）

## 风险

- watcher 重入：用 `FOR UPDATE SKIP LOCKED` + idempotency key 防双推进
- crash recovery：`running + assigned_agent_id != ''` 不允许自动回退 `running → pending`
- 半成品 wakeup / lease 字段语义需在 owner 启动前与现有设计核对（README §"风险" 第 1 条）

## 必测项

- 4 actor lifecycle 测试（start / stop / iteration tick / error 各产指标）
- CAS 重入测试（双 watcher 抢 ready node，只能一方推进成功）
- crash recovery：注入 `running + assigned_agent_id` 但 hook 永久丢失，断言推进 `observe_lost` 而非重 launch

## 输入材料

- README §阶段 0：前置冻结（编号校准 + state machine + RunnerModule + trigger 枚举 + 扩展点契约）
- `dag-runtime-audit` 报告（[`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) §1 摘要）
- `migrations/0023_dag_watcher_phase1.sql` 半成品 wakeup / lease / fence 设计意图


