# Round 056 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 08:22:21 KST
- 结束：2026-05-17 08:25:37 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 cron job 的创建、更新、RunOnce、RPC 参数归一化、cron 表达式/时区与 next_run_at 初始化。重点看用户创建量化定时任务后，是否会提前触发、永不触发、无法手动触发或误覆盖运行态。

- `internal/module/cron/service.go`
- `internal/module/cron/contract.go`
- `internal/module/cron/rpc.go`
- `internal/module/cron/schedule.go`
- `internal/module/cron/service_test.go`
- `internal/module/cron/schedule_test.go`
- `internal/store/cron/store.go`
- `sql/queries/cron_job.sql`

## Findings

1. **[major] 创建/更新没有校验 cron 表达式，错误配置可成功落库后在完成时无法推进下一次运行**
   - 证据：service `validateCreate()` 只检查 `ScheduleExpr` 非空，没有调用 `ParseSchedule`/`ComputeNextRunAt`（`internal/module/cron/service.go:248-279`）。而仓库已经有 parser，能识别 invalid cron（`internal/module/cron/schedule.go:34-64`）。完成时 `markFinished()` 才计算下一次运行；若解析失败，则回退为当前 `job.NextRunAt`（Round 054 已见 `internal/module/cron/scheduler_recovery.go:66-84`）。
   - 风险：用户能创建 `schedule_expr="not a cron"` 的量化任务。它会按初始 next_run_at 被触发一次，但成功完成后 next_run_at 不会前进，导致同一 due 时间反复被 claim，形成重复执行或异常循环。
   - 建议：Create/Update 阶段强校验 `ParseSchedule`，把 `ErrInvalidScheduleExpr` 映射为 InvalidParams；不要等到运行完成才发现表达式不可解析。

2. **[major] 默认 `next_run_at=now+1m` 不使用 cron 表达式，会让未来计划任务提前触发**
   - 证据：`CreateJob()` 在请求未给 `NextRunAt` 时固定 `now.Add(defaultInitialDelay)`，`UpdateJob()` 同样固定为 `now+1m`（`internal/module/cron/service.go:42-64`；`internal/module/cron/service.go:149-153`）。代码注释仍写“Phase 2b will replace this with a real cron-expression parser”，但 parser 已存在（`internal/module/cron/schedule.go:34-64`）。测试也锁定了 exactly defaultInitialDelay（`internal/module/cron/service_test.go:234-263`）。
   - 风险：用户创建每日 09:00 或每周任务时，如果不显式传 `next_run_at`，任务会在 1 分钟后立即执行一次，而不是等到真实 cron 窗口。量化回测/交易巡检类任务会提前跑错时间窗口。
   - 建议：默认 next_run_at 应由 `ComputeNextRunAt(schedule_expr, timezone, now)` 计算；只有显式 RunOnce 才把 next_run_at 设为 now。

3. **[major] `UpdateJob` 是全量覆盖，未带 `next_run_at` 的普通编辑会把已有排程重置为 1 分钟后**
   - 证据：RPC update 先把创建参数转换成完整 `UpdateJobRequest`，遗漏字段使用零值（`internal/module/cron/rpc.go:110-132`）。service update 对 zero `NextRunAt` 固定设为 `now+1m`，然后把整行 schedule/config/enabled/max_attempts 覆盖写回（`internal/module/cron/service.go:118-178`；`sql/queries/cron_job.sql:52-64`）。
   - 风险：用户只想改名称、提示词或通知渠道，但 RPC 未传 `next_run_at` 时，原本明天/下周的量化任务会被改成一分钟后触发。未传 `enabled` 也会经 `createRequestFrom` 默认 true，可能把被暂停任务重新启用。
   - 建议：提供 patch update 语义，或 update handler 先 GetJob 合并旧值；`enabled` 和 `next_run_at` 应区分“未传”和“显式传”。

4. **[moderate] `RunOnce` 只改 `next_run_at`，未来 retry 会屏蔽手动触发**
   - 证据：`RunOnce()` 只调用 `PatchNextRunAt(now)`（`internal/module/cron/service.go:215-243`；`sql/queries/cron_job.sql:72-78`）。代码注释明确：如果 `next_retry_at` 在未来，claim 仍按 `COALESCE(next_retry_at, next_run_at)` 等待 retry delay（`internal/module/cron/service.go:221-223`；`sql/queries/cron_job.sql:92-95`）。
   - 风险：用户看到任务失败后点“立即运行”，但如果系统已有 future retry，手动触发不会立即生效。量化任务的人工补跑会被隐藏的 retry 状态拦截。
   - 建议：RunOnce 应提供 clear_retry/force 参数，或默认清空 `next_retry_at` 并记录 manual trigger 来源。

5. **[moderate] Delete/Disable 不检查 running/claimed 状态，可直接切断运行中任务的状态回写**
   - 证据：`DeleteJob()` 直接按 id 删除（`internal/module/cron/service.go:192-200`；`internal/store/cron/store.go:159-165`）。cron_job_runs 外键 `ON DELETE CASCADE` 会级联删除 run（`migrations/0045_cron_jobs.sql:55-58`）。`SetJobEnabled()` 也只是更新 enabled，不检查 `claim_token/active_turn_id`（`internal/module/cron/service.go:181-189`；`sql/queries/cron_job.sql:66-70`）。
   - 风险：运行中的量化 job 被删除时，终态事件到达后 `CompleteTurn()` 找不到 running run，会当成 benign duplicate 丢弃；provider turn 仍可能继续执行，但本地审计链被切断。Disable 也不会 stop 当前 turn，只会影响后续 claim，用户容易误以为已停止运行。
   - 建议：Delete running job 前要求先 cancel/force complete，或软删除并保留 run；Disable 应明确“只暂停未来触发”，必要时提供 stop-active 选项。

## 误报与已覆盖项

- `schedule.go` 的 parser 本身已有测试覆盖空表达式、非法表达式、时区计算和 retry 上界（`internal/module/cron/schedule_test.go:10-74`）。
- `RunOnce` 会拒绝 disabled job，并正确把 not found 映射为 `ErrNotFound`（`internal/module/cron/service_test.go:457-491`）。
- RPC 创建路径会把未传 `enabled` 默认成 true，行为明确但也导致 update 全量覆盖风险（`internal/module/cron/rpc.go:205-237`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/cron ./internal/store/cron -count=1
```

结果：通过。

## 下一轮建议

- Round 057 审查 cron 与 thread/turn adapter 的 bootstrap 语义：`ThreadServiceBootstrapper`、`CronThreadStarter`、identity/config 透传、首次触发空 thread_id 时是否会重复创建线程或丢失 agent 绑定。
