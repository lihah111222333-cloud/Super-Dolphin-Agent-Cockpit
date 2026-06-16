# Round 050 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:51:30 KST
- 结束：2026-05-17 08:06:04 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 dispatcher retry、`on_failure` 智能重试、failure class 分类、attempt 计数和 SQL 硬上限，重点看量化 DAG 节点失败后是否按可预期策略重试、升级、改图或终止。

- `internal/sidecar/orch/orchestration/retry_strategy.go`
- `internal/sidecar/orch/orchestration/wakeup_dispatcher.go`
- `internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_batch_test.go`
- `internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_dag_test.go`
- `internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_smart_retry_test.go`
- `internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_smart_retry_extra_test.go`
- `internal/sidecar/orch/orchestration/nodeexec/on_failure.go`
- `internal/sidecar/orch/orchestration/nodeexec/types.go`
- `internal/sidecar/orch/orchestration/nodeexec/executor_agent.go`
- `internal/sidecar/orch/orchestration/nodeexec/executor_automation.go`
- `internal/sidecar/orch/store/taskdag/store_wakeup.go`
- `internal/sidecar/orch/sql/queries/task_dag_wakeup_dispatch.sql`

## Findings

1. **[major] retry attempt 上限存在两套口径，`on_failure.max_attempts` 与 DAG `default_retry` 容易冲突**
   - 证据：普通 DAG retry 用 `ResolveRetryPolicy()`，从 DAG metadata `default_retry` 和 node `execution.retry` 计算 `MaxAttempts`（`internal/sidecar/orch/orchestration/retry_strategy.go:79-95`）。smart retry preflight 却用 `nodeexec.MaxAttemptsFor(on_failure)`（`internal/sidecar/orch/orchestration/retry_strategy.go:334-353`），而该函数 `cfg=nil` 或 `max_attempts<=0` 时返回 1（`internal/sidecar/orch/orchestration/nodeexec/on_failure.go:30-39`）。测试也分别覆盖两套口径：DAG `default_retry=2` 得到 `MaxAttempts=3`（`internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_batch_test.go:265-294`），smart retry `max_attempts=1` 会立即失败（`internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_smart_retry_test.go:286-321`）。
   - 风险：量化节点同时配置 DAG 默认重试和 `exec.on_failure` 时，是否还能重试取决于是否进入 smart retry 分支。一个节点可能在普通失败路径允许 3 次，在 `on_failure` 路径只允许 1 次，排障时难以解释。
   - 建议：统一 attempt 策略，明确优先级：`on_failure.max_attempts` 缺省应继承 `RetryPolicy.MaxAttempts`，或者在写入 config 时禁止同时配置两套不一致字段。

2. **[major] `infrastructure` failure class 不在 permanent 列表，默认会按普通 transient 重试**
   - 证据：failure class 枚举包含 `FailureClassInfrastructure`（`internal/sidecar/orch/orchestration/nodeexec/types.go:42-50`），但 `failureClassPermanent()` 只把 `hard` 和 `needs_human` 视为 permanent（`internal/sidecar/orch/orchestration/retry_strategy.go:122-144`）。Agent 与 automation 都可能产生 infrastructure 分类（`internal/sidecar/orch/orchestration/nodeexec/executor_agent.go:232-241`；`internal/sidecar/orch/orchestration/nodeexec/executor_automation.go:290-313`）。
   - 风险：DB、sharedfile、外部服务等基础设施失败如果不配置 `on_failure.by_class.infrastructure`，会按普通重试消耗 attempt，而不是触发专门告警、熔断或延迟退避。量化任务在基础设施异常时可能批量重试放大压力。
   - 建议：给 infrastructure 建立默认策略，例如指数退避、单独告警或 fail-fast；至少在 `ResolveOnFailureStrategy` 默认表中区分 transient 与 infrastructure。

3. **[major] SQL `attempt_count < 8` 硬上限会截断显式配置的大于 8 次重试**
   - 证据：注释说明 SQL 层仍保留 `attempt_count<8` 硬上限（`internal/sidecar/orch/orchestration/retry_strategy.go:34-36`）。实际 `RetryTaskDagWakeup` 在 WHERE 中写死 `AND attempt_count < 8`（`internal/sidecar/orch/sql/queries/task_dag_wakeup_dispatch.sql:47-57`）。store 的 `RetryWakeup()` 和 smart retry patch 路径都调用同一个 SQL（`internal/sidecar/orch/store/taskdag/store_wakeup.go:57-72`、`internal/sidecar/orch/store/taskdag/store_wakeup.go:75-105`）。
   - 风险：配置 `default_retry` 或 `on_failure.max_attempts` 大于 8 时，调度层会认为还有预算，数据库却返回 0 rows，dispatcher 再走 hard-cap 失败。量化长任务在用户显式放宽重试预算时仍会提前终止。
   - 建议：把 8 作为可配置最大值，并在 config 解析时 clamp/报错；README/tool schema 应直接暴露真实上限。

4. **[major] `skip` 和 `ask_human` 在枚举中可配置，但 dispatcher fail-closed**
   - 证据：`OnFailureStrategy` 枚举包含 `skip` 与 `ask_human`（`internal/sidecar/orch/orchestration/nodeexec/types.go:52-64`）。但 `smartRetryStrategyImplemented()` 只接受 retry、escalate_model、append_error、replan、fail_fast（`internal/sidecar/orch/orchestration/retry_strategy.go:367-378`）。测试明确期望 `skip` / `ask_human` 被 unsupported 并 fail closed（`internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_smart_retry_test.go:323-372`）。
   - 风险：调用方看到 schema/枚举会以为量化节点失败可跳过或转人工，但实际会失败并按 fail_fast 级联。策略名称与行为不一致会导致高风险 DAG 的故障演练失真。
   - 建议：在对外 schema 暂时隐藏未实现策略，或实现 `skipped`/`waiting_human` 状态推进和下游调度语义后再开放。

5. **[moderate] 配置 JSON 解析失败静默退回默认重试策略**
   - 证据：`ResolveRetryPolicy()` 注释要求解析失败安静走默认值（`internal/sidecar/orch/orchestration/retry_strategy.go:79-82`）。`decodeDAGSchedulePolicy()` 和 `decodeNodeExecutionPolicy()` 都在 JSON 解析失败时返回空策略（`internal/sidecar/orch/orchestration/retry_strategy.go:97-120`），测试把 malformed JSON 期望为默认单次（`internal/sidecar/orch/orchestration/retry_strategy_test.go:39-47`）。
   - 风险：量化 DAG metadata 或 node config 损坏时，系统不会暴露“策略解析失败”，而是改变重试/FailFast 行为。一个本应 fail_fast 的 DAG 可能因为 metadata 损坏变成不级联。
   - 建议：对运行时策略解析失败输出结构化事件并标记 run metadata；高风险 DAG 应 fail closed 或至少保留上一次有效策略快照。

## 误报与已覆盖项

- smart retry 的 config patch 与 retry wakeup 在同一事务中执行，patch miss 会回滚 retry，不会出现 wakeup 已重试但 node config 未改的半更新（`internal/sidecar/orch/store/taskdag/store_node_config_patch_test.go:86-160`）。
- permanent `hard/needs_human` 若没有 by_class 显式策略，不会被 default replan 误重试（`internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_smart_retry_test.go:207-284`）。
- `escalate_model` 与 `append_error` 明确只支持 agent 节点，automation 节点会 fail closed；本轮不把这个作为缺陷，只记录策略不适用边界（`internal/sidecar/orch/orchestration/retry_strategy.go:469-519`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/orchestration/nodeexec ./internal/sidecar/orch/store/taskdag -count=1
```

结果：通过。

## 下一轮建议

- Round 051 审查 dispatcher claim/lease/reclaim 与 sent-unbound 生命周期，确认重试和回收是否会重复执行量化节点。
