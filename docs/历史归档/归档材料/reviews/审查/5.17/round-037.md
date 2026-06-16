# Round 037 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:08:37 KST
- 结束：2026-05-17 07:18:42 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 `SubmitTurn`、本地/远端 turn 提交、内存队列、claim/start 与 recovery replay，重点看量化任务连续提交、重启、启动失败时是否会丢 turn 或破坏串行化。

- `internal/sidecar/orch/orchestration/service.go`
- `internal/sidecar/orch/orchestration/service_launcher_bridge.go`
- `internal/sidecar/orch/orchestration/launch_helpers.go`
- `internal/sidecar/orch/orchestration/helpers.go`
- `internal/sidecar/orch/orchestration/process_lifecycle.go`
- `internal/sidecar/orch/orchestration/recover.go`
- `internal/sidecar/orch/orchestration/execution_test.go`
- `internal/sidecar/orch/orchestration/launcher_test.go`
- `internal/sidecar/orch/orchestration/recover_test.go`

## Findings

1. **[major] 本地 turn queue 仅存在内存中，重启或 rehydrate 后排队任务直接丢失**
   - 证据：`SubmissionQueue` 只是进程内 `[]TurnSubmission`，`Enqueue/Dequeue/Prepend` 都只改内存切片（`internal/sidecar/orch/orchestration/launch_helpers.go:26-74`）。本地提交直接 `agent.queue.Enqueue(req)`（`internal/sidecar/orch/orchestration/service_launcher_bridge.go:513-534`），而 persisted runtime rehydrate 新建空 queue（`internal/sidecar/orch/orchestration/persistent_runtime_rehydrate.go:185-210`）。
   - 风险：量化 DAG worker 忙碌时后续 turn 会排入内存队列；mcp-orch 崩溃、重启或 runtime rehydrate 后，这些待执行 turn 没有 durable 来源，任务链路表现为“已提交但永不执行”。
   - 建议：把 queued submissions 持久化到 turn/wakeup 表，或明确将 SubmitTurn 的 queued 状态变成 durable idempotency key；重启时按 agent/thread 恢复队列。

2. **[major] claimTurnWork 先 Dequeue 再异步 StartTurn，StartTurn/WaitForSessionReady 失败后不会回队**
   - 证据：`claimTurnWork()` 先 `agent.queue.Dequeue()`，再把状态推进到 `turn_starting` 并返回 `turnWork`（`internal/sidecar/orch/orchestration/process_lifecycle.go:63-90`）。后续 `startTurnExecution()` 若 `WaitForSessionReady` 或 `StartTurn` 失败，只调用 `finishTurnStartFailure()`（`internal/sidecar/orch/orchestration/helpers.go:91-105`）；失败处理只清 `activeTurnID` 和 lastError，不重新 enqueue 原 submission（`internal/sidecar/orch/orchestration/helpers.go:133-151`）。测试也确认 wait 第二次失败后 queue 为空、state 回 idle（`internal/sidecar/orch/orchestration/execution_test.go:165-210`）。
   - 风险：量化节点 wakeup 已被消费后，真正 provider start 失败会把 turn 从本地队列移除；如果上层 DAG/wakeup 没有补偿，节点可能卡在 sent/running 与本地 idle 的错配状态。
   - 建议：claim 后启动失败应按可重试错误回队或写 durable failure/retry 记录；至少把失败 turn id 与 submission 暴露给 DAG retry/reclaimer。

3. **[major] 远端忙态直接拒绝提交，本地忙态却排队，provider 切换会改变任务可靠性语义**
   - 证据：远端路径 `remoteAgentBusy()` 在 state 非 idle 或 activeTurnID 非空时返回 `"agent is busy"`（`internal/sidecar/orch/orchestration/service_launcher_bridge.go:446-500`）；本地路径 busy 时仍 `agent.queue.Enqueue(req)`，并有测试覆盖 running 状态下 SubmitTurn 仍入队（`internal/sidecar/orch/orchestration/execution_test.go:105-125`）。
   - 风险：同一个量化调度策略在本地 provider 下可串行排队，在远端 provider 下会变成同步失败；如果上层调用方把 SubmitTurn 失败当成 terminal，批量 DAG 可能出现 provider 相关的隐性丢任务。
   - 建议：统一 SubmitTurn 契约：要么远端也支持 durable queue，要么本地也显式返回 busy 并要求上层用 wakeup/retry。

4. **[moderate] recover 在重启进程前先清 activeTurnID，startProcessLocked 失败会丢失原活动 turn 绑定**
   - 证据：`recoverAgent()` 先加载 replay，再 `stopProcess()`，随后把 `agent.activeTurnID = ""`，再进入 `normalizeRecoveryState()` 和 `startProcessLocked()`；若 start 失败直接返回错误（`internal/sidecar/orch/orchestration/recover.go:61-89`）。replay 只有 start 成功后才 `Prepend` 回 queue（`internal/sidecar/orch/orchestration/recover.go:221-229`）。
   - 风险：量化 worker recover 时如果新进程启动失败，runtime 中原 active turn 已被清空，后续 completion/reclaimer 很难按原 turn id 精准恢复。
   - 建议：在新 runtime 启动成功并完成 replay enqueue 前保留原 activeTurnID，或用单独 recoveryAttempt 状态记录原 turn。

5. **[moderate] remote SubmitTurn RPC 期间允许 StopAgent 并发完成，远端 turn 可能已启动但本地结果被忽略**
   - 证据：远端 submit 在 prepare 阶段设置 `activeTurnID` 后释放 service lock，再调用 launcher RPC（`internal/sidecar/orch/orchestration/service_launcher_bridge.go:432-443`、`internal/sidecar/orch/orchestration/service_launcher_bridge.go:446-489`）。测试明确要求远端 `turn/start` 阻塞时 `StopAgent()` 不能被锁阻塞，且 stop 可先完成（`internal/sidecar/orch/orchestration/launcher_test.go:414-465`）。RPC 成功回写时若 `activeTurnID` 已变化会静默返回（`internal/sidecar/orch/orchestration/service_launcher_bridge.go:480-488`）。
   - 风险：StopAgent 看似完成，但远端 turn/start 之后仍可能返回成功并在 provider 侧执行；本地因 activeTurnID 已被清理而忽略结果，形成远端执行、本地无 active turn 的漂移。
   - 建议：remote SubmitTurn 使用 cancellable RPC/fence token；StopAgent 应取消 pending turn/start 或在成功回调时执行远端 abort/compensation。

## 误报与已覆盖项

- 本地队列有 clone 保护，外部修改 submission 不会污染已排队对象（`internal/sidecar/orch/orchestration/launch_helpers.go:18-23`、`internal/sidecar/orch/orchestration/submission_test.go:115-144`）。
- `claimTurnWork()` 在状态推进失败时会把刚取出的 submission 重新 enqueue，避免状态机拒绝时丢队列（`internal/sidecar/orch/orchestration/process_lifecycle.go:69-76`）。
- recovery replay 会把恢复出来的活动 turn 放到现有队列之前，测试覆盖 replay turn 优先于旧 queued work（`internal/sidecar/orch/orchestration/recover_test.go:66-105`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration -count=1
```

结果：通过。

## 下一轮建议

- Round 038 审查 turn lifecycle event consumer 与 completion/interruption 的收敛逻辑，重点看重复事件、空 turn id、终态恢复是否会掩盖真实失败。
