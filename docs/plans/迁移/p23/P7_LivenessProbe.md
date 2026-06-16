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
  - hook subscriptions：`internal/sidecar/orch/orchestration/hook_consumer.go:20-26`
- 已走 hook：`TurnCompleted/Interrupted` + 仅 final-answer `ItemCompleted`（`hook_consumer.go:285-287`、`event_relay.go:64-86`）
- **未走 hook**：非 final `ItemCompleted` / `ItemStarted` / `TurnOutputDelta` / 工具事件 / 客户端 stdout
- p21 P1b 续租先例：`internal/module/cron/lease_actor.go:12-16`、`internal/module/cron/progress_subscriber.go:19-20`、`internal/module/cron/scheduler.go:708-724`
- 已有近似字段：`migrations/0023_dag_watcher_phase1.sql:1-4`（`last_event_at`）
- P23 阶段 0 ⑤ 已预留 `last_activity_at TIMESTAMPTZ` 列

## 推荐架构

`dagActivityActor` 只做活性观察与 relaunch 决策，不绕过 P0/P1/P2 state machine。heartbeat 写入来自 hook tap registry 的 activity provider（不是主 hook 分发内联逻辑）的 canonical turn/tool/progress 事件，统一落 `last_activity_at`、`last_activity_kind`、`activity_state`、`heartbeat_seq`、`last_heartbeat_at`，并带 `active_turn_id + attempt_no` fence。默认 cadence：hook 事件实时 coalesce 写；actor probe 每 `probe_interval_sec` 扫 `next_probe_at <= now()` 的 running nodes，默认 interval 60s、jitter ±20%、batch 100（owner 可调但必须 schema 化）。

P9 backpressure 信号是前置要求：hook/launcher/DB lag 超阈值时，P7 只能延后 `next_probe_at` 并打 metric，不 relaunch、不写终态。P9 promhttp 指标未接通时，必须先提供 scheduled-audit fallback（周期性把 lag/queue depth 写 audit/health row）供 P7 判断，否则 P7 自动 relaunch feature gate 关闭。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| activity actor | `internal/sidecar/orch/orchestration/runtime/activity_actor.go` [NEW] | 第 5 个 `Runner.Run(ctx)` actor；按 `next_probe_at` 分片扫描 running nodes；遵守 P9 backpressure |
| hook activity tap provider | `internal/sidecar/orch/orchestration/hook_tap_registry.go` + `internal/sidecar/orch/orchestration/activity_tap.go` [NEW] | 注册 activity provider；Turn/tool/progress heartbeat coalesce 写 `last_activity_*`，terminal 事件 durable；不直接扩主 hook 分发 |
| SQL runtime | `internal/sidecar/orch/sql/queries/task_dag_node_runtime.sql`（扩展） | fenced update：`last_activity_at/kind/state/tool_call_id/tool_started_at/heartbeat_seq/next_probe_at` |
| schema provider | `internal/sidecar/orch/tools/dag_schema_registry.go` + `internal/sidecar/orch/tools/dag_schema_activity.go` [NEW] | 注册 `probe_interval_sec`、`idle_timeout_sec`、`tool_idle_timeout_sec`、batch/jitter/backoff 字段；不直接并行改 `task_tools.go` |
| metrics/backpressure | P9 promhttp metrics or scheduled-audit fallback | 读取 hook lag、launcher lag、DB p99；缺指标时关闭 relaunch |
| archtest | `internal/archtest/dag_activity_actor_test.go` [NEW] | 确认 actor 注册、CAS fence、禁止 watcher 直接写 `observe_lost` |

**已知关键改动方向**：
- 新增 `dagActivityActor`（runner.actors 第 5 个）：定期扫 `running` 节点 `last_activity_at` 老化
- P2 hook tap registry 扩展：由 P7 activity provider 订阅 `ToolCallBegin/End` / `TurnOutputDelta` 等事件并回写 `last_activity_at` + `last_activity_kind`；主 hook consumer 只做 registry fanout/enqueue
- 两种 relaunch 语义：
  - (a) 同 `agent_id` 复用 thread 重投 turn（保留上下文，仅当原 agent 仍可达）
  - (b) 换 `agent_id` 重起新 agent（CAS 推进 status，复用 P23 `relaunch_on_retry`）
- 长工具调用反误杀：`ToolCallBegin` 标记"工具运行中"，使用 `tool_idle_timeout_sec` 双阈值
- 字段：`schedule.default_idle_timeout_sec` + `nodes[].execution.idle_timeout_sec` + `nodes[].execution.tool_idle_timeout_sec`

## DDL / SQL

- P7 不新增独立表；但若 `last_activity_at` 之外的 activity 字段不能由既有列承载，必须申请独立 forward migration，不得把字段塞进无约束 JSONB 后绕过 fence
- 最小列候选：`last_activity_kind`、`activity_state`、`tool_call_id`、`tool_started_at`、`last_heartbeat_at`、`heartbeat_seq`、`last_relaunch_at`、`relaunch_count`、`next_probe_at`；全部写入必须带 `active_turn_id`/attempt fence 或等价 CAS

## 依赖

- P0 / P1 / P2 全部合入
- `last_activity_at` 列已就位（P23 阶段 0 ⑤ 已预留）
- P21 Canonical Turn Observation Contract 已就位

## 风险

- 长工具调用误杀（要靠 `tool_idle_timeout_sec` 双阈值，不能单纯 idle_timeout）
- 与 P8 `awaiting_verify / verifying / repairing` 子状态共存：禁止把"等待 verifier"误判为 idle（P23 README §三子任务叠加冲突缓解契约 第 1 条）
- N=1000 全表扫描 `last_activity_at`：必须分片 + lease jitter（P23 README §三子任务叠加冲突缓解契约 第 2 条）
- relaunch 与 reject/repair 共用 CAS fence，禁止双推进

## 必测项

- 长 `code_run` 5 min 不被误杀（fixture：`ToolCallBegin` 后 5 min 无新事件，`tool_idle_timeout_sec=600` 拒杀）
- 普通 turn 沉默 10 min 触发 relaunch
- 同 agent 重投 vs 换 agent 重起两种 fixture
- `awaiting_verify / verifying` 节点不被误杀
- 分片扫描 + jitter（N=1000 不形成 DB 周期性尖峰）

## 输入材料

- README §"P7 心跳式节点活性监控（Liveness Probe）"
- `gap-liveness` 报告（[`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) §1）
- p21 P1b 活 turn 续租设计

## 活性判定硬契约（需求补全仲裁）

P7 不得只凭一次 idle 超时 relaunch。最小字段/状态：`last_activity_at`、`last_activity_kind`、`activity_state`、`tool_call_id`、`tool_started_at`、`last_relaunch_at`、`relaunch_count`、`next_probe_at`，并全部带 `active_turn_id`/attempt fence。

| 判定输入 | 结果 |
|---|---|
| `verify_phase in ('awaiting_verify','verifying','repairing')` 或 `output_validation_phase!=''` | 不 relaunch，由 P8/P13 owner 推进 |
| tool running 且 `now-tool_started_at < tool_idle_timeout_sec` | 不 relaunch，只刷新 activity |
| hook lag / DB lag 超阈值 | 不 relaunch，触发 backpressure/alert |
| 连续 K 轮无 activity 且 agent reachable | same-agent resubmit，消耗 activity relaunch budget |
| 连续 K 轮无 activity 且 agent unreachable / turn lookup 不可恢复 | `dagReconcileActor` 裁决 `observe_lost` 或 new-agent relaunch，不能由 watcher 直接写终态 |

必须有全局/tenant/DAG kill switch、cooldown、per-node/per-DAG window 上限、launcher backlog gate。`observe_lost` 只允许 `dagReconcileActor` 写；watcher 只负责 pending→running/claim。缺省 `idle_timeout_sec=0` 表示不启用自动 relaunch，非 0 才继承 `schedule.default_idle_timeout_sec`；`tool_idle_timeout_sec` 必须大于等于 `idle_timeout_sec`。

### observe_lost CAS / probe 调度补充（第四轮仲裁）

`dagReconcileActor` 写 `observe_lost` 必须使用 CAS 条件：`status='running' AND active_turn_id=$turn_id AND attempt_no=$attempt_no`（或等价 fence）；0 rows affected 视为 stale，不得重试覆盖。new-agent relaunch 与 `observe_lost` 必须竞争同一 CAS fence，二者只能成功一个。

P7 schema 必须冻结 `probe_interval_sec`、`next_probe_at`、batch size、lease jitter 与 backoff 规则；默认 `idle_timeout_sec=0` 仍表示关闭自动 relaunch。`next_probe_at` 每次 probe 后按 interval+jitter 计算，hook/DB/launcher lag 触发 P9 backpressure 时只延后 probe，不写终态。heartbeat 字段最小集为 `last_activity_at`（业务活动时间）、`last_heartbeat_at`（观测写入时间）、`heartbeat_seq`（单调递增/幂等去重）、`last_activity_kind`（turn_delta/tool_begin/tool_end/progress/stdout/terminal）、`activity_state`（idle_watch/tool_running/relaunching/backpressured），更新 cadence 为 hook 实时 coalesce + actor probe 默认 60s±20% jitter；promhttp 指标或 scheduled-audit fallback 未就绪时，自动 relaunch 不得启用。
