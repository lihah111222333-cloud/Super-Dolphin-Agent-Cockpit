# Round 032 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:19:43 KST
- 结束：2026-05-17 07:34:58 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 smart retry、failure class、node config patch 与 replan planner 的风险，重点看策略解析是否同源、retry/config 更新事务、rows=0 分类以及智能重试失败后的终态处理。

- `cmd/mcp-orch/orchestration/retry_strategy.go`
- `cmd/mcp-orch/orchestration/retry_strategy_test.go`
- `cmd/mcp-orch/orchestration/node_router.go`
- `cmd/mcp-orch/orchestration/node_executor_dispatch.go`
- `cmd/mcp-orch/orchestration/nodeexec/on_failure.go`
- `cmd/mcp-orch/orchestration/nodeexec/config.go`
- `cmd/mcp-orch/orchestration/nodeexec/types.go`
- `cmd/mcp-orch/orchestration/wakeup_dispatcher_shard17_smart_retry_test.go`
- `cmd/mcp-orch/orchestration/wakeup_dispatcher_shard17_smart_retry_extra_test.go`
- `cmd/mcp-orch/store/taskdag/store_node_config_patch_test.go`
- `cmd/mcp-orch/sql/queries/task_dag_node_write.sql`

## Findings

1. **[major] legacy `execution.retry` 与 typed `exec.on_failure.max_attempts` 是两套不同 schema，旧配置可能被 smart retry 当作单次尝试**
   - 证据：`ResolveRetryPolicy()` 解析的是 `nodeConfig.execution.retry`（`cmd/mcp-orch/orchestration/retry_strategy.go:63-95`），但 typed node config 的重试策略在 `exec.on_failure.max_attempts`（`cmd/mcp-orch/orchestration/nodeexec/config.go:59-70`、`cmd/mcp-orch/orchestration/nodeexec/config.go:76-99`）。smart retry preflight 使用 `nodeexec.MaxAttemptsFor(retryCtx.onFailure)`（`cmd/mcp-orch/orchestration/retry_strategy.go:334-353`），而 `MaxAttemptsFor(nil or <=0)` 返回 1（`cmd/mcp-orch/orchestration/nodeexec/on_failure.go:30-39`）。
   - 风险：已有 DAG 若只配置 `execution.retry` 或 DAG `schedule.default_retry`，但没有 `exec.on_failure.max_attempts`，进入 smart retry 分支后可能只尝试一次就 fail closed。量化任务中的短暂 provider/上下文错误会被过早终止。
   - 建议：统一 retry schema，smart retry max attempts 应 fallback 到 `RetryPolicy.MaxAttempts`；迁移期同时读取 `execution.retry`、`exec.retry` 和 `exec.on_failure.max_attempts` 并记录来源。

2. **[major] `RetryWakeupWithNodeConfigPatch` rows=0 被统一当成 hard cap，无法区分 lease fence miss、SQL attempt cap 和 stale config**
   - 证据：smart retry 配置补丁返回 rows=0 时直接调用 `handleRetryHardCap()`（`cmd/mcp-orch/orchestration/retry_strategy.go:521-568`）。store 事务中 rows=0 可能来自 `RetryTaskDagWakeup` 没命中，而该 SQL 的原因包括 fence 不匹配、lease 过期、status 不是 dispatching、`attempt_count >= 8`（`cmd/mcp-orch/sql/queries/task_dag_wakeup_dispatch.sql:47-57`）。测试 `TestDispatcherSmartRetryDoesNotPatchConfigWhenRetryFenceMisses` 只断言不 patch config，没有区分原因（`cmd/mcp-orch/orchestration/wakeup_dispatcher_shard17_smart_retry_extra_test.go:53-87`）。
   - 风险：过期 lease 或并发 reclaimer 造成的 rows=0 会被记录为 retry attempts exhausted，并可能把 DAG node 标 failed。量化系统会把基础设施竞态误判为策略失败。
   - 建议：RetryWakeup 返回结构化原因；或 rows=0 后读取 wakeup 当前状态/attempt_count 再决定是等待、重试、还是 hard cap。

3. **[major] smart retry config patch CAS 失败会直接 fail closed，可能把并发 replan/apply_ops 视为节点失败**
   - 证据：`RetryWakeupWithNodeConfigPatch` 在同事务内先 retry wakeup 再 `PatchTaskDagNodeConfigIfUnchanged`，patch 失败会回滚 retry（`cmd/mcp-orch/store/taskdag/store_wakeup.go:75-114`）。`PatchTaskDagNodeConfigIfUnchanged` 要求 config 完全等于 previous config 且 status 非终态（`cmd/mcp-orch/sql/queries/task_dag_node_write.sql:14-27`）。dispatcher 收到 error 后调用 `failSmartRetryPrepare()` 并最终 `FailWakeup`/`FailNodeAndCancelDownstream`（`cmd/mcp-orch/orchestration/retry_strategy.go:559-572`）。测试锁定 stale config 会 fail closed（`cmd/mcp-orch/orchestration/wakeup_dispatcher_shard17_smart_retry_extra_test.go:89-127`）。
   - 风险：如果 planner 或用户在同一节点上做了合法配置修复，旧 dispatcher 的 smart retry CAS miss 会把节点失败，反而覆盖恢复机会。
   - 建议：CAS miss 应重新读取 node config 并重新计算策略；只有确认当前配置仍不可执行或已超过尝试上限才 fail closed。

4. **[major] `replan` 策略把原 wakeup 标记 sent，但 planner 与原 DAG node 没有可恢复绑定**
   - 证据：`spawnReplanPlanner()` 成功 launch planner 后调用 `markLaunched(ctx, w, fence)`，即把原失败节点的 wakeup 标记为 sent（`cmd/mcp-orch/orchestration/node_router.go:81-101`）。planner LaunchRequest 生成了新的 AgentID，但没有写入原 node 的 active_turn/active_wakeup，也没有创建新的 DAG node 或 replan run（`cmd/mcp-orch/orchestration/node_router.go:89-120`）。测试只断言 planner 被启动、原 wakeup MarkSent（`cmd/mcp-orch/orchestration/wakeup_dispatcher_shard17_smart_retry_test.go:78-114`）。
   - 风险：原节点失败后的恢复工作转移给 planner，但原 wakeup 状态显示 sent，后续 turn.completed/recovery 可能按原节点 wakeup 关联，planner 输出与原节点状态之间缺少 durable contract。量化 DAG 可能停在 running 或被错误标记完成。
   - 建议：replan 应创建专门 planner node/run 或在原 run metadata 写 `replan_attempt`，并把 planner turn 与原 node 明确关联；原 wakeup 不应复用普通 sent 语义。

5. **[moderate] `append_error` 会把验证错误持续追加到 `first_turn`，没有大小/去重限制**
   - 证据：`appendAgentValidationDiagnostic()` 读取 `first_turn` 后直接追加 `"Previous validation error:\n"` 与错误摘要（`cmd/mcp-orch/orchestration/node_executor_dispatch.go:208-230`）。`truncateWakeupError()` 只截 `last_error`，不限制写回 config 的 `first_turn` 体积（`cmd/mcp-orch/orchestration/wakeup_dispatcher.go:439-447`）。
   - 风险：多次 validation failure 会让配置体积和 prompt 持续增长，最终触发 token/context 问题，并污染后续重试输入。对量化输出 schema 校验失败场景尤其容易反复追加同类错误。
   - 建议：把诊断写入结构化 `exec.retry_diagnostics` 并限制 N 条/总字节；渲染 first_turn 时动态注入最近一次错误，而不是永久改写原始配置。

## 误报与已覆盖项

- config patch 与 wakeup retry 在同一事务内执行，stale config 会回滚 wakeup retry，避免“retry 已排队但配置未变”的半成品（`cmd/mcp-orch/store/taskdag/store_node_config_patch_test.go:86-134`）。
- `PatchTaskDagNodeConfigIfUnchanged` 只改 config，不改 title/assignment/deps 等非配置字段，测试覆盖该范围（`cmd/mcp-orch/store/taskdag/store_node_config_patch_test.go:14-52`）。
- unsupported `skip`/`ask_human` 当前 fail closed，并有测试覆盖；本轮不重复报告其未实现语义（`cmd/mcp-orch/orchestration/wakeup_dispatcher_shard17_smart_retry_test.go:323-372`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/orchestration/nodeexec ./cmd/mcp-orch/store/taskdag -count=1
```

结果：通过。

## 下一轮建议

- Round 033 审查 node router 对 active_turn_id、active_wakeup_id、running 状态和 turn.completed 关联的写入时序。
