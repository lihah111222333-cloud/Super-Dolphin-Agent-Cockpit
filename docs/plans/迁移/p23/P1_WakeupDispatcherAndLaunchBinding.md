# P23.1: wakeup dispatcher + node→launch 绑定

> 创建时间：2026-04-25 | 状态：**未开动 - 依赖 P0**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)
> 本文件是 P23 子任务 stub；具体实现细节由 owner 在开工前补全。

## 目标

实现 `dagDispatcherActor`：消费 wakeup 队列 → 调共享 launcher 起 agent 或 submit turn → 回写 `assigned_agent_id` + bind `active_turn_id`。launcher 与 `orchestration_launch_agent` 共用底层 `launchAgentViaLauncher`，不另起一路。

## 现状校准（事实层）

- 共享 launcher 入口：`internal/sidecar/orch/tools/orchestration_tools.go:38-57` → `internal/sidecar/orch/orchestration/service.go:299-301` → `internal/sidecar/orch/orchestration/service_launcher_bridge.go:54-64`
- prompt 自动投递路径：`internal/sidecar/orch/orchestration/service_launcher_bridge.go:89-119`（已确认 first-turn 路径存在）
- launcher：`internal/sidecar/orch/orchestration/service_launcher_bridge.go` 当前无固定并发上限
- wakeup 表 SQL：`migrations/0023_dag_watcher_phase1.sql:9-30`、`internal/sidecar/orch/sql/queries/task_dag_wakeup_query.sql`
- node→agent 绑定 SQL：`task_dag_node_runtime.sql:1-11`（绑定 turn 需 running + wakeup fence）

## 推荐架构

Dispatcher 只接管 P0 watcher claim 后的 launch/bind 半程，采用 durable launch-intent 状态机：

1. watcher 已把 node CAS 到 `running` 并写入 `active_wakeup_id BIGINT`；dispatcher 不重新计算 ready，不把 node 回退到 `pending`。
2. dispatcher claim wakeup 后，在短事务内 upsert `dag_launch_intents`，唯一幂等键为 `(dag_key,node_key,attempt_no,wakeup_id BIGINT)` 或其 deterministic hash `idempotency_key`。
3. `launch_intent` 已持久化后才允许调用外部 launcher；DB 事务不得包住 launcher 调用。
4. 共享 launcher wrapper 返回 accepted 结构 `{agent_id, thread_id, turn_id, accepted_at}`（或返回 deterministic `ExpectedTurnID` 且后续 first-turn 必须使用它）后，dispatcher 调 `BindRunningNodeTurn` CAS：`WHERE dag_key=? AND node_key=? AND status='running' AND active_wakeup_id=? AND attempt_no=? AND active_turn_id=''`，写入 `assigned_agent_id` / `active_turn_id`。
5. `BindRunningNodeTurn` 0 rows 不是重试覆盖信号；必须读取 node 当前 fence，若已绑定同 idempotency_key 的 turn 则 ack，否则标记 stale/conflict 交 reconcile。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| dispatcher actor | `internal/sidecar/orch/orchestration/dag_dispatcher_actor.go` | 消费 due wakeup，持久化 launch intent，调用 launcher，执行 `BindRunningNodeTurn` |
| launch intent DDL/SQL | `migrations/0065_dag_state_machine.sql`（首选并入；拆分时仅允许 `0065a/0065b` no-conflict）+ `internal/sidecar/orch/sql/queries/task_dag_launch_intent.sql` | `idempotency_key` 唯一、status 枚举、crash recovery 查询 |
| store/service contract | `internal/sidecar/orch/store/taskdag/*`、`internal/sidecar/orch/orchestration/dag_launch_intent.go` | `UpsertLaunchIntent` / `MarkLauncherAccepted` / `BindRunningNodeTurn` / stale conflict 处理 |

**已知关键改动方向**：
- `dagDispatcherActor.Run(ctx)` 主循环：`ClaimDueWakeups` → 持久化 `launch_intent`（deterministic idempotency key）→ 调 launcher 或 submit turn（幂等返回 `{agent_id, thread_id, turn_id, accepted_at}` 或 deterministic `ExpectedTurnID`）→ `BindRunningNodeTurn` CAS → `MarkWakeupSent/acked`
- 新增 `nodes[].launch` schema → launcher 调用：把 launch spec 映射到 `LaunchAgent` request
- `assigned_agent_id/active_turn_id` 不能假装在 launcher 前已知；P1 采用三阶段可恢复协议：`launch_intent` 持久化 + deterministic idempotency key → 外部 launcher 幂等调用 → `BindRunningNodeTurn` CAS 写入 `assigned_agent_id/active_turn_id`（除非 `relaunch_on_retry=true`，否则不可覆盖）
- launcher 如需容量治理，应走后续显式 quota 设计，不能恢复硬编码并发上限

## DDL / SQL

新增 `dag_launch_intents`（或等价 outbox 表），字段冻结：

| 字段 | 契约 |
|---|---|
| `launch_intent_id TEXT PRIMARY KEY` | deterministic，可由 `idempotency_key` 派生 |
| `idempotency_key TEXT NOT NULL UNIQUE` | 至少覆盖 `dag_key,node_key,attempt_no,wakeup_id`（其中 `wakeup_id BIGINT`）；dispatcher/launcher 重试共用 |
| `dag_key TEXT NOT NULL` / `node_key TEXT NOT NULL` | 目标 node |
| `attempt_no INTEGER NOT NULL` | 与 node 当前 attempt fence 一致 |
| `wakeup_id BIGINT NOT NULL` | 必须等于 node.`active_wakeup_id`；禁止 TEXT，现有 `task_dag_wakeups.id BIGSERIAL` / store int64 是权威类型 |
| `status TEXT NOT NULL` | `persisted` / `launcher_accepted` / `agent_started` / `turn_bound` / `failed` / `stale` |
| `launcher_request JSONB NOT NULL` | redacted launch spec snapshot |
| `assigned_agent_id TEXT NOT NULL DEFAULT ''` | launcher accepted 后写 |
| `thread_id TEXT NOT NULL DEFAULT ''` | launcher wrapper accepted response 必须返回，不从 hook 反推 |
| `active_turn_id TEXT NOT NULL DEFAULT ''` | launcher wrapper accepted response 必须返回 `turn_id`，或返回 deterministic `ExpectedTurnID` 并保证实际 first turn 使用该 id；禁止等 hook 到达后猜测 |
| `accepted_at TIMESTAMPTZ` | launcher accepted 时间，来自 wrapper 返回结构 |
| `launcher_error TEXT NOT NULL DEFAULT ''` | failed/stale 说明 |
| `created_at/updated_at/accepted_at/bound_at/failed_at TIMESTAMPTZ` | recovery 与审计 |

`status` 枚举含义：

- `persisted`：intent 已落库，尚未确认 launcher accepted；crash recovery 重新用同一 `idempotency_key` 调 launcher 或查询 launcher 幂等结果。
- `launcher_accepted`：launcher 接受请求并返回 `{agent_id, thread_id, turn_id, accepted_at}` 或 deterministic `ExpectedTurnID`，但 node 尚未 CAS bind；recovery 直接重放 `BindRunningNodeTurn`。
- `agent_started`：可选中间态，表示 agent/session 已启动但 first turn 尚未绑定；recovery 继续读取/补投 turn，不能创建第二个 agent。
- `turn_bound`：`BindRunningNodeTurn` CAS 成功，wakeup 可 ack。
- `failed`：launcher 明确失败或超过 dispatcher retry，交 P2/P7 reconcile 以 fenced failure/observe_lost 处理。
- `stale`：node fence 已不匹配（例如 retry 开新 attempt 或已 terminal），不得覆盖当前 node。

`task_dag_node_runtime.sql` 必须新增 dispatcher query：`ClaimDueWakeups`、`UpsertLaunchIntent`、`MarkLaunchIntentAccepted`、`MarkLaunchIntentAgentStarted`、`BindRunningNodeTurn`、`MarkLaunchIntentBound`、`MarkLaunchIntentFailed/Stale`、`AckWakeup`。所有 query 分步 CAS；外部 launcher 调用不在 DB 事务内。

## 依赖

- P0 已合入（4 actor 骨架 + state machine CAS）
- 共享 launcher 路径不变

## 风险

- launcher 异步成功 ≠ agent 真起来：必须区分 `launch_intent persisted` / `launcher accepted` / `agent started` / `turn bound` / `failed`；crash 在任一阶段都必须能用 idempotency key 恢复或读取既有 turn，不得重复 launch
- `dag_ref` 反向绑定 vs watcher 主动 push 的双触发：dispatcher 必须查 `active_turn_id != ''` 或 intent `turn_bound` 直接 noop/ack（不重 launch，不覆盖 `assigned_agent_id`）
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



## watcher / dispatcher SQL 契约摘要

- watcher: `ClaimReadyNodes` 只做 `pending → running`、`attempt_no+1`、`active_wakeup_id BIGINT`；不得写 `assigned_agent_id` / `active_turn_id`。
- dispatcher: `BindRunningNodeTurn` 是唯一写 `assigned_agent_id` / `active_turn_id` 的 P1 SQL；必须带 `active_wakeup_id BIGINT` + `attempt_no` CAS。
- recovery: 先按 `idempotency_key` 查 intent，再查 launcher 幂等结果，最后才决定重放 bind 或标记 failed/stale；禁止盲目再 launch。

## DDL 编号与 wakeup 类型冻结

P1 不再保留占位 migration。`dag_launch_intents` 首选并入 P0 `0065_dag_state_machine.sql`；若实现 PR 必须拆分，只能采用 `0065a_dag_launch_intents.sql` / `0065b_dag_terminal_events.sql` 这类 no-conflict 命名，并在 PR 描述贴 migrations 最大编号校准。`wakeup_id` 与 `active_wakeup_id` 全链路为 `BIGINT`/Go `int64`，对应既有 `task_dag_wakeups.id BIGSERIAL`；任何 TEXT wakeup fence 方案禁止。
