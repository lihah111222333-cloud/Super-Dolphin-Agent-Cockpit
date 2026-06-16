# P3: orchestration wait/exit 归位

## 目标

修正 `internal/sidecar/orch/orchestration` 里 `runnerActor.Run(ctx)` 再次散射 waiter goroutine 的问题，让进程退出观测和状态迁移重新回到单一 owner 之下。本文默认把“local process monitor owner + actor 只吃 exit events”作为首选分层：`RunnerModule` 继续托管 actor，本地 process owner 托管 OS wait，不把 `AgentLauncher` 全接口扩容当成前置。当前代码里的 `waitResult + results chan` 仍只是 Run-local 私有管道，不是正式 exit contract；P3 的目标是把它升级成 owner-facing contract，并删掉旧 waiter 账本。下文的 `目标架构 / 实施步骤` 描述的是 P3 落地后的目标态；在实现完成前，HEAD 仍按旧 waiter 模式运行，不应把这些 contract 误读成“已经存在”。

## 对应 findings

- Finding 8: `internal/sidecar/orch/orchestration/process_lifecycle.go:220-239`

## 现状校准

- `runnerActor` 本身已经作为 `platformrunner.Runner` 进入 `run.Group`
- 但 `startWaiters(...)` 仍在 `Run(ctx)` 路径里对每个 monitor target `go a.waitForExit(...)`
- `waitForExit(...)` 结束后，还会直接调用 `handleProcessExit(...)` 或向结果 channel 投递
- 旁路不只影响 `cmd.Wait()`；当前 `internal/sidecar/orch/orchestration/process_lifecycle.go:136-162` 的 `waitForProcessExit(...)` 还依赖 `lastExitedSeq` 判定退出完成，并在超时后 `forceKillProcess(...)`
- 同类 only-once waiter 问题还出现在 `turn`：`watchTurn()` 与 `waitForTurnSettle()` 会等待同一个 handle 并双写 tracker；`turn.Module` 的 stop 目前只是 cancel ctx，不做 waiter drain

这让状态迁移不再只经过 actor 主循环，实际形成了：

```text
run.Group actor
  -> actor execute
     -> go waitForExit(...)
        -> service.handleProcessExit(...)
```

## 目标架构

优先方案：把“等待进程退出”的 ownership 从 `runnerActor` execute 路径里拿走，收口为显式 process monitor contract。

推荐方向：

- launcher / process holder 返回可订阅的 exit handle
- `runnerActor.Run(ctx)` 只消费 `ExitEvents()`，不再自己起 waiter goroutine
- 进程资源 owner 负责等待 `cmd.Wait()`，并在 stop 时可 join / drain

新的 exit event 最小身份字段必须保留：

- `agentID`
- `launchSeq`
- `err`

其中 `launchSeq` 是 stale-exit 隔离的硬边界，不能退化成只按 agent 维度处理。

这样 `runnerActor` 只保留：

- 定时巡检
- turn queue 消费
- exit event 状态机推进

## 实施方式

- 首选 local-only `process monitor / owner`：launch / recover 时同步 arm monitor，owner 负责 `cmd.Wait()` / exit event / drain；actor 只消费 exit event 并推进状态。
- `runnerActor` 继续承担 `run.Group` actor 的角色，但不再为每个 target 现起 waiter goroutine，也不再直接成为 OS wait owner。
- `kill / timeout / shutdown` 全部回到同一条 `exit event -> handleProcessExit(...)` 链路；`forceKillProcess(...)` 只是 stop 手段，不单独代表退出完成。
- `P3` 的回归守卫同时依赖 `P0` 的 actor AST guard 与本包 hot-file guard：既要防 `Run(ctx)` 里直接 `go`，也要防 `Run(ctx) -> helper -> go waitForExit(...)` 这类一跳回归。
- 当前代码里已有 `waitResult + results chan` 这种私有雏形，但它仍属于 actor 内部实现细节；P3 的目标是把这类退出流升格成 owner 暴露的正式 contract。
- 如必须引入新 contract，优先是 local process handle / monitor contract，而不是先把 `AgentLauncher` 扩成统一 remote/local super-interface。
- `recover.go:61-76` 的 stop/reset/restart、`helpers.go:startProcessLocked(...)` 的首次 launch、以及 `launcher.Stop(...) -> handleProcessExit(...)` 旧旁路，都必须同步 arm 同一个 owner；不允许只修首发 launch 而把 recover / stop 保留成第二套 wait owner。
- `(agentID, launchSeq)` 的 exactly-once fence 要在 owner / consumer 入口先判重，再进入 `handleProcessExit(...)` side effect；`waitResult + results chan` 若继续暂存，也只算过渡实现，不算最终 contract。
- `claimMonitorTargets` / `monitorTarget` / `monitoredSeq` / `lastExitedSeq` 若继续存在，只能下沉为 owner 内部状态；actor 主链不再依赖它们拼装退出语义。

## 实施步骤

### Step 1：定义退出事件 contract

为 orchestration process runtime 增加统一出口，例如：

- `ExitEvents() <-chan waitResult`
- 或 `ProcessHandle.Done()/Err()`

`runnerActor` 只通过该 contract 收事件。

### Step 2：下沉 `cmd.Wait()`

把 `cmd.Wait()` 的真正执行移到进程 owner：

- 可以是 launcher/process wrapper
- 也可以是专门的 monitor service

但不能再由 actor execute 针对每个 target 现起 fire-and-forget goroutine。

补充要求：

- process owner 只负责产出退出结果，不允许直接改 `agentRuntime`、清 session、发 failed/stopped
- 这些 side effects 仍统一留在 `handleProcessExit(...)`
- `helpers.go:222-242` 的 `startProcessLocked(...)` 与 recover 路径也必须接入同一 owner/monitor，不允许只修首次 launch 而保留第二套 wait owner

### Step 3：统一状态迁移入口

`handleProcessExit(...)` 只允许由两条路径触发：

- actor 主循环消费到 exit event
- shutdown 收口的单一兜底路径

避免现在这种 waiter goroutine 直接写状态的旁路。

必须再补一条硬约束：

- `(agentID, launchSeq)` 上 `handleProcessExit(...)` 必须 exactly-once；无论正常投递还是 shutdown 兜底，都不能重复迁移

### Step 4：补 stop/drain 语义

无论 exit owner 最终落在 launcher 还是 monitor，都要具备：

- cancel
- wait/drain
- bounded shutdown

否则只是把脱管 goroutine 挪了位置。

- stop 顺序固定为 `stop -> drain wait owner -> actor 消费剩余 exit event -> return`

还要冻结 timeout/kill 语义：

- `forceKillProcess(...)` 不是退出完成本身
- kill 之后仍必须回到同一条 `exit event -> handleProcessExit(...)` 链路
- 且只能迁移一次

## 收口口径

- 本页关于 actor / wait owner / drain 的依据，以 `docs/契约/modularity-convention.md §4.4 / §7`、`docs/契约/fx-convention.md §2 / §3`、`docs/契约/rungroup-convention.md §2 / §4` 为准：`RunnerModule` 只托管 actor，local process owner 负责 OS wait / join / drain，不把 wait owner 再塞回 `fx.Module` 或 bus callback。
- `(agentID, launchSeq)` 是唯一的 exit identity fence；exactly-once 以此为准，不退化成单 agent 维度。
- `P3` 只闭环 waiter / exit owner；identity/report protocol、`Module/handler.Map` shell、launcher hidden contract 继续归 `P4`。
- `turn` 的同类 waiter / readiness 问题在本页只做记账与对齐，不强写成与 orchestration 同批合入。
- shutdown 顺序固定为 `stop -> drain wait owner -> actor 消费剩余 exit event -> return`；任何 `ctx.Done()` 旁路都必须被删干净或降成唯一兜底。
- 本页不改写 README/P2 的“stop-intake / 退订 ≠ drain”：即便 exit event 将来经本地 channel/owner 流转，也必须显式 drain wait owner；bus 退订或 stop signal 都不能替代 wait/join。
- 若后续保留 package-local helper，静态守卫也必须覆盖到 actor 可达的一跳 helper；不能把 regression 挪进 `startWaiters(...)` 这类包装函数后就算通过。

## 依赖图（文本）

```text
P0 -> P3 -> P4（orchestration hidden contract / protocol shell）
```

## 落地顺序建议

1. `P3` 在 `P0` 之后即可独立推进，不必等待 `P2`。
2. 先冻结 local process owner 与 exit event contract，再决定是否需要改动 launcher facade；不要倒序先扩 `AgentLauncher`。
3. 与 `P4` 同包共享文件的整改要串行：先 waiter / exit owner，后 shell / hidden contract。

## 推荐最小 contract 变化

- 给 process owner 增一个只读退出流，例如 `ExitEvents() <-chan waitResult`
- 给同一 owner 增一个有界收口接口，例如 `Drain(ctx)` 或 `Close(ctx)`，用于 shutdown join pending waits，替掉当前 `ctx.Done()` 分支里直接 `handleProcessExit(context.Background(), ...)` 的旁路

对 `turn` 同类问题，最小 contract 变化也应写明：

- turn 终态等待必须 only-once：`watchTurn` 与 `waitForTurnSettle` 不能继续双 owner
- `Shutdown()` 若继续存在，必须从隐藏 side-channel 升格为显式 lifecycle/drain contract，而不是仅仅 cancel ctx
- `WaitForSessionReady` 若继续保留，必须从本地私扩接口升格进正式 contract 或删掉其中一套 waiter

## 可观测 / crash-window / 回滚约束

- crash-window 状态机必须回链到 `internal/dto/agent/state.go` 的真值：至少显式覆盖 `turn_queued -> turn_starting -> turn_running -> awaiting_user_input -> stopping/stopped/failed/recovering`，并写清每个状态由谁产出 exit event
- 常量冻结要写成代码真值：`processExitWaitTimeout = 30s`、`launchRetryBase = 2s` 继续受测；若实现改动这些超时，必须在本页同步改判
- 最低可观测合同至少包含 `exit_event_total`、`wait_owner_drained`、`onstop_latency_ms`、`launch_seq` / `agent_id` / `phase` 结构化字段；shutdown 时要能证明“先 drain wait owner，再消费剩余 exit event”
- 若实现期必须短暂保留旧 waiter 以便回滚，只能挂在显式 feature flag / env opt-in 下，且默认关闭；旧 `go waitForExit(...)` 不能再作为默认路径

### crash-window 状态 / owner 表

| 状态 | owner | 进入条件 | 退出条件 |
|---|---|---|---|
| `turn_queued` | actor 主循环 | turn 入队 | `turn_starting` / `stopping` |
| `turn_starting` | actor + launch fence | launch accepted | `turn_running` / `failed` / `recovering` |
| `turn_running` | actor + process owner | provider accepted | `awaiting_user_input` / `idle` / `failed` / `stopping` |
| `awaiting_user_input` | actor | user input requested | `turn_running` / `idle` / `failed` |
| `stopping` | wait owner + actor | stop requested | `stopped` / `failed` |
| `stopped` | actor | `process exited` after intentional stop | `recovering` / `idle` |
| `failed` | actor | exit / launch failure | `recovering` |

### 最低 observability contract

- `log`：`exit_event.received`、`stop_phase.begin/end`、`wait_owner.drain.done`
- `metric`：`exit_event_total`、`wait_owner_drained`、`onstop_latency_ms`
- `trace`：`orchestration.wait_owner`、`orchestration.handle_process_exit`
- kill / timeout / shutdown 验证默认配 fake clock / deterministic shutdown；禁裸 `time.Sleep`

## P21 递延 DDL 锚点（只加不删）

> 本页不实现这些 DDL，但 `P21` 递延的索引锚点必须继续可 grep，防止在多轮修订中被顺手删空。

```sql
CREATE UNIQUE INDEX IF NOT EXISTS uq_session_insights_local_turn
CREATE UNIQUE INDEX IF NOT EXISTS uq_session_insights_provider_turn
```

## 回滚卡（R2）

| gate carrier | rollback trigger | state rewind | disable steps | red-green |
|---|---|---|---|---|
| feature flag / env opt-in（default-off） | exit event 重复迁移、`wait_owner_drained` 不达成、`processExitWaitTimeout=30s` 路径回归 | 回退到上一个 wait owner wiring，但保留 `(agentID, launchSeq)` exactly-once fence 与 fail-closed stop 顺序 | 关闭新 wait-owner gate，先 drain 现有 owner，再停 actor 读取新 exit event | hot-file guard + orchestration 行为测试同 PR red-green |

## 非目标

- 不在本页重写 `AgentLauncher` / `remoteLauncher` 的全部协议壳；只有 waiter / exit owner 直接需要的 contract 才进入本页。
- 不把 `Module` / `handler.Map` / `HookConsumer` / report protocol 顺手并入同一批实现；这些继续由 `P4` 负责。
- 不把所有 callback-side mutation 一并收口成 actor-only；本页只处理 exit wait owner 与 exactly-once 状态推进主链。

## TDD 与旧实现清理

- 先补失败测试：正常退出 only-once、shutdown 途中退出不重复迁移、timeout kill 后只走一次 exit 链、wait owner drain 后无残留 waiter。
- 先补 `internal/archtest` / AST 守卫：至少锁住 `runnerActor.Run(ctx)` 及其一跳 helper 不得再 `go a.waitForExit(...)`，避免旧 waiter 路径回归。
- 测试名固定到可派单级别：`TestOrchestrationWaiterHotFileGuard`、`TestExitEventExactlyOnceByLaunchSeq`、`TestStopPathReusesExitOwner`、`TestShutdownDrainWaitOwner`、`TestKillTimeoutStillEmitsSingleExitEvent`、`TestProcessExitStateMachine`
- 验证命令固定写法：`go test ./internal/sidecar/orch/orchestration -run 'Test(ExitEventExactlyOnceByLaunchSeq|StopPathReusesExitOwner|ShutdownDrainWaitOwner|KillTimeoutStillEmitsSingleExitEvent|ProcessExitStateMachine)' -count=1 -v` 与 `go test ./internal/archtest -run 'TestOrchestrationWaiterHotFileGuard' -count=1 -v`
- 修复完成后必须删掉 `runnerActor.Run(ctx)` 里直接 `go a.waitForExit(...)` 的旧路径；不能保留“新 ExitEvents + 旧 waiter goroutine”双轨。
- 若 `waitForExit(...)`、`startWaiters(...)` 只剩 legacy wrapper 价值，应继续删除或合并进新的 process owner，避免留下空壳 helper。
- `handleProcessExit(...)` 的旁路调用点必须收敛到单一 owner；没有删干净的旁路一律视为未完成。

## 同步收口建议

如果实施中已经修改 `service.go` 的 turn/approval 订阅路径，建议顺手把“回调直接改状态”改成 enqueue-only，再由 actor 主循环消费。但这属于建议收口，不是本单的硬前置。

与 `internal/sidecar/orch/orchestration` 同包相关、但不属于 waiter/exit owner 本体的问题，统一转交 [P4_DependencyDirectionAndHiddenContracts.md](P4_DependencyDirectionAndHiddenContracts.md)：

- `orchestration.Module` 作为子包整包装配出口
- `NewOrchestrationHandlers` / `handler.Map` 协议壳
- `generationAwareSessionCleaner`
- `sessionReadyWaiter`
- `turn.PendingLaunchSpawner`
- `HookConsumer`
- `AgentLauncher` / `remoteLauncher` 的 hidden protocol contract

## 验收标准

- `runnerActor.Run(ctx)` 中不再直接 `go a.waitForExit(...)`
- `waitForExit` 若仍存在，也必须下沉到独立 owner，不再由 actor execute 直接调用
- `handleProcessExit(...)` 的状态推进不再从 fire-and-forget goroutine 旁路进入
- `waitForProcessExit(...)`、timeout kill、shutdown drain 都与新的 exit owner 合同一致
- launch / recover / timeout kill 共用同一 exit owner；不再出现一部分路径走新 contract、另一部分路径仍靠临时 waiter
- crash-window 状态名、`processExitWaitTimeout = 30s`、`launchRetryBase = 2s` 与 wait-owner drain 信号都有文档和测试护栏
- exit owner 的 start / stop / drain / timeout 具备最低日志 / metric / trace 口径，可回放 `launchSeq` fence
- `P3` 只闭环 waiter/exit owner；`Module/handler.Map/hidden contract` 已在 `P4` 明确记账，不再处于无人认领状态
- turn / approval / hook 派生状态推进若暂不一并收口，必须在文档中显式记为 P3 残留债，不允许被误读成“actor 已是唯一状态推进入口”
- `registerTurnLifecycle` / `registerApprovalLifecycle` / hook 派生 completed-interrupted 处理若继续存在，也必须升级为明确的 callback-side mutation 残留债，而不是“建议顺手优化”
- `turn` 自身的 waiter/lifecycle/readiness owner 问题也已被记账；不能再把 `thread/turn` 范围误读成“P2 只管 thread callback，P3 只管 orchestration”
- 至少补以下测试：
  - 进程正常退出时只产生一次 exit transition
  - shutdown 途中退出不会重复迁移
  - 多 agent 并发退出时不丢事件
  - runner stop 后不会残留未 join 的 wait routine
  - kill timeout 后仍只走一次 exit-event 链路
