# P3: orchestration wait/exit 归位

## 目标

修正 `cmd/mcp-orch/orchestration` 里 `runnerActor.Run(ctx)` 再次散射 waiter goroutine 的问题，让进程退出观测和状态迁移重新回到单一 owner 之下。

## 对应 findings

- Finding 8: `cmd/mcp-orch/orchestration/process_lifecycle.go:220-238`

## 现状校准

- `runnerActor` 本身已经作为 `platformrunner.Runner` 进入 `run.Group`
- 但 `startWaiters(...)` 仍在 `Run(ctx)` 路径里对每个 monitor target `go a.waitForExit(...)`
- `waitForExit(...)` 结束后，还会直接调用 `handleProcessExit(...)` 或向结果 channel 投递

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

### Step 3：统一状态迁移入口

`handleProcessExit(...)` 只允许由两条路径触发：

- actor 主循环消费到 exit event
- shutdown 收口的单一兜底路径

避免现在这种 waiter goroutine 直接写状态的旁路。

### Step 4：补 stop/drain 语义

无论 exit owner 最终落在 launcher 还是 monitor，都要具备：

- cancel
- wait/drain
- bounded shutdown

否则只是把脱管 goroutine 挪了位置。

## 同步收口建议

如果实施中已经修改 `service.go` 的 turn/approval 订阅路径，建议顺手把“回调直接改状态”改成 enqueue-only，再由 actor 主循环消费。但这属于建议收口，不是本单的硬前置。

## 验收标准

- `runnerActor.Run(ctx)` 中不再直接 `go a.waitForExit(...)`
- `waitForExit` 若仍存在，也必须下沉到独立 owner，不再由 actor execute 直接调用
- `handleProcessExit(...)` 的状态推进不再从 fire-and-forget goroutine 旁路进入
- 至少补以下测试：
  - 进程正常退出时只产生一次 exit transition
  - shutdown 途中退出不会重复迁移
  - 多 agent 并发退出时不丢事件
  - runner stop 后不会残留未 join 的 wait routine
