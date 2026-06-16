# Round 039 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:26:19 KST
- 结束：2026-05-17 07:36:04 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 DAG `TurnCompleted` subscriber、节点 completion/failure 推进、输出物化 claim 与下游调度，重点看量化 DAG agent 终态事件如何落库、触发下游，以及是否存在错配完成或丢补偿窗口。

- `internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go`
- `internal/sidecar/orch/orchestration/dag_turn_completed_subscriber_test.go`
- `internal/sidecar/orch/orchestration/dag_turn_completed_subscriber_shard16_test.go`
- `internal/sidecar/orch/store/taskdag/store.go`
- `internal/sidecar/orch/store/taskdag/store_complete_downstream.go`
- `internal/sidecar/orch/store/taskdag/store_fail_downstream.go`
- `internal/sidecar/orch/store/taskdag/store_output_materialization_claim_test.go`
- `internal/sidecar/orch/store/sqlc/task_dag_node_runtime.sql.go`
- `internal/sidecar/orch/store/sqlc/task_dag_node_write.sql.go`

## Findings

1. **[critical] DAG completion 只按 spawning_thread_id 反查节点，不校验事件 turn_id 与 active_turn_id**
   - 证据：subscriber 入口只取 `ev.ThreadID`，调用 `LookupNodesBySpawningThread()` 后逐条推进节点（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go:152-198`）。`CompleteTaskDagNode` 的 SQL fence 只要求 status in `ready/running/awaiting_verify`，没有 active_turn_id 或 active_wakeup_id 条件（`internal/sidecar/orch/store/sqlc/task_dag_node_runtime.sql.go:71-80`）。
   - 风险：量化 agent 重试、恢复或 provider 回放旧 completion 时，只要 thread id 仍挂在节点上，就可能把当前节点错误标记 done/failed；active turn fence 形同只在 bind 阶段生效，终态阶段缺失。
   - 建议：completion/failure SQL 输入增加 expected_turn_id/expected_wakeup_id，并在 WHERE 中校验 active_turn_id 或 awaiting_verify claim token。

2. **[major] completion 早于 spawning_thread_id 落库时只执行 stop_helper，不会补偿 DAG 节点**
   - 证据：反查空结果时只记录 LookupNoNode，并调用 `stopSpawnedAgentForSubscriber()` 后返回（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go:171-185`）。注释承认该分支可能是 dispatchAgent 尚未来得及记录 `spawning_thread_id`（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go:182-183`），测试也固定了 no-node 分支只 stop、不推进 flow（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber_test.go:377-399`）。
   - 风险：短任务量化 agent 可能在节点绑定写入前完成；subscriber 会停止 agent，但 DAG 节点保持 ready/running，后续没有 durable completion 事件可重放，节点卡住。
   - 建议：no-node completion 写入待匹配事件表或延迟重试；至少不要在未推进 DAG 时直接 stop 掉 spawned agent。

3. **[major] 同一 spawning_thread_id 命中多节点时逐条推进，dirty data 会扩大成多节点终态**
   - 证据：`len(nodes)>1` 只增加 `LookupDirtyData` 并继续 iterate every row（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go:187-198`）。测试要求同一 completion 推进两条 running row（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber_test.go:401-425`）。
   - 风险：retry/recovery 链路若出现 thread id 双挂，一个量化 worker 的输出会同时完成多个节点，可能触发多个下游分支，放大脏数据影响。
   - 建议：多命中时按 active_turn_id/run_id/latest event 做唯一选择；其他行标记 dirty，需要人工或 reclaimer 处理。

4. **[major] 成功输出写入 sharedfile 后，CompleteNode 失败不会进入 durable retry，但 stop_helper 仍会结束 agent**
   - 证据：`materializeSharedfileAfterClaim()` 先 claim 节点为 `awaiting_verify`，再写 sharedfile，随后 `advanceNodeDone()` 可能因 DB 错误返回 false（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go:300-337`、`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go:339-370`）。测试里的 replay 场景依赖再次收到同一 completion 事件才从 `awaiting_verify` 完成（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber_shard16_test.go:122-174`）。subscriber 循环后无论 advance 成功与否都会 stop spawned agent（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go:199-203`）。
   - 风险：量化输出文件已写好但节点仍停在 `awaiting_verify`；如果事件不重放，DAG 无法完成，而 agent 已被停止，缺少自动补偿来源。
   - 建议：`awaiting_verify` 状态应有 reclaimer/verify worker；CompleteNode DB 错误后不要立即 stop，或把 completion payload 持久化后可重放。

5. **[major] 失败 completion 永远以 FailFast=false 推进，DAG 策略无法决定是否级联取消**
   - 证据：subscriber 的 `advanceNodeFailedWithReason()` 固定传 `FailFast: false`（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go:404-417`）。store 层只有 `input.FailFast` 为 true 时才级联 cancel downstream（`internal/sidecar/orch/store/taskdag/store_fail_downstream.go:28-60`）。
   - 风险：量化 DAG 若声明 fail-fast，agent 节点失败后下游 pending 节点仍可能保留，run finalization 依赖后续补偿，失败传播不符合策略。
   - 建议：subscriber 读取 DAG/run retry policy 或 node config，按策略传入 FailFast；至少在失败结果中标注未级联。

## 误报与已覆盖项

- 输出体积超 4KB 时，如果没有 sharedfile 配置，agent 节点会 fail 而不是把超大结果写入 node result（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go:544-559`、`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber_shard16_test.go:423-451`）。
- sharedfile 已存在时会保留 agent 自写内容，避免 subscriber 覆盖报告文件（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go:311-323`、`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber_shard16_test.go:268-309`）。
- duplicate terminal node 会被 skip，并计入 `IdempotentSkipped`（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go:215-220`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/store/taskdag -count=1
```

结果：通过。

## 下一轮建议

- Round 040 审查 store 层 completion/downstream/finalize 的 run 边界、idempotency key 与 ready promotion。
