# 审查：kelindar/event + stateless 状态机

## 1. Event Bus

### 1.1 泛型工厂 API 正确性

- `Bus` 只是对 `*event.Dispatcher` 的注入包装；`NewDispatcher()` 返回底层 dispatcher，本身没有附加语义。证据：`internal/platform/bus/bus.go:5-22`。
- `Route[T]` 是 `event.Subscribe` 的薄封装，`Router` 只负责聚合取消函数，不负责路由判定。证据：`internal/platform/bus/router.go:9-37`。
- `ResilientSubscribe[T]` 在 handler 外层统一 `recover()`，并把 panic 记录到 logger；语义上是“容错订阅”，不是“重试订阅”。证据：`internal/platform/bus/resilient.go:10-29`。
- `Projector[S,E]` 是纯内存 reducer；`Bind()` 只是把 `Apply` 通过 `Route()` 挂到 dispatcher 上。证据：`internal/platform/bus/projection.go:10-43`，`internal/platform/bus/router.go:18-23`。
- `NewEmitter[T]` 与 `TypedEmitter[T]` 都只做 typed publish；其中 `TypedEmitter.On` 是 typed subscribe，`NewEmitter[T]` 本身只返回 publish closure。证据：`internal/platform/bus/emitters.go:32-40`，`internal/platform/bus/typed.go:9-30`。
- 这些工厂 API 在运行时代码中的使用面很薄：`Route()` 的唯一引用是 `Projector.Bind()`；`New()` 状态机工厂的唯一调用点是 orchestration 初始化；`BindEventToNotify()`、`ResilientSubscribe()`、`NewProjector()`、`NewEmitter()` 在运行时代码中都没有业务引用。证据：`internal/platform/bus/projection.go:38-43`，`internal/platform/bus/router.go:18-23`，`internal/sidecar/orch/orchestration/helpers.go:68-73`，`internal/platform/statemachine/factory.go:28-68`。LSP `references` 对 `BindEventToNotify`、`ResilientSubscribe`、`NewProjector` 返回 0。

### 1.2 事件类型清单（6 族）

- V3 typed event ID 表只定义了 6 族共 26 个事件：agent 5 个、turn 7 个、tool 4 个、task 4 个、workspace 3 个、ui 3 个。证据：`internal/dto/shared/event.go:5-37`。
- agent 族：`StateChanged`、`AgentLaunched`、`AgentStopped`、`AgentRecovering`、`AgentFailed`。证据：`internal/dto/agent/event.go:5-44`。
- turn 族：`TurnStarted`、`TurnCompleted`、`TurnInterrupted`、`TurnStalled`、`TurnResumed`、`TurnInputReceived`、`TurnOutputDelta`。证据：`internal/dto/turn/event.go:5-57`。
- tool 族：`ToolCallBegin`、`ToolCallEnd`、`ToolApprovalRequested`、`ToolApprovalResolved`。证据：`internal/dto/tool/event.go:5-38`。
- task 族：`TaskDagCreated`、`TaskNodeStatusChanged`、`TaskWakeupDispatched`、`TaskWakeupCompleted`。证据：`internal/dto/task/event.go:5-41`。
- workspace 族：`WorkspaceRunCreated`、`WorkspaceRunStatusChanged`、`WorkspaceRunMerged`。证据：`internal/dto/workspace/event.go:5-32`。
- ui 族：`UIProjectionUpdated`、`UITimelineAppended`、`UITokensUpdated`。证据：`internal/dto/ui/event.go:5-31`。

### 1.3 EventHeader 嵌入链

- `EventHeader` 是根；agent/tool 分支最长链是 `ToolApprovalHeader -> ToolCallHeader -> TurnHeader -> AgentHeader -> EventHeader`。证据：`internal/dto/shared/event.go:39-74`。
- task 分支链是 `TaskWakeupHeader -> TaskNodeHeader -> TaskDAGHeader -> EventHeader`。证据：`internal/dto/shared/event.go:76-92`。
- workspace 分支是 `WorkspaceRunHeader -> EventHeader`；ui 分支是 `UITurnHeader -> UIProjectionHeader -> EventHeader`。证据：`internal/dto/shared/event.go:94-113`。
- 结构嵌入本身是正确的：每个派生 header 只嵌入一个直接父 header，没有重复嵌入同一祖先。证据：`internal/dto/shared/event.go:44-113`。
- 但“9 层 header，零重复字段”这个约束在当前实现里不成立。当前文件实际定义了 12 个 header struct，不是 9 个。证据：`internal/dto/shared/event.go:40-113`。
- “零重复字段”如果按全局字段名理解也不成立：`DagKey` 同时出现在 `TaskDAGHeader` 和 `WorkspaceRunHeader`，`ThreadID` 同时出现在 `AgentHeader` 和 `UIProjectionHeader`。证据：`internal/dto/shared/event.go:44-49`，`internal/dto/shared/event.go:76-80`，`internal/dto/shared/event.go:95-99`，`internal/dto/shared/event.go:101-106`。

### 1.4 发布/订阅对照表

| 事件族 | 发布面 | 订阅面 | 结论 |
| --- | --- | --- | --- |
| `agent` | orchestration 直接发布 `StateChanged/Launched/Stopped/Recovering/Failed`；provider translator 也可发布 `Launched/Stopped/Failed/Recovering/StateChanged`。证据：`internal/sidecar/orch/orchestration/events.go:13-64`，`internal/sidecar/orch/orchestration/service.go:115-126`，`internal/sidecar/orch/orchestration/service.go:233-234`，`internal/sidecar/orch/orchestration/service.go:327-338`，`internal/sidecar/orch/orchestration/recover.go:35-39`，`internal/provider/claudecli/event_map.go:35-53`，`internal/provider/codexapp/event_map.go:39-74`，`internal/provider/unified/event_map.go:43-66` | `LogSink` 全量订阅。证据：`internal/platform/bus/sink.go:43-49` | 已发布且已订阅，但当前明确的运行时消费者只有 `LogSink`。证据：`internal/platform/bus/module.go:10-23`，`internal/platform/bus/sink.go:21-85` |
| `turn` | provider translator 发布 `TurnStarted/Completed/Interrupted/InputReceived/OutputDelta`。证据：`internal/provider/claudecli/event_map.go:55-85`，`internal/provider/codexapp/event_map.go:76-112`，`internal/provider/unified/event_map.go:43-66` | `LogSink` 订阅 7 个 turn 事件。证据：`internal/platform/bus/sink.go:51-59` | `TurnStalled`、`TurnResumed` 只有订阅没有发布；其余 5 个已发布但没有业务消费者把它们接回 orchestration。证据：`internal/dto/turn/event.go:23-34`，`internal/platform/bus/sink.go:55-56`，`internal/sidecar/orch/orchestration/service.go:244-252` |
| `tool` | provider translator 发布 `ToolCallBegin/End`；approval manager 发布 `ToolApprovalRequested/Resolved`；codex translator 也可发布 approval 事件。证据：`internal/provider/claudecli/event_map.go:87-103`，`internal/provider/codexapp/event_map.go:114-148`，`internal/platform/rpc/approval_events.go:15-35`，`internal/platform/rpc/approval.go:71-96`，`internal/platform/rpc/approval.go:230-257` | `LogSink` 全量订阅。证据：`internal/platform/bus/sink.go:61-66` | 已发布且已订阅，但没有状态机联动。 |
| `task` | 当前发布面未命中。现有命中只有 DTO 定义与 `LogSink` 订阅。证据：`internal/dto/task/event.go:5-41`，`internal/platform/bus/sink.go:68-73` | `LogSink` 订阅。证据：`internal/platform/bus/sink.go:68-73` | 听了没人发。 |
| `workspace` | 当前发布面未命中。现有命中只有 DTO 定义与 `LogSink` 订阅。证据：`internal/dto/workspace/event.go:5-32`，`internal/platform/bus/sink.go:75-79` | `LogSink` 订阅。证据：`internal/platform/bus/sink.go:75-79` | 听了没人发。 |
| `ui` | 当前发布面未命中。现有命中只有 DTO 定义与 `LogSink` 订阅。证据：`internal/dto/ui/event.go:5-31`，`internal/platform/bus/sink.go:81-85` | `LogSink` 订阅。证据：`internal/platform/bus/sink.go:81-85` | 听了没人发。 |

补充：

- `LogSink` 由 `bus.Module` 通过 fx 注入，生命周期内自动创建，因此当前 typed bus 的明确订阅实例是 `NewLogSink()`。证据：`internal/platform/bus/module.go:10-23`，`internal/platform/bus/sink.go:21-85`。
- `BindEventToNotify()` 只是订阅 helper，本身没有被引用；`PushBridge` 当前只被 approval 流程拿来做 RPC callback，不承担 typed bus 事件推送。证据：`internal/platform/rpc/push.go:18-63`，`internal/platform/rpc/module.go:13-21`，`internal/platform/rpc/approval.go:71-96`。LSP `references` 对 `BindEventToNotify` 返回 0。

### 1.5 孤儿事件

- 严格意义上的“发了没人听”在当前 26 个 typed event 中未发现：所有已发布事件都落在 `LogSink` 的订阅覆盖范围内。证据：`internal/platform/bus/sink.go:43-85`，`internal/sidecar/orch/orchestration/events.go:13-64`，`internal/platform/rpc/approval_events.go:15-35`，`internal/provider/claudecli/event_map.go:35-103`，`internal/provider/codexapp/event_map.go:39-148`，`internal/provider/unified/event_map.go:43-66`。
- 但“业务级发了没人用”大量存在：除了 `LogSink` 之外，没有任何 typed subscriber 把 turn/tool 事件回接到 orchestration 或 RPC push。证据：`internal/sidecar/orch/orchestration/service.go:244-252` 是全仓唯一 `agent.sm.FireCtx(...)` 调用；`internal/platform/rpc/push.go:50-63` 的 `BindEventToNotify` 无引用。
- 明确的“听了没人发”集合是 12 个：`TurnStalled`、`TurnResumed`、4 个 task 事件、3 个 workspace 事件、3 个 ui 事件。证据：`internal/dto/turn/event.go:23-34`，`internal/dto/task/event.go:5-41`，`internal/dto/workspace/event.go:5-32`，`internal/dto/ui/event.go:5-31`，`internal/platform/bus/sink.go:55-56`，`internal/platform/bus/sink.go:68-85`。

## 2. 状态机

### 2.1 状态/触发器完整性

- V3 明确定义了 10 个状态：`provisioning`、`idle`、`turn_queued`、`turn_starting`、`turn_running`、`awaiting_user_input`、`recovering`、`stopping`、`stopped`、`failed`。证据：`internal/dto/agent/state.go:8-19`，`internal/dto/agent/state.go:51-62`。
- V3 明确定义了 11 个触发器：`launch_succeeded`、`launch_failed`、`turn_enqueued`、`turn_accepted`、`turn_completed`、`turn_aborted`、`user_input_requested`、`user_input_resolved`、`recover_requested`、`stop_requested`、`process_exited`。证据：`internal/dto/agent/state.go:21-33`，`internal/dto/agent/state.go:64-76`。
- 状态机工厂支持 external storage、queued firing、guard、`OnEntry/OnExit`。证据：`internal/platform/statemachine/factory.go:10-21`，`internal/platform/statemachine/factory.go:28-68`。
- orchestration 用 `buildStatesFromDefinitions()` 把 `TransitionDefinitions` 机械转成 `StateConfig`，再在 `newAgentLocked()` 中实例化 `*stateless.StateMachine`。证据：`internal/sidecar/orch/orchestration/helpers.go:15-30`，`internal/sidecar/orch/orchestration/helpers.go:61-73`。
- `statemachine.Module` 自身没有任何 provider/invoke；真正的状态机装配不在 fx module，而在 orchestration helper。证据：`internal/platform/statemachine/module.go:5`，`internal/app/modules.go:23-38`，`internal/sidecar/orch/orchestration/helpers.go:61-73`。

### 2.2 转换规则覆盖

- 声明表一共列了 23 条转换。证据：`internal/dto/agent/state.go:78-102`。
- 声明表不是运行时唯一权威，因为 orchestration 采用 `fireOrForceLocked()`：`FireCtx()` 失败时直接 `forceStateLocked()` 改状态并补发 `StateChanged`。证据：`internal/sidecar/orch/orchestration/service.go:237-259`。
- `stopped -> launch_failed` 没有声明，但运行时会发生。`prepareLaunchStateLocked()` 在已停止 agent 上保留 `stopped`，`startProcessLocked()` 启动失败时仍触发 `TriggerLaunchFailed`，而声明表只允许 `provisioning/recovering -> failed`。结果只能走 fallback 强制改状态。证据：`internal/sidecar/orch/orchestration/helpers.go:49-59`，`internal/sidecar/orch/orchestration/service.go:215-218`，`internal/dto/agent/state.go:79-80`，`internal/dto/agent/state.go:96-97`，`internal/sidecar/orch/orchestration/service.go:237-259`。
- `awaiting_user_input -> turn_completed` 没有声明，但 `reconcileReadyStateLocked()` 会在 `activeTurnID == ""` 时对 `awaiting_user_input` 直接触发 `TriggerTurnCompleted`，同样只能走 fallback。证据：`internal/sidecar/orch/orchestration/helpers.go:103-109`，`internal/dto/agent/state.go:93-95`，`internal/sidecar/orch/orchestration/service.go:237-259`。
- `recover_requested` 只声明了 `turn_starting -> recovering` 和 `failed -> recovering`，但 `Recover()` 对外公开且不校验当前状态，因此从 `idle/running/stopped` 调用恢复时也只能走 fallback。证据：`internal/dto/agent/state.go:87`，`internal/dto/agent/state.go:100`，`internal/sidecar/orch/orchestration/recover.go:27-41`，`internal/sidecar/orch/orchestration/service.go:237-259`。
- `stop_requested` 只声明了从 `idle/turn_queued/turn_running/awaiting_user_input/failed` 进入 `stopping`，但 `StopAgent()` 对所有已登记 agent 都会执行 `stopAgentLocked()`，因此从 `provisioning/recovering/stopped` 停止同样会绕过声明表。证据：`internal/dto/agent/state.go:82`，`internal/dto/agent/state.go:84`，`internal/dto/agent/state.go:92`，`internal/dto/agent/state.go:95`，`internal/dto/agent/state.go:101`，`internal/sidecar/orch/orchestration/service.go:103-117`，`internal/sidecar/orch/orchestration/service.go:131-138`，`internal/sidecar/orch/orchestration/service.go:237-259`。
- `awaiting_user_input` 这条显式状态目前不可达。触发器 `TriggerUserInputRequested`、`TriggerUserInputResolved` 只有定义，没有任何运行时触发点。证据：`internal/dto/agent/state.go:28-29`，`internal/dto/agent/state.go:90`，`internal/dto/agent/state.go:93`。LSP `text_search` 在 `internal/` 中对这两个 trigger 只命中定义行。
- `CompleteTurn()` 定义了 turn 完成/中止后的状态转移，但没有任何调用者；因此 `turn_completed/turn_aborted` 入口没有从业务链路接上。证据：`internal/sidecar/orch/orchestration/service.go:288-307`。LSP `references` 对 `CompleteTurn` 返回 0。

### 2.3 使用点

- 全仓对 `stateless` 的直接运行时使用集中在 orchestration：`agentRuntime.sm *stateless.StateMachine`、`agent.sm.FireCtx(...)`、`platformstatemachine.AllowedTriggers(...)`。证据：`internal/sidecar/orch/orchestration/service.go:33-53`，`internal/sidecar/orch/orchestration/service.go:183-209`，`internal/sidecar/orch/orchestration/service.go:244-252`。
- `platformstatemachine.New(...)` 的唯一调用点是 `newAgentLocked()`。证据：`internal/sidecar/orch/orchestration/helpers.go:61-73`，`internal/platform/statemachine/factory.go:28-68`。
- `fireOrForceLocked()` 是全部状态变更的汇聚点；其 incoming call hierarchy 覆盖 `SubmitTurn`、`stopAgentLocked`、`startProcessLocked`、`claimTurnWork`、`CompleteTurn`、`handleProcessExit`、`reconcileReadyStateLocked`、`startTurnExecution`、`normalizeRecoveryState`。证据：`internal/sidecar/orch/orchestration/service.go:237-259`，`internal/sidecar/orch/orchestration/service.go:131-138`，`internal/sidecar/orch/orchestration/service.go:140-159`，`internal/sidecar/orch/orchestration/service.go:211-235`，`internal/sidecar/orch/orchestration/service.go:261-307`，`internal/sidecar/orch/orchestration/service.go:309-341`，`internal/sidecar/orch/orchestration/helpers.go:95-121`，`internal/sidecar/orch/orchestration/recover.go:54-56`。

## 3. V2 事件面对照

### 3.1 已迁移事件

- 当前工作树里 V2 可审到的事件面，实际承载在 `go-agent-v2/internal/bus/bus.go` 的 `Msg*`/`Topic*` 常量表，而不是一个可直接读取的 `internal/events/` typed DTO 目录。证据：`go-agent-v2/internal/bus/bus.go:23-89`。
- V2 已有的基础 agent/task/orchestration 消息面非常宽；其中与 V3 已迁移重合的主干是 agent 生命周期、turn 生命周期、tool/approval。证据：`go-agent-v2/internal/bus/bus.go:24-40`，`go-agent-v2/internal/bus/bus.go:51-54`，`internal/dto/shared/event.go:5-23`。
- V2 路由器实际发布 `task_delegate`、`user_message`、`agent_event`；V2 orchestration state 实际发布 `orchestration` 事件。证据：`go-agent-v2/internal/bus/router.go:72-92`，`go-agent-v2/internal/bus/router.go:143-158`，`go-agent-v2/internal/bus/router.go:203-218`，`go-agent-v2/internal/bus/orchestration.go:43-50`，`go-agent-v2/internal/bus/orchestration.go:70-82`，`go-agent-v2/internal/bus/orchestration.go:101-123`。
- V2 状态基线是 5 态：`idle/thinking/running/stopped/error`；其“外显状态”还会被 `effectiveState()` 动态掩码和纠偏。证据：`go-agent-v2/internal/runner/manager.go:17-25`，`go-agent-v2/internal/runner/manager.go:220-276`。
- V2 状态矩阵允许很多事件从任意起点收敛到目标态，例如 `turn_started` 可从 5 个起点都进入 `thinking`，`turn_complete/turn_aborted` 可从 5 个起点都回到 `idle`，`exec_approval_request` 可从 5 个起点都进入 `running`。证据：`go-agent-v2/internal/guards/state_matrix_snapshot.json:37-60`，`go-agent-v2/internal/guards/state_matrix_snapshot.json:63-114`，`go-agent-v2/internal/guards/state_matrix_snapshot.json:438-460`。

### 3.2 缺失事件

- V2 常量表包含但 V3 typed event ID 表未迁移的整族包括：`command_card.*`、`prompt.*`、`skill.*`、`lsp.*`、`lock.*`，以及 `heartbeat.*`、`budget.*`、`rollback.*`、`scheduler.*`。证据：`go-agent-v2/internal/bus/bus.go:41-69`，`go-agent-v2/internal/bus/bus.go:78-87`，`internal/dto/shared/event.go:5-37`。
- 用户特别点名的 `command_card/prompt/skill/lsp/lock` 在 V3 的 typed event ID 表中完全不存在；V3 `internal/dto/shared/event.go` 只覆盖 agent/turn/tool/task/workspace/ui 六族。证据：`go-agent-v2/internal/bus/bus.go:41-57`，`internal/dto/shared/event.go:5-37`。
- V3 虽然保留了部分 “skill/lsp” 能力概念，但没有对应 event family：`provider/turn.go` 只有请求里的 `Skills` 字段，`provider/manifest.go` 只有 `ToolFamily = "lsp"`，都不是 typed bus 事件。证据：`internal/dto/provider/turn.go:13-13`，`internal/dto/provider/manifest.go:6-6`。
- task/workspace/ui 在 V3 里只是“事件定义先行”，迁移并未完成到发布链路。证据：`internal/dto/task/event.go:5-41`，`internal/dto/workspace/event.go:5-32`，`internal/dto/ui/event.go:5-31`，`internal/platform/bus/sink.go:68-85`。

## 4. 事件+状态机联动

- “状态变化 -> 事件” 这条链路是存在的。`fireAndPublishLocked()` 在 `FireCtx()` 成功后调用 `publishStateChanged()`，`forceStateLocked()` 在 fallback 改状态后也会调用同一个 publisher。证据：`internal/sidecar/orch/orchestration/service.go:244-259`，`internal/sidecar/orch/orchestration/events.go:13-23`。
- `AgentLaunched/Stopped/Recovering/Failed` 也都是 orchestration 在生命周期关键点直接发布，不依赖状态机回调。证据：`internal/sidecar/orch/orchestration/events.go:25-64`，`internal/sidecar/orch/orchestration/service.go:115-126`，`internal/sidecar/orch/orchestration/service.go:233-234`，`internal/sidecar/orch/orchestration/service.go:327-338`，`internal/sidecar/orch/orchestration/recover.go:35-39`。
- “事件 -> 状态变化” 这条链路基本不存在。全仓唯一 `agent.sm.FireCtx(...)` 调用在 `fireAndPublishLocked()` 内部，调用方全部是 orchestration 自己的方法，不是 typed event subscriber。证据：`internal/sidecar/orch/orchestration/service.go:244-252`，`internal/sidecar/orch/orchestration/helpers.go:95-121`，`internal/sidecar/orch/orchestration/recover.go:54-56`。
- provider raw event 已经能进入 typed bus：session 把 raw event 送进 `EventDispatcher.Dispatch()`，translator 把 raw event 转成 `agent/turn/tool` typed event，再由 unified dispatcher `event.Publish()` 到 bus。证据：`internal/provider/claudecli/session.go:364-380`，`internal/provider/codexapp/session.go:221-235`，`internal/provider/unified/event_map.go:43-66`，`internal/provider/claudecli/module.go:21-26`，`internal/provider/codexapp/module.go:21-26`。
- 但 turn 完成事件没有驱动 orchestration。`claudecli` 把 `turn:complete` 翻译成 `turndto.TurnCompleted`，`codexapp` 把 `turn/completed`、`turn/aborted` 翻译成 `turndto.TurnCompleted`，却没有任何 subscriber 去调用 `orchestration.CompleteTurn()`；该方法在仓内无引用。证据：`internal/provider/claudecli/event_map.go:76-81`，`internal/provider/codexapp/event_map.go:80-85`，`internal/sidecar/orch/orchestration/service.go:288-307`。LSP `references` 对 `CompleteTurn` 返回 0。
- approval/user-input 事件也没有驱动 `awaiting_user_input`。approval manager 只发布 `ToolApprovalRequested/Resolved`，但没有任何代码把这些事件映射到 `TriggerUserInputRequested/Resolved`。证据：`internal/platform/rpc/approval_events.go:15-35`，`internal/platform/rpc/approval.go:71-96`，`internal/platform/rpc/approval.go:230-257`，`internal/dto/agent/state.go:28-29`，`internal/dto/agent/state.go:90`，`internal/dto/agent/state.go:93`。
- RPC push 侧也没有接上 typed bus。`BindEventToNotify()` 已经实现了 typed event -> jrpc2 notify 的桥，但未被任何模块引用。证据：`internal/platform/rpc/push.go:50-63`。LSP `references` 对 `BindEventToNotify` 返回 0。
- 同一 `agentdto.StateChanged` 现在有双源：orchestration 自己在状态机路径里发布一次，codex translator 还能把外部 `thread/status/changed` 再翻译发布一次；当前没有归一化层。证据：`internal/sidecar/orch/orchestration/events.go:13-23`，`internal/provider/codexapp/event_map.go:47-53`，`internal/provider/unified/event_map.go:43-66`。

## 结论

### Blocker

- `awaiting_user_input` 是死状态。触发器 `TriggerUserInputRequested/Resolved` 只有定义没有运行时触发；approval/user-input 事件也没有映射回状态机。证据：`internal/dto/agent/state.go:28-29`，`internal/dto/agent/state.go:90`，`internal/dto/agent/state.go:93`，`internal/platform/rpc/approval_events.go:15-35`。
- `TurnCompleted/TurnAborted` 没有业务入口接到 orchestration。provider 已能发布 `turndto.TurnCompleted`，但 `orchestration.CompleteTurn()` 无调用者，因此 turn 生命周期不会驱动 agent 状态机收口。证据：`internal/provider/claudecli/event_map.go:76-81`，`internal/provider/codexapp/event_map.go:80-85`，`internal/sidecar/orch/orchestration/service.go:288-307`。
- 声明式状态表不是运行时权威；`fireOrForceLocked()` 会在非法转换时静默强制改状态，已知至少覆盖 `stopped -> launch_failed`、`awaiting_user_input -> turn_completed`、任意态 `recover_requested`。证据：`internal/sidecar/orch/orchestration/service.go:237-259`，`internal/sidecar/orch/orchestration/helpers.go:49-59`，`internal/sidecar/orch/orchestration/helpers.go:103-109`，`internal/sidecar/orch/orchestration/recover.go:27-41`，`internal/dto/agent/state.go:78-102`。

### Warning

- EventHeader 体系并不满足“9 层、零重复字段”的字面目标：当前是 12 个 header struct，且存在跨分支字段名重复。证据：`internal/dto/shared/event.go:40-113`。
- typed bus 的运行时消费者基本只有 `LogSink`；`BindEventToNotify` 未接线，状态机也不消费 bus 事件。证据：`internal/platform/bus/sink.go:21-85`，`internal/platform/rpc/push.go:50-63`，`internal/sidecar/orch/orchestration/service.go:244-252`。
- V2 的 `command_card/prompt/skill/lsp/lock` 等事件族未迁移到 V3 typed bus；V3 只保留六族 typed event。证据：`go-agent-v2/internal/bus/bus.go:41-69`，`internal/dto/shared/event.go:5-37`。
- `agentdto.StateChanged` 存在双源发布，后续一旦出现业务 subscriber，事件语义可能冲突。证据：`internal/sidecar/orch/orchestration/events.go:13-23`，`internal/provider/codexapp/event_map.go:47-53`。

### OK

- `kelindar/event` 的 V3 包装层机械实现是正确的：typed publish/subscribe、panic-safe subscribe、projector、subscription cleanup 语义都自洽。证据：`internal/platform/bus/bus.go:5-22`，`internal/platform/bus/router.go:9-37`，`internal/platform/bus/resilient.go:10-29`，`internal/platform/bus/projection.go:10-43`，`internal/platform/bus/typed.go:9-30`。
- 10 状态、11 触发器、23 转换的声明集本身完整，且 `buildStatesFromDefinitions()` 到 `stateless.NewStateMachineWithExternalStorage()` 的装配链条是正确的。证据：`internal/dto/agent/state.go:8-102`，`internal/sidecar/orch/orchestration/helpers.go:15-30`，`internal/sidecar/orch/orchestration/helpers.go:61-73`，`internal/platform/statemachine/factory.go:28-68`。
- 已发布的 typed event 不存在“完全无人订阅”的硬孤儿，因为 `LogSink` 覆盖了全部六族。证据：`internal/platform/bus/sink.go:43-85`。

## 互辩：批判其他 4 份报告

### 对 audit-fx-rpc 的批判

1. `docs/plans/迁移/audit-fx-rpc.md:158-167` 把 blocker 几乎全压在“方法数量差距”和“占位 handler”上，但漏掉了更直接影响 jrpc2 可用性的主链路断点：`rpc.Module` 虽然提供了 `NewPushBridge`（`internal/platform/rpc/module.go:13-21`），真正负责把 bus 事件推给客户端的只有 `BindEventToNotify`（`internal/platform/rpc/push.go:50-63`），而 `PushBridge` 的运行时使用面只在审批回调 `ApprovalManager.RequestApproval/RestorePending`（`internal/platform/rpc/approval.go:71-96`，`internal/platform/rpc/approval_lifecycle.go:23-34`）。也就是说，通用“typed bus -> RPC push”根本没接上，这比“少 93 个方法”更靠近运行时主路径。
2. `docs/plans/迁移/audit-fx-rpc.md:43-48` 把 `runners` group 判成“完整”，结论过满。当前 run group 里一个 runner 是 orchestration actor，另一个只是 `*rpc.Server` 的监听循环（`internal/app/runner.go:26-37`，`internal/platform/rpc/server.go:37-58`）；server 本身不消费 event bus，也不建立任何 notify 订阅，而 bus->notify 的唯一 helper 仍是未接线的 `internal/platform/rpc/push.go:50-63`。所以“有两个 runner”不等于“RPC 运行时闭环”。
3. `docs/plans/迁移/audit-fx-rpc.md:17-18,169-172` 只是把 `platformrunner.Module`、`statemachine.Module` 记成空模块，没有把这件事上升为设计级问题。代码里这两个 fx module 的确都是空壳（`internal/platform/runner/module.go:1-5`，`internal/platform/statemachine/module.go:1-5`），而 orchestration 直接在 helper 里手工 `platformstatemachine.New(...)`（`internal/sidecar/orch/orchestration/helpers.go:61-73`）。这意味着状态机根本不是 fx 管理对象，DI 图“闭合”并不能说明状态机/事件桥接被装配对了。

### 对 audit-store-sqlc 的批判

1. `docs/plans/迁移/audit-store-sqlc.md:140` 把“`agent_codex_binding` / `AgentThreadBindingStore` 没有迁到 V3”表述成 blocker，过度绝对化了。V3 实际已经有 `binding.Store`，并在 `agent_provider_binding` 上提供 `GetByProviderThread`、`Upsert`、`UpdateSessionUUID`、`SetArchived`、`GetByAgentID`（`internal/store/binding/contract.go:7-14`，`internal/store/binding/store.go:18-80`，`sql/queries/agent_provider_binding.sql:1-31`）；thread 服务还显式走了 provider-thread fallback（`internal/module/thread/service.go:183-210`）。更准确的说法应是“旧表名/旧 repo 名未保留”，而不是“绑定能力未迁移”。
2. `docs/plans/迁移/audit-store-sqlc.md:142` 把 `DBQueryStore` placeholder 列为 blocker，但代码显示它连 app 图都没接进去。`dbquery.Module` 只是独立模块（`internal/store/dbquery/module.go:5-7`），顶层 `store.Module` 没有包含它（`internal/store/module.go:14-21`），`app.Module` 也只接 `store.Module`（`internal/app/modules.go:23-38`）；当前 `dbquery/store.go` 唯一实现也只是 `Placeholder()`（`internal/store/dbquery/store.go:13-25`）。这更像“未启用的草稿”，不是当前运行面的 blocker。
3. `docs/plans/迁移/audit-store-sqlc.md:155-156` 把 `taskdag` 能力放进 OK，证据不够。`store.taskdag` 确实有完整 contract/store（`internal/store/taskdag/contract.go:9-39`，`internal/store/taskdag/store.go:21-219`），但 `taskdag.Module` 没被纳入顶层 `store.Module`（`internal/store/taskdag/module.go:5-7`，`internal/store/module.go:14-21`），而 `taskdag.NewStore` 的 LSP references 只命中它自己的 `module.go:6`。这说明“代码存在”不等于“已接入应用图”。
4. `docs/plans/迁移/audit-store-sqlc.md:138-157` 完全没提 store 写路径与 typed event 的脱节，这比 schema/生成漂移更接近运行时一致性问题。workspace 服务只有 `storeworkspace.Store` 依赖（`internal/module/workspace/service.go:29-31`），创建/转状态/合并路径都只写 store（`internal/module/workspace/service.go:33-46`，`internal/module/workspace/service.go:95-147`）；但 V3 同时定义了 `WorkspaceRunCreated/WorkspaceRunStatusChanged/WorkspaceRunMerged` 事件并让 `LogSink` 订阅它们（`internal/dto/workspace/event.go:5-32`，`internal/platform/bus/sink.go:75-79`）。store 报告若不指出“持久化层和事件层没接上”，结论就不完整。

### 对 audit-provider 的批判

1. `docs/plans/迁移/audit-provider.md:80-84` 把 turn/provider 调用链描述为“闭合”，这个结论只在 turn 模块内部成立，不成立于系统级闭环。`watchTurn()` 在 `handle.Done()` 后只更新 `turnTracker`（`internal/module/turn/service.go:169-187`），而 orchestration 真正负责 agent 生命周期收口的 `CompleteTurn()` 仍然无人调用（`internal/sidecar/orch/orchestration/service.go:288-307`）。所以这条链最多是“provider -> turnTracker 闭合”，不是“provider -> orchestration/state machine 闭合”。
2. `docs/plans/迁移/audit-provider.md:94-98` 的 blocker 选得不够狠，漏掉了比 `SessionManager` 泄漏更严重的事件到状态机断链。provider translator 已经会发 `TurnCompleted` 和 approval 事件（`internal/provider/claudecli/event_map.go:76-81`，`internal/provider/codexapp/event_map.go:80-85`，`internal/provider/codexapp/event_map.go:132-144`），但 orchestration 全仓唯一 `FireCtx` 入口仍是内部 `fireAndPublishLocked()`（`internal/sidecar/orch/orchestration/service.go:244-252`）；`TriggerUserInputRequested/TriggerUserInputResolved` 也只存在定义与状态表中（`internal/dto/agent/state.go:28-29`，`internal/dto/agent/state.go:90`，`internal/dto/agent/state.go:93`）。这意味着 provider 已发出的 turn/approval 事件根本不会驱动 agent 状态机。
3. `docs/plans/迁移/audit-provider.md:38-42,86-90` 把注意力集中在“谁生产 event”，却漏掉了“生产出来之后谁消费”。当前 typed bus 的显式运行时消费者基本只有 `LogSink`（`internal/platform/bus/module.go:10-23`，`internal/platform/bus/sink.go:21-85`）；RPC push helper `BindEventToNotify` 未接线（`internal/platform/rpc/push.go:50-63`），orchestration 也没有订阅 provider 事件来驱动 `CompleteTurn()` 或 user-input trigger。只批判 producer ownership，而不批判 consumer 真空，会把 provider 统一层的问题看轻。

### 对 audit-module-v2-parity 的批判

1. `docs/plans/迁移/audit-module-v2-parity.md:8-10` 把口径限定为“注册覆盖率 + handler 功能等价率”，这天然漏掉了 V2 最关键的事件/状态迁移面。V2 有明确的 5 态状态矩阵，并且 `turn_started`、`turn_complete`、`turn_aborted`、`exec_approval_request` 等事件直接驱动态变化（`go-agent-v2/internal/guards/state_matrix_snapshot.json:2-8`，`go-agent-v2/internal/guards/state_matrix_snapshot.json:37-114`，`go-agent-v2/internal/guards/state_matrix_snapshot.json:438-569`）；V3 则是 10 状态/11 触发器的显式状态机（`internal/dto/agent/state.go:8-33`，`internal/dto/agent/state.go:78-102`），但 `awaiting_user_input` 相关触发器是死的，`CompleteTurn()` 也无人调用（`internal/sidecar/orch/orchestration/service.go:288-307`）。只看 RPC key，会高估“迁移完整性”。
2. `docs/plans/迁移/audit-module-v2-parity.md:298` 把审批闭环单列为 blocker，但问题其实更广。即便完全不走 approval，provider 发出的 `TurnCompleted` 也不会调用 `orchestration.CompleteTurn()`，所以 turn 完成到 agent 状态收口这一条基础链路同样断着（`internal/provider/claudecli/event_map.go:76-81`，`internal/provider/codexapp/event_map.go:80-85`，`internal/sidecar/orch/orchestration/service.go:288-307`）。把 blocker 收窄为 approval lifecycle，会低估非审批路径的普遍失真。
3. `docs/plans/迁移/audit-module-v2-parity.md:304-305,309-310` 说 skill/workspace 的缺口不是主要问题，这个判断至少对 workspace 过于宽松。workspace 模块只提供 `NewService/NewWorkspaceHandlers`（`internal/module/workspace/module.go:5-8`），service 本体也只有 store 依赖、只做 DB 状态迁移（`internal/module/workspace/service.go:29-31`，`internal/module/workspace/service.go:33-46`，`internal/module/workspace/service.go:95-147`）；但 V3 自己又定义了 `WorkspaceRunCreated/WorkspaceRunStatusChanged/WorkspaceRunMerged` 并让 `LogSink` 订阅（`internal/dto/workspace/event.go:5-32`，`internal/platform/bus/sink.go:75-79`）。如果这些事件从未发布，workspace 的迁移就不能算“次要缺口”。
