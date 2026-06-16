# 并发安全全量检查

审查时间：2026-03-21

审查方法：
- 仅使用 LSP `text_search`、`read_file`、`diagnostics`。
- 未使用 `grep/find/cat/sed/awk`。
- 关键并发文件的 LSP `diagnostics` 返回为空。

审查范围：
- 以生产代码为主，测试文件只作为补充信号，不参与主结论。
- 重点覆盖 `internal/platform/rpc/`、`internal/provider/`、`internal/module/`、`internal/ui/wails/`、`internal/app/`、`internal/platform/runner/`。

## 结论摘要

总体结论：核心共享 map 大多有明确锁保护，当前代码库没有发现“已经成型的、确定性可复现”的死锁链；但存在 5 个需要处理的并发热点，其中 3 个属于真实竞态风险。

高优先级问题：
1. `SessionManager.Register` 与 `Remove` 只有 map 级互斥，没有 session 代际保护；并发 create/remove 时，`Remove` 可能删掉并关闭刚注册的新 session。证据：`internal/provider/unified/client.go:47-65`、`internal/provider/unified/session.go:31-85`、`internal/sidecar/orch/orchestration/service.go:104-108`、`internal/sidecar/orch/orchestration/service.go:355-369`。
2. `codexapp.session.threadID` 在后台 goroutine 已启动后，由 driver 无锁写入；同时读路径已经存在，属于真实 data race。证据：`internal/provider/codexapp/session.go:87-103`、`internal/provider/codexapp/driver.go:78-93`、`internal/provider/codexapp/driver.go:96-110`、`internal/provider/codexapp/recovery.go:79-87`、`internal/provider/codexapp/session_approval.go:60-71`。
3. `ApprovalManager` 的 `pending.dispatcher` 在锁外写入，而 `finishPending -> publishResolved` 在锁外读取；如果 approval 很快被 `Respond`，存在 register/resolve 并发窗口。证据：`internal/platform/rpc/approval.go:79-85`、`internal/platform/rpc/approval.go:108-117`、`internal/platform/rpc/approval.go:237-264`、`internal/platform/rpc/approval_events.go:34-42`。

中优先级问题：
4. `rpc.Server.methods` 是裸 `handler.Map`，`Register` 没有互斥；当前只因它在 Fx 启动期注册一次，才“按约定安全”，不是“按构造安全”。证据：`internal/platform/rpc/server.go:21-24`、`internal/platform/rpc/server.go:37-40`、`internal/platform/rpc/server.go:50-61`、`internal/platform/rpc/server.go:114-124`、`internal/platform/rpc/module.go:50-52`。
5. `unified.closeSession` 和 `wails.runner.Run` 都使用“起 goroutine + 超时返回”的模式；如果底层 `Close`/`Run` 永久阻塞，调用方会返回，但后台 goroutine 仍可能残留。证据：`internal/provider/unified/session.go:118-132`、`internal/ui/wails/runner.go:24-46`。

死锁结论：
- 本轮未发现现有代码中的反向锁序。
- 发现的跨锁层级基本都是单向的：`service.mu -> queue.mu`、`writeMu -> stateMu`。
- 现状是“基本安全但依赖约束”；后续如果出现反向拿锁，很容易把现在的单向结构破坏掉。

## 1. 共享状态保护总表

### 1.1 `sync.Mutex`

| 位置 | 受保护状态 | 结论 |
| --- | --- | --- |
| `internal/sidecar/orch/orchestration/submission.go:9-12` | `SubmissionQueue.items` | 所有 `Enqueue/Dequeue/Peek/Len/Clear` 都走 `q.mu`，队列本身安全。 |
| `internal/platform/rpc/approval.go:19-25` | `pending`、`pendingByRequestID`、approval 生命周期字段 | map 主体受保护；但 `pending.dispatcher` 额外在锁外写，见热点 3。 |
| `internal/platform/rpc/transport_ws.go:45-49` | websocket send 序列化 | `Send` 串行化写操作，安全。 |
| `internal/provider/claudecli/session.go:38-40` | `threadID/sessionID/transport/activeTurn` | 主状态受 `s.mu` 保护，未发现 map 竞态。 |
| `internal/provider/claudecli/session.go:42-49` | `turnHandle.err` | `finish/Err` 都有锁和 `sync.Once`，安全。 |
| `internal/provider/claudecli/transport.go:22-31` | `doneErr`、`stdin` 写入、`limitedBuffer.buf` | `doneMu/writeMu/b.mu` 使用一致，未发现反向锁序。 |
| `internal/provider/codexapp/session.go:31-37` | `turns`、恢复/读循环状态 | `turns` map 有锁；`threadID` 不在锁保护范围内，见热点 2。 |
| `internal/provider/codexapp/session.go:41-48` | `turnHandle.err` | `complete/Err` 安全。 |
| `internal/provider/codexapp/transport.go:50-61` | websocket/process 状态与写入序列化 | `stateMu`/`writeMu` 分工清晰，无现成死锁链。 |
| `internal/ui/wails/bridge.go:22-29` | `cancels` | `Start/Stop` 都先复制后释放锁，安全。 |

### 1.2 `sync.RWMutex`

| 位置 | 受保护状态 | 结论 |
| --- | --- | --- |
| `internal/sidecar/orch/orchestration/service.go:29-39` | `agents` 运行态总表 | `Launch/Stop/List/Snapshot/CompleteTurn` 都经 `s.mu`；map 访问安全。 |
| `internal/module/thread/service.go:26-36` | `threadAgents` 索引 | `remember/lookup/forget` 都走 `threadAgentsMu`，map 访问安全。 |
| `internal/module/turn/tracker.go:13-16` | `turns` 追踪表 | `Start/Attach/Update/Complete/Cleanup/Get` 全部受保护。 |
| `internal/platform/bus/projection.go:10-14` | projector state | `Apply/State` 分别走写锁/读锁，安全。 |
| `internal/platform/rpc/server.go:18-26` | `active`、`onConnects` | 运行态连接表安全；但 `methods` 不是这把锁保护对象。 |
| `internal/provider/codexapp/session.go:41-48` | `turnHandle` 读路径 | `Err` 走读锁，安全。 |
| `internal/provider/codexapp/transport.go:50-61` | `ws/cmd` 读写 | `currentWS/processRunning` 走读锁，配合写锁一致。 |
| `internal/provider/unified/event_map.go:13-18` | translators 切片 | `Dispatch` 先拷贝后释放锁，避免持锁回调。 |
| `internal/provider/unified/session.go:15-19` | `sessions` map | map 访问安全；但 create/remove 语义竞态仍在，见热点 1。 |
| `internal/ui/wails/lifecycle.go:27-41` | quit/shutdown/emitter 函数指针 | 读写分离清楚，外部调用在锁外执行。 |

### 1.3 `atomic`

生产代码中的 `atomic` 使用点如下：

| 位置 | 用途 | 结论 |
| --- | --- | --- |
| `internal/platform/rpc/approval.go:23` | `nextRequestID atomic.Int64` | 用于 approval request id 分配，合理。 |
| `internal/provider/codexapp/session.go:35` | `lastReadAt atomic.Int64` | 健康检查时间戳，合理。 |
| `internal/provider/codexapp/transport.go:58-60` | `nextID`、`looping`、`closed` | 调用 id、读循环单实例、关闭标记，合理。 |
| `internal/ui/wails/lifecycle.go:36-40` | quit/shutdown/frontend flags | 一次性状态位，用法正确。 |

补充：
- 测试中的 `atomic.AddInt32/LoadInt32/AddInt64/LoadInt64` 仅出现在 `internal/platform/bus/*_test.go`，不影响生产结论。

## 2. map 并发访问检查

结论：
- 共享 map 本身多数都受 mutex/RWMutex 或 `sync.Map` 保护。
- 这项检查没有发现“明显无锁 map 读写并在运行态并发使用”的现成崩溃点。
- 但发现 2 个重要例外：`SessionManager.sessions` 虽然加锁，但 create/remove 语义仍有竞态；`rpc.Server.methods` 是未加锁的运行态 map，依赖启动期只注册一次。

### 2.1 已确认受保护的共享 map

| map | 位置 | 保护方式 | 判断 |
| --- | --- | --- | --- |
| `ApprovalManager.pending` | `internal/platform/rpc/approval.go:21` | `m.mu` | 通过 |
| `ApprovalManager.pendingByRequestID` | `internal/platform/rpc/approval.go:22` | `m.mu` | 通过 |
| `SessionManager.sessions` | `internal/provider/unified/session.go:17` | `m.mu` | map 访问通过；生命周期语义不通过 |
| `service.agents` | `internal/sidecar/orch/orchestration/service.go:37` | `s.mu` | 通过 |
| `thread.service.threadAgents` | `internal/module/thread/service.go:35` | `threadAgentsMu` | 通过 |
| `turnTracker.turns` | `internal/module/turn/tracker.go:15` | `t.mu` | 通过 |
| `rpc.Server.active` | `internal/platform/rpc/server.go:24` | `s.mu` | 通过 |
| `codexapp.session.turns` | `internal/provider/codexapp/session.go:36` | `s.mu` | 通过 |
| `codexapp.transport.pending` | `internal/provider/codexapp/transport.go:57` | `sync.Map` | 通过 |

### 2.2 条件安全或需补强的 map

| map | 位置 | 风险 | 结论 |
| --- | --- | --- | --- |
| `rpc.Server.methods` | `internal/platform/rpc/server.go:21` | `Register` 直接 `maps.Copy(s.methods, current)`，无锁；`Dispatch`/`serveConn` 直接读这个 map。当前只有 `internal/platform/rpc/module.go:50-52` 在启动期调用。 | 当前依赖启动期单线程初始化，建议加锁或在 `Run` 后冻结注册。 |
| 各类函数内局部 map | 如 `internal/module/skill/skills_match.go:117`、`internal/sidecar/orch/orchestration/helpers.go:17` | goroutine 局部对象，不形成共享状态 | 不构成并发问题 |

### 2.3 `SessionManager.sessions` 专项结论

`SessionManager.sessions` 的 map 访问本身是安全的，但 create/remove 组合不是线性化的。

关键代码：
- 注册：`previous := m.sessions[id]; m.sessions[id] = session`，然后在锁外 `previous.ForceStop()`，见 `internal/provider/unified/session.go:37-45`。
- 删除：`session := m.sessions[id]; delete(m.sessions, id)`，然后在锁外 `closeSession/ForceStop`，见 `internal/provider/unified/session.go:69-83`。
- 创建入口：`Client.open -> c.sessions.Register(agentID, session)`，见 `internal/provider/unified/client.go:47-65`。
- 删除入口：`orchestration.service.removeSession -> SessionManager.Remove`，见 `internal/sidecar/orch/orchestration/service.go:104-108`。

竞态序列：
1. 旧 session `A` 已存在。
2. `Register(B)` 先拿锁，把 map 里的值改成 `B`，释放锁。
3. `Remove(agentID)` 后拿锁，读到的是 `B`，把 `B` 删除并在锁外关闭。
4. `Register(B)` 再在锁外关闭旧 `A`。
5. 最终 map 为空，刚创建的 `B` 被误删。

结论：这是“语义竞态”，不是 map 崩溃，但会直接破坏 session 生命周期。

## 3. goroutine 泄漏检查

以下只列生产代码中的 `go` 启动点。

| 启动点 | 退出机制 | 结论 |
| --- | --- | --- |
| `internal/app/app.go:78-88` | `app.Done()` 或调用方关闭 `stop` | 通过 |
| `internal/app/runner.go:32-68` | `cancel()` + `done` 通知 | 基本通过；依赖 `RunGroup` 和各 runner 响应 `ctx` |
| `internal/sidecar/orch/orchestration/runner_actor.go:48-60` | `cmd.Wait()` 结束；actor 退出时 `StopAllAgents()` 杀进程 | 通过 |
| `internal/module/turn/service.go:173-203` | `handle.Done()` 或 `trackerTTL` | 通过 |
| `internal/platform/rpc/approval.go:154-169` | callback `ctx` 被 `finishPending/resetDispatch` 取消 | 通过 |
| `internal/platform/rpc/server.go:93-128` | `acceptLoop` 用 `WaitGroup`，连接退出时 `srv.Stop()` | 通过 |
| `internal/provider/claudecli/session_events.go:13-31` | `Receive()` 出错即退出；关闭 transport 会触发退出 | 通过 |
| `internal/provider/claudecli/transport.go:33-63` | `cmd.Wait()` 结束后关闭 `done` | 通过 |
| `internal/provider/codexapp/recovery.go:65-71` | 单次 `attemptRecovery`，受 `session ctx` 约束 | 有临时堆积风险；缺少 coalescing |
| `internal/provider/codexapp/recovery.go:113-126` | `s.ctx.Done()` | 通过 |
| `internal/provider/codexapp/recovery.go:155-191` | `transport.ReadLoop` 返回后退出 | 通过 |
| `internal/provider/codexapp/session_approval.go:14-24` | approval 请求使用 `s.ctx` + transport call timeout | 基本通过；无并发上限 |
| `internal/provider/codexapp/transport.go:295-316` | 1 秒等待后 `Kill` 并收尸 | 通过 |
| `internal/provider/unified/session.go:118-132` | 调用方 `ctx` 超时即可返回 | 不完全通过；`session.Close` 若永久阻塞，后台 goroutine 仍存活 |
| `internal/ui/wails/lifecycle.go:145-173` | 一次性 fire-and-forget closure | 通过 |
| `internal/ui/wails/runner.go:20-46` | `Quit()` + 最多等待 5 秒 | 不完全通过；超时后调用方返回，但 `app.Run()` goroutine 可能残留 |

专项说明：
- `codexapp.handleConnectionDead` 每次事件都直接 `go attemptRecovery`，而真正串行化是靠 `recoveryMu`，见 `internal/provider/codexapp/recovery.go:73-79`。这不会产生数据竞争，但在抖动场景下会临时堆积很多等待锁的 goroutine。
- `ApprovalManager.dispatchApproval` 的 goroutine 生命周期设计是完整的：成功时 `finishPending`，失败时 `resetDispatch/failPending`，都能把 dispatch context 收掉，见 `internal/platform/rpc/approval.go:193-218`。

## 4. channel 使用检查

| channel | 位置 | 关闭/超时机制 | 结论 |
| --- | --- | --- | --- |
| `stop chan struct{}` | `internal/app/app.go:78-88` | 调用方 `close(stopWatch)` | 通过 |
| `done chan error` | `internal/app/runner.go:30-43` | goroutine `close(done)` | 通过 |
| `results chan waitResult` | `internal/sidecar/orch/orchestration/runner_actor.go:31-45` | actor 生命周期内常驻，不要求关闭 | 通过 |
| `pending.result chan ApprovalDecision` | `internal/platform/rpc/approval.go:144` | 仅写入，不关闭，也没有读者 | 无立即 bug，但字段已基本失效 |
| `pending.done chan struct{}` | `internal/platform/rpc/approval.go:146` | `finishPending` 中统一 `close` | 通过 |
| `signals chan os.Signal` | `internal/platform/runner/group.go:51-60` | `signal.Stop(signals)`，actor 退出即释放 | 通过 |
| `threadReady chan struct{}` | `internal/provider/claudecli/driver.go:108` | `threadReadyOnce.Do(close)` | 通过 |
| `turnHandle.done` | `internal/provider/claudecli/session.go:55`、`internal/provider/codexapp/session.go:44` | `sync.Once + close(done)` | 通过 |
| `transport.done` | `internal/provider/claudecli/transport.go:27-31` | `wait()` 中 `close(t.done)` | 通过 |
| `readLoopDone` | `internal/provider/codexapp/recovery.go:163-206` | `finishReadLoop` 关闭 | 通过 |
| `pendingCall.done` | `internal/provider/codexapp/transport.go:87`、`internal/provider/codexapp/transport_helpers.go:12-18` | `resolve()` 中 `close` | 通过 |
| `done chan error` | `internal/provider/unified/session.go:122-130` | 依赖 `ctx.Done()` 超时 | 有 goroutine 残留风险 |
| `done chan error` | `internal/ui/wails/runner.go:24-45` | `waitForQuit` 5 秒超时 | 有 goroutine 残留风险 |

结论：
- 绝大多数 channel 都有清晰的关闭方或超时边界。
- 主要问题不在“channel 没关”，而在“超时返回后后台 goroutine 是否仍可能卡住”。

## 5. race condition 热点

### 5.1 approval pending 并发 register/resolve

主结论：
- `pending` / `pendingByRequestID` 两张表的增删查都在 `m.mu` 下，map 级并发访问是安全的。
- 真正的问题不是 map，而是 `pending.dispatcher` 这个附属字段的锁外写入。

证据链：
- `RequestApproval` 在 `registerPending` 之后、锁外执行 `pending.dispatcher = bridge.dispatcher`，见 `internal/platform/rpc/approval.go:79-85`。
- 并发响应入口 `Respond -> finishPending` 可能在另一个 goroutine 上先完成，见 `internal/platform/rpc/approval.go:108-117`、`internal/platform/rpc/approval.go:237-264`。
- `publishResolved` 在锁外读取 `pending.dispatcher`，见 `internal/platform/rpc/approval_events.go:34-42`。

风险：
- 如果 request 刚注册就被快速响应，`publishResolved` 可能和 `pending.dispatcher = ...` 发生并发读写。
- 即使 race detector 没立刻报，语义上也缺少 happens-before 保证。

建议：
- 把 dispatcher 放进 `registerPending` 的构造期一次性写入。
- 或者把 `pending.dispatcher` 的读写统一纳入 `m.mu`。

### 5.2 session 并发 Create/Remove

主结论：
- 这是本轮最明确的生命周期竞态。
- 当前实现只能保证 map 不炸，不能保证“删的是旧 session，不是新 session”。

证据链：
- `StartSession/ResumeSession` 完成 driver 调用后统一 `Register`，见 `internal/provider/unified/client.go:29-65`。
- orchestration 在 `StopAgent`、`StopAllAgents`、`handleProcessExit` 路径都会调用 `removeSession`，见 `internal/sidecar/orch/orchestration/service.go:127-153`、`internal/sidecar/orch/orchestration/service.go:355-369`。
- `Register` 和 `Remove` 都在锁外做真正的 `ForceStop/Close`，见 `internal/provider/unified/session.go:37-45`、`internal/provider/unified/session.go:69-83`。

额外风险：
- `GetSession` 返回的 session 没有 lease/代际检查；调用方拿到 session 后，另一条路径可以立刻 `Remove` 并关闭它，见 `internal/provider/unified/session.go:49-58`。

建议：
- 给 session manager 引入 generation/token。
- `Remove(agentID)` 改为 compare-and-remove 或 `RemoveIfSame(agentID, session)`。
- `Register` 只在 map 仍指向旧 session 时再 `ForceStop(previous)`。

### 5.3 `codexapp.session.threadID` 无锁读写

主结论：
- 这是明确的 data race。

证据链：
- `newSession` 创建后立刻启动 `startReadLoop()` 和 `startHealthLoop()`，见 `internal/provider/codexapp/session.go:87-103`。
- 但 driver 在更晚的 `startRemoteThread/resumeRemoteThread` 之后才写 `s.threadID = threadID`，见 `internal/provider/codexapp/driver.go:87-93`、`internal/provider/codexapp/driver.go:105-110`。
- 与此同时，多处后台路径会读 `s.threadID`：`internal/provider/codexapp/session.go:111-209`、`internal/provider/codexapp/recovery.go:79-87`、`internal/provider/codexapp/session_approval.go:60-71`、`internal/provider/codexapp/session_history.go:13-26`。

建议：
- 最稳妥的修法：把 `startReadLoop/startHealthLoop` 延后到 threadID 确定之后。
- 或者让 `threadID` 读写统一走 `s.mu`。

### 5.4 `rpc.Server.methods` 启动期约束而非构造性安全

主结论：
- 当前没有看到运行态重复注册，但实现本身没有并发防护。

证据链：
- `Server.Register` 直接写 `handler.Map`，见 `internal/platform/rpc/server.go:37-40`。
- `Dispatch` 和 `serveConn` 会在后续运行态读取 `s.methods`，见 `internal/platform/rpc/server.go:50-61`、`internal/platform/rpc/server.go:114-124`。
- 当前唯一路径是 Fx 启动期 `registerAllHandlers`，见 `internal/platform/rpc/module.go:50-52`。

建议：
- 要么在 `Server` 内部加锁保护 `methods`。
- 要么在 `Run` 前冻结注册，并在运行态调用 `Register` 时直接返回错误。

## 6. 死锁风险检查

检查方法：
- 用 LSP 搜索了全部 `.Lock()` / `.RLock()` / `Unlock()` / `RUnlock()` 使用点。
- 重点回读了 `orchestration`、`codexapp transport`、`claudecli transport`、`approval manager`、`wails lifecycle`、`thread/service`。

### 6.1 已确认的锁顺序

| 锁顺序 | 位置 | 结论 |
| --- | --- | --- |
| `service.mu -> queue.mu` | `internal/sidecar/orch/orchestration/service.go:166-187`、`internal/sidecar/orch/orchestration/service.go:291-324`、`internal/sidecar/orch/orchestration/helpers.go:119-138` | 只发现这一种方向，未发现 `queue.mu -> service.mu` 反向路径 |
| `writeMu -> stateMu` | `internal/provider/codexapp/transport.go:192-201`、`internal/provider/codexapp/transport.go:271-284` | `writeJSON -> currentWS` 会这样拿锁；未发现 `stateMu` 持有期间再去拿 `writeMu` |
| `ApprovalManager.mu` 单锁 | `internal/platform/rpc/approval.go:127-350` | 外部动作如 `cancel`、`publishResolved` 在解锁后执行，避免持锁回调 |
| `WailsLifecycle.mu` 单锁 | `internal/ui/wails/lifecycle.go:53-191` | 取函数指针后在锁外执行，避免自锁 |
| `EventBridge.mu` 单锁 | `internal/ui/wails/bridge.go:42-79` | cancel 函数在锁外调用，避免锁内阻塞 |

### 6.2 结论

- 本轮未发现现有反向锁序。
- 没有看到“持有锁时再调用外部回调/阻塞 IO”的危险模式在共享核心路径中形成闭环。
- 当前死锁风险属于“低到中”，主要风险来自未来演化时破坏现有单向顺序，而不是当前代码已经构成死锁。

## 7. 整改建议

1. 先修 `SessionManager` 的代际问题。这个问题会直接造成新 session 被误删，优先级最高。
2. 立即修 `codexapp.session.threadID` 的无锁读写。最简单的方案是 threadID 就绪后再启动后台循环。
3. 把 `ApprovalManager.pending.dispatcher` 收回锁内或构造期写死，消掉 register/resolve 快速竞争窗口。
4. 给 `rpc.Server.methods` 加注册期保护，避免未来出现运行态并发注册。
5. 审视所有“超时返回但后台 goroutine 继续跑”的包装函数，至少给 `Session.Close` 明确契约：`ForceStop` 后必须尽快使 `Close` 返回。

## 8. 最终判定

- 共享状态保护：部分通过。
- map 并发访问：大体通过，但 `SessionManager.sessions` 存在生命周期竞态，`rpc.Server.methods` 依赖启动期约束。
- goroutine 泄漏：大体通过，但 `unified.closeSession`、`wails.runner.Run` 不是严格收敛。
- channel 使用：大体通过，少数点依赖超时而不是显式关闭。
- race 热点：不通过，至少有 3 个真实问题需要修。
- 死锁风险：当前未发现确定性死锁链，暂时通过。
