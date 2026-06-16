# Round 018 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:08:33 KST
- 结束：2026-05-17 06:12:54 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 DAG retry / fail-fast / smart retry / replan 路径，重点看 `attempt_count`、`default_retry`、`on_failure.max_attempts`、`FailureClass` 和 replan planner 的状态量化是否一致。

- `internal/sidecar/orch/orchestration/retry_strategy.go`
- `internal/sidecar/orch/orchestration/wakeup_dispatcher.go`
- `internal/sidecar/orch/orchestration/node_router.go`
- `internal/sidecar/orch/orchestration/node_executor_dispatch.go`
- `internal/sidecar/orch/orchestration/nodeexec/on_failure.go`
- `internal/sidecar/orch/orchestration/nodeexec/types.go`
- `internal/sidecar/orch/orchestration/nodeexec/executor_agent.go`
- `internal/sidecar/orch/sql/queries/task_dag_wakeup_dispatch.sql`
- `internal/sidecar/orch/sql/queries/task_dag_node_write.sql`
- `internal/sidecar/orch/store/taskdag/store_wakeup.go`
- `internal/sidecar/orch/store/taskdag/store_node_config_patch_test.go`
- 相关测试：`retry_strategy_test.go`、`wakeup_dispatcher_shard17_smart_retry_test.go`、`wakeup_dispatcher_shard17_smart_retry_extra_test.go`

## Findings

1. **[major] `on_failure.max_attempts` / `default_retry` 可配置超过 8，但 SQL 仍用 `attempt_count < 8` 物理截断，策略上限会被静默改写**
   - 证据：`ResolveRetryPolicy()` 把 `schedule.default_retry` 转为 `MaxAttempts=retry+1`，没有把上限限制为 8（`internal/sidecar/orch/orchestration/retry_strategy.go:83-94`）；`OnFailureConfig.MaxAttempts` 同样按配置原样返回，`<=0` 才退到 1（`internal/sidecar/orch/orchestration/nodeexec/on_failure.go:30-39`）。dispatcher smart retry 用 `w.AttemptCount >= MaxAttemptsFor(...)` 判断是否耗尽（`internal/sidecar/orch/orchestration/retry_strategy.go:334-354`），但真正 retry 落库的 SQL 额外要求 `attempt_count < 8`（`internal/sidecar/orch/sql/queries/task_dag_wakeup_dispatch.sql:47-57`）。代码注释承认 SQL 是不可越过的物理上限（`internal/sidecar/orch/orchestration/retry_strategy.go:34-36`），但策略层和配置 schema 没有暴露这个裁剪。
   - 风险：用户配置 `max_attempts: 12` 或 `default_retry: 10` 时，量化引擎表面接受该策略，实际第 8 次后由 `handleRetryHardCap()` 失败（`internal/sidecar/orch/orchestration/retry_strategy.go:214-232`）。这会让调参、SLO 和告警统计误以为还有重试预算，且失败原因从策略耗尽变成硬上限兜底。
   - 建议：在解析策略时统一 clamp 并返回 `effective_max_attempts`，或在 DAG/node 配置校验阶段拒绝超过 SQL hard cap 的值；审计日志和 UI 中同时展示 requested/effective，避免策略被静默改写。

2. **[major] replan 成功启动 planner 后把原失败 wakeup 标为 `sent`，但没有把原节点转入明确的 `replanning`/`waiting_replan` 状态或绑定 planner 线程**
   - 证据：`spawnReplanPlanner()` 启动 `dag_designer` planner 后直接调用 `markLaunched()`（`internal/sidecar/orch/orchestration/node_router.go:81-100`）；测试也只断言 planner 被启动、原 wakeup `MarkSent`，且没有 retry/fail node（`internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_smart_retry_test.go:80-113`）。`handleClaimedViaRouter()` 对非 failed outcome 同样把 wakeup 当完成处理（`internal/sidecar/orch/orchestration/node_executor_dispatch.go:302-341`）。planner prompt 要求其后续手动调用 `task_dag_apply_ops`（`internal/sidecar/orch/orchestration/node_router.go:111-120`），但这里没有记录 planner agent id、planner thread id、原 node 的 replan 状态或超时恢复入口。
   - 风险：原节点失败后不会进入 failed，也不会保留 pending retry；它的 wakeup 已经 `sent`，上一轮已确认 sent-unbound 没有 reclaim。若 planner 启动后不执行 apply_ops、执行失败、或无法回链到原 dag/node，原节点可能长期停留在 ready/running 等旧状态，调度器难以量化“正在重规划”和“已经丢失重规划”。
   - 建议：为 replan 引入显式 node 状态或 side table，例如 `replanning(planner_agent_id, planner_thread_id, started_at, source_wakeup_id)`；MarkSent 前后都要能通过状态机查询和超时补偿恢复。

3. **[major] smart retry 的 config patch 失败会直接 fail 节点，CAS 竞争被量化成永久失败而非重读策略后重试**
   - 证据：`retryWakeupWithSmartRetryConfig()` 先在同事务里 retry wakeup，再 `PatchTaskDagNodeConfigIfUnchanged`；patch 报错时调用 `failSmartRetryPrepare()`，最终 `FailWakeup` + `FailNodeAndCancelDownstream`（`internal/sidecar/orch/orchestration/retry_strategy.go:521-595`）。SQL patch 用 `config = previous_config` 作为 CAS，且只排除终态（`internal/sidecar/orch/sql/queries/task_dag_node_write.sql:14-27`）。测试明确锁定 stale config 会回滚 retry 并返回 `ErrNotFound`（`internal/sidecar/orch/store/taskdag/store_node_config_patch_test.go:86-133`），dispatcher 侧测试则锁定 patch failure 后直接 fail closed（`internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_smart_retry_extra_test.go:89-127`）。
   - 风险：并发 `task_dag_apply_ops`、人工改配置或另一条 dispatcher 尝试只要改变了 `config`，本轮智能重试就会把节点永久失败。实际这类 CAS miss 更像“策略上下文过期，需要重读”，但当前被量化为 smart retry prepare failed，可能级联取消下游。
   - 建议：区分 stale config 与真正 patch 错误；stale 时重新读取 node + on_failure 并重新决策，或把 wakeup 回 pending 而不是 fail node。若必须 fail closed，应在结果里标明 `stale_config`，不要与真实准备失败混淆。

4. **[moderate] `FailureClassInfrastructure` 已定义，但 agent launch 分类永远不会产出 infrastructure，by_class[infrastructure] 对 agent 节点基本不可达**
   - 证据：`FailureClassInfrastructure` 是七类失败之一（`internal/sidecar/orch/orchestration/nodeexec/types.go:42-50`），但 `classifyAgentLaunchError()` 只返回 quota/capability/validation/transient，未知也落 transient（`internal/sidecar/orch/orchestration/nodeexec/executor_agent.go:403-439`）。`FailureClassPermanent()` 也只把 hard/needs_human 视作永久，其余走重试或 smart retry（`internal/sidecar/orch/orchestration/retry_strategy.go:122-148`）。
   - 风险：用户为 agent 节点配置 `by_class.infrastructure` 期望对数据库、外部服务或 provider 基础设施故障走专门策略时，实际不会命中；例如本应 replan 或 ask_human 的基础设施故障会按 transient 继续普通重试。
   - 建议：agent launcher 分类补齐 infrastructure 关键字或 typed error，至少把 DB / service unavailable / dependency unavailable 与普通 transient 分开；同时增加 by_class[infrastructure] 命中测试。

5. **[moderate] permanent class 只允许 by_class 非重跑策略或 default=fail_fast，`default: replan` 对 hard/needs_human 被静默忽略**
   - 证据：`smartRetryStrategyFor()` 在 `failureClassPermanent()` 为 true 时调用 `permanentClassStrategyAllowed()`；default 策略只有 `fail_fast` 被允许，by_class 只有非 rerun 策略允许（`internal/sidecar/orch/orchestration/retry_strategy.go:291-331`）。测试明确锁定 hard/needs_human 不使用 `default: replan`（`internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_smart_retry_test.go:244-287`）。
   - 风险：配置层看起来支持 `default: replan`，但 hard/needs_human 失败不会按 default replan 执行，只有显式 by_class 才可能进入 replan。对量化策略来说，这是“配置可见但决策不可达”的隐性分支，容易让运行表现和策略文件不一致。
   - 建议：配置校验阶段对 permanent class 的 default 策略给出明确错误/警告；或在审计结果中记录“default suppressed for permanent class”，便于用户理解为什么没有 replan。

## 误报与已覆盖项

- `attempt_count` 在 claim 时递增，`MaxAttempts` 语义是包含首发；`default_retry=0` 一次失败即终态、`default_retry=2` 三次尝试已有单测覆盖（`internal/sidecar/orch/orchestration/retry_strategy_test.go:10-59`）。
- smart retry 的 retry + config patch 在 store 层是同事务，patch miss 会回滚 wakeup retry，不会留下“pending 但配置未变”的半状态（`internal/sidecar/orch/store/taskdag/store_wakeup.go:75-113`、`internal/sidecar/orch/store/taskdag/store_node_config_patch_test.go:86-133`）。本轮风险集中在 dispatcher 对 patch miss 的语义处理。
- unsupported 策略 `skip` / `ask_human` 当前 fail-closed 且已有测试覆盖，不报告为“未处理导致静默跳过”（`internal/sidecar/orch/orchestration/retry_strategy.go:367-377`、`internal/sidecar/orch/orchestration/wakeup_dispatcher_shard17_smart_retry_test.go:323-370`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/store/taskdag ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/orchestration/nodeexec -count=1
```

结果：Go guard、`internal/archtest`、`internal/sidecar/orch/store/taskdag`、`internal/sidecar/orch/orchestration` 与 `internal/sidecar/orch/orchestration/nodeexec` 通过。

## 下一轮建议

- Round 019 审查 DAG recover / repair / reclaimer 路径，重点看 sent-unbound、running stale、active_wakeup_id、spawning_thread_id 和 run finalization 是否存在永久卡住或重复补偿。
