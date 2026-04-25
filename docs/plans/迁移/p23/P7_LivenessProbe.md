# P23.7: 心跳式节点活性监控（Liveness Probe）

> 创建时间：2026-04-25 | 状态：**未开动（后段子任务，依赖 P0/P1/P2）**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)
> 本文件是 P23 子任务 stub；具体实现细节由 owner 在开工前补全。

## 目标

DAG 每 N 分钟探测 running node 上 agent 有无新动向（工具调用、turn progress、消息）；超阈值无活动 → 自动 relaunch（区分同 agent 重投 / 换 agent 重起）。新增 `dagActivityActor`（第 5 个 actor）。**后段子任务**：必须在 P0/P1/P2 合入后开工。

## 现状校准（事实层）

- P23 当前 4 actor 不覆盖活性探针：`docs/plans/迁移/p23/README.md`（实施路线图 P0–P3 行）
- 可作活性信号的事件：
  - `internal/dto/turn/event.go:42-64`（`TurnOutputDelta/Started/InputReceived/Stalled/Resumed`）
  - `internal/dto/turn/progress.go:24-54`（`ItemStarted/ItemCompleted`）
  - `internal/dto/tool/event.go:9-61`（`ToolCallBegin/End/Approval/Diff`）
  - hook subscriptions：`cmd/mcp-orch/orchestration/hook_consumer.go:20-26`
- 已走 hook：`TurnCompleted/Interrupted` + 仅 final-answer `ItemCompleted`（`hook_consumer.go:285-287`、`event_relay.go:64-86`）
- **未走 hook**：非 final `ItemCompleted` / `ItemStarted` / `TurnOutputDelta` / 工具事件 / 客户端 stdout
- p21 P1b 续租先例：`internal/module/cron/lease_actor.go:12-16`、`internal/module/cron/progress_subscriber.go:19-20`、`internal/module/cron/scheduler.go:708-724`
- 已有近似字段：`migrations/0023_dag_watcher_phase1.sql:1-4`（`last_event_at`）
- P23 阶段 0 ⑤ 已预留 `last_activity_at TIMESTAMPTZ` 列

## 推荐架构

> 待 owner 在开工前补全；约束以 [`README.md`](README.md) §"当前基线约束" / §"默认值安全原则" / §"收口口径" 为准。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| _待补_ | _待补_ | _待补_ |

**已知关键改动方向**：
- 新增 `dagActivityActor`（runner.actors 第 5 个）：定期扫 `running` 节点 `last_activity_at` 老化
- P2 hook tap 扩展：`ToolCallBegin/End` / `TurnOutputDelta` 等事件回写 `last_activity_at` + `last_activity_kind`
- 两种 relaunch 语义：
  - (a) 同 `agent_id` 复用 thread 重投 turn（保留上下文，仅当原 agent 仍可达）
  - (b) 换 `agent_id` 重起新 agent（CAS 推进 status，复用 P23 `relaunch_on_retry`）
- 长工具调用反误杀：`ToolCallBegin` 标记"工具运行中"，使用 `tool_idle_timeout_sec` 双阈值
- 字段：`schedule.default_idle_timeout_sec` + `nodes[].execution.idle_timeout_sec` + `nodes[].execution.tool_idle_timeout_sec`

## DDL / SQL

- 不新增表
- 可能扩 `task_dag_node` 加 `last_activity_kind TEXT` 列

## 依赖

- P0 / P1 / P2 全部合入
- `last_activity_at` 列已就位（P23 阶段 0 ⑤ 已预留）
- P21 Canonical Turn Observation Contract 已就位

## 风险

- 长工具调用误杀（要靠 `tool_idle_timeout_sec` 双阈值，不能单纯 idle_timeout）
- 与 P8 `pending_verify / verifying / repairing` 子状态共存：禁止把"等待 verifier"误判为 idle（P23 README §三子任务叠加冲突缓解契约 第 1 条）
- N=1000 全表扫描 `last_activity_at`：必须分片 + lease jitter（P23 README §三子任务叠加冲突缓解契约 第 2 条）
- relaunch 与 reject/repair 共用 CAS fence，禁止双推进

## 必测项

- 长 `code_run` 5 min 不被误杀（fixture：`ToolCallBegin` 后 5 min 无新事件，`tool_idle_timeout_sec=600` 拒杀）
- 普通 turn 沉默 10 min 触发 relaunch
- 同 agent 重投 vs 换 agent 重起两种 fixture
- `pending_verify / verifying` 节点不被误杀
- 分片扫描 + jitter（N=1000 不形成 DB 周期性尖峰）

## 输入材料

- README §"P7 心跳式节点活性监控（Liveness Probe）"
- `gap-liveness` 报告（[`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) §1）
- p21 P1b 活 turn 续租设计


