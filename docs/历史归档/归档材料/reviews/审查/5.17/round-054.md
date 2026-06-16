# Round 054 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 08:10:02 KST
- 结束：2026-05-17 08:17:35 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查通用 `internal/module/cron` 定时 turn 调度器，重点看 claim、提交、active turn 绑定、恢复、终态事件回写是否会导致量化任务漏跑、重复提交或状态不可恢复。

- `internal/module/cron/scheduler.go`
- `internal/module/cron/scheduler_recovery.go`
- `internal/module/cron/turn_adapter.go`
- `internal/module/cron/service.go`
- `internal/module/cron/scheduler_test.go`
- `internal/module/turn/service.go`
- `internal/store/cron/store.go`
- `internal/store/cron/contract.go`
- `sql/queries/cron_job.sql`
- `migrations/0045_cron_jobs.sql`
- `migrations/0060_turn_dedupe_registry.sql`

## Findings

1. **[major] `SetActiveTurn` 失败会留下 submitted run 与空 active_turn 的不一致状态**
   - 证据：`persistSubmittedTurn()` 先 `SetRunTurn`，再把 run 从 `submitting` CAS 到 `submitted`，发布 `submitted` 事件后才调用 `SetActiveTurn()`（`internal/module/cron/scheduler.go:371-399`）。`SetActiveTurn` 受 `claim_token` fence 约束，0 行会返回 `ErrClaimTokenMismatch`（`internal/store/cron/store.go:368-386`；`sql/queries/cron_job.sql:180-184`）。测试只覆盖 happy path，没有覆盖 `SetActiveTurn` 失败后的补偿（`internal/module/cron/scheduler_test.go:243-291`）。
   - 风险：如果 lease 已过期、被其他 scheduler 抢占，或 DB 在最后一步失败，run 已经是 `submitted` 且有 `turn_id`，但 `cron_jobs.active_turn_id/thread_id/agent_id` 可能没写入。后续 `ExtendClaimForTurnProgress()` 按 active_turn 找 job，会找不到该 turn；进度无法延长 lease，终态事件虽然能按 run 查回，但 `MarkFinished/MarkFailed` 又依赖 job 当前 claim_token，容易变成不可完成的悬挂 run。
   - 建议：将 `SetRunTurn + CAS submitted + SetActiveTurn` 放进同一事务，或在 `SetActiveTurn` 失败时把 run 标成可恢复错误并由 recovery 重新绑定 active_turn；至少增加回归测试覆盖 claim_token mismatch。

2. **[major] 恢复路径用旧 claim_token 执行 `MarkFailed`，过期/空 claim 会让 recovery 无法终结 dangling run**
   - 证据：`RecoverDanglingRuns()` 直接读取 unresolved runs，再 `GetJobByID()` 获取 job，并在 `finalizeRecoveredFailure()` / `finalizeRecoveredObserveLost()` 中用 `job.ClaimToken` 调 `MarkFailed()`（`internal/module/cron/scheduler_recovery.go:134-168`；`internal/module/cron/scheduler_recovery.go:222-241`）。`MarkFailed` 要求非空 claim_token 且 SQL `WHERE id = $id AND claim_token = $token`（`internal/store/cron/store.go:339-364`；`sql/queries/cron_job.sql:162-176`）。
   - 风险：进程重启时，dangling run 对应 job 的 claim 可能已被释放、为空，或被新 scheduler 抢走。此时 recovery 识别到 provider lookup miss、submitted missing turn、running lease expired 等终态条件，也会因为 claim fence 失败而无法写入 `failed/observe_lost`，下次启动继续扫描同一 unresolved run。
   - 建议：为 recovery 增加独立的 run-level 终结 SQL，或先原子 claim recovery ownership 再 MarkFailed；不要复用普通执行路径的旧 claim_token 作为恢复终结前提。

3. **[major] `recoverRunningRun` 把 lease 过期直接判为 `observe_lost`，可能误杀仍在运行的长任务**
   - 证据：`recoverRunningRun()` 只要 `job.LeaseExpiresAt.Before(now)` 就调用 `finalizeRecoveredObserveLost(..., "cron: running lease expired")`，不会先 `Observe(turnID)`（`internal/module/cron/scheduler_recovery.go:197-205`）。而正常长任务依赖 `RenewLeases()` 和 `ExtendClaimForTurnProgress()` 保持 claim（`internal/module/cron/scheduler_recovery.go:102-128`；`internal/module/cron/scheduler_recovery.go:244-261`）。
   - 风险：scheduler 暂停、事件延迟、active_turn 绑定失败或进度事件丢失，都会让 lease 过期；但 provider 端 turn 可能仍在执行。重启恢复会直接把 run/job 标为 `observe_lost`，量化任务完成后终态事件将被 `CompleteTurn()` 当成无 running row 的重复事件丢弃。
   - 建议：即使 lease 过期，也应先尝试 `Observe(turnID)` 或查询 durable turn 状态；只有 provider 明确 not found/permission denied 时再进入 `observe_lost`。

4. **[moderate] `CompleteTurn` 只查 `status='running'`，会丢弃已 submitted 但尚未 CAS running 的真实终态事件**
   - 证据：`CompleteTurn()` 通过 `GetRunningRunByTurnID()` 查 run（`internal/module/cron/scheduler_recovery.go:20-33`），SQL 固定 `WHERE turn_id = $1 AND status = 'running' LIMIT 1`（`sql/queries/cron_job.sql:256-266`）。提交路径在 `persistSubmittedTurn()` 后才 `observeStartedTurn()` CAS 到 running（`internal/module/cron/scheduler.go:371-415`）。
   - 风险：如果 provider turn 极快完成，终态事件可能在 run 仍是 `submitted` 时到达；`CompleteTurn` 会把它视为 benign duplicate 直接返回 nil。之后如果没有第二个终态事件，run 只能依赖 recovery 或人工清理。
   - 建议：终态 subscriber 应能处理 `submitted/running` 两种状态，或者将提交到 running 的 CAS 与观察注册前置成不会错过终态的顺序。

5. **[moderate] cron run 的幂等键每次 claim 随机生成，不能表达同一 scheduled_at 的稳定业务幂等**
   - 证据：`createPendingRun()` 每次用 `s.newID()` 生成 `idempotencyKey`，再用 `DedupeKey(job.ID, scheduledAt, idempotencyKey)` 生成 provider dedupe（`internal/module/cron/scheduler.go:320-328`）。表注释也定义 dedupe_key 包含随机 idempotency_key（`migrations/0045_cron_jobs.sql:9-11`），唯一约束是 `(job_id, idempotency_key)` 和 `dedupe_key`（`migrations/0045_cron_jobs.sql:83-88`）。
   - 风险：同一个 `scheduled_at` 如果因 claim 过期、恢复误判或人工重复触发重新走 `createPendingRun()`，会得到新的 idempotency/dedupe key，无法靠 DB 或 provider collapse 成同一业务窗口。量化任务可能对同一个时间窗口重复执行。
   - 建议：将业务窗口纳入稳定幂等键，例如 `job_id + scheduled_at + trigger_kind`，随机 ID 只作为 run 主键；手工 RunOnce 可显式生成单独 trigger id。

## 误报与已覆盖项

- `RunTick()` 遇到单个 job 失败会继续循环 claim 后续 job，并把错误 join 返回，不会像 DAG scheduled ticker 那样一个失败阻断整批（`internal/module/cron/scheduler.go:248-272`）。
- `StartTurn` 失败路径会把 run 尝试标为 failed，并通过 `MarkFailed` 释放 claim、写 retry（`internal/module/cron/scheduler.go:300-302`；`internal/module/cron/scheduler.go:418-438`）。
- `Observe` 失败路径明确进入 `observe_lost` 且不自动 retry，已有测试覆盖（`internal/module/cron/scheduler_test.go:326-352`）。
- durable dedupe registry 已存在，`LookupByDedupeKey` 会在 tracker miss 后查 store（`internal/module/turn/service.go:321-370`；`migrations/0060_turn_dedupe_registry.sql:1-45`）。本轮 finding 不是说完全没有跨进程 dedupe，而是当前 cron run 自身仍使用随机业务幂等键，且 registry 写入是 best-effort。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/cron ./internal/store/cron -count=1
```

结果：通过。

## 下一轮建议

- Round 055 审查 `internal/module/cron` 的 actor/subscriber wiring、progress event 队列、`NewCronProgressSubscribers` 与 Fx module 默认 wiring，确认终态事件和进度续租是否可能在生产路径未订阅、丢失或阻塞。
