# Round 030 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:50:35 KST
- 结束：2026-05-17 07:04:12 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 DAG run completion/finalization 的闭环风险，重点看节点终态如何聚合成 run 终态、失败路径是否会悬挂下游、final_output 是否稳定，以及已终止 run 的 wakeup 残留处理。

- `internal/sidecar/orch/sql/queries/task_dag_run.sql`
- `internal/sidecar/orch/sql/queries/task_dag_wakeup_dispatch.sql`
- `internal/sidecar/orch/store/taskdag/store_complete_downstream.go`
- `internal/sidecar/orch/store/taskdag/store_fail_downstream.go`
- `internal/sidecar/orch/store/taskdag/store_finalize_run_test.go`
- `internal/sidecar/orch/store/taskdag/store_wakeup_test.go`
- `internal/sidecar/orch/orchestration/dag.go`
- `internal/sidecar/orch/orchestration/node_router.go`
- `internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go`

## Findings

1. **[critical] `FailFast=false` 的失败节点不会终止依赖它的 pending 下游，run 可永久停在 `running`**
   - 证据：`FailNodeAndCancelDownstream()` 只有在 `input.FailFast` 为 true 时才调用 `cancelDownstreamTx()`（`internal/sidecar/orch/store/taskdag/store_fail_downstream.go:43-49`），随后 `maybeFinalizeRunTx()` 只在所有 runtime nodes 都进入终态时才推进 run（`internal/sidecar/orch/sql/queries/task_dag_run.sql:90-188`）。测试 `TestCompleteNode_NotAllTerminal_RunUnchanged` 明确锁定“仍有 pending 节点则 run 保持 running”（`internal/sidecar/orch/store/taskdag/store_finalize_run_test.go:563-587`）。
   - 风险：量化 DAG 中某个非 fail-fast 策略节点失败后，所有依赖它且尚未启动的计算/风控/报告节点不会被标记 skipped/cancelled，也不会被调度。run 外观看起来仍在 running，scheduled DAG 的单 running 限制又可能阻止下一次周期启动。
   - 建议：对失败节点引入 `on_failure` 显式语义；非 fail-fast 也应把“依赖已不可能满足”的下游标记为 skipped 或 blocked，并允许 run 终态聚合。

2. **[major] run finalization 只挂在少数状态写路径上，绕过 NodeFlowStore 的终态更新可能不推进 run**
   - 证据：`maybeFinalizeRunTx()` 只在 `CompleteNodeAndScheduleDownstream()` 和 `FailNodeAndCancelDownstream()` 内调用（`internal/sidecar/orch/store/taskdag/store_complete_downstream.go:56-63`、`internal/sidecar/orch/store/taskdag/store_fail_downstream.go:50-54`）。`service.UpdateNodeStatus()` 对 `status="done"` 走 flow store，但非 done 状态仍保留旧 `UpdateNodeStatus` 路径（`internal/sidecar/orch/orchestration/dag.go:186-242`）；对应测试也说明 `status != "done"` 保持 legacy update（`internal/sidecar/orch/orchestration/dag_complete_downstream_test.go:117-139`）。
   - 风险：人工工具、RPC 或测试桩若把节点直接改成 failed/cancelled/skipped，节点已经终态但 run 不会同步终态。量化批次可能显示 running，后续 cron/监控会基于错误状态做调度或告警。
   - 建议：所有 runtime terminal status 更新统一进入 NodeFlowStore；或在 store 的底层 `UpdateNodeStatus` 对终态也调用 finalize SQL。

3. **[major] failed/cancelled run 不保存 final output 或失败摘要，终态可观测性不足**
   - 证据：finalization SQL 只有 `final_status = 'succeeded'` 且 `final_output` 非空时才写 `metadata.final_output`（`internal/sidecar/orch/sql/queries/task_dag_run.sql:171-184`）。测试 `TestCompleteNode_FailedRunDoesNotPromoteFinalOutput` 明确期望 failed run metadata 只保留原始 request_id（`internal/sidecar/orch/store/taskdag/store_finalize_run_test.go:442-470`）。
   - 风险：失败批次可能已经产出部分报告、日志文件或失败诊断，但 run 级 metadata 不暴露这些指针。量化审计人员只能逐节点排查，自动 dashboard 也无法汇总失败原因。
   - 建议：为 failed/cancelled run 写 `metadata.failure_summary`，并允许配置 `diagnostic_node_key` 或 `last_error_output`；成功输出与失败诊断分开建模。

4. **[major] final output 选择读取当前 DAG 模板 metadata，而不是 run 创建时的快照**
   - 证据：`FinalizeTaskDagRunIfAllNodesTerminal` 的 `dag_meta` CTE 直接从 `task_dags` 当前行读取 `metadata->>'final_node_key'`（`internal/sidecar/orch/sql/queries/task_dag_run.sql:110-126`）。虽然 `CreateTaskDagRun` 注释称 `dag_version_snapshot` 用于保证模板改动不影响本次 run（`internal/sidecar/orch/sql/queries/task_dag_run.sql:8-15`），final output 实际没有使用 run snapshot metadata。
   - 风险：长时间运行的量化 DAG 在执行中被更新 final node，旧 run 完成时会按新模板挑选输出，可能把日报、风控报告或回测结果指向错误节点。
   - 建议：StartDAG 时把 `final_node_key` 和输出策略写入 run metadata；finalize 只读取 run metadata 或 runtime node snapshot，不读取当前模板 metadata。

5. **[moderate] finalized run 的 pending wakeup 会变成不可 claim 的残留记录**
   - 证据：`ClaimDueTaskDagWakeups` 只有当 wakeup 关联的 run 仍为 `running` 时才允许 claim（`internal/sidecar/orch/sql/queries/task_dag_wakeup_dispatch.sql:13-24`）。测试 `TestClaimDueWakeupsSkipsFinalizedRunWakeup` 验证 finalized run 的 pending wakeup 不会被 claim（`internal/sidecar/orch/store/taskdag/store_wakeup_test.go:26-45`）。
   - 风险：run finalization 后若还有 pending wakeup，调度器不会处理、也不会失败它们。长期 scheduled DAG 会积累不可见垃圾，并干扰运维判断“还有没有待派发节点”。
   - 建议：run finalize 同事务把本 run 未发送 wakeup 标记 cancelled/stale；或提供清理任务与 dashboard 指标。

## 误报与已覆盖项

- finalize SQL 的终态优先级有测试覆盖：failed 高于 cancelled，done/skipped 聚合为 succeeded（`internal/sidecar/orch/store/taskdag/store_finalize_run_test.go:473-560`）。
- `CompleteNodeAndScheduleDownstream()` 在 done 分支会同事务调度下游并尝试 finalize，成功路径的链式调度闭环是存在的（`internal/sidecar/orch/store/taskdag/store_complete_downstream.go:44-64`）。
- `ClaimDueTaskDagWakeups` 跳过非 running run 可避免已终止 run 继续执行节点；本轮问题是缺少终止后清理和可观测性。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/store/taskdag ./internal/sidecar/orch/orchestration -count=1
```

结果：通过。

## 下一轮建议

- Round 031 审查 DAG wakeup dispatcher、lease、retry、sent/bound 状态闭环，重点看 dispatched 后未绑定 turn、lease 过期、失败分类和重试上限。
