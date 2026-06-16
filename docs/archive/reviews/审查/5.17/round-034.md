# Round 034 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:49:32 KST
- 结束：2026-05-17 08:03:44 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 lifecycle hooks、event surface、DAG subscriber/running/retry metrics 的可观测性和审计风险，重点看节点执行事件是否可追踪、hook 失败是否会影响状态、Prometheus 导出是否覆盖关键异常。

- `cmd/mcp-orch/orchestration/node_executor_dispatch.go`
- `cmd/mcp-orch/orchestration/events.go`
- `cmd/mcp-orch/orchestration/factory_events.go`
- `cmd/mcp-orch/orchestration/dag_subscriber_metric.go`
- `cmd/mcp-orch/orchestration/dispatch_agent_running_metric.go`
- `internal/platform/eventsurface/bind.go`
- `internal/platform/metrics/dag.go`
- `cmd/mcp-orch/orchestration/node_router_test.go`

## Findings

1. **[major] production lifecycle hook 只写 debug log，不产生持久化审计事件**
   - 证据：`ProvideNodeLifecycleHooks()` 注入 before/after/state_change/failure 四类 hook，但 handler 只是 `logger.Debug("node lifecycle hook", ...)`（`cmd/mcp-orch/orchestration/node_executor_dispatch.go:24-64`）。`executeNodeWithLifecycleHooks()` 调 hook 后直接执行节点，不写 run events 或 task events（`cmd/mcp-orch/orchestration/node_executor_dispatch.go:66-77`）。
   - 风险：量化节点执行开始、失败分类、自动化命令执行结果等关键事件只在 debug 日志中，无法作为审计记录或 dashboard 数据。出现误下单/漏跑时，事后只能拼日志。
   - 建议：将 lifecycle hook 事件写入 `task_dag_runs.events` 或独立 task event 表，至少保留 before/after/failure 的 node_key、run_id、wakeup_id、failure_class。

2. **[major] hook 异步脱离父 ctx，最多等待 100ms，失败只 warn，可能造成审计顺序错乱**
   - 证据：`invokeLifecycleHook()` 使用 `context.WithoutCancel(ctx)`，再开 goroutine 执行 hook；调用方只等待 `lifecycleHookDispatchWait=100ms`，超时后记录 “still running asynchronously” 并继续（`cmd/mcp-orch/orchestration/node_executor_dispatch.go:19-22`、`cmd/mcp-orch/orchestration/node_executor_dispatch.go:79-119`）。
   - 风险：节点已经进入 done/failed 后，hook 仍可能稍后写日志或失败。对高频量化 DAG，事件顺序会与真实状态写入顺序不一致，审计链不可靠。
   - 建议：把状态变更审计从 hook 中剥离为同步、事务内或 outbox 事件；hook 仅做非关键 side effect。

3. **[major] DAG subscriber/running/fallback 关键异常计数未导出到 Prometheus**
   - 证据：`DAGSubscriberCounters()`、`DispatchAgentRunningCounters()`、`DAGFallbackCounters()` 只是包内 atomic snapshot API（`cmd/mcp-orch/orchestration/dag_subscriber_metric.go:104-129`、`cmd/mcp-orch/orchestration/dispatch_agent_running_metric.go:57-79`）。Prometheus collector 只导出 dispatch_failed/retry/overflow 等 retry 指标（`internal/platform/metrics/dag.go:9-62`）。
   - 风险：`lookup_no_node`、`dirty_data`、`running_write_failed`、`thread_stopped_fallback` 等最能暴露 DAG 状态污染的问题不会进入常规监控。量化任务可能长期部分失败但外部只看到 retry 指标正常。
   - 建议：把 subscriber/running/fallback/stop helper 计数统一接入 `internal/platform/metrics`，并补 dashboard/alert 阈值。

4. **[major] task/node statusChanged event surface 存在 DTO，但本轮未发现 DAG 状态写入路径发布该事件**
   - 证据：eventsurface 支持 `MethodTaskNodeStatusChanged` 并渲染 dag/node/status/active turn/wakeup 字段（`internal/platform/eventsurface/bind.go:73-120`）。但本轮搜索到的 DAG status 写入路径是 store SQL 和 subscriber/router 调用，未发现 `taskdto.TaskNodeStatusChanged` 在这些路径被 publish。
   - 风险：前端或外部订阅者无法实时看到 DAG node 状态变更，只能轮询或依赖间接事件。对量化监控，ready/running/failed 的延迟或缺失会掩盖策略异常。
   - 建议：在 NodeFlowStore 的 complete/fail/running/bind/retry 状态变更成功后发布 `TaskNodeStatusChanged`，或用 DB outbox 保证事件与状态一致。

5. **[moderate] `emitEvent()` 遇未知 eventType 或 nil bus 静默返回，事件接线错误难以及时发现**
   - 证据：`emitEvent()` 对 nil bus、未知 eventType 都直接 return（`cmd/mcp-orch/orchestration/factory_events.go:53-63`）。调用侧如 `publishAgentFailed()` 等不会看到发布失败（`cmd/mcp-orch/orchestration/events.go:5-55`）。
   - 风险：新增量化审计事件时，如果 eventType 未登记或 bus 未接入，生产不会报错，状态变化只在内存/DB 中发生，订阅链断裂不易发现。
   - 建议：未知 eventType 至少 warn 或在测试中强制覆盖；关键事件发布失败应有计数器。

## 误报与已覆盖项

- hook 执行不会阻塞节点主流程，测试覆盖慢 hook 不阻塞 dispatch，这是执行可用性保护，不是审计保证（`cmd/mcp-orch/orchestration/node_router_test.go:171-214`）。
- retry 指标已有 Prometheus collector，包括 per-node retry count 和 overflow（`internal/platform/metrics/dag.go:9-62`）。
- event surface 已具备 task node status changed 的 payload 形状，本轮问题是状态写入路径没有发布它。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./internal/platform/metrics -count=1
```

结果：通过。

## 下一轮建议

- Round 035 审查 stop helper、agent archive/stop 与 spawned agent 清理，重点看任务完成后资源回收和错误归因。
