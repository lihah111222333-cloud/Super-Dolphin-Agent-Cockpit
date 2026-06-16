# Round 042 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:23:57 KST
- 结束：2026-05-17 07:31:40 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 `NodeExecutorRouter` 与 agent/automation executor 的派发边界，重点看量化 DAG wakeup 已被 claim 后，节点是否会被错误标记、重复派发或丢失完成传播。

- `internal/sidecar/orch/orchestration/node_router.go`
- `internal/sidecar/orch/orchestration/node_executor_dispatch.go`
- `internal/sidecar/orch/orchestration/dispatch_agent_running_test.go`
- `internal/sidecar/orch/orchestration/nodeexec/executor_agent.go`
- `internal/sidecar/orch/orchestration/nodeexec/executor_automation.go`

## Findings

1. **[major] agent 启动成功但 ready→running 写回失败时，RouteByWakeup 仍返回成功**
   - 证据：`dispatchAgent()` 在 executor 成功后调用 `advanceAgentNodeToRunning()`，但无论写回是否成功都 `return outcome, nil`（`internal/sidecar/orch/orchestration/node_router.go:379-395`）。`advanceAgentNodeToRunning()` 对普通 DB 错误只记 Warn 和 metric，不向上返回错误（`internal/sidecar/orch/orchestration/node_router.go:416-439`）。测试明确锁定“DB error not propagated”（`internal/sidecar/orch/orchestration/dispatch_agent_running_test.go:102-139`）。
   - 风险：dispatcher 可能继续 `MarkWakeupSent`，但节点仍停在 `ready/pending`，或没有正确的 `active_wakeup_id`。量化任务看起来已派发，实际状态机缺口会影响完成订阅和后续重试判断。
   - 建议：区分 subscriber 已先终结的 `ErrNoRows` 与真实 DB 写失败；真实写失败应返回 transient error，或立即停止刚启动的 child agent 并 retry wakeup。

2. **[major] spawning_thread_id 写回失败只填 ErrorSummary，节点状态仍是 done**
   - 证据：`spawnWriteback()` 在 `RecordNodeSpawn()` 失败时只返回 `"spawning_thread_id write-back failed"` 文案（`internal/sidecar/orch/orchestration/nodeexec/executor_agent.go:282-297`），`finalizeAgentOutcome()` 固定返回 `Status: done`（`internal/sidecar/orch/orchestration/nodeexec/executor_agent.go:266-268`）。
   - 风险：child agent 已运行，但 DAG subscriber 依赖 `spawning_thread_id` 反查节点时会失联；后续 `turn.completed` 无法归档到正确节点，量化 DAG 会卡在 running/sent。
   - 建议：把 spawn 记录失败提升为 framework transient，或为已启动但未写回的线程创建 durable compensation 记录。

3. **[major] RouteByWakeup 不校验目标节点当前状态，可由 stale/duplicate wakeup 派发终态节点**
   - 证据：`RouteByWakeup()` 查到节点后直接按 `node_type` 派发（`internal/sidecar/orch/orchestration/node_router.go:134-158`）；`lookupTargetNode()` 仅按 `node_key` 匹配节点，不检查 `Status`、`active_wakeup_id` 或 `assigned_to`（`internal/sidecar/orch/orchestration/node_router.go:303-314`）。
   - 风险：过期 wakeup、重复 wakeup 或恢复路径中的 sent/pending 残留可能再次执行已经 `done/failed/running` 的节点，造成量化任务重复下单、重复写文件或重复启动 agent。
   - 建议：RouteByWakeup 增加状态白名单和 `active_wakeup_id` fence；非 ready/pending 节点应返回可观测的 skipped outcome，而不是执行。

4. **[major] automation 完成传播失败只记录 Warn，dispatcher 仍会把 wakeup 标记 sent**
   - 证据：`dispatchAutomation()` 在 `outcome.Status == done` 时调用 `completeAutomationNode()`，但错误只 Warn，不改变返回 outcome（`internal/sidecar/orch/orchestration/node_router.go:442-463`）。注释也说明失败不阻塞主流（`internal/sidecar/orch/orchestration/node_router.go:466-468`）。
   - 风险：command card 已执行成功，但节点 result 和下游调度未持久化；dispatcher 标记 wakeup sent 后，缺少自动 retry 入口，量化 DAG 可能静默停在中间态。
   - 建议：完成传播失败应返回 infrastructure/transient outcome，或在 wakeup 层保留 dispatching/retry，直到 `CompleteNodeAndScheduleDownstream` 成功。

5. **[moderate] lifecycle hooks 是异步 best-effort，审计/状态钩子失败不影响节点结果**
   - 证据：`executeNodeWithLifecycleHooks()` 调用 hook 后忽略其失败（`internal/sidecar/orch/orchestration/node_executor_dispatch.go:66-76`）；`invokeLifecycleHook()` 用 `WithoutCancel` 派发 goroutine，100ms 未完成只 Warn，hook 错误也只 Warn（`internal/sidecar/orch/orchestration/node_executor_dispatch.go:90-119`）。
   - 风险：如果 hook 承载审计、指标或外部风控，量化节点可能已经执行但审计链缺失；失败不会进入 retry 或 fail-fast。
   - 建议：把 hook 分成强制型与观察型；强制型 hook 失败应阻止派发或使节点进入 retry。

## 误报与已覆盖项

- agent launch 本身失败时不会写 running，dispatcher 仍能按失败 outcome 处理（`internal/sidecar/orch/orchestration/dispatch_agent_running_test.go:141-145`）。
- `pgx.ErrNoRows` 被用于处理 subscriber 先终结的竞态，这个窗口不是本轮主要问题；本轮关注普通 DB 错误也被吞掉。
- automation command card disabled 会被分类为 hard failure，不会执行命令（`internal/sidecar/orch/orchestration/nodeexec/executor_automation.go:261-271`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/orchestration/nodeexec -count=1
```

结果：通过。

## 下一轮建议

- Round 043 继续审查 automation executor 的模板渲染、args 注入、输出写入和 command runner 安全边界。
