# Round 029 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:48:18 KST
- 结束：2026-05-17 06:50:34 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 scheduled DAG cron/scheduler 与 automation 自动运行组合风险，重点看 `next_run_at` 更新顺序、StartDAG 幂等、单个 DAG 失败对批次的影响、多实例锁与自动运行风险策略。

- `cmd/mcp-orch/orchestration/cron/scheduler_cron.go`
- `cmd/mcp-orch/orchestration/cron/scheduler_cron_test.go`
- `cmd/mcp-orch/orchestration/cron/scheduler_cron_sql_test.go`
- `cmd/mcp-orch/orchestration/scheduler.go`
- `cmd/mcp-orch/orchestration/dag_start_test.go`
- `cmd/mcp-orch/store/taskdag/store_dag_ops.go`
- `cmd/mcp-orch/store/taskdag/store_update_dag_patch_test.go`
- `cmd/mcp-orch/orchestration/dag_ops_update_dag_test.go`

## Findings

1. **[critical] scheduler 先推进 `next_run_at` 再 `StartDAG`，StartDAG 失败会跳过本次计划触发**
   - 证据：`triggerDueDAG()` 先计算 nextRunAt，再调用 `store.UpdateNextRun()`，最后才 `starter.StartDAG()`（`cmd/mcp-orch/orchestration/cron/scheduler_cron.go:404-413`）。测试 `TestScheduledDAGTicker_NextRunAtUpdated` 也锁定 update 后 start（`cmd/mcp-orch/orchestration/cron/scheduler_cron_test.go:237-258`）；`TestScheduledDAGTicker_StartDAGErrorPassthrough` 只断言错误透传，没有回滚或恢复 next_run_at（`cmd/mcp-orch/orchestration/cron/scheduler_cron_test.go:294-306`）。
   - 风险：数据库瞬断、StartDAG 事务失败、命令卡/automation 配置错误等都会导致 `next_run_at` 已经移动到下一次 cron 时间，但当前 run 没有创建。量化日报/风控扫描会少跑一个周期，UI 只看到下一次时间已更新，很难发现漏触发。
   - 建议：把 due claim 和 run creation 放进一个事务；或先创建 scheduled run，再 CAS 推进 next_run_at；StartDAG 失败时保留 next_run_at 或写 `last_schedule_error` 并让下一 tick 重试。

2. **[major] ScheduledDAGTicker 对每个 due DAG 没有独立容错，任一失败会中断后续到期 DAG**
   - 证据：`Tick()` 遍历 due DAG，`triggerDueDAG()` 返回错误后立即 `return triggered, err`（`cmd/mcp-orch/orchestration/cron/scheduler_cron.go:374-384`）。cron parse、update_next_run_at、StartDAG error 都会走这一条。测试只覆盖单个 StartDAG 错误透传，没有“后续 DAG 继续启动”的语义（`cmd/mcp-orch/orchestration/cron/scheduler_cron_test.go:260-306`）。
   - 风险：一个坏 cron_expr、一个坏 DAG 或一个临时 StartDAG 错误，会阻塞同一 tick 后面的所有 scheduled DAG。量化系统中不同策略/市场的定时任务相互影响，单点配置错误会造成批量漏跑。
   - 建议：每个 DAG 单独记录错误并继续处理后续 due 项；Tick 返回聚合错误和 triggered/failed 计数。只有 scan/advisory lock 级错误才中断全批。

3. **[major] scheduled StartDAG 没有 idempotency key，重复 tick/重启可能创建多个同一计划时间的 run**
   - 证据：`DAGStarter` 接口只传 `dagKey` 和 `triggerSource`（`cmd/mcp-orch/orchestration/cron/scheduler_cron.go:311-315`）。`StartDAG()` 在 `IdempotencyKey` 为空时用 `unix_nano` 生成唯一 run_key（`cmd/mcp-orch/orchestration/scheduler.go:62-78`）。当前 scheduled ticker 没有把 scheduledAt/next_run_at 或 cron occurrence 传入 StartDAG，因此无法按计划时间去重。
   - 风险：如果 tick 重入、进程在 update 与 start 周期内重启、或多个实例在 advisory lock 缺失/异常时同时执行，同一计划时间可生成多个 running run。对于量化任务，这会重复下单、重复写报告或重复计算相同窗口。
   - 建议：`DAGStarter.StartDAG` 增加入参 `scheduledAt`，生成 idempotency key 如 `scheduled:<dagKey>:<scheduledAtUTC>`；`task_dag_runs` 对 `(dag_key, trigger_source, scheduled_at)` 加唯一约束或 metadata 去重。

4. **[major] 自动 scheduled run 不检查 DAG 内高风险 automation / command card 策略**
   - 证据：scheduled ticker 只按 `trigger='scheduled'`、`cron_expr`、`next_run_at` 扫 DAG 并调用 `StartDAG`（`cmd/mcp-orch/orchestration/cron/scheduler_cron.go:439-475`）。`StartDAG` 只创建 run、clone nodes、promote roots（`cmd/mcp-orch/orchestration/scheduler.go:64-128`），不检查节点类型、command card `risk_level` 或 approval policy。
   - 风险：Round 027/028 已确认 command card `risk_level` 和 review/run history 没接执行。scheduled DAG 会把这些高风险 automation 放到无人值守路径上，放大 shell/sandbox 缺口。
   - 建议：在 schedule enable 或 tick StartDAG 前做 DAG policy preflight：若含 high/critical command card，要求人工审批或禁止 cron；把 policy 快照写入 run metadata。

5. **[moderate] UpdateNextRun 没有行数检查，dag_key 丢失或被删除时 tick 可能继续尝试 StartDAG**
   - 证据：`SQLDAGScheduleStore.UpdateNextRun()` 执行 `UPDATE task_dags ... WHERE dag_key=$2` 后直接返回 `err`，不检查 affected rows（`cmd/mcp-orch/orchestration/cron/scheduler_cron.go:467-475`）。如果 DueDAG 是 stale 投影或并发删除，update 0 行不会报错。
   - 风险：tick 会继续调用 StartDAG，然后由 StartDAG 的 GetDAG 失败。问题最终可见，但错误分类会指向 StartDAG，而不是 schedule store 的 stale row/CAS miss；排查并发删除或竞态时更困难。
   - 建议：检查 rows affected，0 行返回 `TickErrorClassValidation` 或 infrastructure CAS miss；同时在 update WHERE 中加 `next_run_at <= dueAt` 防止并发推进覆盖。

## 误报与已覆盖项

- 多实例互斥有 advisory lock 路径，测试覆盖一个实例持锁时另一个 tick 不触发（`cmd/mcp-orch/orchestration/cron/scheduler_cron_test.go:308-342`）。
- scheduled DAG 的 cron_expr 在 update_dag 操作中有解析校验，测试覆盖 invalid/empty/manual DAG 带 cron_expr 等配置错误（`cmd/mcp-orch/orchestration/dag_ops_update_dag_test.go:195-345`）。
- StartDAG 本身在事务内创建 run、clone runtime nodes、promote roots，避免 run 已建但根节点未 ready 的半成品（`cmd/mcp-orch/orchestration/scheduler.go:110-138`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration/cron ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/store/taskdag -count=1
```

结果：待执行。

## 下一轮建议

- Round 030 审查 DAG run completion/finalization 与 scheduled run 状态闭环，重点看 running run 如何变 succeeded/failed、final_output 写入和 stuck run 恢复。
