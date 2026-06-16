# Round 051 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:57:45 KST
- 结束：2026-05-17 07:59:20 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 dispatcher claim/lease/reclaim、DAG wakeup sent/bind turn 生命周期、agent/automation 路由成功路径和 recover replay。重点看量化 DAG 节点被 claim 后，是否会因 lease 过期、sent-unbound、running 写回失败或 recovery 口径不同导致重复执行、卡死或无法诊断。

- `internal/sidecar/orch/sql/queries/task_dag_wakeup_dispatch.sql`
- `internal/sidecar/orch/sql/queries/task_dag_wakeup_query.sql`
- `internal/sidecar/orch/sql/queries/task_dag_node_runtime.sql`
- `internal/sidecar/orch/store/taskdag/store_wakeup.go`
- `internal/sidecar/orch/store/taskdag/store.go`
- `internal/sidecar/orch/store/taskdag/store_fencing_test.go`
- `internal/sidecar/orch/orchestration/wakeup_dispatcher.go`
- `internal/sidecar/orch/orchestration/node_executor_dispatch.go`
- `internal/sidecar/orch/orchestration/wakeup_reclaim.go`
- `internal/sidecar/orch/orchestration/wakeup_reclaim_test.go`
- `internal/sidecar/orch/orchestration/node_router.go`
- `internal/sidecar/orch/orchestration/recover.go`

## Findings

1. **[major] stale dispatching 回收会清掉 claim 证据，但不记录回收原因或上一次执行阶段**
   - 证据：claim 时直接把 wakeup 置为 `dispatching` 并递增 `attempt_count`（`internal/sidecar/orch/sql/queries/task_dag_wakeup_dispatch.sql:6-30`）。reclaim SQL 只把过期 `dispatching` 改回 `pending`，清空 `claimed_at/claimed_by/lease_expires_at`，没有写 `last_error` 或 reclaim reason（`internal/sidecar/orch/sql/queries/task_dag_wakeup_query.sql:1-4`）。reclaimer 也只记录总行数（`internal/sidecar/orch/orchestration/wakeup_reclaim.go:100-117`）。
   - 风险：量化节点可能已经启动了外部 agent、执行了 automation 或写了 sharedfile，只是 dispatcher 在 MarkSent/Retry/Fail 前超时。回收后下一轮会再次 claim 并执行，`attempt_count` 变大但无法从行内判断这是“真正失败重试”还是“执行后状态写回丢失”。
   - 建议：reclaim 时写入结构化 `last_error`/`last_reclaimed_at`/`last_claimed_by`，或追加 run event，至少区分 claim-only、launch-started、mark-sent-fence-miss 等阶段。

2. **[major] `ProcessBatch` 在 launcher 缺失时退化成 `Tick`，会 claim 但不执行**
   - 证据：`ProcessBatch()` 如果 `d.launcher == nil` 直接返回 `d.Tick(ctx)`（`internal/sidecar/orch/orchestration/wakeup_dispatcher.go:195-201`）。`Tick()` 注释明确 claim 后不调用 launcher，wakeup 会停在 `dispatching` 直到 reclaim cron 回收（`internal/sidecar/orch/orchestration/wakeup_dispatcher.go:449-456`）。
   - 风险：部署或 fx wiring 漏注 launcher 时，系统不会 fail closed，而是不断 claim due wakeup、等待 lease 过期、再回收为 pending。量化任务看起来像“在调度”，实际没有执行，还会持续消耗 attempt_count 并污染重试指标。
   - 建议：生产 runner 中 launcher/nodeRouter 缺失应禁用 dispatcher 或启动失败；`Tick` 只保留测试/诊断入口，不应作为 `ProcessBatch` 的运行时 fallback。

3. **[major] automation 执行成功但完成写回失败时，dispatcher 仍会把 wakeup 标记为 sent**
   - 证据：DAG router 的非失败 outcome 默认 `markLaunched()`（`internal/sidecar/orch/orchestration/node_executor_dispatch.go:335-341`），`markLaunched()` 只写 wakeup `status='sent'`（`internal/sidecar/orch/orchestration/wakeup_dispatcher.go:278-293`）。automation 执行成功后调用 `completeAutomationNode()`，但该写回失败只 logWarn，`dispatchAutomation()` 仍返回原成功 outcome（`internal/sidecar/orch/orchestration/node_router.go:442-463`）；注释也说明失败不阻塞主流，dispatcher 仍会 MarkWakeupSent（`internal/sidecar/orch/orchestration/node_router.go:466-468`）。
   - 风险：量化 automation 的命令副作用已经发生，但节点状态可能没有 `done`，下游也没有被调度；同时 wakeup 已是 `sent`，不会被 stale-dispatching reclaimer 处理。后续既可能卡住，也可能被人工补偿时重复执行同一 automation。
   - 建议：automation 完成写回失败应作为 framework error 传播给 dispatcher 走 retry/fail，或引入 automation-specific sent recovery，把“side effect done but node completion missing”作为独立状态补偿。

4. **[major] sent-unbound 只有查询接口，未看到自动回收或重绑生产路径**
   - 证据：SQL 能列出 `status='sent' AND bound_turn_id IS NULL` 的 wakeup（`internal/sidecar/orch/sql/queries/task_dag_wakeup_query.sql:6-10`），store 暴露 `ListSentUnboundWakeups()`（`internal/sidecar/orch/store/taskdag/store_wakeup.go:135-139`）。本轮在 orchestration/store 范围内只看到该接口定义与查询，未看到 dispatcher/reclaimer 自动消费它的生产调用点。
   - 风险：agent launch 成功后如果 StartTurn/BindRunningNodeTurn 之前失败，或 automation 成功后没有 child turn，本行会长期停留在 sent-unbound。它既不再是 pending，也不是 dispatching，不受现有 lease reclaim 保护。
   - 建议：增加 sent-unbound reclaimer：超过阈值后按 node_type 分流，agent 走查询运行时/重绑 turn，automation 走完成补偿或 fail；所有处理都要带 dag_key/node_key/run_id 审计。

5. **[moderate] recovery replay 只覆盖已绑定 active turn 的 agent 路径，不覆盖 automation/sent-unbound 空档**
   - 证据：recover replay 需要 agent runtime 有 `activeTurnID`（`internal/sidecar/orch/orchestration/recover.go:98-115`），再从该 agent 的 running nodes 中找 `active_turn_id` 匹配的节点（`internal/sidecar/orch/orchestration/recover.go:117-139`），并要求 wakeup `status='sent'`、`bound_turn_id` 等于 active turn、`turn_bound_at` 非空（`internal/sidecar/orch/orchestration/recover.go:171-182`）。
   - 风险：这是合理的 agent replay fence，但也意味着 automation 节点、agent launch 后未绑定 turn 的 sent-unbound、以及 ready->running 写回失败后的 sent 行，都不在 recover 范围内。量化 DAG 恢复能力会因节点类型和失败窗口不同而不一致。
   - 建议：把 recover 拆成 agent-turn replay 与 DAG wakeup reconciliation 两层；后者按 wakeup/node 状态矩阵补偿 sent-unbound、running-no-active-turn、automation completion-missing。

## 误报与已覆盖项

- MarkSent/Retry/Fail 都使用完整 claim fence，过期 lease 的旧 dispatcher 无法继续提交状态；这是防止旧 worker 覆盖新 claim 的有效保护（`internal/sidecar/orch/sql/queries/task_dag_wakeup_dispatch.sql:32-67`；`internal/sidecar/orch/store/taskdag/store_fencing_test.go:103-180`）。
- `BindRunningNodeTurn()` 把 wakeup turn 绑定与 running node active_turn 绑定放在同一个事务中，且 node SQL 校验 `status='running'`、`active_turn_id IS NULL`、`active_wakeup_id` 匹配；本轮不把绑定本身视为缺陷（`internal/sidecar/orch/store/taskdag/store.go:181-210`；`internal/sidecar/orch/sql/queries/task_dag_node_runtime.sql:1-13`）。
- `UpdateRunningTaskDagNodeStatus` 只允许 pending/ready 进入 running，避免把终态节点反向覆盖为 running；本轮关注的是写回失败后 dispatcher 仍然 sent 的补偿空档（`internal/sidecar/orch/sql/queries/task_dag_node_runtime.sql:28-36`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/store/taskdag -count=1
```

结果：通过。

## 下一轮建议

- Round 052 审查 DAG turn.completed subscriber、CompleteNodeAndScheduleDownstream、active_turn_id fence 与 run finalization 的交互，确认 completion 事件是否会错配量化节点或遗留运行态。
