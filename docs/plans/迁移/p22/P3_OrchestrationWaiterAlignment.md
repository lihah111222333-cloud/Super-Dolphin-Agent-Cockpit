# P3: orchestration wait/exit 归位

## 目标

修正 `cmd/mcp-orch/orchestration` 里 `runnerActor.Run(ctx)` 再次散射 waiter goroutine 的问题，让进程退出观测和状态迁移重新回到单一 owner 之下。

## 对应 findings

- Finding 8: `cmd/mcp-orch/orchestration/process_lifecycle.go:220-238`

## 现状校准

- `runnerActor` 本身已经作为 `platformrunner.Runner` 进入 `run.Group`
- 但 `startWaiters(...)` 仍在 `Run(ctx)` 路径里对每个 monitor target `go a.waitForExit(...)`
- `waitForExit(...)` 结束后，还会直接调用 `handleProcessExit(...)` 或向结果 channel 投递
- 旁路不只影响 `cmd.Wait()`；当前 [waitForProcessExit](/Users/mima0000/Desktop/wj/super-agent-v3/cmd/mcp-orch/orchestration/process_lifecycle.go#L128) 还依赖 `lastExitedSeq` 判定退出完成，并在超时后 `forceKillProcess(...)`
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

还要冻结 timeout/kill 语义：

- `forceKillProcess(...)` 不是退出完成本身
- kill 之后仍必须回到同一条 `exit event -> handleProcessExit(...)` 链路
- 且只能迁移一次

## 推荐最小 contract 变化

- 给 process owner 增一个只读退出流，例如 `ExitEvents() <-chan waitResult`
- 给同一 owner 增一个有界收口接口，例如 `Drain(ctx)` 或 `Close(ctx)`，用于 shutdown join pending waits，替掉当前 `ctx.Done()` 分支里直接 `handleProcessExit(context.Background(), ...)` 的旁路

对 `turn` 同类问题，最小 contract 变化也应写明：

- turn 终态等待必须 only-once：`watchTurn` 与 `waitForTurnSettle` 不能继续双 owner
- `Shutdown()` 若继续存在，必须从隐藏 side-channel 升格为显式 lifecycle/drain contract，而不是仅仅 cancel ctx
- `WaitForSessionReady` 若继续保留，必须从本地私扩接口升格进正式 contract 或删掉其中一套 waiter

## TDD 与旧实现清理

- 先补失败测试：正常退出 only-once、shutdown 途中退出不重复迁移、timeout kill 后只走一次 exit 链、wait owner drain 后无残留 waiter。
- 修复完成后必须删掉 `runnerActor.Run(ctx)` 里直接 `go a.waitForExit(...)` 的旧路径；不能保留“新 ExitEvents + 旧 waiter goroutine”双轨。
- 若 `waitForExit(...)`、`startWaiters(...)` 只剩 legacy wrapper 价值，应继续删除或合并进新的 process owner，避免留下空壳 helper。
- `handleProcessExit(...)` 的旁路调用点必须收敛到单一 owner；没有删干净的旁路一律视为未完成。

## 同步收口建议

如果实施中已经修改 `service.go` 的 turn/approval 订阅路径，建议顺手把“回调直接改状态”改成 enqueue-only，再由 actor 主循环消费。但这属于建议收口，不是本单的硬前置。

与 `cmd/mcp-orch/orchestration` 同包相关、但不属于 waiter/exit owner 本体的问题，统一转交 [P4_DependencyDirectionAndHiddenContracts.md](P4_DependencyDirectionAndHiddenContracts.md)：

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
