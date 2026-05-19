# Round 019 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:13:02 KST
- 结束：2026-05-17 06:20:17 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 DAG recover / reclaimer / turn lifecycle / turn.completed subscriber，重点看 `sent`、`bound_turn_id`、`active_turn_id`、`active_wakeup_id`、`last_event_at` 和 run finalization 这些量化状态是否能在失败后恢复。

- `cmd/mcp-orch/orchestration/recover.go`
- `cmd/mcp-orch/orchestration/wakeup_reclaim.go`
- `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go`
- `cmd/mcp-orch/orchestration/service.go`
- `cmd/mcp-orch/orchestration/helpers.go`
- `cmd/mcp-orch/orchestration/process_lifecycle.go`
- `cmd/mcp-orch/orchestration/service_launcher_bridge.go`
- `cmd/mcp-orch/store/taskdag/store.go`
- `cmd/mcp-orch/store/taskdag/store_wakeup.go`
- `cmd/mcp-orch/store/taskdag/store_complete_downstream.go`
- `cmd/mcp-orch/sql/queries/task_dag_wakeup_query.sql`
- `cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql`
- `cmd/mcp-orch/sql/queries/task_dag_node_read.sql`
- 相关测试：`recover_test.go`、`store_fencing_test.go`、`dag_turn_completed_subscriber_test.go`

## Findings

1. **[critical] 持久化 turn 绑定端口没有生产调用点，DAG recover 依赖的 `active_turn_id` / `bound_turn_id` 很可能永远不落库**
   - 证据：store 提供了 `BindRunningNodeTurn()`，同事务写 `task_dag_wakeups.bound_turn_id` 和 `task_dag_nodes.active_turn_id`（`cmd/mcp-orch/store/taskdag/store.go:181-210`），测试也确认该端口能绑定并在完成后清理（`cmd/mcp-orch/store/taskdag/store_fencing_test.go:292-330`）。但全仓 `rg` 只有接口、实现和测试调用 `BindRunningNodeTurn()` / `BindWakeupTurn()`，生产路径没有调用点。`TurnStarted` 订阅只调用 `svc.BindActiveTurnID()`，它只改内存里的 `agent.activeTurnID`（`cmd/mcp-orch/orchestration/service.go:242-266`、`cmd/mcp-orch/orchestration/helpers.go:47-63`）。recover 则要求 running node 匹配 `ActiveTurnID`，再要求 wakeup 是 `sent` 且 `BoundTurnID` 等于 active turn（`cmd/mcp-orch/orchestration/recover.go:117-183`）。
   - 风险：dispatcher 把 DAG node 推到 running 并把 wakeup 标记 `sent` 后，生产 turn started 事件不会把对应 node/wakeup 的 turn 绑定写入 DB。进程崩溃或 stall recover 时，`loadRecoveredTurnSubmission()` 找不到可重放的 store-backed active turn，DAG 节点可能永久 running，wakeup 永久 sent/unbound。
   - 建议：在 `TurnStarted` 事件中携带并消费 DAG `wakeup_id/dag_key/node_key/run_id`，调用 `BindRunningNodeTurn()`；或者在 submit 队列创建 turn 时就把 `ExpectedTurnID` 与 wakeup/node 同事务绑定。

2. **[major] 本地 `StartTurn` 失败只清内存 active turn，不会把已 `sent` 的 wakeup 退回 pending 或标 failed**
   - 证据：本地队列执行时先设置 `agent.activeTurnID` 并进入 `turnWork`（`cmd/mcp-orch/orchestration/process_lifecycle.go:63-90`），`startTurnExecution()` 调 `turnStarter.StartTurn()`；失败只进入 `finishTurnStartFailure()`（`cmd/mcp-orch/orchestration/helpers.go:91-105`）。`finishTurnStartFailure()` 只清 `agent.activeTurnID`、写 `lastError` 并推进内存状态机（`cmd/mcp-orch/orchestration/helpers.go:133-150`），没有调用 `RetryWakeup` / `FailWakeup` / `FailNodeAndCancelDownstream`。
   - 风险：DAG dispatcher 已 `MarkWakeupSent`，但真正 turn start 失败时，wakeup 不会回到 pending，也不会进入 failed；node 仍可能保持 running + active_wakeup_id。reclaimer 只处理 dispatching，不处理 sent，因此这个失败窗口不会自动补偿。
   - 建议：把 DAG-backed submission 的 `wakeup_id` 带到 `turnWork`，`StartTurn` 失败时按 retry policy 回滚 wakeup 或 fail node；至少把 sent-unbound 超时扫描接到 retry/fail。

3. **[major] `turn.completed` subscriber 对 Complete/Fail DB 错误只 warn，事件没有 durable retry，完成事件一旦丢失会卡住 running node**
   - 证据：`advanceNodeDone()` 调 `CompleteNodeAndScheduleDownstream()`，普通 DB 错误只记录 warn 并返回 false（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:339-371`）。失败路径 `advanceNodeFailedWithReason()` 对 `FailNodeAndCancelDownstream()` 的普通错误也只 warn（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:404-430`）。订阅器说明自己是 fire-and-forget（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:339-341`），没有把失败事件写入重试表。
   - 风险：agent 已经发出 terminal event，但 DB 瞬时错误会让节点仍保持 running/awaiting_verify，run 无法 finalize，下游无法调度。后续 recover 依赖 active turn 绑定，而上一条显示绑定也可能缺失，因此该状态很难自愈。
   - 建议：为 DAG turn.completed 建 durable inbox/outbox，DB 写失败后按 thread_id + turn_id + node_key 重试；或将 subscriber 失败转入 explicit `completion_pending` 状态，避免沉默丢事件。

4. **[major] reclaimer 只回收 `dispatching` lease，`sent` 但未绑定 turn 的 wakeup 已有查询却没有定时处理**
   - 证据：`WakeupReclaimer.ReclaimOnce()` 只调用 `ReclaimStaleDispatchingWakeups()`（`cmd/mcp-orch/orchestration/wakeup_reclaim.go:100-118`），SQL 只更新 `status='dispatching' AND lease_expires_at < NOW()`（`cmd/mcp-orch/sql/queries/task_dag_wakeup_query.sql:1-4`）。同一 SQL 文件已有 `ListSentUnboundTaskDagWakeups` 查询 `status='sent' AND bound_turn_id IS NULL`（`cmd/mcp-orch/sql/queries/task_dag_wakeup_query.sql:6-10`），但当前 reclaimer 没有消费它。
   - 风险：MarkSent 成功后，任何 turn-start 绑定失败、start turn 失败、planner agent 不回调、或事件丢失，都会让 wakeup 脱离 retry/lease 体系。`attempt_count` 不再增加，调度器也不会重新 claim。
   - 建议：增加 sent-unbound TTL 策略：超过阈值后按 DAG policy retry/fail，并同步修复 node 的 running/active_wakeup_id 状态。

5. **[moderate] 依赖满足但未指派的下游会被 promote 到 `ready` 后跳过 enqueue，注释仍说“保持 pending”，容易造成 repair 口径错误**
   - 证据：`scheduleDownstreamWakeupsTx()` 对每个依赖满足的 pending 下游先 `PromoteSingleNodePendingToReady`（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:135-160`），随后 `tryEnqueueDownstream()` 若 `assigned_to` 为空就返回 nil（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:200-220`）。但 `tryEnqueueDownstream()` 的注释仍说“节点状态保持 pending”（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:207-217`），与实际 ready 状态冲突。
   - 风险：运维或 repair job 如果按注释寻找 pending 未指派节点，会漏掉实际处于 ready 但没有 wakeup 的节点。run finalization SQL 把 ready 视为非终态，run 会继续 running（`cmd/mcp-orch/sql/queries/task_dag_run.sql:63-73`）。
   - 建议：修正文档和 repair 查询口径，明确 ready+assigned_to 空是“等待指派”的可观测状态；增加扫描 ready 无 pending/sent wakeup 的节点。

## 误报与已覆盖项

- store 层 `BindRunningNodeTurn()` 本身是事务化的，能同时绑定 wakeup 和 node，并拒绝同一 wakeup 的第二个 turn 绑定（`cmd/mcp-orch/store/taskdag/store_fencing_test.go:292-361`）。本轮风险是生产链路没有调用它。
- recover 对已被 reclaim 回 pending 的 wakeup 会跳过 replay，这是测试锁定的（`cmd/mcp-orch/orchestration/recover_test.go:260-293`）。本轮不报告“reclaimed wakeup 被错误重放”。
- `CompleteTaskDagNode` 已接受 ready/running/awaiting_verify，覆盖 turn.completed 先于 ready→running 的 race A（`cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql:47-61`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/orchestration -count=1
```

结果：Go guard、`internal/archtest`、`cmd/mcp-orch/store/taskdag` 与 `cmd/mcp-orch/orchestration` 通过。

## 下一轮建议

- Round 020 审查 DAG start / run idempotency / budget / final output 路径，重点看 run 多实例、idempotency_key、budget_used 和 final_node_key 的状态量化是否一致。
