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
- `task_dag_node_runtime.sql` 加 CAS 形 SQL（语义为 `WHERE status = <expected_prev_status>`，并在 terminal 写入中同时校验 `active_turn_id`）
- archtest：`dag_watcher_no_lifecycle_loop` / `dag_runner_actors_present` / `dag_status_cas_only`

## DDL / SQL

**0063_dag_state_machine.sql** 草案（待 owner 细化）：
- `task_dag_nodes` 加 `assigned_agent_id TEXT NOT NULL DEFAULT ''`
- `task_dag_nodes` 加 `last_activity_at TIMESTAMPTZ`（P23 阶段 0 ⑤ 预留）
- `task_dag_nodes.status` 加 P0 基础 CHECK 约束 `('pending','running','done','failed','observe_lost')`；仅 P8 可在 0067 forward-only 扩 terminal `verdict_lost`，其它后段不得扩主 status

## 依赖

- 阶段 0 最小 checklist 全部完成（migration 编号 / state machine 契约 / RunnerModule 角色 / trigger enum / 扩展点契约 + compliance checklist）
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

## 待办

- **strict_state_machine 默认 true（a2）**：新 DAG 不允许降级；旧 DAG 兼容期 false 必须打 audit + deprecation。
- **terminal 唯一键（a2+a5）**：P0/P2 必须新增 durable terminal event inbox/outbox；去重键冻结为 `(dag_key,node_key,turn_id,event_type)`，`INSERT ... ON CONFLICT DO NOTHING` 后由 actor 消费，同一 terminal 只能生效一次。
- **wakeup TTL / GC DDL（a2+a5）**：补 wakeup 状态机 `pending/claimed/sent/acked/expired`、`claim_owner`、`claim_expires_at`、reclaim query、TTL、FK/归档级联校验，P1/P11/P12 上线前必须闭环。
- **真实 allowlist 收紧（a1 ❌）**：收紧 `internal/archtest/dependency_direction_mcp_orch_test.go:23-29`，不得继续宽泛放行 `internal/store` / `internal/module`。
- **当前实现只存不跑（a1+a2）**：P0 合入前必须明确 UI/RPC 文案“DAG 仅存储不执行”；P0 PR 必须同时落 `StartDAG` 最小入口、4 actor wiring、trigger enum fail-fast。
- **EnqueueWakeup 返回值修正（a2）**：`EnqueueTaskDagWakeup` 不能返回 execrows 给调用方当 wakeup id；改为返回真实 wakeup id，冲突时查询既有 id，避免 `active_wakeup_id` fence 误绑。
- **状态 DDL 最后防线（a5）**：`0063` 必须给 `task_dag_nodes.status` 加 CHECK；P8 扩 `verdict_lost` 只能通过后续 migration 扩 CHECK，子状态不得混入主 `status`。
- **last_activity_at vs last_event_at（a5+a6）**：P7 活性字段与 P21 event ordering 字段分离；若沿用 `last_event_at`，必须改名/语义冻结，不能让 P7 误杀长工具调用。
- **P0/P1 首版性能硬门（a3）**：ready claim 第一版必须采用 `FOR UPDATE SKIP LOCKED LIMIT K`；`remaining_deps` 或等价依赖计数前移到 `0063_dag_state_machine.sql`，禁止先全量 load DAG nodes 再内存过滤。

## Runner inventory 扩展冻结（需求补全仲裁）

P0 PR 只实现 4 个基础 actor，但 `dag_runner_actors_present` 的 inventory 必须从第一版覆盖全生命周期：P0 `dagWatcherActor/dagDispatcherActor/dagLeaseActor/dagReconcileActor`，P7 `dagActivityActor`，P8 `dagArbiterActor`，P11 `dagConvergenceActor`，P12 `dagSwarmArbiterActor`，P13 `outputValidationActor`。后段 actor 可先以 stub/compile-time symbol 占位，但不得绕过 `group:"runners"`、`Runner.Run(ctx)`、drain、heartbeat metric 与 no-fire-and-forget archtest。
