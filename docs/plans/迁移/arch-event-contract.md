# 架构合规：Event Bus 契约完整性

## 范围与方法

- 本次核对只使用 LSP 读文件与符号检索完成，未使用 `grep/find/cat/sed/awk`。
- 核对范围覆盖：
  - typed event 定义：`internal/dto/shared/event.go`、`internal/dto/{agent,turn,tool,task,workspace,ui}/event.go`
  - bus 装配与日志订阅：`internal/platform/bus/{module.go,sink.go,emitters.go,typed.go}`
  - 发布入口：`internal/sidecar/orch/orchestration/events.go`、`internal/provider/{unified,claudecli,codexapp}/event_map.go`、`internal/platform/rpc/approval_events.go`、`internal/module/workspace/{service.go,service_helpers.go}`
  - 订阅入口：`internal/sidecar/orch/orchestration/module.go`、`internal/platform/rpc/{module.go,push.go}`、`internal/ui/wails/{module.go,bridge.go}`。

## 总结

| 检查项 | 结论 | 说明 |
| --- | --- | --- |
| 1. 6 族 Emitter 是否全部被 `fx.Provide` | 通过 | `bus.Module` 明确提供 `Agent/Turn/Tool/Task/Workspace/UI` 6 族 emitter。 |
| 2. 发布→订阅对照 | 通过（针对当前已发布事件） | 当前能找到发布源的 19 个事件类型都至少有 1 个订阅者；最小消费者集是 `LogSink`。 |
| 3. 孤儿事件 | 不通过 | 零订阅事件为 0；但零发布事件为 9 个。 |
| 4. `EventHeader` 嵌入 | 通过（按嵌入链） | 当前 header 嵌入链内没有重复字段声明；但这是分叉树，不是单一线性层级。 |
| 5. `LogSink` 覆盖 | 通过 | `LogSink` 订阅了 6 族全部 28 个已定义事件。 |
| 6. bus lifecycle | 部分通过 | `orchestration/rpc/wails` 订阅均在 `OnStart` 建立并在 `OnStop` 释放；`LogSink` 在构造阶段即订阅，不满足“OnStart 启动订阅”的字面要求。 |

## 1. 6 族 Emitter 是否全部被 `fx.Provide`

`internal/platform/bus/module.go:10-23` 的 `fx.Provide(...)` 已完整暴露：

- `NewAgentEmitters`
- `NewTurnEmitters`
- `NewToolEmitters`
- `NewTaskEmitters`
- `NewWorkspaceEmitters`
- `NewUIEmitters`

`internal/app/modules.go:23-44` 将 `bus.Module` 纳入应用总装配，因此这 6 族 emitter 都会进入 Fx 容器。

补充观察：

- `WorkspaceEmitters` 已被 `internal/module/workspace/module.go:9-14` / `internal/module/workspace/service.go:49-59` 注入使用。
- 其余 5 族 emitter 当前仍停留在“已提供但未被业务模块显式注入”的状态；生产代码更常见的是直接持有 `*event.Dispatcher` 并 `Publish/Subscribe`。

## 2. 发布入口

当前生产发布面可以收敛为 4 组：

1. `orchestration` 主动发布 agent 族事件  
   证据：`internal/sidecar/orch/orchestration/events.go:13-64`

2. provider translator 返回 typed DTO，再由 unified dispatcher 统一 `event.Publish`  
   证据：
   - `internal/provider/unified/event_map.go:42-64`
   - `internal/provider/claudecli/event_map.go:21-103`
   - `internal/provider/codexapp/event_map.go:24-154`

3. `ApprovalManager` 主动发布 tool approval 事件  
   证据：`internal/platform/rpc/approval_events.go:23-43`

4. `workspace.Service` 通过 `bus.NewEmitter[T]` 发布 workspace 族事件  
   证据：
   - `internal/module/workspace/service.go:49-59`
   - `internal/module/workspace/service_helpers.go:220-284`

未找到 task 族与 ui 族的生产发布入口。

## 3. 订阅入口

当前生产订阅面可以收敛为 4 组：

1. `LogSink` 订阅全部已知 typed event 并写日志  
   证据：`internal/platform/bus/sink.go:21-99`

2. `orchestration` 在 lifecycle 中订阅 `TurnStarted` / `TurnCompleted`  
   证据：`internal/sidecar/orch/orchestration/module.go:25-50`

3. `rpc` 在 lifecycle 中订阅 `StateChanged` / `TurnStarted` / `TurnCompleted`  
   证据：
   - `internal/platform/rpc/module.go:54-72`
   - `internal/platform/rpc/push.go:75-92`

4. `wails` 在 lifecycle 中订阅 `StateChanged` / `TurnStarted` / `TurnCompleted`  
   证据：
   - `internal/ui/wails/module.go:120-134`
   - `internal/ui/wails/bridge.go:42-79`

生产代码未发现 `TypedEmitter[T].On(...)` 的使用；`.On(...)` 仅出现在 `internal/platform/bus/typed_test.go`。

## 4. 发布→订阅对照

结论：当前所有“确实被发布”的事件类型，都有至少一个 `Subscribe/On` 消费者。

| 事件类型 | 发布源 | 订阅方 | 结果 |
| --- | --- | --- | --- |
| `agent.StateChanged` | `orchestration`、`codexapp translator -> unified dispatcher` | `LogSink`、`rpc`、`wails` | 通过 |
| `agent.AgentLaunched` | `orchestration`、`claude/codex translator -> unified dispatcher` | `LogSink` | 通过 |
| `agent.AgentStopped` | `orchestration`、`claude/codex translator -> unified dispatcher` | `LogSink` | 通过 |
| `agent.AgentRecovering` | `orchestration`、`codex translator -> unified dispatcher` | `LogSink` | 通过 |
| `agent.AgentFailed` | `orchestration`、`claude/codex translator -> unified dispatcher` | `LogSink` | 通过 |
| `turn.TurnStarted` | `claude/codex translator -> unified dispatcher` | `LogSink`、`orchestration`、`rpc`、`wails` | 通过 |
| `turn.TurnCompleted` | `claude/codex translator -> unified dispatcher` | `LogSink`、`orchestration`、`rpc`、`wails` | 通过 |
| `turn.TurnInterrupted` | `claude/codex translator -> unified dispatcher` | `LogSink` | 通过 |
| `turn.TurnInputReceived` | `claude translator -> unified dispatcher` | `LogSink` | 通过 |
| `turn.TurnOutputDelta` | `claude/codex translator -> unified dispatcher` | `LogSink` | 通过 |
| `tool.ToolCallBegin` | `claude/codex translator -> unified dispatcher` | `LogSink` | 通过 |
| `tool.ToolCallEnd` | `claude/codex translator -> unified dispatcher` | `LogSink` | 通过 |
| `tool.ToolApprovalRequested` | `ApprovalManager`、`codex translator -> unified dispatcher` | `LogSink` | 通过 |
| `tool.ToolApprovalResolved` | `ApprovalManager`、`codex translator -> unified dispatcher` | `LogSink` | 通过 |
| `workspace.WorkspaceRunCreated` | `workspace.Service` | `LogSink` | 通过 |
| `workspace.WorkspaceRunStatusChanged` | `workspace.Service` | `LogSink` | 通过 |
| `workspace.WorkspaceRunMerged` | `workspace.Service` | `LogSink` | 通过 |
| `workspace.WorkspaceRunAborted` | `workspace.Service` | `LogSink` | 通过 |
| `workspace.WorkspaceRunMergeError` | `workspace.Service` | `LogSink` | 通过 |

补充判断：

- 这里的“通过”依赖 `LogSink` 作为兜底消费者，因此不代表这些事件已经具备业务级投影、持久化或 UI 消费链。
- 除 `StateChanged`、`TurnStarted`、`TurnCompleted` 外，其余已发布事件当前都只被 `LogSink` 消费。

## 5. 孤儿事件

### 5.1 零发布事件

以下事件类型已定义，但未找到任何生产发布入口：

- `turn.TurnStalled`
- `turn.TurnResumed`
- `task.TaskDagCreated`
- `task.TaskNodeStatusChanged`
- `task.TaskWakeupDispatched`
- `task.TaskWakeupCompleted`
- `ui.UIProjectionUpdated`
- `ui.UITimelineAppended`
- `ui.UITokensUpdated`

对应证据模式一致：

- 定义位于 `internal/dto/{turn,task,ui}/event.go`
- `LogSink` 在 `internal/platform/bus/sink.go:51-87` 中订阅了它们
- 但在生产发布入口中找不到对应 `event.Publish(...)`、translator `return XxxEvent{...}` 或 typed emitter 调用

### 5.2 零订阅事件

未发现零订阅事件。

原因：

- `internal/platform/bus/sink.go:21-99` 已显式订阅 agent 5 个、turn 7 个、tool 4 个、task 4 个、workspace 5 个、ui 3 个事件，总计 28 个，与 `internal/dto/shared/event.go:5-38` 的 event type 常量集合一致。

## 6. `EventHeader` 嵌入与重复字段

`internal/dto/shared/event.go:41-127` 的 header 体系可以拆成以下嵌入链：

- agent session 链：`EventHeader -> ThreadHeader -> AgentHeader -> AgentSessionHeader`
- turn 链：`EventHeader -> ThreadHeader -> AgentHeader -> TurnIDHeader -> TurnHeader`
- tool approval 链：`EventHeader -> ThreadHeader -> AgentHeader -> TurnIDHeader -> TurnHeader -> ToolCallHeader -> ToolApprovalHeader`
- task wakeup 链：`EventHeader -> DAGHeader -> TaskDAGHeader -> TaskNodeHeader -> TaskWakeupHeader`
- workspace 链：`EventHeader -> DAGHeader -> WorkspaceRunHeader`
- ui turn 链：`EventHeader -> ThreadHeader -> UIProjectionHeader -> UITurnHeader`，其中 `UITurnHeader` 额外嵌入 `TurnIDHeader`

结论：

- 以“结构体字段声明”口径看，当前各嵌入链内是零重复字段。
- `Timestamp` 只在 `EventHeader` 声明一次。
- `ThreadID`、`TurnID`、`DagKey` 被抽到 `ThreadHeader`、`TurnIDHeader`、`DAGHeader` 这类复用 header 中，子结构没有再次声明同名字段。
- 但该体系是分叉树，不是单一线性层级；如果目标是“所有域共用一条线性 header 链”，当前实现仍不是那种形态。

## 7. `LogSink` 覆盖

`internal/platform/bus/sink.go:43-87` 对 6 族事件的覆盖是完整的：

- agent：5 个
- turn：7 个
- tool：4 个
- task：4 个
- workspace：5 个
- ui：3 个

与 `internal/dto/shared/event.go:5-38` 中的 event type 常量数量完全对齐，因此“所有事件族是否都被 `LogSink` 记录”这一项是通过的。

## 8. bus lifecycle

### 8.1 合规部分

以下订阅都满足“`OnStart` 建立 / `OnStop` 释放”：

- `orchestration`  
  `internal/sidecar/orch/orchestration/module.go:31-49`

- `rpc`  
  `internal/platform/rpc/module.go:57-70`

- `wails`  
  `internal/ui/wails/module.go:124-133`

此外，dispatcher 本身会在 bus module 的 `OnStop` 中关闭：`internal/platform/bus/module.go:25-35`。

### 8.2 不合规部分

`LogSink` 不满足“`OnStart` 才启动订阅”：

- `internal/platform/bus/sink.go:21-32` 的 `NewLogSink(...)` 在构造阶段立即执行 `bindAgent/bindTurn/bindTool/bindTask/bindWorkspace/bindUI`
- 这些 `bind*` 最终在 `logEvent[T]` 中直接调用 `event.Subscribe(...)`：`internal/platform/bus/sink.go:89-99`
- `internal/platform/bus/module.go:25-35` 只在 `OnStop` 执行 `sink.Close()`，没有对应的 `OnStart` 启动动作

因此 lifecycle 结论应为：

- 订阅释放路径基本完备
- 订阅启动路径并不统一
- `LogSink` 是当前总线生命周期契约里的主要偏差点

## 9. 结论

当前 Event Bus 契约的主要状态是：

- “6 族 emitter 已通过 Fx 暴露”成立
- “所有已发布事件至少有 1 个订阅者”成立
- “零订阅事件”不存在
- “零发布事件”存在 9 个，集中在 `turn/task/ui`
- `EventHeader` 嵌入链按字段声明口径没有重复
- `LogSink` 覆盖完整
- lifecycle 仍有一个明确缺口：`LogSink` 需要从“构造即订阅”收口到“`OnStart` 订阅，`OnStop` 退订”

如果要把这份审计结论转成整改项，优先级建议是：

1. 先改 `LogSink` lifecycle，使订阅行为完全受 Fx 生命周期管理。
2. 再决定 `turn/task/ui` 这 9 个零发布事件是补发布链路，还是删除死定义。
3. 最后再决定是否要继续推进“统一用 typed emitter 注入，而不是直接裸持有 `*event.Dispatcher`”。
