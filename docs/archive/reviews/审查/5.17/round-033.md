# Round 033 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:34:59 KST
- 结束：2026-05-17 07:49:31 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 agent 节点 launch、`spawning_thread_id`、`active_turn_id`、`active_wakeup_id` 与 turn.completed 反查的绑定链路，重点看 launch 成功后各状态写入是否事务一致、写失败是否会重复启动或丢完成事件。

- `cmd/mcp-orch/orchestration/node_router.go`
- `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go`
- `cmd/mcp-orch/orchestration/hook_consumer.go`
- `cmd/mcp-orch/orchestration/turn_lifecycle.go`
- `cmd/mcp-orch/store/taskdag/store_node_spawn.go`
- `cmd/mcp-orch/store/taskdag/store.go`
- `cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql`
- `cmd/mcp-orch/store/taskdag/store_update_running_status_test.go`
- `cmd/mcp-orch/store/taskdag/store_lookup_by_spawning_thread_test.go`

## Findings

1. **[critical] `BindRunningNodeTurn` 有持久化实现但未接生产事件流，active turn 链路基本悬空**
   - 证据：store 提供 `BindRunningNodeTurn()`，在一个事务中绑定 wakeup 和 node active turn（`cmd/mcp-orch/store/taskdag/store.go:181-210`），SQL 要求 node 处于 `running`、`active_turn_id IS NULL` 且 `active_wakeup_id` 匹配 wakeup（`cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql:1-13`）。但全仓 `rg` 只发现定义和测试，没有生产调用点；turn lifecycle 只更新 in-memory agent state（`cmd/mcp-orch/orchestration/turn_lifecycle.go:18-42`），DAG subscriber 反查也只靠 `spawning_thread_id`（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:171-202`）。
   - 风险：量化 DAG 的 active turn 无法持久化到 node/wakeup，recovery 的 `ListRunningNodesByAssignee` 需要 active turn 才能 replay（见 Round 031），因此 agent 进程崩溃后很难可靠恢复当前节点。
   - 建议：在 turn accepted/started 事件中调用 `BindRunningNodeTurn`，并把 wakeup id、run id、dag/node key 作为 event correlation 数据传递。

2. **[major] agent launch、`spawning_thread_id`、running 状态、wakeup sent 是分散写入，任何一步失败都会产生半绑定节点**
   - 证据：AgentExecutor launch 后通过 `RecordNodeSpawn()` 写 `spawning_thread_id`（`cmd/mcp-orch/store/taskdag/store_node_spawn.go:46-70`），router 随后 `UpdateRunningNodeStatus()` 写 running 和 `active_wakeup_id`（`cmd/mcp-orch/orchestration/node_router.go:379-440`），dispatcher 再 `MarkWakeupSent()`（`cmd/mcp-orch/orchestration/wakeup_dispatcher.go:278-293`）。这些写入不在同一事务，且 running 写失败只 warn/metric 后仍返回成功 outcome（`cmd/mcp-orch/orchestration/node_router.go:398-439`）。
   - 风险：量化节点可能已经启动子 agent，但 node 未 running 或 wakeup 未 sent；后续 reclaimer 可能重派，turn.completed 也可能找不到或误找节点。
   - 建议：为 DAG agent launch 引入单个 store 操作 `ClaimLaunchAndMarkRunning`，至少把 running + active_wakeup + spawning_thread 写在同一事务中；MarkSent 失败应按 Round 031 分类处理。

3. **[major] turn.completed 仅按 `spawning_thread_id` 反查，无法用 active wakeup/run 做强校验**
   - 证据：DAG subscriber 对 `ev.ThreadID` 调 `LookupNodesBySpawningThread()`，然后遍历所有返回节点推进终态（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:171-202`）。store 注释说明 N>1 是“normal on retry / recovery chains”，调用方会逐个推进（`cmd/mcp-orch/store/taskdag/store.go:149-157`）；测试覆盖同一 thread id 可返回多行（`cmd/mcp-orch/store/taskdag/store_lookup_by_spawning_thread_test.go:94-142`）。
   - 风险：同一 thread id 若因重试、恢复或数据污染挂到多个 node，单个 turn.completed 事件会推进多个节点。量化 DAG 中这可能把不同策略节点同时标 done/failed。
   - 建议：turn.completed 事件携带 wakeup id/run id/node key；subscriber 除 `spawning_thread_id` 外必须校验 active_wakeup_id 或 run_id。

4. **[major] `UpdateRunningNodeStatus` 不校验 assigned_to 或 wakeup 目标 agent，错误 dispatcher 可把节点置 running**
   - 证据：SQL 只按 dag_key、node_key、run_id、status pending/ready 更新 running，并写 `active_wakeup_id`（`cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql:28-36`）；store 调用也只传这些字段（`cmd/mcp-orch/store/taskdag/store.go:228-242`）。没有校验 wakeup.target_agent_id 与 node.assigned_to。
   - 风险：并发 dispatcher、错误 wakeup 或伪造 wakeup 可把不属于该 agent 的节点标 running，后续完成事件会污染节点状态。
   - 建议：running 更新 WHERE 加 `assigned_to=$agentID` 或 join wakeup 校验 `target_agent_id`；调用方传入并记录 dispatcher owner。

5. **[moderate] thread.stopped fallback 对所有非终态/非 awaiting_verify 节点都 fail，可能误杀仍在绑定间隙的 ready 节点**
   - 证据：fallback 通过 `LookupNodesBySpawningThread()` 找到节点后，除 done/failed/cancelled/skipped/awaiting_verify 外都会 `FailNodeAndCancelDownstream()`（`cmd/mcp-orch/orchestration/hook_consumer.go:410-470`）。`CompleteTaskDagNode` 又允许 ready→done 以覆盖 race window（`cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql:47-61`），说明 ready 状态下也可能收到 completion。
   - 风险：如果 thread.stopped 与 turn.completed 乱序，fallback 可能先把仍可完成的 ready/running 节点失败；后续 completed 只能被 fence 拒绝。
   - 建议：fallback 使用 active turn/wakeup 和停止原因判定；对 ready 且刚 spawn 的节点引入短 TTL 或等待 completion grace period。

## 误报与已覆盖项

- running 状态更新的 SQL fence 已允许 pending/ready，避免 ready 根节点无法进入 running 的历史问题，测试覆盖该点（`cmd/mcp-orch/store/taskdag/store_update_running_status_test.go:11-90`）。
- `RecordNodeSpawn()` 拒绝空 thread id，避免覆盖已有有效 thread id（`cmd/mcp-orch/store/taskdag/store_node_spawn.go:46-60`）。
- subscriber 在节点已终态时会跳过，重复 turn.completed 不会直接覆盖终态（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:28-41`、`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:215-230`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/store/taskdag -count=1
```

结果：通过。

## 下一轮建议

- Round 034 审查 lifecycle hooks、event surface 与 metrics 的副作用隔离，重点看 hook timeout、失败吞掉、事件体是否足够审计。
