# Round 055 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 08:18:01 KST
- 结束：2026-05-17 08:22:02 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 cron turn 调度器的 actor、progress subscriber 与 BusModule wiring。重点看终态事件、进度续租、启动恢复和多实例运行时的可观测性/可靠性风险。

- `internal/module/cron/progress_subscriber.go`
- `internal/module/cron/subscribers.go`
- `internal/module/cron/subscribers_test.go`
- `internal/module/cron/tick_actor.go`
- `internal/module/cron/lease_actor.go`
- `internal/module/cron/module.go`
- `internal/module/cron/scheduler.go`
- `internal/module/cron/scheduler_recovery.go`
- `internal/platform/bus/module.go`
- `internal/platform/bus/subscribers.go`
- `internal/contract/bus.go`
- `sql/queries/cron_job.sql`

## Findings

1. **[major] 默认 `ClaimedBy` 固定为 `cron-scheduler`，多实例会互相续租/完成对方 claim**
   - 证据：`SchedulerConfig.withDefaults()` 在未配置时固定写 `ClaimedBy = "cron-scheduler"`（`internal/module/cron/scheduler.go:180-183`）。claim 时把该值写入 `claimed_by`，但 lease/finish 的强 fence 只有 `claim_token`（`internal/module/cron/scheduler.go:275-283`；`sql/queries/cron_job.sql:88-128`）。续租和进度续租先按 `claimed_by` 列出 job（`sql/queries/cron_job.sql:268-279`），再使用各行 claim_token 续租。
   - 风险：两个进程如果都走默认配置，就会共享同一个 claimed_by。任一实例的 `RenewLeases()` / `ExtendClaimForTurnProgress()` 都可能续租另一个实例 claim 的 job；终态事件处理也会按 job 当前 claim_token 继续 MarkFinished/MarkFailed。多实例下 claim ownership 的可观测边界会被抹平。
   - 建议：生产默认 `ClaimedBy` 应包含实例 UUID/hostname/pid，并在配置为空时自动生成；`ListJobsClaimedBy` 不应成为跨实例共享 owner。

2. **[major] `RecoverDanglingRuns()` 只在 TickActor 启动时跑一次，后续 dangling run 没有周期性修复**
   - 证据：`TickActor.Run()` 启动时调用一次 `scheduler.RecoverDanglingRuns(ctx)`，之后循环只调用 `RunTick()`（`internal/module/cron/tick_actor.go:41-60`）。`RunTick()` 只 claim due jobs，不扫描 unresolved runs（`internal/module/cron/scheduler.go:248-272`）。
   - 风险：运行中如果终态事件丢失、progress worker 失败、`SetActiveTurn` 失败或 CAS running 后进程未重启，run 会长期停在 `submitting/submitted/running`。系统只有重启才触发 recovery，量化定时任务可能一直被旧 claim/active_turn 状态卡住。
   - 建议：增加独立 recovery actor 或低频 recovery tick，并确保 recovery 本身有 run-level fence，避免与正常执行冲突。

3. **[major] 终态事件处理失败只 debug 记录，不重试也不进入持久化队列**
   - 证据：progress worker 收到 `TurnCompleted/TurnInterrupted` 后调用 `scheduler.CompleteTurn()`，返回错误只 `Debug` 日志后丢弃（`internal/module/cron/progress_subscriber.go:180-186`）。Bus subscriber 本身只做内存入队（`internal/module/cron/progress_subscriber.go:206-221`），没有 durable outbox。
   - 风险：DB 短暂错误、claim_token mismatch、GetJobByID 失败或 MarkFinished 失败时，唯一终态事件被消费但未落库。run 会停在 running，job claim/active_turn 不释放；下一次量化窗口可能被阻塞或重复恢复。
   - 建议：终态事件应写 durable retry/outbox，或在 `CompleteTurn` 失败时将 run 标记为待补偿；至少 warn/error 级别并暴露指标。

4. **[moderate] progress worker 队列无界，事件风暴可造成内存增长并延迟终态**
   - 证据：`cronProgressWorker` 用 slice 保存 `queue []cronProgressRequest`，`enqueue()` 每次 append，无容量上限、去重或背压（`internal/module/cron/progress_subscriber.go:43-58`；`internal/module/cron/progress_subscriber.go:119-137`）。单 goroutine 串行 `dispatch()`，每个请求都可能做 DB 查询/更新（`internal/module/cron/progress_subscriber.go:153-187`）。
   - 风险：高频 `ItemCompleted` 事件或 provider 批量回放会把续租事件堆积到内存；终态事件排在大量进度事件后面时，MarkFinished/MarkFailed 延迟，claim 更容易过期并触发 Round 054 所述恢复误判。
   - 建议：对 progress 事件按 turn_id 合并/限流，终态事件优先处理；队列应有容量与 dropped/lag 指标。

5. **[moderate] BusModule 停止时忽略 progress worker drain 错误，未处理事件可能静默丢失**
   - 证据：cron subscriber cancel 里调用 `_ = worker.Stop(context.Background())`，直接忽略错误（`internal/module/cron/subscribers.go:30-39`）。BusModule OnStop 只调用 `subscribers.Cancel()`，没有接收 cancel 返回错误的通道（`internal/platform/bus/module.go:48-57`；`internal/platform/bus/subscribers.go:66-76`）。
   - 风险：shutdown 时如果 DB 卡住超过 drain grace，Stop 会返回超时但被忽略；队列里的终态/续租请求可能没处理完，下一次启动只能靠一次性 recovery，且 recovery 也存在 claim fence 风险。
   - 建议：Subscriber cancel 支持返回 error，BusModule 聚合并上报；cron worker drain 失败时写 warn/error 和指标。

## 误报与已覆盖项

- cron progress subscriber 已通过 BusModule 的 `bus.subscribers` group 注册，不是模块自行在 Fx lifecycle 里开散落 goroutine（`internal/module/cron/module.go:31`；`internal/platform/bus/module.go:22-37`）。
- subscriber 注册/取消的幂等性有测试覆盖：取消后再次发布 `ItemCompleted` 不再触发续租查询（`internal/module/cron/subscribers_test.go:46-84`）。
- TickActor 和 LeaseActor 都是 `group:"runners"` 管理的 Runner，ctx cancel 后退出行为有测试覆盖（`internal/module/cron/actors_test.go:22-78`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/cron ./internal/platform/bus -count=1
```

结果：通过。

## 下一轮建议

- Round 056 审查 cron service/RPC 创建更新路径：`CreateJob`、`UpdateJobSchedule`、`RunOnce`、cron 表单校验、时区/next_run_at 初始化、max_attempts 默认值与用户可见 API 是否会创建永不触发或无法重试的量化任务。
