# Round 053 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 08:04:12 KST
- 结束：2026-05-17 08:08:40 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 DAG scheduled ticker、cron daemon wrapper、SQL schedule store、advisory lock、`StartDAG` 幂等与 scheduled DAG 的 `next_run_at` 初始化。重点看定时量化任务是否会漏跑、重复跑、提前确认成功或因 wiring 缺失根本不跑。

- `cmd/mcp-orch/orchestration/cron/scheduler_cron.go`
- `cmd/mcp-orch/orchestration/cron/scheduler_cron_test.go`
- `cmd/mcp-orch/orchestration/cron/scheduler_cron_sql_test.go`
- `cmd/mcp-orch/orchestration/scheduler.go`
- `cmd/mcp-orch/orchestration/dag_start_test.go`
- `cmd/mcp-orch/store/taskdag/store_dag_ops.go`
- `cmd/mcp-orch/store/taskdag/store_update_dag_patch_test.go`
- `cmd/mcp-orch/sql/queries/task_dag_run.sql`

## Findings

1. **[major] `next_run_at` 先推进再 `StartDAG`，启动失败会漏掉本次定时窗口**
   - 证据：`triggerDueDAG()` 先计算下一次时间并调用 `UpdateNextRun()`，之后才调用 `StartDAG()`（`cmd/mcp-orch/orchestration/cron/scheduler_cron.go:404-412`）。测试只断言 next_run_at 被更新且 StartDAG 被调用，没有覆盖 StartDAG 失败后回滚 next_run_at（`cmd/mcp-orch/orchestration/cron/scheduler_cron_test.go:237-257`；`cmd/mcp-orch/orchestration/cron/scheduler_cron_test.go:294-305`）。
   - 风险：量化 DAG 到点后如果 `StartDAG` 因 DB、模板、根节点 promote 等错误失败，`next_run_at` 已经跳到下一周期；本周期任务会被系统当作已处理而漏跑。
   - 建议：把 claim/update-next-run 与 StartDAG 变成单个事务性 trigger record，或先创建 scheduled run 成功后再推进 next_run_at；失败时保留 due 状态并记录 last_schedule_error。

2. **[major] 单个 due DAG 失败会中断本 tick 的后续 DAG**
   - 证据：`Tick()` 遍历 `DueDAGs()` 结果，只要 `triggerDueDAG()` 返回错误就立即返回（`cmd/mcp-orch/orchestration/cron/scheduler_cron.go:374-383`）。`DueDAGs` 按 `next_run_at ASC, id ASC` 返回所有 due DAG（`cmd/mcp-orch/orchestration/cron/scheduler_cron.go:439-448`）。
   - 风险：一个坏 cron_expr、一个 StartDAG 失败或一个 update_next_run_at 基础设施错误，会阻断同一批次后续所有量化定时任务。低优先级或配置损坏 DAG 可造成批处理头阻塞。
   - 建议：逐 DAG 记录错误并继续处理后续 due DAG；最终返回聚合错误和触发/失败统计。

3. **[major] scheduled run 未传入按 scheduled_at 派生的幂等键**
   - 证据：cron 子包的 `DAGStarter` 适配口只有 `StartDAG(ctx, dagKey, triggerSource)`（`cmd/mcp-orch/orchestration/cron/scheduler_cron.go:311-315`），`triggerDueDAG()` 只传 `scheduledTriggerSource`（`cmd/mcp-orch/orchestration/cron/scheduler_cron.go:404-412`）。`StartDAG()` 只有在 `IdempotencyKey` 非空时才用稳定 run_key，否则用 `time.Now().UnixNano()` 生成新 run（`cmd/mcp-orch/orchestration/scheduler.go:64-78`；`cmd/mcp-orch/orchestration/scheduler.go:211-218`）。
   - 风险：同一 scheduled_at 在崩溃重试、手工补跑或多实例锁失效时会创建多个不同 run。量化报表/交易模拟这类定时任务会重复执行同一时间窗口。
   - 建议：把 `scheduled_at` 纳入 `StartDAGRequest.IdempotencyKey`，例如 `schedule:<dag_key>:<due_at_utc>`；`DAGStarter` 接口应携带 due timestamp。

4. **[major] advisory lock 是可选依赖，缺失时默认无锁执行**
   - 证据：`NewScheduledDAGTicker()` 不要求 `Locker` 非 nil（`cmd/mcp-orch/orchestration/cron/scheduler_cron.go:338-350`），`tryAdvisoryLock()` 在 `t.locker == nil` 时直接返回 noop handle 和 acquired=true（`cmd/mcp-orch/orchestration/cron/scheduler_cron.go:387-390`）。测试中正常扫描也用 nil locker（`cmd/mcp-orch/orchestration/cron/scheduler_cron_test.go:210-220`）。
   - 风险：生产 wiring 若漏注 `PGAdvisoryLocker`，多个 mcp-orch 实例会各自扫描 due DAG；结合缺少 scheduled_at 幂等键，会放大重复启动。
   - 建议：生产构造路径应要求 locker 必填，测试显式注入 noop locker；或在 nil locker 下拒绝启动并暴露配置错误。

5. **[moderate] scheduled DAG 的 `next_run_at` 依赖调用方传入，仓库层不会自动计算首次触发时间**
   - 证据：`UpdateDAGPatch` 只有在 trigger/cron_expr 更新或 next_run_at 为空时写 `COALESCE($7, next_run_at)`，如果调用方没有传 `$7` 且原值为空，则仍为空（`cmd/mcp-orch/store/taskdag/store_dag_ops.go:83-90`）。`DueDAGs` 又要求 `next_run_at IS NOT NULL` 才扫描（`cmd/mcp-orch/orchestration/cron/scheduler_cron.go:439-448`）。测试明确要求 UpdateDAGPatch SQL 不自动初始化为 NOW（`cmd/mcp-orch/store/taskdag/store_update_dag_patch_test.go:58-64`）。
   - 风险：DAG 被设置为 `trigger=scheduled` 且 `cron_expr` 非空，但上层忘记计算并传入 `next_run_at` 时，该量化任务永远不会进入 due 扫描。
   - 建议：把首次 next_run_at 计算集中到 service 层并加测试，或在 store 层拒绝 scheduled+cron_expr 但 next_run_at 为空的写入。

## 误报与已覆盖项

- `StartDAG()` 本身在事务内完成 DAG row lock、CreateRun、CloneNodesForRun、PromoteRootNodesToReady，根节点 promote 失败会回滚 run 创建（`cmd/mcp-orch/orchestration/scheduler.go:110-137`）。
- `SQLDAGScheduleStore.DueDAGs()` 已过滤 `trigger='scheduled'`、`cron_expr<>''`、`next_run_at<=now`，不会扫描手动 DAG（`cmd/mcp-orch/orchestration/cron/scheduler_cron.go:439-448`；`cmd/mcp-orch/orchestration/cron/scheduler_cron_sql_test.go:14-28`）。
- 多实例 advisory lock 的基本行为有测试覆盖：一个 ticker 持锁时另一个 ticker 返回 0，且 Tick 退出会释放锁（`cmd/mcp-orch/orchestration/cron/scheduler_cron_test.go:308-363`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration/cron ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/store/taskdag -count=1
```

结果：通过。

## 下一轮建议

- Round 054 审查 `internal/module/cron` 通用 cron turn scheduler：claim/active_turn/observe_lost/MarkFinished 语义与 DAG scheduled ticker 是否存在同类漏跑或重复提交问题。
