# P23.2: 节点终态回写 + timeout/retry/on_failure

> 创建时间：2026-04-25 | 状态：**未开动 - 依赖 P1**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md)
> 本文件是 P23 子任务 stub；具体实现细节由 owner 在开工前补全。

## 目标

在 hook consumer 上装 tap：turn terminal → durable terminal event inbox/outbox → `dagReconcileActor` 消费 → 带 `active_turn_id` fence 的 `CompleteNode` / `MarkFailed` 回写。执行 `execution.timeout_sec` / `retry` / `on_failure` 与 `schedule.fail_fast`。**P2 reconcile tap 必须 enqueue-only**（P23 阶段 0 ⑤ 硬约束，是 P8 verifier gate 的硬前置）。

## 现状校准（事实层）

- hook consumer 链路：`cmd/mcp-orch/orchestration/hook_consumer.go:96-220`、`cmd/mcp-orch/orchestration/hook_consumer.go:148-151,260-275,285-287`
- hook 订阅入口：`cmd/mcp-orch/runtime.go:216-219` 的 `subscribeOrchestrationHooks` → `cmd/mcp-orch/hook_subscription.go:13-40`
- terminal 事件类型：`internal/dto/turn/event.go:10-21`（`TurnCompleted.Success/Result/Summary/Error`）
- 完成节点 SQL：`task_dag_node_runtime.sql:37-42`（`CompleteNode`）
- timeout/retry/on_failure 字段当前只存 JSON，无执行：`cmd/mcp-orch/tools/task_tools.go:120-121,269-284`

## 推荐架构

> 待 owner 在开工前补全；约束以 [`README.md`](README.md) §"当前基线约束" / §"默认值安全原则" / §"收口口径" 为准。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| _待补_ | _待补_ | _待补_ |

**已知关键改动方向**：
- 在 `hook_consumer.go` 的 `OnTurnCompleted` 链上装 enqueue tap，只做 bounded parse + durable insert terminal event；禁止纯内存队列承载 terminal 事件
- `dagReconcileActor` 消费 durable event：先按 `(dag_key,node_key,turn_id,event_type)` 去重，再 CAS `running → done | failed`，SQL 必带 `status=running AND active_turn_id=$turn_id`，0 rows 视为 stale terminal
- 带 `output_schema` 的 node 必须先转入 P13 `outputValidationActor`，validation pass 后才可 terminal CAS 或进入 P8 verify
- timeout 扫描循环：定期扫 `running` 节点 `started_at + timeout_sec < now()`
- retry 预算：`execution.retry > 0` 时 default 复用已绑定 agent 再投一轮 turn；`relaunch_on_retry=true` 才换 agent_id
- `last_activity_at` 在 hook tap 上同时回写（P7 预留消费）

## DDL / SQL

- 新增/复用 P0 terminal event inbox/outbox：唯一键 `(dag_key,node_key,turn_id,event_type)`；hook `INSERT ... ON CONFLICT DO NOTHING`，actor 消费并标记 processed
- `task_dag_node_runtime.sql` 加 timeout 扫描 query + retry CAS query；`CompleteNode/MarkFailed/Retry` 全部带 `active_turn_id` / `attempt_no` fence

## 依赖

- P1 已合入（dispatcher + launch binding）
- P21 Canonical Turn Observation Contract（terminal precedence / token snapshot / call_id→turn_id）已就位

## 风险

- enqueue-only tap：禁止在 callback 内做长跑 / 重 DB / 派生 launch（archtest 守）
- terminal precedence：`interrupted/aborted` 不能被 late `completed` 覆盖（参考 P21 §Canonical Turn Observation Contract）
- timeout 与 hook terminal 竞态：timeout 扫描期间 hook 也到达，CAS 必须只允许一方推进
- bounded queue 只允许承载可丢弃 progress/delta；terminal/reconcile/validation 必须 durable，不允许 silent drop

## 必测项

- hook tap enqueue-only 验证（archtest）
- terminal precedence（先 interrupt 后 completed 仍 interrupted）
- timeout 扫描 + late hook 竞态
- retry 复用 agent vs `relaunch_on_retry=true` 换 agent
- retry 后旧 turn late completed 返回 0 rows，不覆盖新 attempt
- output_schema 节点 invalid 时不得先写 `done` 再回滚
- `last_activity_at` 在 hook 时被回写（为 P7 留位）

## 输入材料

- README §"core ↔ orch 事件链路的真实入口"
- README §阶段 0 ⑤ 扩展点契约（hook tap enqueue-only）
- p21 P1b 章节 §"Crash-window idempotency state machine"（参考状态机表达形式）


