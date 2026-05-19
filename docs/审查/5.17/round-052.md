# Round 052 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 08:00:32 KST
- 结束：2026-05-17 08:04:12 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 DAG `turn.completed` subscriber、`CompleteNodeAndScheduleDownstream`、下游 promote/enqueue、run finalization、`spawning_thread_id` 反查和 sharedfile 输出物化。重点看量化 DAG 的 child agent 完成事件是否会错配节点、遗漏下游调度或在失败时留下不可恢复的中间态。

- `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go`
- `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber_test.go`
- `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber_shard16_test.go`
- `cmd/mcp-orch/orchestration/hook_consumer.go`
- `cmd/mcp-orch/store/taskdag/store_complete_downstream.go`
- `cmd/mcp-orch/store/taskdag/store_fail_downstream.go`
- `cmd/mcp-orch/store/taskdag/store_output_materialization_claim_test.go`
- `cmd/mcp-orch/sql/queries/task_dag_node_read.sql`
- `cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql`
- `cmd/mcp-orch/sql/queries/task_dag_node_write.sql`
- `cmd/mcp-orch/sql/queries/task_dag_run.sql`

## Findings

1. **[major] `spawning_thread_id` 多命中时同一个 TurnCompleted 会推进所有非终态节点**
   - 证据：反查 SQL 明确允许 N>1，注释说明 partial index 非 UNIQUE 且返回多行（`cmd/mcp-orch/sql/queries/task_dag_node_read.sql:29-42`）。subscriber 遇到 `len(nodes)>1` 只记录 dirty metric 和 warn，然后逐条调用 `advanceNodeForTurnCompleted()`（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:187-198`）。完成 SQL 只按 dag/node/run/status fence，不校验 `active_turn_id` 或 `active_wakeup_id`（`cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql:54-61`）。
   - 风险：重试、恢复或写回竞态导致多个节点残留同一 child thread id 时，一个完成事件可能把多个量化节点同时置 done/failed，并触发多条下游调度链。dirty data 被观测到了，但仍被当作可执行事实。
   - 建议：subscriber 应只推进与当前 active turn/wakeup 匹配的单个节点；N>1 时进入隔离/人工修复状态，或按 `active_turn_id`、`active_wakeup_id`、run_id 进一步收敛。

2. **[major] TurnCompleted 失败路径固定 `FailFast=false`，与 DAG `fail_fast` 策略不同源**
   - 证据：失败事件最终调用 `FailNodeAndCancelDownstream`，但 `FailFast` 参数固定为 false（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:373-417`）。测试也明确期望 `failCalls[0].FailFast = false`（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber_test.go:244-264`）。而 dispatcher retry 耗尽路径会解析并透传 DAG `RetryPolicy.FailFast`（`cmd/mcp-orch/orchestration/retry_strategy.go:168-202`）。
   - 风险：同一量化 DAG 配置了 `fail_fast=true` 时，调度失败耗尽会级联取消下游，但 agent 主动返回失败只会标记当前节点，依赖它的 pending 下游会继续保持非终态，run 可能长期 running。
   - 建议：subscriber 失败路径应读取 run/DAG retry policy，或在 node config 中明确区分“agent reported failure 是否触发 fail_fast”。

3. **[major] 完成写入与下游调度失败只是 warn，缺少 durable retry**
   - 证据：`advanceNodeDone()` 调用 `CompleteNodeAndScheduleDownstream()`；除 fence no rows 外，其他 DB/store 错误只 warn 并返回 false（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:339-370`）。注释也说明 subscriber 是 fire-and-forget。`CompleteNodeAndScheduleDownstream()` 把 complete、schedule downstream、finalize run 放在同一事务里，任一 schedule/finalize 错误会回滚整次完成（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:29-69`）。
   - 风险：child turn 已经结束且事件已消费，但完成事务失败后，没有持久化重试任务保证再次推进。量化节点会停留在 running/awaiting_verify，结果和下游调度依赖下一次重复 hook 或人工补偿。
   - 建议：TurnCompleted 处理应落 durable inbox/outbox，成功推进后 ack；DB 错误保留可重放 payload，并暴露到 DAG run events。

4. **[major] sharedfile 输出物化存在 claim 后写文件前崩溃的不可恢复窗口**
   - 证据：sharedfile 路径不存在时，subscriber 先 `ClaimNodeOutputMaterialization()` 把节点置 `awaiting_verify` 并写入结果引用（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:300-336`；`cmd/mcp-orch/sql/queries/task_dag_node_write.sql:60-73`），随后才调用 `writeAgentTurnSharedfile()` 写外部文件。写失败会尝试 fail node，但进程崩溃或上下文取消发生在 claim 与 write 之间时，只有 DB 中的 sharedfile 引用，没有对应文件。
   - 风险：量化 DAG 可能显示节点正在等待验证或已持有输出引用，但下游读取时文件缺失。当前恢复依赖 TurnCompleted 事件再次投递，缺少后台扫描 `awaiting_verify` + missing sharedfile 的补偿。
   - 建议：把 sharedfile 写入做成 outbox/事务后任务，或 claim 时记录 materialization operation id，并由后台 worker 发现并重试/失败。

5. **[moderate] 下游 `depends_on` 解码失败会静默视为“不满足”，不暴露 DAG 配置损坏**
   - 证据：`dependsSatisfiedForUpstream()` 对 `decodeDependsOn()` 错误直接返回 false，注释称上层会再 decode 并冒错（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:173-185`），但当前调度循环只在返回 true 后才进入 promote/enqueue 分支（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:135-169`）。
   - 风险：某个下游节点的 `depends_on` JSON 损坏时，不会被 promote，也不会产生调度错误；run 会因为该节点保持 pending 而无法 finalize。量化 DAG 看起来只是“依赖未满足”，实际是配置损坏。
   - 建议：在 DAG 创建/ApplyOps 时强校验 `depends_on`；运行期扫描到解码失败应 fail DAG 或写 run event，而不是当作未满足。

## 误报与已覆盖项

- `CompleteNodeAndScheduleDownstream()` 的正常成功路径是同事务完成节点、promote/enqueue 下游、尝试 run finalize；不会出现节点 done 但同事务内下游 enqueue 只写了一半的情况（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:29-69`）。
- 下游 wakeup 的 idempotency key 已包含 run_id，避免多 run 间共享同一个 `dag/<dag>/<node>/start` 去重键（`cmd/mcp-orch/store/taskdag/store_complete_downstream.go:221-255`）。
- `CompleteTaskDagNode` 会清空 `active_turn_id` 与 `active_wakeup_id`，完成态不会继续保留 active 绑定（`cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql:54-61`）。
- sharedfile duplicate event 写入前有 materialization claim fence，terminal 节点不会被迟到事件再次写文件；本轮关注的是 claim 成功后、外部文件写入前的崩溃窗口（`cmd/mcp-orch/store/taskdag/store_output_materialization_claim_test.go:15-80`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/store/taskdag -count=1
```

结果：通过。

## 下一轮建议

- Round 053 审查 cron/scheduled DAG 启动、scheduler lease、next_run_at 推进与并发触发，确认定时量化任务是否会漏跑、重复跑或提前确认成功。
