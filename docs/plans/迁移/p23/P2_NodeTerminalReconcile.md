# P23.2: 节点终态回写 + timeout/retry/on_failure

> 创建时间：2026-04-25 | 状态：**未开动 - 依赖 P1**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)
> 本文件是 P23 子任务 stub；具体实现细节由 owner 在开工前补全。

## 目标

在 hook consumer 上装 tap：turn terminal → durable terminal event inbox/outbox → `dagReconcileActor` 消费 → 带 `active_turn_id` fence 的 `CompleteNode` / `MarkFailed` 回写。执行 `execution.timeout_sec` / `retry` / `on_failure` 与 `schedule.fail_fast`。**P2 reconcile tap 必须 enqueue-only**（P23 阶段 0 ⑤ 硬约束，是 P8 verifier gate 的硬前置）。

## 现状校准（事实层）

- hook consumer 链路：`internal/sidecar/orch/orchestration/hook_consumer.go:96-220`、`internal/sidecar/orch/orchestration/hook_consumer.go:148-151,260-275,285-287`
- hook 订阅入口：`cmd/mcp-orch/runtime.go:216-219` 的 `subscribeOrchestrationHooks` → `cmd/mcp-orch/hook_subscription.go:13-40`
- terminal 事件类型：`internal/dto/turn/event.go:10-21`（`TurnCompleted.Success/Result/Summary/Error`）
- 完成节点 SQL：`task_dag_node_runtime.sql:37-42`（`CompleteNode`）
- timeout/retry/on_failure 字段当前只存 JSON，无执行：`internal/sidecar/orch/tools/task_tools.go:120-121,269-284`

## 推荐架构

P2 只负责 durable terminal fact 与 fenced 状态推进，不依赖 P13 actor 先上线：

1. hook consumer tap 只做 bounded parse + durable terminal event insert；不在 callback 内执行 terminal SQL、retry、launcher 或 verifier。
2. durable event 必须携带 `dag_key,node_key,active_turn_id,attempt_no,event_type,terminal_kind,result_snapshot`；去重键为 `(dag_key,node_key,active_turn_id,event_type)`。
3. `dagReconcileActor` 消费 event 后执行 fenced CAS。`CompleteNode` / `MarkFailed` / `RetryNode` / `MarkObserveLost` 全部必须带 `active_turn_id + attempt_no`；0 rows 统一视为 stale terminal，不允许覆盖新 attempt 或已 terminal 状态。
4. `aborted` / `interrupted` / `timeout` 不扩主 `status`：若没有专用主状态，落 `status='failed'`，并在 `result.terminal_kind` 写 `aborted|interrupted|timeout`。late `completed` 因 fence 不匹配返回 0 rows，必须记 stale，不得改回 `done`。
5. P2 对 P13 只提供 extension hook：有 `output_schema` 时可在 feature gate 开启后把 terminal fact 转交 P13 output validation；P13 未启用时 P2 不被阻塞，但不得伪造 schema pass。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| terminal tap | `internal/sidecar/orch/orchestration/hook_consumer.go`、`internal/sidecar/orch/orchestration/dag_terminal_tap.go` | bounded parse + durable insert；禁止在 callback 内推进 node |
| durable event DDL/SQL | `migrations/0065_dag_state_machine.sql`（首选并入；拆分时仅允许 `0065a/0065b` no-conflict）+ `internal/sidecar/orch/sql/queries/task_dag_terminal_event.sql` | 去重键 `(dag_key,node_key,active_turn_id,event_type)`，携带 `attempt_no` / `terminal_kind` |
| reconcile actor | `internal/sidecar/orch/orchestration/dag_reconcile_actor.go`、`internal/sidecar/orch/store/taskdag/*` | fenced `CompleteNode/MarkFailed/RetryNode/MarkObserveLost`，0 rows 作为 stale terminal |

**已知关键改动方向**：
- 在 `hook_consumer.go` 的 `OnTurnCompleted` 链上装 enqueue tap，只做 bounded parse + durable insert terminal event；禁止纯内存队列承载 terminal 事件
- `dagReconcileActor` 消费 durable event：先按 `(dag_key,node_key,active_turn_id,event_type)` 去重，再 CAS `running → done | failed`，SQL 必带 `status=running AND active_turn_id=$turn_id AND attempt_no=$attempt_no`，0 rows 视为 stale terminal
- 带 `output_schema` 的 node 只落 durable terminal event + extension hook；`outputValidationActor` 分支由 P13 feature gate 接管，未启用 P13 时不阻塞 P2
- timeout 扫描循环：定期扫 `running` 节点 `started_at + timeout_sec < now()`，生成 `terminal_kind=timeout` 的 durable event，再用 `active_turn_id + attempt_no` CAS 落 `failed`
- retry 预算：`execution.retry > 0` 时 default 复用已绑定 agent 再投一轮 turn；`relaunch_on_retry=true` 才换 agent_id
- `last_activity_at` 在 hook tap 上同时回写（P7 预留消费）

## DDL / SQL

- 新增/复用 P0 `0065_dag_state_machine.sql` terminal event inbox/outbox：唯一键 `(dag_key,node_key,active_turn_id,event_type)`；hook `INSERT ... ON CONFLICT DO NOTHING`，actor 消费并标记 processed。P2 不再保留占位 migration；拆分仅允许 `0065a/0065b` no-conflict 方案。
- `task_dag_node_runtime.sql` 加 timeout 扫描 query + retry CAS query；`CompleteNode/MarkFailed/Retry/MarkObserveLost` 全部带 `active_turn_id` + `attempt_no` fence；0 rows 必须暴露给调用方作为 stale terminal 计数

## 依赖

- P1 已合入（dispatcher + launch binding）
- P21 Canonical Turn Observation Contract（terminal precedence / token snapshot / call_id→turn_id）已就位

## 风险

- enqueue-only tap：禁止在 callback 内做长跑 / 重 DB / 派生 launch（archtest 守）
- terminal precedence：`interrupted/aborted/timeout` 映射为 `status=failed + result.terminal_kind`，不能被 late `completed` 覆盖（参考 P21 §Canonical Turn Observation Contract）
- timeout 与 hook terminal 竞态：timeout 扫描期间 hook 也到达，CAS 必须只允许一方推进
- bounded queue 只允许承载可丢弃 progress/delta；terminal/reconcile/validation 必须 durable，不允许 silent drop

## 必测项

- hook tap enqueue-only 验证（archtest）
- terminal precedence（先 interrupt 后 completed 仍 interrupted）
- timeout 扫描 + late hook 竞态
- retry 复用 agent vs `relaunch_on_retry=true` 换 agent
- retry 后旧 turn late completed 返回 0 rows，不覆盖新 attempt
- P13 feature gate 开启后，output_schema 节点 invalid 时不得先写 `done` 再回滚；P13 未启用时 P2 只落 event/hook，不阻塞本阶段
- `last_activity_at` 在 hook 时被回写（为 P7 留位）

## 输入材料

- README §"core ↔ orch 事件链路的真实入口"
- README §阶段 0 ⑤ 扩展点契约（hook tap enqueue-only）
- p21 P1b 章节 §"Crash-window idempotency state machine"（参考状态机表达形式）



## terminal / retry / on_failure / fail_fast 真值表

| 输入事实 event | CAS fence | retry 剩余 | on_failure | fail_fast | P2 动作 |
|---|---|---:|---|---|---|
| completed(success) | `active_turn_id + attempt_no` match | 任意 | 任意 | 任意 | CAS `running → done`；若 P8 verify enabled，则由 P8 gate 消费并推进 `verify_phase` |
| completed(success) late | 0 rows | 任意 | 任意 | 任意 | 记 stale terminal，绝不覆盖当前 node |
| failed/error | match | > 0 | 任意 | 任意 | 先落 terminal fact，再 `RetryNode` 开新 attempt；retry 优先 |
| failed/error | match | 0 | continue | false | CAS `running → failed`，下游按依赖规则不 ready，其它分支可继续 |
| failed/error | match | 0 | fail | 任意 | CAS `running → failed`，依赖该 node 的下游停止 |
| failed/error | match | 0 | 任意 | true | CAS `running → failed`；fail_fast 停止新的 ready claim，不取消 running |
| aborted/interrupted/timeout | match | > 0 | 任意 | 任意 | 写 `status=failed` + `result.terminal_kind`，再按 retry 预算开新 attempt |
| aborted/interrupted/timeout | match | 0 | 任意 | 任意 | 写 `status=failed` + `result.terminal_kind='aborted|interrupted|timeout'` |
| observe_lost | match | > 0 | 任意 | 任意 | 写 durable observe_lost fact，再按 retry 预算开新 attempt |
| observe_lost | match | 0 | 任意 | 任意 | CAS 到主状态 `observe_lost`；late terminal 0 rows stale |

`verify.max_rounds` 与 `execution.retry` 分账：verify reject/repair 只扣 verify 轮次；执行失败、timeout、aborted/interrupted、observe_lost 才扣 execution retry。terminal CAS 永远先落事实，再按 retry/on_failure/fail_fast 决策派生后续动作。
