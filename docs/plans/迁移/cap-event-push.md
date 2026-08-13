# 能力+容错审查：Event Bus → Push → Wails 事件推送链

审查时间：2026-03-21
审查范围：V3 `internal/platform/bus` / `internal/platform/rpc` / `internal/ui/wails` / `cmd/mcp-orch/orchestration` / `internal/provider/*`，以及 V2 `go-agent-v2` 对照实现。
取证方式：仅使用 LSP `text_search` / `workspace_symbol` / `references(compact)` / `call_hierarchy` / `read_file(func_start/func_end)`。

## 结论摘要

- bus 运行时没有“发布后 0 订阅”的硬孤儿，因为 `internal/platform/bus/module.go:10-23` 会统一装配 `NewLogSink`，至少把已知 typed event 订阅到日志。
- 但存在大量“仅日志订阅”的软孤儿：`AgentLaunched` / `AgentStopped` / `AgentRecovering` / `AgentFailed` / `TurnInterrupted` / `TurnInputReceived` / `TurnOutputDelta` / 全部 tool approval/call 事件 / 全部 workspace 事件，当前都没有业务消费者，也不进 jrpc2 push / Wails bridge。
- jrpc2 push 与 Wails bridge 都只桥接 3 个事件：`agentdto.StateChanged`、`turndto.TurnStarted`、`turndto.TurnCompleted`。证据见 `internal/platform/rpc/push.go:75-92`、`internal/ui/wails/bridge.go:42-89`。
- Wails bridge 是独立于 jrpc2 push 的第二套订阅链，不复用 `NotifyAll`。FX wiring 分别见 `internal/platform/rpc/module.go:51-69` 与 `internal/ui/wails/module.go:120-133`。
- Wails 后端桥接频道名确实是 `bridge-event`，并且负载形状与 V2 `type + payload` 一致；但 V3 当前 embedded frontend 只有占位页 `internal/ui/wails/frontend/index.html:1-53`，没有任何 `bridge-event` 或 `app-will-quit` 监听，因此链路目前只到 Wails runtime，不到前端消费逻辑。
- bus 底层 `github.com/kelindar/event@v1.5.2` 是“异步队列 + 定时唤醒”模型，不是同步逐个回调；但慢 subscriber 队列打满会反压发布者，见 `event.go:154-159`、`188-213`、`243-270`。
- 顺序保证只成立在“同一 event type、同一个 subscriber 队列”的范围内；同一 agent 的跨类型事件没有顺序保证，跨 agent 更没有全局顺序保证。
- 错误处理分裂：`ResilientSubscribe` 会 recover panic 并记日志；plain `event.Subscribe` 不 recover，panic 会把 goroutine 直接炸出。push 失败记 warn 并吞掉；Wails bridge 没有错误返回路径。
- agent 事件存在双源：orchestration 和 provider translator 都在发 `agentdto.*`，没有 source 字段、没有隔离层、没有去重；更糟的是双方 `SessionID` 编码都不一致。
- V2 `agentcore/types.go` 暴露 63 个命名事件；V3 typed bus 只定义 28 个，当前 live publisher 约 19 个，而 push/Wails 只外放 3 个，迁移面明显不足。

## 1. bus 发布→订阅闭环

### 1.1 运行时是否存在“硬孤儿”

结论：**没有硬孤儿，但这是因为 `LogSink` 兜底，不代表有业务闭环。**

- `internal/platform/bus/module.go:10-23` 通过 FX 提供 `NewLogSink`，并在应用主模块 `internal/app/modules.go:23-44` 中装配。
- `internal/platform/bus/sink.go:43-87` 为 agent / turn / tool / task / workspace / ui 六类 typed event 全部预埋了日志订阅。
- 因此，只要发布的是这些已知 DTO，至少会被 `LogSink` 订阅一次。

### 1.2 live publish → subscribe 矩阵

| 事件 | 发布点 | 订阅点 | 判定 |
| --- | --- | --- | --- |
| `agentdto.StateChanged` | orchestration `cmd/mcp-orch/orchestration/events.go:13-23`；codex translator `internal/provider/codexapp/event_map.go:39-74` | `LogSink` `internal/platform/bus/sink.go:43-49`；jrpc2 push `internal/platform/rpc/push.go:81-90`；Wails bridge `internal/ui/wails/bridge.go:53-63` | 有业务闭环，但存在双源 |
| `turndto.TurnStarted` | claude translator `internal/provider/claudecli/event_map.go:55-85`；codex translator `internal/provider/codexapp/event_map.go:76-112` | `LogSink` `internal/platform/bus/sink.go:51-59`；orchestration turn lifecycle `cmd/mcp-orch/orchestration/module.go:25-53`；jrpc2 push；Wails bridge | 有业务闭环 |
| `turndto.TurnCompleted` | claude translator；codex translator | `LogSink`；orchestration turn lifecycle；jrpc2 push；Wails bridge | 有业务闭环 |
| `agentdto.AgentLaunched` | orchestration `cmd/mcp-orch/orchestration/events.go:25-33`；claude/codex translator | 仅 `LogSink` | 软孤儿 |
| `agentdto.AgentStopped` | orchestration `cmd/mcp-orch/orchestration/events.go:35-43`；claude/codex translator | 仅 `LogSink` | 软孤儿 |
| `agentdto.AgentRecovering` | orchestration `cmd/mcp-orch/orchestration/events.go:45-53`；codex translator | 仅 `LogSink` | 软孤儿，且双源 |
| `agentdto.AgentFailed` | orchestration `cmd/mcp-orch/orchestration/events.go:55-64`；claude/codex translator | 仅 `LogSink` | 软孤儿 |
| `turndto.TurnInterrupted` | claude/codex translator | 仅 `LogSink` | 软孤儿 |
| `turndto.TurnInputReceived` | claude translator `internal/provider/claudecli/event_map.go:59-64` | 仅 `LogSink` | 软孤儿 |
| `turndto.TurnOutputDelta` | claude/codex translator | 仅 `LogSink` | 软孤儿 |
| `tooldto.ToolCallBegin` | claude/codex translator | 仅 `LogSink` | 软孤儿 |
| `tooldto.ToolCallEnd` | claude/codex translator | 仅 `LogSink` | 软孤儿 |
| `tooldto.ToolApprovalRequested` | codex translator `internal/provider/codexapp/event_map.go:132-137`；approval manager `internal/platform/rpc/approval_events.go:20-29` | 仅 `LogSink` | 软孤儿，且双源 |
| `tooldto.ToolApprovalResolved` | codex translator；approval manager `internal/platform/rpc/approval_events.go:31-40` | 仅 `LogSink` | 软孤儿，且双源 |
| `workspacedto.WorkspaceRun*` 5 类 | workspace service `internal/module/workspace/service.go:45-55` + `service_helpers.go:220-284` | 仅 `LogSink` | 软孤儿 |

### 1.3 软孤儿列表

当前“发布存在，但只有 `LogSink` 订阅，没有业务/bridge 消费”的事件：

- `agentdto.AgentLaunched`
- `agentdto.AgentStopped`
- `agentdto.AgentRecovering`
- `agentdto.AgentFailed`
- `turndto.TurnInterrupted`
- `turndto.TurnInputReceived`
- `turndto.TurnOutputDelta`
- `tooldto.ToolCallBegin`
- `tooldto.ToolCallEnd`
- `tooldto.ToolApprovalRequested`
- `tooldto.ToolApprovalResolved`
- `workspacedto.WorkspaceRunCreated`
- `workspacedto.WorkspaceRunStatusChanged`
- `workspacedto.WorkspaceRunMerged`
- `workspacedto.WorkspaceRunAborted`
- `workspacedto.WorkspaceRunMergeError`

### 1.4 死订阅 / 未落地事件

这些 typed event 已定义、`LogSink` 已订阅，但当前仓库没找到发布点：

- `turndto.TurnStalled`
- `turndto.TurnResumed`
- `taskdto.TaskDagCreated`
- `taskdto.TaskNodeStatusChanged`
- `taskdto.TaskWakeupDispatched`
- `taskdto.TaskWakeupCompleted`
- `uidto.UIProjectionUpdated`
- `uidto.UITimelineAppended`
- `uidto.UITokensUpdated`

证据：`text_search` 对 `TaskDagCreated{`、`UIProjectionUpdated{` 无结果；`TurnStalled` / `TurnResumed` 只有 DTO + `LogSink` 订阅，见 `internal/dto/turn/event.go`、`internal/platform/bus/sink.go:51-59`。

## 2. bus → jrpc2 push

结论：**3 个订阅点 wiring 正确，但 V2 method name 兼容只做到“部分兼容”。**

### 2.1 是否正确订阅并推送

- `internal/platform/rpc/module.go:51-69` 在 FX `OnStart` 时调用 `subscribeCoreEventPushes(...)`。
- `internal/platform/rpc/push.go:75-92` 明确只订阅：
  - `agentdto.StateChanged` → `ui/state/changed`
  - `turndto.TurnStarted` → `turn/started`
  - `turndto.TurnCompleted` → `turn/completed`
- `internal/platform/rpc/server.go:66-75` 使用 `Server.NotifyAll(...)` 广播给当前活动的 jrpc2 连接。
- `internal/platform/rpc/server.go:157-169` 设置 `AllowPush: true`，push 能力是开启的。

### 2.2 V2 method name 兼容性

兼容部分：

- `ui/state/changed`：V2 UI 层把它当作关键 bridge method 之一，见 `go-agent-v2/cmd/agent-terminal/app_helpers.go:104-110`。
- `turn/started`：V2 codex client map 显式映射到 `EventTurnStarted`，见 `go-agent-v2/legacy-agentsdk/codex/client_appserver_events.go:636-647`。
- `turn/completed`：V2 codex client map 显式映射到 `EventTurnComplete`，同上。

不兼容部分：

- V2 仍保留独立 `turn/aborted` method，见 `go-agent-v2/legacy-agentsdk/codex/client_appserver_events.go:641-644` 与 `go-agent-v2/cmd/agent-terminal/app_helpers.go:104-110`。
- 但 V3 codex translator 把 raw `turn/aborted` 折叠成 `turndto.TurnCompleted{Success:false}`，见 `internal/provider/codexapp/event_map.go:76-85`；随后 push bridge 固定发 `turn/completed`，不会发 `turn/aborted`。

结论：**成功态是 exact-compatible；终止/中断语义不是 exact-compatible，而是“折叠兼容”。**

## 3. bus → Wails bridge

结论：**后端桥是独立的，频道名也是 `bridge-event`；但当前仓库没有前端消费。**

### 3.1 是否独立于 jrpc2 push

是，且是两条平行链：

- jrpc2 push：`internal/platform/rpc/module.go:51-69`
- Wails bridge：`internal/ui/wails/module.go:120-133`

两边都各自对同一个 bus 再订阅一次 3 个核心事件，不共享 `NotifyAll`，也不共用订阅取消器。

### 3.2 频道名与负载形状

- 频道名常量定义在 `internal/ui/wails/lifecycle.go:11-15`，`bridgeEventName = "bridge-event"`。
- `internal/ui/wails/bridge.go:81-88` 统一发：
  - channel: `bridge-event`
  - payload: `{ "type": method, "payload": ... }`

这与 V2 `buildBridgeEventPayload()` 的负载形状一致，见 `go-agent-v2/cmd/agent-terminal/app_bridge.go:13-19`。

### 3.3 与 V2 的差异

- V2 会同时发 `bridge-event` 和 `agent-event`，见 `go-agent-v2/cmd/agent-terminal/app_bridge.go:94-115`。
- 当前 V3 内部搜索不到 `agent-event`，只保留 `bridge-event`。

### 3.4 终点是否真的到前端

没有证据表明已经到。

- `internal/ui/wails/module.go:88-95` 只是把 emitter 绑定到 `wailsApp.Event.Emit(channel, payload)`。
- 但 embedded frontend 只有静态占位页 `internal/ui/wails/frontend/index.html:1-53`。
- 在 `internal/ui/wails/frontend` 下未找到 `bridge-event`、`app-will-quit`、任何 Wails event listener。

结论：**后端 bridge 存在，但当前仓库里的前端消费链是空的。**

## 4. 事件丢失风险

结论：**bus 本身偏“异步排队”，不主动丢弃；但 bridge 两端都没有 replay，存在启动窗口丢失与背压停顿风险。**

### 4.1 bus 是同步还是异步

底层 `kelindar/event@v1.5.2` 不是同步逐个回调：

- `Publish()` 只把事件 append 到 subscriber queue，见 `event.go:154-159`、`243-270`
- 每个 subscriber 独立 goroutine `Listen()` 处理自己的 FIFO 队列，见 `event.go:188-213`
- 每个 event group 还有一个 `Process()` ticker 周期性 `Broadcast()` 唤醒，见 `event.go:226-240`

所以语义是：**发布阶段主要是入队，消费在异步 goroutine 中完成。**

### 4.2 subscriber 阻塞会不会影响发布者

会，但不是立刻同步阻塞，而是**队列打满后反压**：

- `group.Broadcast()` 在 `maxQueue` 到顶时会 `cond.Wait()`，见 `event.go:248-260`
- `maxQueue` 默认 50000，见 `event.go:41-45`

这意味着：

- 正常情况下慢 subscriber 不会立刻卡发布者
- 一旦某个 subscriber 长时间处理不过来，把自己队列打满，就会阻塞同类型事件的后续发布
- 这是“停顿风险”，不是“静默丢弃”

### 4.3 push/Wails 的 replay 缺口

jrpc2 push 没有 replay：

- `Server.NotifyAll()` 只遍历当前 `snapshotActive()` 的活连接，见 `internal/platform/rpc/server.go:66-75`、`146-155`
- 客户端未连上之前发生的事件不会补发

Wails bridge 也没有 replay：

- `EventBridge.publish()` 直接 `lifecycle.EmitEvent(...)`，见 `internal/ui/wails/bridge.go:81-88`
- `EmitEvent()` 没有缓冲队列，见 `internal/ui/wails/lifecycle.go:114-122`

### 4.4 Wails 启动窗口丢失

这是当前最现实的风险点：

- `RunDesktop()` 先 `app.Start()`，再 `wailsApp.Run()`，见 `internal/app/app.go:29-50`
- `bridge.Start()` 在 FX `OnStart` 就发生，见 `internal/ui/wails/module.go:120-133`
- `frontendReady` 直到 Wails `ApplicationStarted` 才标记，见 `internal/ui/wails/module.go:93-95`

所以存在窗口：

1. FX runtime 已启动
2. bridge 已开始从 bus 收事件
3. Wails frontend 还未 ready / 还没 listener
4. 事件直接 `Event.Emit(...)` 出去，没有缓存

结果：**启动早期事件可能已经发到 Wails runtime，但前端并不会补收。**

## 5. 事件顺序保证

结论：**只保证“同类型、同 subscriber 队列”的 FIFO；同一 agent 的跨类型事件、跨 bridge 事件都不保证观察顺序。**

### 5.1 同一 event type

对单个 subscriber 而言，同类型事件 FIFO：

- `group.Broadcast()` 按发布顺序 append 到 `sub.queue`，见 `event.go:263-269`
- `consumer.Listen()` 顺序遍历 `pending`，见 `event.go:209-212`

因此：

- 同一个 subscriber 看到的 `StateChanged` 序列是 FIFO
- 同一个 subscriber 看到的 `TurnCompleted` 序列也是 FIFO

### 5.2 同一 agent 的跨类型事件

**不保证。**

原因是不同 event type 被分到不同 group，不同 group 对应不同 subscriber goroutine。

具体例子：

- launch 成功时，orchestration 先 `fireOrForceLocked(...TriggerLaunchSucceeded)` 产出 `StateChanged(idle)`，再 `publishAgentLaunched()`，见 `cmd/mcp-orch/orchestration/service.go:255-263`
- 但 `StateChanged` 和 `AgentLaunched` 是两个不同 typed event
- 对同时订阅两者的消费者来说，观察顺序取决于两个 goroutine 的调度，不受发布先后严格约束

### 5.3 跨 agent

**没有 agent 级顺序保证。**

- 同类型事件只会按“实际拿到 group 锁的 publish 顺序”排入队列
- 多 agent 并发 publish 时，这个顺序本身就是竞争结果

### 5.4 jrpc2 push 与 Wails 之间

两条 bridge 彼此独立订阅同一 bus：

- push 一个 subscriber
- Wails 一个 subscriber

因此即使同一事件都能到两边，**“哪边先到”也不保证。**

## 6. 错误处理

结论：**有 recover，但不统一；panic 风险仍然存在，且大部分错误最后都只是日志。**

### 6.1 bus subscriber 报错时怎么处理

typed handler 没有 error 返回值，只有 panic 这条异常路径。

- plain `event.Subscribe(...)`：无 recover，见 `event.go:94-150`、`188-213`
- `ResilientSubscribe(...)`：包一层 `recoverCall`，panic 记日志，见 `internal/platform/bus/resilient.go:10-29`

### 6.2 运行时哪些订阅是 resilient

是 resilient 的：

- orchestration turn lifecycle 订阅，见 `cmd/mcp-orch/orchestration/module.go:33-44`
- jrpc2 push 订阅，见 `internal/platform/rpc/push.go:81-90`
- Wails bridge 订阅，见 `internal/ui/wails/bridge.go:53-63`

不是 resilient 的：

- `LogSink` 的全部订阅，见 `internal/platform/bus/sink.go:89-99`

因此：

- 业务 bridge 不会因 handler panic 直接把进程打崩
- `LogSink` 如果出现 panic，仍有进程级风险

### 6.3 push / Wails 的错误传播

- jrpc2 push：`NotifyAll()` 内部只记 `Warn`，不向发布者回传错误，见 `internal/platform/rpc/server.go:66-75`
- Wails bridge：`publish()` 没有 error 返回；`EmitEvent()` 也是 fire-and-forget，见 `internal/ui/wails/bridge.go:81-88`、`internal/ui/wails/lifecycle.go:114-122`
- `payloadToMap()` 序列化失败时，会把 payload 退化成 `{error: ...}`，而不是中断事件，见 `internal/ui/wails/bridge.go:91-109`

结论：**当前链路整体偏“吞错 + 日志告警”，不是 fail-fast。**

## 7. 双源事件

结论：**存在，而且没有隔离、没有去重、没有统一 session 键。**

### 7.1 哪些 agent 事件是双源

orchestration 会发布：

- `StateChanged`
- `AgentLaunched`
- `AgentStopped`
- `AgentRecovering`
- `AgentFailed`

见 `cmd/mcp-orch/orchestration/events.go:13-64`。

provider translator 也会发布：

- codex：同样 5 类，见 `internal/provider/codexapp/event_map.go:39-74`
- claude：`AgentLaunched` / `AgentStopped` / `AgentFailed`，见 `internal/provider/claudecli/event_map.go:35-53`

### 7.2 是否有 source / dedupe

没有。

- `agentdto.StateChanged` 结构只有 `OldState` / `NewState` / `Trigger`，没有 source 字段，见 `internal/dto/agent/event.go:5-18`
- push/Wails 都直接把这个 DTO 发出去，不做来源标记或去重，见 `internal/platform/rpc/push.go:81-90`、`internal/ui/wails/bridge.go:53-63`

### 7.3 SessionID 还不统一

这是当前去重最麻烦的点：

- orchestration 的 `SessionID` 用本地 `launchSeq`，见 `cmd/mcp-orch/orchestration/events.go:66-88`
- codex translator 的 `SessionID` 用远端 `sessionId`，拿不到时退回 `threadId`，见 `internal/provider/codexapp/event_map.go:153-164`
- claude translator 的 `SessionID` 用远端 `session_id`，见 `internal/provider/claudecli/event_map.go:105-115`

所以：

- 同一个 agent 的两路事件未必能按 `session_id` 对齐
- 现在的桥接链也没有统一 canonical source

### 7.4 实际影响

对外桥接层，`ui/state/changed` 尤其有风险：

- orchestration 状态机每次成功 transition 都发 `StateChanged`，见 `cmd/mcp-orch/orchestration/service.go:281-289`
- codex 原始 `thread/status/changed` 也会翻译成 `StateChanged`，见 `internal/provider/codexapp/event_map.go:47-53`

结果：**同一 agent 的状态 UI 事件可能重复、交织，且无法区分是“本地调度状态”还是“provider 上报状态”。**

## 8. Wails lifecycle 事件

结论：**后端会推 `app-will-quit`，但当前仓库没有前端监听，端到端不成立。**

### 8.1 quit overlay 是否推送

会。

- `ShouldQuit()` 在有活跃 agent 时调用 `emitQuitOverlay(activeCount)`，见 `internal/ui/wails/lifecycle.go:82-99`
- `emitQuitOverlay()` 发频道 `app-will-quit`，见 `internal/ui/wails/lifecycle.go:137-143`
- emitter 绑定到 `wailsApp.Event.Emit(channel, payload)`，见 `internal/ui/wails/module.go:88-92`

与 V2 一致：

- V2 也发 `app-will-quit`，见 `go-agent-v2/cmd/agent-terminal/main_setup.go:388-391`

### 8.2 是否真的被前端接住

当前看不到。

- `internal/ui/wails/frontend/index.html:1-53` 只有静态占位内容
- `internal/ui/wails/frontend` 下未找到 `app-will-quit` 监听代码

### 8.3 退出最终收尾

后端失败/停止后，`NotifyBackendFailed()` 会触发真正 `Quit()`，见：

- `internal/app/runner.go:44-53`
- `internal/app/app.go:78-88`
- `internal/ui/wails/lifecycle.go:105-112`

但这里没有第二个“前端生命周期事件”，只是直接请求应用退出。

### 8.4 风险

- `pendingQuit` 只缓存“最终退出请求”，不缓存 `app-will-quit` overlay 事件，见 `internal/ui/wails/lifecycle.go:105-112`、`157-173`
- 所以如果 overlay 在 frontend listener 准备前发出，会直接丢

## 9. 状态机 → 事件

结论：**`StateChanged` 是强耦合；`TurnStarted` / `TurnCompleted` 不是。**

### 9.1 `StateChanged`

是稳定的。

- 所有成功的状态迁移都经 `fireAndPublishLocked()`，见 `cmd/mcp-orch/orchestration/service.go:281-289`
- 该函数在 `agent.sm.FireCtx(...)` 成功后立刻 `publishStateChanged(...)`

所以：**状态机 transition 成功后，一定发 `StateChanged`。**

### 9.2 `TurnStarted` / `TurnCompleted`

不是状态机内生事件，而是 provider 输入事件：

- `TurnStarted` 只在 provider translator 中构造，见 `internal/provider/claudecli/event_map.go:55-85`、`internal/provider/codexapp/event_map.go:76-112`
- `TurnCompleted` 也只在 provider translator 中构造
- orchestration 只是订阅这些 turn 事件来反推状态机，见 `cmd/mcp-orch/orchestration/module.go:25-53`

因此链路方向是：

1. provider raw event
2. translator 发 `TurnStarted` / `TurnCompleted`
3. orchestration 订阅后调用 `BindActiveTurnID()` / `CompleteTurn()`
4. 状态机 transition
5. `StateChanged`

不是：

1. 状态机 transition
2. turn 事件

### 9.3 由此带来的空洞

- 本地 `turn_queued -> turn_starting` 发生时，只会发 `StateChanged`，不会发 `TurnStarted`，见 `cmd/mcp-orch/orchestration/service.go:291-324`
- `finishTurnStartSuccess()` 把 `turn_starting -> turn_running` 时，同样只是状态迁移，不产出 `TurnStarted`，见 `cmd/mcp-orch/orchestration/helpers.go:153-174`
- `finishTurnStartFailure()` / `reconcileReadyStateLocked()` 能把 turn 状态收回 `idle`，也只会产出 `StateChanged`，见 `cmd/mcp-orch/orchestration/helpers.go:119-138`、`176-192`

结论：**当前只保证“状态机会发 `StateChanged`”；并不保证“状态转移后必有对应 `TurnStarted` / `TurnCompleted`”。**

### 9.4 sideband agent 事件也不是状态机产物

- `publishAgentRecovering()` 在真正 `TriggerRecoverRequested` 之前先发，见 `cmd/mcp-orch/orchestration/recover.go:27-40`
- `publishAgentStopped()` 在 `StopAgent()` 返回路径里手工发，见 `cmd/mcp-orch/orchestration/service.go:127-141`
- `publishAgentFailed()` 在 `recordProcessExitError()` 里手工发，然后才做 `process_exited` transition，见 `cmd/mcp-orch/orchestration/service.go:372-394`

所以 `AgentLaunched` / `AgentStopped` / `AgentRecovering` / `AgentFailed` 只是 sideband 事件，不是状态机统一出口。

## 10. V2 事件面覆盖

结论：**V3 typed bus 只覆盖了 V2 的一部分，而且真正桥接到 push/Wails 的只剩 3 个事件。**

### 10.1 V2 有哪些事件族

`go-agent-v2/legacy-agentsdk/agentcore/types.go:210-274` 一共定义了 **63 个命名事件**。按语义可归为：

1. agent / session / turn lifecycle
   例：`session_configured`、`turn_started`、`turn_complete`、`turn_aborted`、`idle`、`shutdown_complete`、`connection_dead`
2. agent 输出 / reasoning 流
   例：`agent_message*`、`agent_reasoning*`、`reasoning*`
3. exec / tool / approval
   例：`exec_approval_request`、`exec_command_*`、`dynamic_tool_call`
4. file / patch / diff / undo
   例：`patch_apply*`、`file_read`、`file_updated`、`turn_diff`、`undo_*`
5. plan / review / collab
   例：`turn_plan`、`plan_delta`、`plan_update`、`entered_review_mode`、`collab_*`
6. MCP / integration
   例：`mcp_tool_call*`、`mcp_list_tools_response`、`mcp_startup_*`、`mcp_oauth_completed`
7. thread 元数据
   例：`thread_name_updated`、`thread_rolled_back`
8. error / warning / token / context / background
   例：`error`、`warning`、`stream_error`、`token_count`、`context_compacted`、`background_event`

### 10.2 V3 定义了多少

V3 `internal/dto/shared/event.go:5-39` 只定义了 **28 个 typed event**，6 个家族：

1. agent：5
2. turn：7
3. tool：4
4. task：4
5. workspace：5
6. ui projection：3

### 10.3 V3 live publisher 覆盖了多少

当前 live publisher 大约覆盖 **19 / 28**：

- agent：5 / 5
- turn：5 / 7
  缺 `TurnStalled`、`TurnResumed`
- tool：4 / 4
- workspace：5 / 5
- task：0 / 4
- ui projection：0 / 3

### 10.4 Push / Wails 实际外放了多少

只外放 **3 个**：

- `ui/state/changed`
- `turn/started`
- `turn/completed`

这意味着：

- 若按 V2 raw method 精确对比：**3 / 63**
- 若按 V3 typed bus live 对比：**3 / 19**

### 10.5 主要缺失项

相对 V2，当前 V3 push/Wails 明显缺失：

- turn 异常态：`turn/aborted` 独立 method 不再外放
- turn 流式输出：`agent_message_delta` / `reasoning_delta` / `exec_output_delta` 等
- tool 生命周期与 approval 事件
- patch / file / diff / undo 事件
- plan / review / collab 事件
- MCP / OAuth / list skills / startup 事件
- warning / error / token / context / background 事件
- thread 元数据事件

另外，V3 新增了 `task` / `workspace` / `ui projection` typed family，但：

- `workspace` 虽然会发布，却没有 bridge/export
- `task` / `ui projection` 当前连发布点都没有

## 最终判定

### 通过项

- bus、jrpc2 push、Wails bridge 的基础 wiring 都存在
- Wails bridge 独立于 jrpc2 push
- `bridge-event` 频道名正确
- `bridge-event` 负载形状与 V2 一致
- 状态机成功迁移后必发 `StateChanged`

### 主要问题

1. **双源 agent 事件没有隔离/去重。**
   尤其 `StateChanged`，codex provider 与 orchestration 会同时进入同一桥接方法 `ui/state/changed`。

2. **顺序保证不足。**
   只能保证同类型 FIFO；同一 agent 的跨类型事件没有顺序语义。

3. **启动窗口存在前端事件丢失。**
   Wails bridge 在 FX start 后就工作，但 frontend ready 更晚，且没有 replay。

4. **Wails 端到端链路未闭环。**
   当前 embedded frontend 只是占位页，没有 `bridge-event` / `app-will-quit` 监听。

5. **V2 事件面覆盖严重不足。**
   push/Wails 只外放 3 个事件，迁移远未覆盖 V2 的主要流式/工具/计划/MCP/错误事件面。

## 建议优先级

### P0

- 为 `agentdto.StateChanged` 等双源事件增加 `source` 字段，至少区分 `orchestration` / `provider`
- 定义统一 `session_id` 规则，否则无法可靠去重
- 明确前端是否仍需要 `agent-event`；若需要，Wails bridge 需补发

### P1

- 为 Wails bridge 增加 startup buffering / replay，至少覆盖 frontend ready 前的关键事件
- 明确 turn 终态语义：是继续折叠为 `turn/completed(success=false)`，还是恢复独立 `turn/aborted`

### P2

- 规划 V2 → V3 事件族迁移表，优先补齐：
  - turn streaming
  - tool approval / tool lifecycle
  - error / warning / token / context
  - MCP / plan / collab

## 互审

### 1. 对 `docs/plans/迁移/cap-orchestration-agent.md`

1. 这份“报告”当前最直接的问题是**交付物不存在**。对 `docs/plans/迁移/cap-orchestration-agent.md` 直接做 LSP `read_file` 返回 `path not found`，说明用户给定路径下没有可复核正文。
2. 这也不是简单的文件名偏差。对 `docs/plans/迁移` 做 LSP `text_search("# cap-orchestration-agent")` 与 `text_search("能力+容错审查：Orchestration Agent")` 都是 0 命中，说明文档矩阵里没有同名标题可供替代复核。
3. 证据链已经断到结论层。`docs/plans/迁移/final-verdict-2.md:92` 仍把 “`orchestration agent.submit* 真执行链` 已确认修复” 写入总判定，但仓库里没有对应 standalone 报告正文可追溯。也就是说，这个目标的主要问题不是内容瑕疵，而是**引用了不存在的证据载体**。

### 2. 对 `docs/plans/迁移/cap-provider-session.md`

1. 报告在 §1 把 “provider 必填导致与 V2 默认 provider 不等价” 主要落在 `thread.Start`，范围写窄了。它引用的是 `internal/module/thread/lifecycle.go:180-183`，但 `Resume` 其实同样硬性要求 `provider`，见 `internal/module/thread/lifecycle.go:189-203`；而 `newResumeHandler` 只从 `svc.Get(threadID)` 回填 `AgentID`，不回填 `Provider`，见 `internal/module/thread/rpc.go:118-130`。所以默认 provider/fallback 缺口不只影响 Start，也影响 Resume。
2. 报告把 close deadline 问题主要归因于 driver `Close(ctx)` 不 honor `ctx`，但漏掉了更近的调用点 bug。`SessionManager.Remove` 自己就直接 `session.Close(context.Background())`，见 `internal/provider/unified/session.go:59-82`；而 orchestration 的 `StopAgent`、`StopAllAgents`、`handleProcessExit` 都走 `removeSession(agent.id)`，见 `cmd/mcp-orch/orchestration/service.go:127-141`、`143-153`、`355-368`。因此即使 driver 之后修正为 honor ctx，当前 per-agent cleanup 仍然会先把 deadline 丢掉，这一层应该比 driver 侧更先被点名。
3. 报告在 codex recovery 一节停在 “transport-level reconnect 不等于 session-level recovery”，但漏掉了**它也没有进入 orchestration 状态机**。`attemptRecovery` 只发 raw `recovery.attempt`，见 `internal/provider/codexapp/recovery.go:69-94`；translator 只是把它翻成 `agentdto.AgentRecovering`，见 `internal/provider/codexapp/event_map.go:39-70`；而 orchestration live 订阅只有 `TurnStarted` / `TurnCompleted`，见 `cmd/mcp-orch/orchestration/module.go:25-53`，`TriggerRecoverRequested` 也只在手工 `Recover(ctx, agentID)` 时触发，见 `cmd/mcp-orch/orchestration/recover.go:27-58`。这说明 provider 自动恢复与 agent 状态机是脱节的，问题比报告表述的“半层恢复”还更严重。
4. 报告把 stale session 问题放进总结里的 `P0` 没错，但**作用域需要收窄**。主线 stop/exit 路径并不会保留 session：`StopAgent`、`StopAllAgents`、`handleProcessExit` 都明确 `removeSession`，见 `cmd/mcp-orch/orchestration/service.go:127-141`、`143-153`、`355-368`。真正留下 closed-but-retained session 的是 `thread/archive` / `thread/delete` 这两条 thread-service 支线，因为它们只走 `closeSessionIfActive -> session.Close(ctx)`，不走 `SessionManager.Remove`，见 `internal/module/thread/archive.go:5-13`、`internal/module/thread/service.go:102-119`、`228-241`。所以这是“特定入口导致的 retained session”，不是 provider session 主链普遍泄漏。

### 3. 对 `docs/plans/迁移/cap-fx-lifecycle.md`

1. 报告把 R1 “大量悬空 provider” 放进高风险发现，严重度判得过重。Fx 本身就是惰性构造：`Provide` 注释明确写着 “Constructors are called only if one or more of their returned types are needed”，见 `/Users/mima0000/go/pkg/mod/go.uber.org/fx@v1.24.0/provide.go:47-53`；示例文档也明确写着 “Fx calls constructors lazily”，见 `/Users/mima0000/go/pkg/mod/go.uber.org/fx@v1.24.0/example_test.go:44-46`。再看 `bus.NewAgentEmitters` / `NewTurnEmitters` 等构造函数本身，只是返回 wrapper，不起 goroutine、不注册 hook，见 `internal/platform/bus/emitters.go:42-63`。这更像 module surface 冗余，不是 live lifecycle 高风险。
2. 报告在 `RunDesktop()` 一节把 “`app.Start()` 成功后若 `wailsApp == nil` 或 `lifecycle == nil` 会早退且不 `Stop()`” 当成显式缺口，但这条路径实际上是**死防御代码**。`RunDesktop()` 是通过 `newDesktopFXApp(fx.Populate(&wailsApp, &lifecycle))` 取这两个对象，见 `internal/app/app.go:29-50`；`uiwails.Module` 明确提供 `NewWailsLifecycle` 与 `NewWailsApplication`，见 `internal/ui/wails/module.go:17-25`；而这两个构造函数都直接返回非 nil 对象，见 `internal/ui/wails/lifecycle.go:43-50`、`internal/ui/wails/module.go:72-98`。因此如果 DI 真的缺类型，`app.Start` 就应先失败；在当前 live graph 下，“Start 成功但 Populate 结果为 nil” 不具现实性。
3. 报告把 headless signal 双入口列成 R4 高风险，但这在当前仓库里是**未接入发布入口的 dormant risk**。`internal/app.Run()` 的 LSP `references` 为 0，说明没有 caller；当前唯一二进制入口是 `cmd/agent-terminal/main.go:10-15`，它只调用 `app.RunDesktop()`。也就是说，headless 的 Fx `app.Done()` + `RunGroup` 双 signal actor 的确存在于代码路径里，但不是当前 live 运行面上的高风险，最多是未来启用 headless 时必须补的硬化项。
