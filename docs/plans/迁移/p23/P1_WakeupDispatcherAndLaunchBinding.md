# P23.1: wakeup dispatcher + node→launch 绑定

> 创建时间：2026-04-25 | 状态：**未开动 - 依赖 P0**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)
> 本文件是 P23 子任务 stub；具体实现细节由 owner 在开工前补全。

## 目标

实现 `dagDispatcherActor`：消费 wakeup 队列 → 调共享 launcher 起 agent 或 submit turn → 回写 `assigned_agent_id` + bind `active_turn_id`。launcher 与 `orchestration_launch_agent` 共用底层 `launchAgentViaLauncher`，不另起一路。

## 现状校准（事实层）

- 共享 launcher 入口：`cmd/mcp-orch/tools/orchestration_tools.go:38-57` → `cmd/mcp-orch/orchestration/service.go:299-301` → `cmd/mcp-orch/orchestration/service_launcher_bridge.go:54-64`
- prompt 自动投递路径：`cmd/mcp-orch/orchestration/service_launcher_bridge.go:89-119`（已确认 first-turn 路径存在）
- launcher 并发上限：`cmd/mcp-orch/orchestration/service_launcher_bridge.go:22-30`（`maxConcurrentLaunches=10`）
- wakeup 表 SQL：`migrations/0023_dag_watcher_phase1.sql:9-30`、`cmd/mcp-orch/sql/queries/task_dag_wakeup_query.sql`
- node→agent 绑定 SQL：`task_dag_node_runtime.sql:1-11`（绑定 turn 需 running + wakeup fence）

## 推荐架构

> 待 owner 在开工前补全；约束以 [`README.md`](README.md) §"当前基线约束" / §"默认值安全原则" / §"收口口径" 为准。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| _待补_ | _待补_ | _待补_ |

**已知关键改动方向**：
- `dagDispatcherActor.Run(ctx)` 主循环：`ClaimDueWakeups` → 持久化 `launch_intent`（deterministic idempotency key）→ 调 launcher 或 submit turn（幂等返回 agent/turn）→ `BindRunningNodeTurn` CAS → `MarkWakeupSent/acked`
- 新增 `nodes[].launch` schema → launcher 调用：把 launch spec 映射到 `LaunchAgent` request
- `assigned_agent_id/active_turn_id` 不能假装在 launcher 前已知；P1 采用三阶段可恢复协议：`launch_intent` 持久化 + deterministic idempotency key → 外部 launcher 幂等调用 → `BindRunningNodeTurn` CAS 写入 `assigned_agent_id/active_turn_id`（除非 `relaunch_on_retry=true`，否则不可覆盖）
- launcher 并发上限提取成 config 参数（P23 阶段 0 ⑤）

## DDL / SQL

- 复用 P0/P1 落地的 wakeup + `launch_intent` 持久化字段/表；不得把外部 launcher 调用包进 DB 长事务
- `task_dag_node_runtime.sql` 加 dispatcher 用 query：claim wakeup、记录 launch intent、bind agent_id/turn_id、ack wakeup 分步 CAS

## 依赖

- P0 已合入（4 actor 骨架 + state machine CAS）
- 共享 launcher 路径不变

## 风险

- launcher 异步成功 ≠ agent 真起来：必须区分 `launch_intent persisted` / `launcher accepted` / `turn bound` / `agent failed` 四态；crash 在任一阶段都必须能用 idempotency key 恢复或读取既有 turn，不得重复 launch
- `dag_ref` 反向绑定 vs watcher 主动 push 的双触发：dispatcher 必须查 `assigned_agent_id != ''` 直接 noop（不重 launch）
- prompt 投递偶发不触发的历史观察（参考会话 2026-04-25 早期记录）需在 owner 启动前确认是否复现

## 必测项

- 单 wakeup 单 launch（基线）
- crash-window fixture：intent 后 launcher 前、launcher 后 bind 前、bind 后 ack 前均可恢复且不重 launch
- 双 dispatcher 抢同一 wakeup（CAS / SKIP LOCKED 不重 launch）
- launcher 异步成功但 agent 启动失败 → reconcile 推进 `failed`
- `dag_ref` 反向绑定 noop（已 bound 节点不重 launch）

## 输入材料

- README §"`dag_ref` / `nodes[].launch` 协同（authoritative）"
- README §"实施路线图" P1 行
- `launch-param-design-2` 报告（[`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) 早期参数设计调研）


