# 审查：Provider 统一层

## 1. Contract

### 1.1 接口完整性

- `Driver` / `Session` / `TurnHandle` 合同本身是闭合的，且 claudecli、codexapp 都实现了 `Driver` 与 `Session`。`Driver` 定义见 `internal/contract/provider.go:10-14`；`Session` 定义见 `internal/contract/provider.go:23-37`；`TurnHandle` 定义见 `internal/contract/provider.go:40-45`。语义实现定位到 `internal/provider/claudecli/driver.go:19-131`、`internal/provider/codexapp/driver.go:17-186`、`internal/provider/claudecli/session.go:19-393`、`internal/provider/codexapp/session.go:17-338`。
- V3 contract 与 V2 的真实对等面是 `go-agent-v2/legacy-agentsdk/agentcore.Client`，而不是仓内不存在的 `go-agent-v2/internal/provider/*`。V2 `Client` 提供 `SpawnAndConnect` / `Submit` / `SendCommand` / `SendDynamicToolResult` / `RespondError` / `ListThreads` / `ResumeThread` / `ForkThread` / `Shutdown` / `Kill` / `Running`，见 `go-agent-v2/legacy-agentsdk/agentcore/client.go:7-20`。V3 将启动/恢复下沉到 `Driver`，将线程内行为收敛到 `Session`，并新增异步 `TurnHandle`，见 `internal/contract/provider.go:10-14`、`internal/contract/provider.go:23-45`。
- `ToolCallResponder` 是未接线合同。声明仅出现在 `internal/contract/provider.go:47-50`；语义实现查询返回 0 个实现；全仓引用仅有该声明本身。与之相对，V2 `agentcore.Client` 明确暴露了 `SendDynamicToolResult` 与 `RespondError`，见 `go-agent-v2/legacy-agentsdk/agentcore/client.go:13-14`；Claude V2 有 `SendDynamicToolResult` / `RespondError`，见 `go-agent-v2/legacy-agentsdk/claude/client.go:363-393`；Codex V2 会在事件对象上挂 `RespondFunc` / `RespondResultFunc`，见 `go-agent-v2/legacy-agentsdk/codex/client_appserver_events.go:265-274`，并由 `SendDynamicToolResult` 落到 JSON-RPC 响应/通知，见 `go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go:326-349`。结论：动态 tool result / error 返回链在 V3 Provider 统一层中缺失。

### 1.2 DTO 类型

- `internal/dto/provider/` 实际是 8 个文件，不是题面中的 7 个：`capability.go`、`event.go`、`manifest.go`、`message.go`、`session.go`、`thread.go`、`thread_config.go`、`turn.go`，定义分别见 `internal/dto/provider/*.go`。
- `CapabilityError` 的类型化本身是正确的：它是具名结构体指针并通过 `NewCapabilityError` 返回 `error`，见 `internal/dto/provider/capability.go:17-28`。但它的生产使用面很窄，目前只在 Claude 的 `ListThreads` / `ForkThread` 上实例化，见 `internal/provider/claudecli/session.go:229-235`。RPC capability gate 走的是通用 JRPC 错误，不会返回 `CapabilityError`，见 `internal/platform/rpc/handler.go:70-82`。结论：类型定义正确，但没有贯穿主调用路径。
- `InputItem` 统一到 `dto/shared` 是完整的。Provider DTO 直接别名到 shared DTO，见 `internal/dto/provider/turn.go:24`；共享结构字段是 `Type` / `Content` / `Path` / `Name` / `URL`，见 `internal/dto/shared/input.go:3-9`；V2 对等结构 `agentcore.TurnInput` 字段也是 `Type` / `Text` / `URL` / `Path` / `Name` / `Content`，见 `go-agent-v2/legacy-agentsdk/agentcore/types.go:20-22`。结论：字段面未丢失。

## 2. unified 层

### 2.1 Registry + Client

- `fx group:"drivers"` 收集链路正确。统一层消费组定义在 `internal/provider/unified/module.go:11-27`，`RegistryParams.Drivers` 带 `group:"drivers"`，见 `internal/provider/unified/module.go:14`；Claude/Codex driver factory 都用 `fx.Annotate(..., fx.ResultTags(\`group:"drivers"\`))` 输出，见 `internal/provider/claudecli/module.go:12-26`、`internal/provider/codexapp/module.go:12-26`。
- `Registry` 会规范化 provider 名称并延迟构造 driver，逻辑完整，见 `internal/provider/unified/registry.go:15-56`。
- `Client` 在成功 `StartSession` / `ResumeSession` 后注册 session，见 `internal/provider/unified/client.go:29-67`；`SessionManager.Register` 的唯一入边调用也只来自 `Client.open`，语义调用层次见 `internal/provider/unified/client.go:63-65` 与 `internal/provider/unified/session.go:30-46`。

### 2.2 SessionManager lifecycle

- `SessionManager` 的“创建 -> 使用 -> Remove”闭环不成立。创建路径只有 `Client.open -> SessionManager.Register`，见 `internal/provider/unified/client.go:47-67`、`internal/provider/unified/session.go:30-46`；`Remove` 的入边只有 `sessionCleanerAdapter.RemoveSession`，再向上只有 orchestration 的 `removeSession`，见 `internal/provider/unified/session.go:59-67`、`internal/provider/unified/session_adapter.go:29-34`、`internal/sidecar/orch/orchestration/service.go:80-84`。
- orchestration 确实会在 `StopAgent`、`StopAllAgents` 和进程退出时调用 `RemoveSession`，见 `internal/sidecar/orch/orchestration/service.go:103-117`、`internal/sidecar/orch/orchestration/service.go:119-129`、`internal/sidecar/orch/orchestration/service.go:309-323`。
- 但 thread 删除路径只做 `session.Close(ctx)`，不做 `RemoveSession`，也不做 `StopAgent`。删除入口见 `internal/module/thread/service.go:102-119`；其内部 `closeSessionIfActive` 只查 session 并 `Close`，见 `internal/module/thread/service.go:228-240`。由于 `SessionManager` 自身不会在 `Close` 后剔除条目，已关闭 session 会残留在 map 中。结论：这是明确的 lifecycle 漏洞。
- `CloseAll` 目前是死代码。定义在 `internal/provider/unified/session.go:69-83`，语义引用查询只有定义本身，没有任何调用点。结论：统一层没有全局 session 收口点。

### 2.3 SessionResolver 使用面

- 所有 thread-scoped RPC session 解析都通过 `SessionResolver`。接口定义见 `internal/contract/session_resolver.go:5-7`；实现见 `internal/provider/unified/session_resolver.go:12-46`；调用面只有 turn RPC 与 capability resolver，见 `internal/module/turn/rpc.go:20-29`、`internal/platform/rpc/handler.go:20-30`。
- thread 模块没有用 `SessionResolver`，而是使用更窄的 `SessionProvider.GetSession(agentID)`，这是按 agent 维度查找 session 的有意设计，见 `internal/module/thread/service.go:21-24`、`internal/module/thread/lifecycle.go:231-236`。这一点本身没有问题。
- 但 `SessionResolver` 只依赖 `threadStore.GetByThreadID`，见 `internal/provider/unified/session_resolver.go:34-45`；而 thread 模块自身的绑定解析还支持 `lookupThreadAgent` 缓存与 `bindingStore.GetByProviderThread` 回退，见 `internal/module/thread/service.go:183-210`。结论：统一层 resolver 只接受 canonical thread id，别名/provider thread id 的兼容能力弱于 thread 模块。

### 2.4 EventDispatcher

- `EventDispatcher` 本身只是“把原始事件广播给所有 translator”，没有 provider 级隔离，见 `internal/provider/unified/event_map.go:12-66`。当前之所以能工作，依赖的是 Claude 使用 `agent:` / `turn:` 命名，Codex 使用 `thread/` / `turn/` 命名，原始事件名大多不冲突，见 `internal/provider/claudecli/event_map.go:35-103`、`internal/provider/codexapp/event_map.go:39-148`。
- 事件归属存在实质性越界：provider translator 不仅发 turn/tool 级事件，还发 agent 级事件。Claude translator 会发 `AgentLaunched` / `AgentStopped` / `AgentFailed`，见 `internal/provider/claudecli/event_map.go:35-53`；Codex translator 会发 `AgentLaunched` / `StateChanged` / `AgentStopped` / `AgentRecovering` / `AgentFailed`，见 `internal/provider/codexapp/event_map.go:39-74`。与此同时 orchestration 也会发同一组 agent 级事件，见 `internal/sidecar/orch/orchestration/events.go:13-64`。结论：`orchestration 发 agent 级、provider translator 发 turn 级` 的边界当前没有被遵守。

## 3. claudecli Driver

### 3.1 接口实现

- `Driver` 实现完整：`Name` / `StartSession` / `ResumeSession` 均存在，见 `internal/provider/claudecli/driver.go:51-131`。
- `Session` 实现完整：`ThreadID` / `Capabilities` / `StartTurn` / `Interrupt` / `ListThreads` / `ForkThread` / `ReadHistory` / `Configure` / `Close` / `ForceStop` 全部存在，见 `internal/provider/claudecli/session.go:77-393`、`internal/provider/claudecli/session_history.go:12-57`。
- `TurnHandle` 实现完整：`LocalID` / `ProviderID` / `Done` / `Err`，见 `internal/provider/claudecli/session.go:39-75`。
- `ReadHistory` 基本链路正确：session 调 backend 读 JSONL，再按 `limit` 截断并转成 provider DTO，见 `internal/provider/claudecli/session_history.go:12-45`；backend 会按 `~/.claude/projects/*/<thread>.jsonl` 查找并逐行解析，见 `internal/provider/claudecli/history.go:18-68`。
- Claude 历史回放丢失附件元数据。当前 parser 只保留纯文本并删除注入的附件提示，见 `internal/provider/claudecli/history.go:84-100`、`internal/provider/claudecli/history.go:113-134`；`toProviderHistory` 也没有回填 `Metadata`，见 `internal/provider/claudecli/session_history.go:35-43`。V2 会把 `[Image: ...]` / `[File: ...]` 还原成 `metadata.input`，见 `go-agent-v2/legacy-agentsdk/claude/history_backend.go:164-242`。结论：Claude `ReadHistory` 的内容保真度低于 V2。

### 3.2 V2 对照

- 启动/停止 lifecycle 与 V2 concrete CLI client 基本同构：V3 启动通过 `launchCLI` + `transport`，见 `internal/provider/claudecli/driver.go:86-130`、`internal/provider/claudecli/transport_config.go:23-48`；停止通过 `Close` / `ForceStop` 最终走 `transport.Close` / `Kill`，见 `internal/provider/claudecli/session.go:258-300`、`internal/provider/claudecli/transport.go:98-197`。V2 对应是 `spawnWithResume` / `Shutdown` / `Kill`，见 `go-agent-v2/legacy-agentsdk/claude/client.go:176-239`、`go-agent-v2/legacy-agentsdk/claude/client.go:414-466`。
- turn 输入拼装基本延续 V2 “把文件/图片提示注入文本”的做法。V3 用 `buildTurnText` / `appendTurnInput` 拼提示，见 `internal/provider/claudecli/session.go:162-203`；V2 用 `injectFileHints`，见 `go-agent-v2/legacy-agentsdk/claude/client.go:241-305`。
- Claude 的 thread list / fork 能力在 V2 内部本身就不一致。V2 capability matrix 声称 `thread.fork` / `thread.list` 为 true，见 `go-agent-v2/legacy-agentsdk/claude/capabilities.go:16-20`、`go-agent-v2/legacy-agentsdk/claude/capabilities.go:49-53`；但 concrete `CLIClient` 的 `ListThreads` / `ResumeThread` / `ForkThread` 实际返回 unsupported，见 `go-agent-v2/legacy-agentsdk/claude/client.go:395-405`。V3 Claude 也把 `ListThreads` / `ForkThread` 作为 capability error 返回，见 `internal/provider/claudecli/session.go:229-235`。结论：V3 与 V2 concrete client 一致，但与 V2 capability 声明不一致。
- V2 有动态 tool result / error 返回能力，V3 Claude 没有。V2 见 `go-agent-v2/legacy-agentsdk/claude/client.go:363-393`；V3 只有 tool 事件翻译，没有 responder 接口实现，见 `internal/provider/claudecli/event_map.go:87-103`、`internal/contract/provider.go:47-50`。

## 4. codexapp Driver

### 4.1 接口实现

- `Driver` 实现完整：`Name` / `StartSession` / `ResumeSession` 均存在，见 `internal/provider/codexapp/driver.go:73-109`。
- `Session` 实现完整：`ThreadID` / `Capabilities` / `StartTurn` / `Interrupt` / `ListThreads` / `ForkThread` / `ReadHistory` / `Configure` / `Close` / `ForceStop` 全部存在，见 `internal/provider/codexapp/session.go:90-337`、`internal/provider/codexapp/session_history.go:13-62`。
- `TurnHandle` 实现完整：`LocalID` / `ProviderID` / `Done` / `Err`，见 `internal/provider/codexapp/session.go:34-41`、`internal/provider/codexapp/session.go:279-304`。
- `ReadHistory` 有本地 rollout + RPC 回退双路径，见 `internal/provider/codexapp/history.go:19-39`。但本地 rollout parser 只保留 `Role` / `Content` / `Timestamp`，不会提取用户图像 metadata，见 `internal/provider/codexapp/history_rollout.go:52-72`；而 `session_history` 只有在 `Message.Metadata` 已存在时才解码 metadata，见 `internal/provider/codexapp/session_history.go:28-50`。V2 rollout reader 会从 `input_image` 中抽取 `metadata.input`，见 `go-agent-v2/legacy-agentsdk/codex/rollout_reader.go:216-239`。结论：Codex 本地历史回放同样低于 V2。
- 中断完成链存在缺口。session 只在收到 `turn/completed` / `turn/aborted` 时 finish handle，见 `internal/provider/codexapp/session.go:221-259`；但 translator 明确把 `turn/interrupted` 视为 turn 终态事件，见 `internal/provider/codexapp/event_map.go:76-90`。结论：如果后端只发 `turn/interrupted` 而不再补 `turn/aborted` / `turn/completed`，`TurnHandle.Done()` 将悬空。
- `recoveryManager` 已声明并注入到 session，但没有任何调用点。定义见 `internal/provider/codexapp/recovery.go:12-44`；session 仅在结构体字段和构造时持有，见 `internal/provider/codexapp/session.go:22`、`internal/provider/codexapp/session.go:78`。结论：恢复逻辑尚未真正接线。

### 4.2 V2 对照

- 启动/恢复主流程与 V2 大体对等：V3 是 `newSession -> initializeSession -> thread/start|thread/resume`，见 `internal/provider/codexapp/driver.go:75-154`；V2 是 `spawnAndInitialize -> ThreadStart|ResumeThread`，见 `go-agent-v2/legacy-agentsdk/codex/client_appserver_runtime.go:103-200`、`go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go:102-149`、`go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go:371-424`。
- 关闭/杀死流程也基本对等：V3 `Close` / `ForceStop` 最终到 transport `Close` / `Kill`，见 `internal/provider/codexapp/session.go:195-205`、`internal/provider/codexapp/transport.go:121-152`；V2 对应 `Shutdown` / `Kill`，见 `go-agent-v2/legacy-agentsdk/codex/client_appserver_runtime.go:202-286`。
- V3 的 thread list / fork 面比 V2 更强。V3 会真正调用 `thread/list` 与 `thread/fork`，见 `internal/provider/codexapp/session.go:132-174`；V2 `AppServerClient.ListThreads` 只返回当前 thread，`ForkThread` 直接 unsupported，见 `go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go:351-356`、`go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go:426-428`。
- 但 V3 丢了 V2 的 request-scoped 动态 tool 响应能力与更完整的连接恢复。V2 在收到服务端 request 时会挂 `RequestID`、`RespondFunc`、`RespondResultFunc`，见 `go-agent-v2/legacy-agentsdk/codex/client_appserver_events.go:265-274`；当前 V3 只有 `ToolCallBegin` / `ToolCallEnd` / approval 事件翻译，见 `internal/provider/codexapp/event_map.go:114-148`，没有任何 responder 实现，见 `internal/contract/provider.go:47-50`。V2 还包含 ping/reconnect/respawn/runtime 逻辑，见 `go-agent-v2/legacy-agentsdk/codex/client_appserver_runtime.go:17-127`，而 V3 的 `recoveryManager` 未接线，见 `internal/provider/codexapp/recovery.go:12-44`、`internal/provider/codexapp/session.go:22`、`internal/provider/codexapp/session.go:78`。

## 5. Turn 集成

### 5.1 Provider 调用链

- turn RPC 的 provider 调用链是闭合的：`NewTurnHandlers` 先用 `SessionResolver` 从 `threadId` 解析 session，再调用 `Service.PrepareTurn` / `StartTurn` / `InterruptTurn` / `ForceCompleteTurn`，见 `internal/module/turn/rpc.go:14-93`。
- `PrepareTurn` 会组装输入、解析 skills、按 capability 构造 overrides、生成 MCP manifest，见 `internal/module/turn/service.go:37-59`。输入组装在 `internal/module/turn/assembler.go:47-189`；skill 解析在 `internal/module/turn/skills.go:11-93`；manifest 构造在 `internal/module/turn/manifest.go:7-14`；capability gating 的 override 逻辑在 `internal/module/turn/service.go:242-253`。
- `StartTurn` 先把 local turn 写入 tracker，再调用 provider `Session.StartTurn`，随后绑定 `ProviderID` 并以 `handle.Done()` 观察完成，见 `internal/module/turn/service.go:61-92`、`internal/module/turn/service.go:158-205`、`internal/module/turn/tracker.go:34-119`。

### 5.2 事件归属

- agent 级事件已经由 orchestration 发布：`StateChanged` / `AgentLaunched` / `AgentStopped` / `AgentRecovering` / `AgentFailed` 都在 `internal/sidecar/orch/orchestration/events.go:13-64`。
- turn/tool 级事件也确实由 provider translator 负责翻译：Claude 见 `internal/provider/claudecli/event_map.go:55-103`，Codex 见 `internal/provider/codexapp/event_map.go:76-148`。
- 但 provider translator 同时还在发布 agent 级事件，见 `internal/provider/claudecli/event_map.go:35-53`、`internal/provider/codexapp/event_map.go:39-74`。结论：当前实现不是“orchestration 发 agent 级、provider translator 发 turn 级”的单一归属模型，而是双源发布。

## 结论

### Blocker

- `ToolCallResponder` 迁移未完成。V3 合同存在、实现为空、生产引用为空；V2 具备 `SendDynamicToolResult` / `RespondError` 与 request-scoped responder 绑定。证据：`internal/contract/provider.go:47-50`、`go-agent-v2/legacy-agentsdk/agentcore/client.go:13-14`、`go-agent-v2/legacy-agentsdk/codex/client_appserver_events.go:265-274`、`go-agent-v2/legacy-agentsdk/claude/client.go:363-393`。
- `SessionManager` lifecycle 不闭合。注册只在 `Client.open`，清理只经 orchestration，thread 删除只做 `session.Close()` 不做 `RemoveSession()`。证据：`internal/provider/unified/client.go:47-67`、`internal/provider/unified/session.go:30-67`、`internal/provider/unified/session_adapter.go:29-34`、`internal/module/thread/service.go:102-119`、`internal/module/thread/service.go:228-240`、`internal/sidecar/orch/orchestration/service.go:80-84`。
- agent 级事件归属冲突。orchestration 与 provider translator 同时发布 `AgentLaunched` / `AgentStopped` / `AgentFailed` / `AgentRecovering` / `StateChanged` 家族事件。证据：`internal/sidecar/orch/orchestration/events.go:13-64`、`internal/provider/claudecli/event_map.go:35-53`、`internal/provider/codexapp/event_map.go:39-74`。

### Warning

- `SessionResolver` 只支持 canonical thread id，弱于 thread 模块的 alias/provider-thread 回退解析。证据：`internal/provider/unified/session_resolver.go:34-45`、`internal/module/thread/service.go:183-210`。
- Claude `ReadHistory` 丢失附件 metadata。证据：`internal/provider/claudecli/history.go:84-134`、`internal/provider/claudecli/session_history.go:35-43`、`go-agent-v2/legacy-agentsdk/claude/history_backend.go:164-242`。
- Codex 本地 rollout `ReadHistory` 丢失用户图片 metadata。证据：`internal/provider/codexapp/history_rollout.go:52-72`、`internal/provider/codexapp/session_history.go:41-50`、`go-agent-v2/legacy-agentsdk/codex/rollout_reader.go:216-239`。
- Codex 中断完成链依赖 `turn/completed` / `turn/aborted`，未覆盖 `turn/interrupted`。证据：`internal/provider/codexapp/session.go:221-259`、`internal/provider/codexapp/event_map.go:86-90`。
- `recoveryManager` 已声明但未接线，Codex 的恢复/重连能力明显低于 V2。证据：`internal/provider/codexapp/recovery.go:12-44`、`internal/provider/codexapp/session.go:22`、`internal/provider/codexapp/session.go:78`、`go-agent-v2/legacy-agentsdk/codex/client_appserver_runtime.go:17-127`。
- `CapabilityError` 结构正确，但没有贯穿 RPC capability gate 主路径。证据：`internal/dto/provider/capability.go:17-28`、`internal/provider/claudecli/session.go:229-235`、`internal/platform/rpc/handler.go:70-82`。

### OK

- unified 的 driver 注册、`group:"drivers"` 收集、registry 解析和 client 启动链路是正确的。证据：`internal/provider/unified/module.go:11-27`、`internal/provider/unified/registry.go:15-56`、`internal/provider/unified/client.go:29-67`、`internal/provider/claudecli/module.go:12-26`、`internal/provider/codexapp/module.go:12-26`。
- claudecli 与 codexapp 对 `Driver` / `Session` / `TurnHandle` 的基础实现面是完整的。证据：`internal/provider/claudecli/driver.go:51-131`、`internal/provider/claudecli/session.go:39-393`、`internal/provider/codexapp/driver.go:73-186`、`internal/provider/codexapp/session.go:34-338`。
- `InputItem` 统一到 `dto/shared` 的字段面与 V2 `TurnInput` 对齐。证据：`internal/dto/provider/turn.go:24`、`internal/dto/shared/input.go:3-9`、`go-agent-v2/legacy-agentsdk/agentcore/types.go:20-22`。

## 互辩：批判其他 4 份报告

### 对 audit-fx-rpc 的批判

1. `docs/plans/迁移/audit-fx-rpc.md:158-161` 把 “V2 151 方法 vs V3 74 方法” 作为 fx/rpc 主 blocker，口径失焦。RPC 装配链本身是闭合的，`rpc.Module` 只负责 `NewServer/NewPushBridge/NewApprovalManager/NewCapabilityResolver` 和 handler 注册，见 `internal/platform/rpc/module.go:13-22`；更直接的 rpc 集成断点反而是 `BindEventToNotify` 完全未接线，定义在 `internal/platform/rpc/push.go:50-63`，`references` 结果只有声明本身。这说明它把 module parity 问题放进了 fx/rpc blocker，却漏掉了真正的 rpc 桥接断点。
2. `docs/plans/迁移/audit-fx-rpc.md:171-172` 用 `drivers` group 闭合推出 provider/rpc 侧“合格”，结论过度乐观。它验证到的只是 `DriverFactory -> group:"drivers" -> Registry`，见 `internal/provider/unified/module.go:14-26`、`internal/provider/claudecli/module.go:21-26`、`internal/provider/codexapp/module.go:21-26`；但 provider 合同里的 `ToolCallResponder` 仅在 `internal/contract/provider.go:47-50` 声明，`text_search("ToolCallResponder")` 在 `internal/*.go` 只命中这一处。V2 却把 `SendDynamicToolResult/RespondError` 作为基础 client 面，见 `go-agent-v2/legacy-agentsdk/agentcore/client.go:13-14`。所以 driver group 闭合不等于 provider-rpc 交互闭合。
3. `docs/plans/迁移/audit-fx-rpc.md:172` 的“未发现实际挂空依赖”只验证了 DI 图，没有验证运行时生命周期。`SessionManager.Register` 只由 `internal/provider/unified/client.go:63-65` 调用，`SessionManager.Remove` 只沿 `internal/provider/unified/session_adapter.go:29-34 -> internal/sidecar/orch/orchestration/service.go:80-84` 触发；而 `thread.Delete` 仅 `session.Close(ctx)` 不 `Remove`，见 `internal/module/thread/service.go:102-119`、`internal/module/thread/service.go:228-240`。结果是 DI 还能查到已关闭 session，这比报告列出的 optional 依赖 warning 更接近现实 blocker。
4. `docs/plans/迁移/audit-fx-rpc.md:171-173` 对 jrpc2 的审查只停留在 handler 签名合法性，没有核对 push 侧是否真正连通。`PushBridge` 虽然被 `rpc.Module` 提供，见 `internal/platform/rpc/module.go:15-18`，但真正负责 typed event -> notify 的 `BindEventToNotify` 无任何调用点，见 `internal/platform/rpc/push.go:50-63`。这使报告的 “jrpc2 包装方式当前合法” 成为纯语法结论，不是端到端集成结论。

### 对 audit-event-sm 的批判

1. `docs/plans/迁移/audit-event-sm.md:123` 把 `agentdto.StateChanged` 双源发布降为 Warning，严重性偏低。实际双源不止 `StateChanged`：orchestration 发布 `StateChanged/AgentLaunched/AgentStopped/AgentRecovering/AgentFailed`，见 `internal/sidecar/orch/orchestration/events.go:13-64`；Claude translator 同时发布 `AgentLaunched/AgentStopped/AgentFailed`，见 `internal/provider/claudecli/event_map.go:35-49`；Codex translator 还发布 `StateChanged/AgentStopped/AgentRecovering/AgentFailed`，见 `internal/provider/codexapp/event_map.go:41-70`。这已经是 agent 生命周期 source-of-truth 冲突，不只是 warning。
2. `docs/plans/迁移/audit-event-sm.md:104-105` 证明了 raw event 能进 typed bus，但漏掉了更底层的 dispatcher 设计缺口：`EventDispatcher.Dispatch` 对每个 raw event 都遍历全部 translator，见 `internal/provider/unified/event_map.go:42-65`；而两个 provider translator 都在 app 启动时全局注册，见 `internal/provider/claudecli/module.go:21-26`、`internal/provider/codexapp/module.go:21-26`。当前正确性依赖事件名偶然不冲突，而不是 provider 身份隔离。这个缺口比“typed bus 消费者少”更基础。
3. `docs/plans/迁移/audit-event-sm.md:129` 以 `LogSink` 覆盖六族事件为由认定“无硬孤儿”，判断标准过低。`LogSink` 的 subscriber 只做 `logger.Info`，见 `internal/platform/bus/sink.go:87-96`；`BindEventToNotify` 在 `internal/platform/rpc/push.go:50-63` 定义但零引用；`orchestration.CompleteTurn` 在 `internal/sidecar/orch/orchestration/service.go:282-301` 定义且 `references` 只有声明本身。事件即使被记录日志，控制流仍然是孤儿。
4. `docs/plans/迁移/audit-event-sm.md:121-123` 已经看到 “状态机不消费 bus 事件” 和 “双源 StateChanged”，但没有继续追到 provider 层的 turn/agent 分界错误。provider translator 现在不仅发 turn/tool 事件，也发 agent 事件，见 `internal/provider/claudecli/event_map.go:35-53`、`internal/provider/codexapp/event_map.go:39-74`；这直接违反了应由 orchestration 独占 agent 生命周期发布面的边界。报告抓到了症状，但没有把根因提升为主结论。

### 对 audit-store-sqlc 的批判

1. `docs/plans/迁移/audit-store-sqlc.md:140` 把 `AgentThreadBindingStore` 写成“没有迁到 V3 / 实际代码没有对应 repo”，用词过头。V3 已经有 `binding.Module`，见 `internal/store/binding/module.go:5-7`，并被聚合进 `internal/store/module.go:14-21`；其运行时 surface 包含 `GetByProviderThread/Upsert/DeleteByAgentID/UpdateSessionUUID/SetArchived/GetByAgentID`，见 `internal/store/binding/contract.go:7-14` 与 `internal/store/binding/store.go:18-81`，而且 thread service 已注入该 repo，见 `internal/module/thread/service.go:29-45`。更准确的结论应是 “V3 已有缩窄版 binding store，但缺少 V2 的 legacy dual-write 兼容层”，而不是 “repo 不存在”。
2. `docs/plans/迁移/audit-store-sqlc.md:140-143` 聚焦 repo 覆盖率，却遗漏了更直接的 binding 语义退化。V3 `Binding` 仍同时保留 `ProviderThreadID` 与 `CodexThreadID` 字段，见 `internal/store/binding/contract.go:16-25`、`internal/store/binding/contract.go:39-50`；但 `persistThreadState` 对所有 provider 都把两者写成同一个 `state.ThreadID`，见 `internal/module/thread/lifecycle.go:262-267`。V2 的 `normalizeBindingInput` 和 `selectBindingForRead` 明确区分 provider id 与 legacy id，见 `go-agent-v2/internal/store/agent_thread_binding.go:56-77`、`go-agent-v2/internal/store/agent_thread_binding.go:99-118`。这是比“只接入了 5 个 repo”更危险的 store 语义错误。
3. `docs/plans/迁移/audit-store-sqlc.md:143` 只从 “app 只装 5 个 store repo” 下结论，也遗漏了 store API 与 provider 解析之间 already-existing 的错位。`binding.Store` 已暴露 `GetByProviderThread`，见 `internal/store/binding/contract.go:8`、`internal/store/binding/store.go:18-28`；thread service 也确实依赖它做 fallback，见 `internal/module/thread/service.go:183-210`；但统一层 `SessionResolver` 仍只查 `threadStore.GetByThreadID`，见 `internal/provider/unified/session_resolver.go:34-45`。真正影响 provider 正确性的不是 repo 数量，而是 resolver 没有利用现有 store 面。
4. `docs/plans/迁移/audit-store-sqlc.md:155-157` 的 OK 结论把“19 个 repo 子包结构齐全”与“高风险类型映射无明显错配”作为正面结果，但没有覆盖最关键的 provider-thread 绑定读写路径。当前 thread service 的 binding fallback、session resolver 的 thread-only lookup、以及 binding store 的 provider-thread query 三者已经出现行为分裂，见 `internal/module/thread/service.go:183-210`、`internal/provider/unified/session_resolver.go:23-45`、`internal/store/binding/store.go:18-28`。这说明结构完整性不足以推出 store 层正确性。

### 对 audit-module-v2-parity 的批判

1. `docs/plans/迁移/audit-module-v2-parity.md:293-305` 只围绕 RPC handler parity 下结论，完全跳过了 app 实际已装配的 provider 子系统：`internal/app/modules.go:37-39` 明确装了 `unified.Module`、`claudecli.Module`、`codexapp.Module`。结果它没有检查 V2 `SendDynamicToolResult/RespondError` (`go-agent-v2/legacy-agentsdk/agentcore/client.go:13-14`) 对应到 V3 仅剩声明、无实现的 `ToolCallResponder` (`internal/contract/provider.go:47-50`；`text_search("ToolCallResponder")` 在 `internal/*.go` 只命中声明)。这是明显遗漏的 parity blocker。
2. `docs/plans/迁移/audit-module-v2-parity.md:297-299` 把 thread/orchestration/approval 当成主要 blocker，却没有覆盖 provider session 生命周期。V3 session 注册只来自 `internal/provider/unified/client.go:63-65`，删除只走 `internal/provider/unified/session_adapter.go:29-34 -> internal/sidecar/orch/orchestration/service.go:80-84`；`thread.Delete` 只 `Close` 不 `Remove`，见 `internal/module/thread/service.go:102-119`、`internal/module/thread/service.go:228-240`。这会直接破坏 thread/turn 的运行时等价性，比多处 handler 参数缩水更基础。
3. `docs/plans/迁移/audit-module-v2-parity.md:293-310` 也漏掉了 event ownership parity。当前 orchestration 发布 agent lifecycle，见 `internal/sidecar/orch/orchestration/events.go:13-64`；Claude/Codex translator 同时发布同族 agent 事件，见 `internal/provider/claudecli/event_map.go:35-49`、`internal/provider/codexapp/event_map.go:41-70`。只看 RPC key 覆盖率，无法解释这种运行时双源偏差，因此它的 parity 结论与 provider 审查冲突。
4. `docs/plans/迁移/audit-module-v2-parity.md:298` 对审批链的 blocker 判断仍然偏窄。它抓到了 `RequestApproval` 无外部调用点，见 `internal/platform/rpc/approval.go:71-103` 的 `references` 只有声明与 `RequestUserInput` 内部转发；但更深一层的 V2 parity 缺口是：即便未来 approval request 接上线，V3 仍缺少 dynamic tool result/error 的 responder 实现，见 `internal/contract/provider.go:47-50` 与 `go-agent-v2/legacy-agentsdk/agentcore/client.go:13-14`。报告没有把这条更深的 provider parity 缺口纳入主结论。
