# 审查：Module V2 迁移完整性

## 方法与口径

- 读取手段仅使用 LSP：`read_file`、`text_search`、`document_symbol`、`workspace_symbol`、`implementation`、`references`、`call_hierarchy`。
- `HandlerMapResult` 聚合点位于 `internal/platform/rpc/module.go:31-35`；`internal/module/*` 下当前只存在 5 个 `handler.Map` 生产点：`internal/module/thread/rpc.go:18-82`、`internal/module/turn/rpc.go:14-93`、`internal/module/skill/rpc.go:12-65`、`internal/module/workspace/rpc.go:13-96`、`internal/sidecar/orch/orchestration/rpc.go:11-36`。
- 应用当前实际装配的模块为 `skill`、`thread`、`turn`、`orchestration`、`workspace`，见 `internal/app/modules.go:32-36`。
- 覆盖率分两层口径：
  - 注册覆盖率：V2 方法名在 V3 中是否仍有 handler。
  - 功能等价率：V3 handler 是否有证据达到 V2 行为/参数/返回语义。

## 1. thread 模块

### 1.1 V3 handler 清单（29 个）

证据：`internal/module/thread/rpc.go:18-82`

1. `thread/start`
2. `thread/resume`
3. `thread/fork`
4. `thread/recover`
5. `thread/archive`
6. `thread/unarchive`
7. `thread/delete`
8. `thread/list`
9. `thread/loaded/list`
10. `thread/read`
11. `thread/resolve`
12. `thread/messages`
13. `thread/name/set`
14. `thread/config/get`
15. `thread/config/set`
16. `thread/model/set`
17. `thread/personality/set`
18. `thread/approvals/set`
19. `thread/compact/start`
20. `thread/rollback`
21. `thread/undo`
22. `thread/backgroundTerminals/clean`
23. `thread/mcp/list`
24. `thread/skills/list`
25. `thread/debugMemory`
26. `thread/realtime/start`
27. `thread/realtime/appendAudio`
28. `thread/realtime/appendText`
29. `thread/realtime/stop`

### 1.2 V2 方法清单

- V2 源码里不存在 `registerThreadMethods`；实际注册入口是 `go-agent-v2/internal/apiserver/methods.go:143` 调用的 `go-agent-v2/internal/apiserver/methods_thread_turn.go:8-113`。
- 其中 `thread/*` 路由共 29 个，位于 `go-agent-v2/internal/apiserver/methods_thread_turn.go:9-111`。
- `go-agent-v2/internal/apiserver/methods_thread.go:16-359` 是 thread typed handler/参数实现文件，不是注册入口。

### 1.3 逐一对照表（✅/⚠️/❌）

| Key | 结论 | 证据 | 说明 |
| --- | --- | --- | --- |
| `thread/start` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread.go:16-27,44-76`; V3: `internal/module/thread/rpc_types.go:7-16`, `internal/module/thread/lifecycle.go:44-76,204-219` | V3 缺 `modelProvider`、`baseInstructions`、`developerInstructions`、`sandbox`、`summary` 等 V2 参数，仅保留精简启动面。 |
| `thread/resume` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread.go:237-249`; V3: `internal/module/thread/rpc_types.go:18-21`, `internal/module/thread/rpc.go:116-128`, `internal/module/thread/lifecycle.go:77-106` | V3 不再接收 V2 的 `path/cwd/model`，恢复能力依赖现有 thread 记录与 binding。 |
| `thread/recover` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread.go:257-267`; V3: `internal/module/thread/rpc.go:36,90-93`, `internal/module/thread/lifecycle.go:137-169` | V2 返回 `recovered/mode`，V3 仅返回 effect，无恢复结果面。 |
| `thread/fork` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread.go:224-235`; V3: `internal/module/thread/rpc.go:33-35`, `internal/module/thread/lifecycle.go:108-135` | V3 返回 `ForkResult{newThreadID}`，不保留 V2 `thread{id,forkedFrom}` 响应形状。 |
| `thread/archive` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:13-25`; V3: `internal/module/thread/archive.go:5-13` | V2 走 provider archive 并 stop inline manager；V3 只改状态、binding、session。 |
| `thread/unarchive` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods.go:183-201`; V3: `internal/module/thread/archive.go:15-20` | V2 反归档后会在进程不存活时触发 `EnsureProcessAlive`；V3 仅改状态。 |
| `thread/delete` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods.go:204-227`; V3: `internal/module/thread/service.go:102-119` | V2 还会删除 archive 目录；V3 只删 binding/session/thread 记录。 |
| `thread/name/set` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:28-32`, `go-agent-v2/legacy-agentsdk/service/lifecycle/thread_lifecycle_logic.go:179-197`; V3: `internal/module/thread/rpc.go:55-57`, `internal/module/thread/service.go:92-100` | V2 更新 runtime alias 和持久化 alias；V3 只把 `Prompt` 写回 thread store。 |
| `thread/compact/start` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:33-35`; V3: `internal/module/thread/rpc.go:63,99-113`, `internal/module/thread/command.go:18-35` | V3 仍注册 key，但 `SendCommand` 不支持 `/compact`，属于骨架。 |
| `thread/rollback` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread.go:274-293`; V3: `internal/module/thread/rpc.go:64,99-113`, `internal/module/thread/command.go:18-35` | V3 仍走 command 骨架，`/rollback` 未实现。 |
| `thread/list` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread.go:301-324`; V3: `internal/module/thread/rpc.go:41-43`, `internal/module/thread/service.go:62-83` | V2 支持 `archived` 过滤；V3 仅返回 `[]Ref`，无过滤与 envelope。 |
| `thread/loaded/list` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread.go:326-340`; V3: `internal/module/thread/rpc.go:44-46`, `internal/module/thread/service.go:75-83` | V2 支持 `cursor/limit`；V3 退化为 `ListByStatus(statusCreated)`。 |
| `thread/read` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:39-43`, `go-agent-v2/legacy-agentsdk/service/lifecycle/thread_lifecycle_logic.go:242-250`; V3: `internal/module/thread/rpc.go:47-50,131-134`, `internal/module/thread/service.go:66-73` | V2 返回 `history` payload；V3 只返回弱化后的 `Ref`。 |
| `thread/resolve` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:44-48`, `go-agent-v2/legacy-agentsdk/service/lifecycle/thread_lifecycle_logic.go:253-289`; V3: `internal/module/thread/rpc.go:48-50,131-134`, `internal/module/thread/service.go:66-73` | V2 可解析 `state/port/providerThreadId/uuid/hasHistory`；V3 退化为 `Get`。 |
| `thread/config/get` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread.go:343-355`, `go-agent-v2/internal/apiserver/codexadapter/thread_config_guard.go:47-59`; V3: `internal/module/thread/rpc.go:58,99-113`, `internal/module/thread/command.go:18-35` | V3 仍注册 key，但 `/config/get` 不在 `SendCommand` 支持面内。 |
| `thread/config/set` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread.go:347-359`, `go-agent-v2/internal/apiserver/codexadapter/thread_config_guard.go:102-173`; V3: `internal/module/thread/rpc.go:59,99-113`, `internal/module/thread/command.go:18-35` | V3 仍注册 key，但 `/config/set` 不在 `SendCommand` 支持面内。 |
| `thread/messages` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:51-55`, `go-agent-v2/internal/apiserver/codexadapter/thread_messages.go:77-103,150-189`; V3: `internal/module/thread/rpc.go:51-53`, `internal/module/thread/history.go:22-45` | V2 有 provider history + runtime hydration + response envelope；V3 只读 session history 并本地裁剪。 |
| `thread/backgroundTerminals/clean` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:56-58`; V3: `internal/module/thread/rpc.go:66,99-113`, `internal/module/thread/command.go:18-35` | V3 注册 key，但 `/clean` 未实现。 |
| `thread/model/set` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:98-100`, `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:434-436`; V3: `internal/module/thread/rpc.go:60,99-113`, `internal/module/thread/command.go:18-25,54-66`, `internal/module/thread/rpc_types.go:34-37` | V3 有闭环，但请求面从 `{model}` 变为 `args string`，RPC 契约漂移。 |
| `thread/personality/set` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:101-103`, `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:434-436`; V3: `internal/module/thread/rpc.go:61,99-113`, `internal/module/thread/command.go:18-25,54-66`, `internal/module/thread/rpc_types.go:34-37` | 行为可达，但 RPC 请求面漂移。 |
| `thread/approvals/set` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:104-106`, `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:434-436`; V3: `internal/module/thread/rpc.go:62,99-113`, `internal/module/thread/command.go:18-25,54-66`, `internal/module/thread/rpc_types.go:34-37` | 行为可达，但 RPC 请求面漂移。 |
| `thread/realtime/start` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:73-77`, `go-agent-v2/legacy-agentsdk/service/lifecycle/thread_lifecycle_logic.go:143-147`; V3: `internal/module/thread/rpc.go:77,105-113`, `internal/module/thread/command.go:18-35` | V3 注册 key，但 `/realtime/start` 未实现。 |
| `thread/realtime/appendAudio` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:78-82`, `go-agent-v2/legacy-agentsdk/service/lifecycle/thread_lifecycle_logic.go:149-153`; V3: `internal/module/thread/rpc.go:78,105-113`, `internal/module/thread/command.go:18-35` | V3 注册 key，但 `/realtime/appendAudio` 未实现。 |
| `thread/realtime/appendText` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:83-87`, `go-agent-v2/legacy-agentsdk/service/lifecycle/thread_lifecycle_logic.go:155-159`; V3: `internal/module/thread/rpc.go:79,105-113`, `internal/module/thread/command.go:18-35` | V3 注册 key，但 `/realtime/appendText` 未实现。 |
| `thread/realtime/stop` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:88-92`, `go-agent-v2/legacy-agentsdk/service/lifecycle/thread_lifecycle_logic.go:161-163`; V3: `internal/module/thread/rpc.go:80,105-113`, `internal/module/thread/command.go:18-35` | V3 注册 key，但 `/realtime/stop` 未实现。 |
| `thread/undo` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:95-97`; V3: `internal/module/thread/rpc.go:65,99-113`, `internal/module/thread/command.go:18-35` | V3 注册 key，但 `/undo` 未实现。 |
| `thread/mcp/list` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:107-109`; V3: `internal/module/thread/rpc.go:67,99-113`, `internal/module/thread/command.go:18-35` | V3 注册 key，但 `/mcp` 未实现。 |
| `thread/skills/list` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:110-110`, `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:437-439`; V3: `internal/module/thread/rpc.go:68,99-113`, `internal/module/thread/command.go:18-35` | V3 仍把它留在 thread RPC，但 command backend 不支持 `/skills`。 |
| `thread/debugMemory` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods.go:373-387`; V3: `internal/module/thread/rpc.go:70-75,137-141` | V2 根据 `action=drop|update` 转 `/debug-m-drop|update`；V3 直接返回宿主进程 `runtime.MemStats`。 |

### 1.4 contract.go 与 service 覆盖度

- `thread.Service` 只有 16 个方法，见 `internal/module/thread/contract.go:9-26`；实现体是 `internal/module/thread/service.go:26-38`，LSP `implementation` 只命中这一处。
- `thread/rpc.go` 中大量 V2 细粒度方法被压到统一的 `SendCommand`，见 `internal/module/thread/rpc.go:58-80,99-113`；而 `SendCommand` 实际只支持 `/model`、`/personality`、`/approvals`、`/interrupt` 四类，见 `internal/module/thread/command.go:18-35`。
- `thread/rpc.go` 自身已写明“当前只有 `/model`、`/personality`、`/approvals` 真正闭环，其余走骨架通道”，见 `internal/module/thread/rpc.go:96-99`。
- `ReadHistory`、`ListByCWD` 存在于 contract 中，见 `internal/module/thread/contract.go:17,22`，但当前 RPC 面没有任何 handler 暴露这两个方法；现有列表入口只有 `thread/list` 与 `thread/loaded/list`，见 `internal/module/thread/rpc.go:41-46`。

### 1.5 history/archive/listing/command/messages 子文件对等性

- `history.go`：只做 `session.ReadHistory` + 本地 `before` 过滤，见 `internal/module/thread/history.go:13-45`；不具备 V2 `thread_messages.go` 的 runtime hydration、分页 page 通知、`total` envelope，见 `go-agent-v2/internal/apiserver/codexadapter/thread_messages.go:77-103,150-189`。
- `archive.go`：只做 thread status 与 binding archive 标记，见 `internal/module/thread/archive.go:5-20`；V2 归档/反归档还会触发 provider/archive 操作与进程恢复，见 `go-agent-v2/internal/apiserver/methods_thread_turn.go:13-25`、`go-agent-v2/internal/apiserver/methods.go:183-201`。
- listing：没有独立 `listing.go`；列表能力散在 `service.go` 的 `List/ListByStatus/ListByCWD`，见 `internal/module/thread/service.go:62-90`。V2 `thread/list`/`thread/loaded/list` 则有独立的 archived 过滤与 cursor/limit，见 `go-agent-v2/internal/apiserver/methods_thread.go:301-340`。
- `command.go`：只有 4 个 command 分支，见 `internal/module/thread/command.go:18-35`，与 V2 覆盖的 compact/rollback/clean/realtime/mcp/skills/debugMemory 等 command 面不对等。
- 当前 thread 包文件集中仅有 `archive.go`、`command.go`、`contract.go`、`history.go`、`lifecycle.go`、`module.go`、`rpc.go`、`rpc_types.go`、`service.go`，见这些文件各自的 `:1` 包声明；不存在文档中列出的 `events.go`、`config.go`、`helpers.go`。

## 2. turn 模块

### 2.1 handler 清单（6 个）

证据：`internal/module/turn/rpc.go:32-91`

1. `turn/start`
2. `turn/steer`
3. `turn/interrupt`
4. `turn/forceComplete`
5. `review/start`
6. `approval/respond`

### 2.2 V2 对照

- `turn/start`、`turn/steer`、`turn/interrupt`、`turn/forceComplete`、`review/start` 的 V2 注册入口都在 `go-agent-v2/internal/apiserver/methods_thread_turn.go:60-93`。
- `approval/respond` 位于 V2 core 注册 `go-agent-v2/internal/apiserver/methods.go:157-166`，具体 handler 为 `go-agent-v2/internal/apiserver/server_approval.go:456-483`。

### 2.3 逐一对照表（✅/⚠️/❌）

| Key | 结论 | 证据 | 说明 |
| --- | --- | --- | --- |
| `turn/start` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_turn.go:24-37,48-66`; V3: `internal/module/turn/rpc_types.go:5-12`, `internal/module/turn/rpc.go:33-47`, `internal/module/turn/service.go:37-58,61-92` | V3 只暴露 `prompt/images/files/model/effort`，没有 V2 的 `input[]`、`selectedSkills`、`manualSkillSelection`、`outputSchema`、`approvalPolicy`。 |
| `turn/steer` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_turn.go:68-82`; V3: `internal/module/turn/rpc_types.go:14-17`, `internal/module/turn/rpc.go:49-58`, `internal/module/turn/service.go:94-100` | V2 支持 `expectedTurnId` 和结构化输入；V3 退化为单个 `prompt`。 |
| `turn/interrupt` | ✅ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:62-66`; V3: `internal/module/turn/rpc.go:60-65`, `internal/module/turn/rpc_types.go:19-22`, `internal/module/turn/service.go:102-126` | 参数语义保持 `threadId + source`；V3 额外等待 turn settle。 |
| `turn/forceComplete` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:67-71`; V3: `internal/module/turn/rpc.go:67-72`, `internal/module/turn/service.go:128-144` | V2 有专门 `TurnForceComplete`；V3 退化为 `Interrupt(source=force_complete)`。 |
| `review/start` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_turn.go:116-186`; V3: `internal/module/turn/rpc.go:74-77` | V3 仅保留注册壳，直接返回 `ErrNotImplemented`。 |
| `approval/respond` | ⚠️ | V2: `go-agent-v2/internal/apiserver/server_approval.go:456-483`; V3: `internal/module/turn/rpc_types.go:28-33`, `internal/module/turn/rpc.go:79-91` | V3 参数面已支持 `decision: RawMessage` 与 `approved: *bool`，但审批闭环未接通，见 2.4。 |

### 2.4 approval 闭环

- V2 `approval/respond` 参数为 `requestId int64`、`approved *bool`、`decision any`，见 `go-agent-v2/internal/apiserver/server_approval.go:456-460`。
- V2 pending request 分配与等待在 `go-agent-v2/internal/apiserver/server_approval.go:351-380`、`go-agent-v2/internal/apiserver/server_conn.go:221-229,258-260`；响应则通过 `ResolvePendingRequest` 落回等待方，见 `go-agent-v2/internal/apiserver/server_approval.go:462-483`、`go-agent-v2/internal/apiserver/server_conn.go:241-257`。V2 闭环是 `request -> pending -> approval/respond -> ResolvePendingRequest`。
- V3 `approval/respond` 参数为 `callId string`、`requestId *int64`、`approved *bool`、`decision json.RawMessage`，见 `internal/module/turn/rpc_types.go:28-33`；handler 最终只调用 `ApprovalResponder.Respond(...)`，见 `internal/module/turn/rpc.go:79-91`。
- V3 `ApprovalManager` 确实具备 `RequestApproval`、`registerPending`、`finishPending`、`publishRequested`、`publishResolved`、`RestorePending`、`PendingSnapshot`，见 `internal/platform/rpc/approval.go:71-96,125-157,230-256`、`internal/platform/rpc/approval_events.go:15-35`、`internal/platform/rpc/approval_lifecycle.go:10-43`。
- 但 LSP `call_hierarchy` 显示 `RequestApproval` 的唯一 incoming 调用者是 `RequestUserInput` 本文件内部包装，见 `internal/platform/rpc/approval.go:98-103`；不存在 turn/provider/orchestration 外部调用点。`Respond` 的外部调用者只有 `internal/module/turn/rpc.go:87`，其余都是 `ApprovalManager` 内部自调用，见 `internal/platform/rpc/approval.go:119,194`。
- 同时，provider 侧已直接把 approval 事件翻译成 DTO：`internal/provider/codexapp/event_map.go:132-144` 直接产出 `ToolApprovalRequested/Resolved`。这说明 V3 当前是“事件翻译链”和“ApprovalManager pending 链”并存，但没有统一闭环。
- `awaiting_user_input` 状态及 `TriggerUserInputRequested/Resolved` 只定义在 `internal/dto/agent/state.go:14,28-29,90-93`；当前 orchestration 代码未见触发点。审批不会把 agent state 可靠推进到 `awaiting_user_input -> turn_running`。

## 3. orchestration 模块

### 3.1 handler 清单（9 个）

证据：`internal/sidecar/orch/orchestration/rpc.go:11-36`

1. `agent.launch`
2. `agent.stop`
3. `agent.list`
4. `agent.snapshot`
5. `task/dag/create`
6. `task/dag/get`
7. `task/dag/list`
8. `task/node/update`
9. `orchestration/report`

### 3.2 V2 12 方法对照

证据：`go-agent-v2/internal/apiserver/methods_orchestration.go:14-27`

| V2 方法 | V3 结论 | 证据 | 说明 |
| --- | --- | --- | --- |
| `agent.launch` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:29-70`, `go-agent-v2/internal/runner/manager_launch.go:268-333`; V3: `internal/sidecar/orch/orchestration/rpc_types.go:5-10`, `internal/sidecar/orch/orchestration/rpc.go:13-20`, `internal/sidecar/orch/orchestration/service.go:86-101,211-234` | V2 有 `prompt/instructions/dynamic_tools/config/provider`；V3 只接 `agentId/name/cwd/command` 并直接起进程。 |
| `agent.submit` | ❌ | V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:16,73-91`; V3: 无同名 handler，仅有 `SubmitTurn` service `internal/sidecar/orch/orchestration/contract.go:14-18`, `internal/sidecar/orch/orchestration/service.go:140-159` | service 存在，但 RPC 面未暴露。 |
| `agent.submitPrompt` | ❌ | V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:17,73-91`; V3: 无同名 handler | 兼容 alias 未迁入。 |
| `agent.stop` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:18,93-108`, `go-agent-v2/internal/runner/manager_lifecycle.go:19-92`; V3: `internal/sidecar/orch/orchestration/rpc.go:21-23`, `internal/sidecar/orch/orchestration/service.go:103-117` | 同名功能存在，但 V3 参数标签改为 `agentId`，返回面也不再给 `success`。 |
| `agent.list` | ⚠️ | V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:19,110-116`, `go-agent-v2/internal/runner/manager.go:147-157,278-313`; V3: `internal/sidecar/orch/orchestration/rpc.go:24-26`, `internal/sidecar/orch/orchestration/service.go:161-170,183-208` | V3 返回 `AgentSnapshot`，但缺 V2 `port/provider/last_report`，并改为进程/状态机视角。 |
| `agent.getReport` | ❌ | V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:20,118-135`, `go-agent-v2/internal/runner/manager.go:475-477`; V3: `internal/sidecar/orch/orchestration/rpc.go:34,38-41` | V3 没有 `agent.getReport`；`orchestration/report` 只是未实现占位。 |
| `agent.rememberReportRequest` | ❌ | V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:21,137-160`, `go-agent-v2/internal/apiserver/orchestration_report.go:23-37`; V3: 无同名 handler | V2 的 waiter 注册链未迁入。 |
| `agent.reportEvent` | ❌ | V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:22,179-209`; V3: 无同名 handler | V2 runtime event 上报口缺失。 |
| `agent.getState` | ❌ | V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:23,162-177`; V3: 无同名 handler，最近似的是 `agent.snapshot` `internal/sidecar/orch/orchestration/rpc.go:27-29` | 仅有近似替代，无兼容入口。 |
| `agent.saveSubAgent` | ❌ | V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:24,211-220`; V3: 无 | 子 agent 持久化能力未迁入 RPC。 |
| `agent.deleteSubAgent` | ❌ | V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:25,222-229`; V3: 无 | 同上。 |
| `agent.persistSubAgentBinding` | ❌ | V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:26,231-241`; V3: 无 | 同上。 |

补充：

- `agent.snapshot` 是 V3 新增 key，见 `internal/sidecar/orch/orchestration/rpc.go:27-29`；V2 没有同名 RPC，最接近的是内部 `Snapshot()`，见 `go-agent-v2/internal/runner/manager_lifecycle.go:139-149`。
- `task/dag/*` 与 `orchestration/report` 都是 V3 新增 key，但当前统一返回 `ErrNotImplemented`，见 `internal/sidecar/orch/orchestration/rpc.go:30-41`。

### 3.3 service 能力

- `orchestration.Service` 现有 contract 只有 `LaunchAgent`、`ListAgents`、`StopAgent`、`SubmitTurn`、`CompleteTurn`、`Recover`、`Snapshot`，见 `internal/sidecar/orch/orchestration/contract.go:10-18`；DAG 方法仍停留在 TODO 注释，见 `internal/sidecar/orch/orchestration/contract.go:20-24`。
- `SubmitTurn` 的当前能力是把 `TurnSubmission` 放入本地 FIFO 队列，并在 state 为 `idle` 时触发 `turn_queued`，见 `internal/sidecar/orch/orchestration/service.go:140-159`。
- V2 `SubmitOrQueueWithMetadata` 则包含 queue position、`SubmissionMetadata`、dead-client 自恢复、pending wakeup context、非队列提交和回滚逻辑，见 `go-agent-v2/internal/runner/manager_submission.go:243-275,307-331,356-440,514-526`。V3 只覆盖了最薄的排队语义。
- `CompleteTurn` 只校验 `activeTurnID` 并做成功/失败状态切换，见 `internal/sidecar/orch/orchestration/service.go:288-307`；V2 事件流还会抓 turn completion report、message、tracked artifacts，见 `go-agent-v2/internal/runner/manager_event.go:418-484`。
- `Recover` 在 V3 是显式入口：stop process -> 清理状态 -> 重新启动，见 `internal/sidecar/orch/orchestration/recover.go:27-56`；V2 `RecoverAgentWithOptions` 还包含 provider-aware recovery target 推导、early-silence circuit breaker、active submission replay、历史线程 resume，见 `go-agent-v2/internal/runner/manager_recover.go:219-332`。
- `Snapshot` 在 V3 返回的是结构化 `AgentSnapshot`，见 `internal/sidecar/orch/orchestration/service.go:172-208`；V2 对外 list 使用 `AgentInfo`，内部 `Snapshot()` 则返回 `[]*AgentProcess` 供 shutdown/kill，见 `go-agent-v2/internal/runner/manager.go:147-157,278-313`、`go-agent-v2/internal/runner/manager_lifecycle.go:139-149`。两者用途不同，不能直接视为等价迁移。

### 3.4 RunnerActor + StallDetector + SubmissionQueue 对照

- V3 `runnerActor` 只有 3 个核心动作：启动 waiters、消费 turn queue、按 200ms ticker 做 stall recovery，见 `internal/sidecar/orch/orchestration/runner_actor.go:26-44,48-77`。
- V3 `SubmissionQueue` 只是进程内 `[]turn.TurnSubmission` FIFO，见 `internal/sidecar/orch/orchestration/submission.go:9-50`；没有 V2 `queuedSubmission` 的 prompt/images/files/dispatch、active submission、queue position、replay、progressed 标记，见 `go-agent-v2/internal/runner/manager_submission.go:243-275,286-297,333-374`。
- V3 `StallDetector` 只检查 `state == turn_running` 且 `updatedAt` 超阈值，见 `internal/sidecar/orch/orchestration/recover.go:11-24`；V2 除 stall 之外，还有 `connection_dead` 自动恢复窗口、intentional interrupt allowlist、early silence 自恢复等，见 `go-agent-v2/internal/runner/manager_event.go:15-16,307-320`、`go-agent-v2/internal/runner/manager.go:51-57`、`go-agent-v2/internal/runner/manager_recover.go:236-253`。
- 结论：V3 `RunnerActor + StallDetector + SubmissionQueue` 只覆盖了 V2 runner 的最小骨架，不具备 V2 的自恢复、submission metadata、报告聚合、event-normalize 完整面。

## 4. skill + workspace 快扫

### 4.1 skill

- V3 `skill` 当前有 22 个 handler，见 `internal/module/skill/rpc.go:20-62`。
- 其中 V2 等价技能面共有 15 个：14 个来自 `registerSkillMethods`，见 `go-agent-v2/internal/apiserver/methods.go:229-236`；另有 `command/exec` 来自 core 注册 `go-agent-v2/internal/apiserver/methods.go:157-160`，其 V3 对应在 `internal/module/skill/rpc.go:31-33`。
- V3 额外新增 7 个 `command/card/*` 方法，见 `internal/module/skill/rpc.go:20-30`；V2 全仓检索未见 `command/card/`，说明这是 V3-only 增量面。
- 结论：按方法名计，V2 skill+command 面在 V3 已全部有注册入口；V3 还扩了 command card 家族。

### 4.2 workspace

- V3 `workspace` 当前有 8 个 handler，见 `internal/module/workspace/rpc.go:15-94`；contract 也对应 8 个 service 方法，见 `internal/module/workspace/contract.go:11-20`。
- V2 `workspace` 注册面只有 5 个：`create/get/list/merge/abort`，见 `go-agent-v2/internal/apiserver/methods.go:255-259`，具体实现位于 `go-agent-v2/internal/apiserver/workspace_methods.go:41-166`。
- V3 新增了 3 个 V2 不存在的方法：`workspace/run/status/update`、`workspace/run/files/list`、`workspace/run/file/get`，见 `internal/module/workspace/rpc.go:42-50,75-93`；对 `go-agent-v2` 全仓文本检索这 3 个 key 均无命中。
- 结论：V2 workspace 5 个方法在 V3 都已注册；V3 额外扩了 3 个只读/状态面。

## 5. 迁移文档准确性

### 5.1 文档 vs 代码不一致清单

1. `v3-migration-plan.md` 的 `methods.go`/`methods_thread_turn.go`/`methods_turn.go` 映射表只是高层 file-to-file 归宿，见 `docs/plans/迁移/v3-migration-plan.md:1472-1480`。这部分没有明确方法数断言，本轮未发现可直接证伪的条目。
2. `v3-module-migration-details.md` 断言 `thread/loaded/list`、`thread/resolve`、`thread/model/set`、`thread/personality/set`、`thread/approvals/set` 已合并或下沉，`thread/debugMemory` 已删除，见 `docs/plans/迁移/v3-module-migration-details.md:60-77`；实际这些 key 仍注册在 `internal/module/thread/rpc.go:44-75`。
3. 同文档声称 `review/start` 已并入 `module/turn`，见 `docs/plans/迁移/v3-module-migration-details.md:78`；实际 `review/start` 仍是空壳并直接 `ErrNotImplemented`，见 `internal/module/turn/rpc.go:74-77`。
4. 同文档列出 thread 目标文件 `events.go`、`config.go`、`helpers.go`，见 `docs/plans/迁移/v3-module-migration-details.md:31-38`；实际 thread 包仅有 `archive.go`、`command.go`、`contract.go`、`history.go`、`lifecycle.go`、`module.go`、`rpc.go`、`rpc_types.go`、`service.go`，见这些文件各自 `:1`。文档文件结构已过时。
5. 同文档称 `thread.Module` “只对外提供一个 Facade、线程查询 facade 和 handler.Map 片段”，见 `docs/plans/迁移/v3-module-migration-details.md:41`；实际 `internal/module/thread/module.go:7-18` 只 `Provide(NewService, NewThreadHandlers)`，没有独立 facade provider。
6. 同文档对 skill 的 jrpc2 描述只覆盖 `skills/*` 家族，见 `docs/plans/迁移/v3-module-migration-details.md:204`；实际 `internal/module/skill/rpc.go:20-33` 还包含 `command/card/*` 与 `command/exec`。文档低估了 V3 实际 skill RPC 面。
7. 同文档对 workspace 的 jrpc2 描述只写 `workspace/run/create|get|list|merge|abort`，见 `docs/plans/迁移/v3-module-migration-details.md:340`；实际 `internal/module/workspace/rpc.go:42-93` 还包含 `status/update`、`files/list`、`file/get` 3 个新增 handler。
8. 同文档列出 workspace 目标文件 `merge.go`、`helpers.go`，见 `docs/plans/迁移/v3-module-migration-details.md:327-333`；实际 workspace 包只有 `contract.go`、`module.go`、`rpc.go`、`rpc_types.go`、`service.go`，见这些文件各自 `:1`。文件结构描述已过时。
9. 同文档列出 orchestration 目标文件 `phase1_watcher.go`、`patterns.go`，并宣称 `orchestration.Module` 提供 DAG service、phase1 watcher、recovery service、tool-facing facade、Runner，见 `docs/plans/迁移/v3-module-migration-details.md:263-278`；实际 orchestration 包只有 `contract.go`、`events.go`、`helpers.go`、`module.go`、`recover.go`、`rpc.go`、`rpc_types.go`、`runner_actor.go`、`service.go`、`submission.go`，见这些文件 `:1`，且 `internal/sidecar/orch/orchestration/module.go:5-11` 只提供 `NewService`、`Service`、`NewOrchestrationHandlers`、`NewRunnerActor`。
10. 同文档写 “jrpc2：负责 orchestration_*、task_* 以及 DAG/phase1 相关 RPC”，见 `docs/plans/迁移/v3-module-migration-details.md:277`；实际 `internal/sidecar/orch/orchestration/rpc.go:30-41` 中所有 `task/dag/*` 与 `orchestration/report` 都是 `ErrNotImplemented`。
11. `v2-v3-alignment-report.md` 前半段仍写“provider registry 无”“internal/provider 目录不存在”“events 缺失”，见 `docs/plans/迁移/v2-v3-alignment-report.md:50-58`；但实际 `internal/app/modules.go:17-19,37-39` 已装配 `unified/claudecli/codexapp` provider 模块，`internal/provider/unified/module.go:17-26` 与 `internal/provider/unified/registry.go:11-40` 已存在 registry，`internal/sidecar/orch/orchestration/events.go:25-63` 已发布 `AgentLaunched/Stopped/Recovering/Failed`。该报告存在过时段落。
12. 同报告写“当前 V3 仍是 six-state idle/thinking/running/paused/stopped/error”，见 `docs/plans/迁移/v2-v3-alignment-report.md:75`；实际 `internal/dto/agent/state.go:8-18,78-102` 已是 10 态状态机。该条已经失真。
13. 同报告后半段的“修复项验证”反而更接近现状，见 `docs/plans/迁移/v2-v3-alignment-report.md:169-175`。该文档内部前后口径不一致，需要清理过时结论。

## 6. 总注册数

### 6.1 V3 总数

- 当前装配到 `HandlerMapResult` 的模块只有 5 个，见 `internal/platform/rpc/module.go:31-35` 与 `internal/app/modules.go:32-36`。
- 这 5 个模块的 handler 数分别是：
  - thread 29：`internal/module/thread/rpc.go:18-82`
  - turn 6：`internal/module/turn/rpc.go:32-91`
  - skill 22：`internal/module/skill/rpc.go:20-62`
  - workspace 8：`internal/module/workspace/rpc.go:15-94`
  - orchestration 9：`internal/sidecar/orch/orchestration/rpc.go:12-34`
- 结论：V3 当前总 RPC handler 数为 `74`。

### 6.2 V2 总数

本次审查采用与用户指定范围一致的 V2 基线：

- thread 29：`go-agent-v2/internal/apiserver/methods_thread_turn.go:9-111`
- turn/review 5：`go-agent-v2/internal/apiserver/methods_thread_turn.go:60-93`
- `approval/respond` 1：`go-agent-v2/internal/apiserver/methods.go:157-166`
- skill 14：`go-agent-v2/internal/apiserver/methods.go:229-236`
- `command/exec` 1：`go-agent-v2/internal/apiserver/methods.go:157-160`
- workspace 5：`go-agent-v2/internal/apiserver/methods.go:255-259`
- orchestration 12：`go-agent-v2/internal/apiserver/methods_orchestration.go:14-27`
- `mock/experimentalMethod` 1：`go-agent-v2/internal/apiserver/methods_thread_turn.go:111-112`

结论：本审查口径下 V2 总 RPC 方法数为 `68`。

### 6.3 迁移覆盖率

- 按“V2 方法名在 V3 仍有 handler 或同域入口”计算：
  - thread：29/29
  - turn + `approval/respond`：6/6
  - skill + `command/exec`：15/15
  - workspace：5/5
  - orchestration：3/12
  - `mock/experimentalMethod`：0/1
- 因此注册覆盖率为 `58 / 68 = 85.3%`。
- 该 `85.3%` 只是“名字/入口仍在”的覆盖率，不代表功能等价。thread/turn/orchestration 的功能等价率明显低于这个数，尤其 thread 29 个 key 中大量仍是骨架或降级实现。

### 6.4 缺失方法完整列表

以下方法在 V2 有注册，但 V3 当前没有同名 handler：

1. `mock/experimentalMethod` — V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:111-112`
2. `agent.submit` — V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:16,73-91`
3. `agent.submitPrompt` — V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:17,73-91`
4. `agent.getReport` — V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:20,118-135`
5. `agent.rememberReportRequest` — V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:21,137-160`
6. `agent.reportEvent` — V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:22,179-209`
7. `agent.getState` — V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:23,166-177`
8. `agent.saveSubAgent` — V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:24,217-220`
9. `agent.deleteSubAgent` — V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:25,226-229`
10. `agent.persistSubAgentBinding` — V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:26,236-241`

V3-only 新增面：

- skill 新增 7 个 `command/card/*`，见 `internal/module/skill/rpc.go:20-30`
- workspace 新增 3 个 `workspace/run/status/update|files/list|file/get`，见 `internal/module/workspace/rpc.go:42-93`
- orchestration 新增 6 个 `agent.snapshot`、`task/dag/create|get|list`、`task/node/update`、`orchestration/report`，见 `internal/sidecar/orch/orchestration/rpc.go:27-34`

## 结论

### Blocker

- thread 29 个 key 只是“名字齐全”，不是 V2 等价迁移。`internal/module/thread/rpc.go:96-99` 已明确只有 `/model`、`/personality`、`/approvals` 真正闭环，其余 command 路由仍是骨架；`thread/read`、`thread/resolve`、`thread/messages`、`thread/list` 也都明显弱化。
- `approval/respond` 虽已接到 `ApprovalResponder.Respond`，但 `RequestApproval` 没有任何 turn/provider/orchestration 外部调用点，见 `internal/platform/rpc/approval.go:71-103` 的 call hierarchy 结果；V3 审批链没有形成 V2 那样的 `request -> pending -> respond -> resolve` 闭环。
- orchestration 的 V2 12 方法面只保留了 3 个同名入口，`agent.submit*`、`agent.getReport`、`agent.getState`、report request/event、sub-agent 持久化相关全部缺失，见 `go-agent-v2/internal/apiserver/methods_orchestration.go:14-27` 对比 `internal/sidecar/orch/orchestration/rpc.go:12-34`。

### Warning

- 迁移文档中最不准确的是 `v3-module-migration-details.md` 和 `v2-v3-alignment-report.md`。前者高估了 thread/orchestration 的下沉与删除完成度，后者则同时保留了已过时和已修复两套结论。
- skill/workspace 的注册面数量是通的，但文档没有同步 V3 新增 RPC：skill 的 `command/card/*`，workspace 的 `status/update/files/list/file/get`。
- `85.3%` 只是注册覆盖率，不能作为迁移完成度。真实 blocker 集中在 thread command/history/listing 语义、approval lifecycle、orchestration 12 方法面缺口。

### OK

- 当前 V3 RPC 装配结构是清晰的：`HandlerMapResult` 聚合、5 个模块各自产出 `handler.Map`、app 层按 module 装配，见 `internal/platform/rpc/module.go:31-35`、`internal/app/modules.go:23-44`。
- skill/workspace 的 V2 基线方法数与当前 V3 注册面能够建立稳定映射，缺口主要不在这两个模块。

## 互辩：批判其他 4 份报告

### 对 audit-fx-rpc 的批判

1. `docs/plans/迁移/audit-fx-rpc.md:160` 把 “151 vs 74 的总方法数差距” 作为首要 blocker，但这份报告没有指出更严重的事实：大量已计入 74 的 live handler 其实只是骨架。最典型的是 thread 面，`internal/module/thread/rpc.go:58-80,96-99` 把一串 V2 方法压到统一 `SendCommand`，而 `internal/module/thread/command.go:18-35` 实际只支持 `/model`、`/personality`、`/approvals`、`/interrupt` 四类命令。按方法数看像“已迁移”，按行为看却是未完成；这比单纯的总量缩水更直接影响兼容性。
2. `docs/plans/迁移/audit-fx-rpc.md:172` 说 “当前 app.Module 下未发现实际挂空依赖”，这个结论过宽。`rpc.Module` 确实提供了 `ApprovalResponder`，见 `internal/platform/rpc/module.go:17-20`，但 `ApprovalManager.RequestApproval` 在仓内没有任何外部调用点；LSP `references` 只命中它自己的 `RequestUserInput` 包装，见 `internal/platform/rpc/approval.go:71-103`。也就是说，DI 图是闭合的，但 approval 请求半链根本没接上，这不是“挂空依赖”，而是“挂空功能”。
3. 这份报告审的是 fx + jrpc2，却没有把 push/approval callback 的未接线列成核心问题。`BindEventToNotify` 已实现 typed event -> jrpc2 notify 桥，见 `internal/platform/rpc/push.go:50-63`，但 LSP `references` 返回 0；`ApprovalManager.RequestApproval` 也没有外部调用，见 `internal/platform/rpc/approval.go:71-103`。这说明当前 RPC 层的 push/callback 基础设施大部分处于未使用状态，严重性不低于它列出的 optional 依赖与命名风格问题。
4. 作为 handler/register 审计，报告没有指出迁移文档已经和 live RPC 面冲突。文档 `docs/plans/迁移/v3-module-migration-details.md:58-78` 声称 `thread/loaded/list`、`thread/resolve`、`thread/debugMemory` 已合并/删除，`review/start` 已并入 turn；但实际这些 key 仍注册在 `internal/module/thread/rpc.go:44-75` 与 `internal/module/turn/rpc.go:74-77`。报告一边精确数 handler，一边漏掉这组 doc-code 偏差，结论不够挑剔。

### 对 audit-event-sm 的批判

1. `docs/plans/迁移/audit-event-sm.md:106,114` 正确抓到了 approval 事件没有驱动 `awaiting_user_input`，但它只看到了“事件没有映射到状态机”的后半段，没看到更严重的前半段：`ApprovalManager.RequestApproval` 本身没有任何外部调用者。LSP `references` 对 `internal/platform/rpc/approval.go:71` 只返回同文件 `RequestUserInput` 的内部包装 `internal/platform/rpc/approval.go:102`。这意味着不是“approval event 发了但没驱动状态”，而是“request -> pending” 这半条链都没有真正启动。
2. `docs/plans/迁移/audit-event-sm.md:129` 说 “已发布的 typed event 不存在完全无人订阅的硬孤儿，因为 LogSink 覆盖了全部六族”，这个 OK 口径过于宽松。日志订阅并不等于业务消费。`orchestration.CompleteTurn` 定义于 `internal/sidecar/orch/orchestration/service.go:282-301`，LSP `references` 返回 0；`BindEventToNotify` 定义于 `internal/platform/rpc/push.go:50-63`，LSP `references` 也返回 0。也就是说，`TurnCompleted` 和 typed push 事件虽然“被记录”，但并没有驱动业务闭环，这类事件在业务语义上仍然是孤儿。
3. `docs/plans/迁移/audit-event-sm.md:123` 只把双源发布问题收束到 `agentdto.StateChanged` 与 Codex translator，低估了碰撞面。实际 Claude translator 也发布 agent 级事件，见 `internal/provider/claudecli/event_map.go:35-53`；Codex translator 发布 `AgentLaunched` / `StateChanged` / `AgentStopped` / `AgentRecovering` / `AgentFailed`，见 `internal/provider/codexapp/event_map.go:39-74`；orchestration 自己也发布同一族事件，见 `internal/sidecar/orch/orchestration/events.go:25-63`。冲突不是单个 `StateChanged`，而是整组 agent 生命周期事件的归属冲突。
4. 这份报告没有追打迁移文档与代码的状态机口径冲突。`docs/plans/迁移/v2-v3-alignment-report.md:75` 仍写“当前 V3 还是 idle/thinking/running/paused/stopped/error 六态”，但实际状态集已经是 `internal/dto/agent/state.go:8-18,78-102` 的 10 状态、23 转换。event/state 主题下不指出这条文档失真，遗漏了最该纠偏的文档问题。

### 对 audit-store-sqlc 的批判

1. `docs/plans/迁移/audit-store-sqlc.md:140` 把 `agent_codex_binding` / `AgentThreadBindingStore` 缺失列成 blocker，但 V3 live 代码并没有任何对 `agent_codex_binding` 的运行时引用。LSP `text_search(path="internal", query="agent_codex_binding")` 返回 0。当前 app 真正装配的是 `binding.Module`，见 `internal/store/module.go:14-21`；thread live 路径也实际使用 `binding.Store` 的 `GetByAgentID/GetByProviderThread/SetArchived/DeleteByAgentID`，见 `internal/store/binding/contract.go:7-14`、`internal/store/binding/store.go:18-80`、`internal/module/thread/service.go:183-210`。所以这条 blocker 更像“按 V2 repo 名称对账”，不是“按当前运行时 call path 排严重度”。
2. `docs/plans/迁移/audit-store-sqlc.md:142-143` 把 `DBQueryStore placeholder` 和 “app 只接入 5 个 repo” 放进 blocker，但当前 app 根本没有装配 `dbquery.Module` 或 `topologyapproval.Module`；`internal/app/modules.go:23-44` 不含这两个模块，`internal/store/module.go:14-21` 也没有它们。相较之下，真正影响当前 live RPC 兼容性的 store 问题是已装配的 thread/binding store 仍不足以支撑 V2 `thread/read` / `thread/resolve` / `thread/loaded/list` 语义：`internal/store/thread/contract.go:48-64` 的 `Thread` 不含 `providerThreadID/uuid/archived`，而 V2 `thread/resolve` 明确依赖这些字段，见 `go-agent-v2/legacy-agentsdk/service/lifecycle/thread_lifecycle_logic.go:253-289`。报告没有把 blocker 放在 live surface 最痛的位置。
3. 这份报告花了大量篇幅讨论 `topology_approval.sql`，但完全没碰当前真正紧急的 `approval/respond` 闭环，而且 store 层事实上根本不在这条运行时路径上。V3 审批待处理项由 `ApprovalManager.pending map[string]*pendingApproval` 维护，见 `internal/platform/rpc/approval.go:19-24`；注册与完成都在内存里完成，见 `internal/platform/rpc/approval.go:125-147,230-256`。同时 app 并未装配 `topologyapproval.Module`。换言之，store/sqlc 审计把“topology approval 数据面”当成了“runtime approval 闭环”的替身，但代码并不支持这种优先级。
4. 报告没有把 store 结论和方法/文档口径对齐。文档 `docs/plans/迁移/v3-module-migration-details.md:340` 仍说 workspace jrpc2 面只有 `create|get|list|merge|abort`，但 live 代码 `internal/module/workspace/rpc.go:42-93` 已经新增了 `status/update`、`files/list`、`file/get`。既然这份报告在审 store 聚合范围和 app 装配，忽略这类与 store 直接相关的 doc-code 偏差，会让“迁移完整性”判断失焦。

### 对 audit-provider 的批判

1. `docs/plans/迁移/audit-provider.md:111-112` 把 `Driver/Session/TurnHandle` 基础实现面判成 OK，但这个 OK 没有穿透到 live 调用路径。当前 RPC 侧仍暴露大量 provider-facing thread 方法，见 `internal/module/thread/rpc.go:58-80`；而这些入口最终大多落到 `SendCommand`，`internal/module/thread/rpc.go:96-99` 已明写只有 `/model`、`/personality`、`/approvals` 真正闭环，`internal/module/thread/command.go:18-35` 也证明其余 command 根本不支持。provider 层即便接口实现齐全，只要 live RPC 进不去，这个 OK 就过宽。
2. `docs/plans/迁移/audit-provider.md:96-98` 抓到了 `ToolCallResponder` 和 session lifecycle，却漏掉了 provider 与 approval 闭环之间更严重的断层。provider translator 已经直接发布 `ToolApprovalRequested/Resolved`，见 `internal/provider/codexapp/event_map.go:132-144`；但 `ApprovalManager.RequestApproval` 在仓内没有外部调用者，LSP `references` 对 `internal/platform/rpc/approval.go:71` 只返回 `internal/platform/rpc/approval.go:102` 的内部包装。也就是说，provider 侧已经在发 approval 结果 DTO，但真正的 `request -> pending -> respond -> resolve` manager 链并没被 provider/session 使用。报告没有把这条断层列成 blocker，和 V2 完整性审查结论不一致。
3. 这份 provider 审计没有清理最关键的文档失真。`docs/plans/迁移/v2-v3-alignment-report.md:57` 仍写 “provider registry 无，internal/provider 目录当前不存在”，但 live 代码里 `internal/provider/unified/registry.go:15-56` 已有 registry，`internal/app/modules.go:37-39` 也明确装配了 `unified`、`claudecli`、`codexapp` 三个 provider 模块。provider 主题下不指出这条文档已过时，会让后续审查继续围绕错误前提展开。
4. 报告聚焦 driver/session 内部 parity，却没有指出 V2 `agent.submit` / `agent.submitPrompt` 已经没有 RPC 入口可把请求送到 provider。V2 在 `go-agent-v2/internal/apiserver/methods_orchestration.go:15-17` 注册了 `agent.launch` / `agent.submit` / `agent.submitPrompt`，而 V3 orchestration RPC 只剩 `agent.launch/stop/list/snapshot` 与若干 task stub，见 `internal/sidecar/orch/orchestration/rpc.go:12-34`。这意味着即便 provider 层有 `Session.StartTurn` 等实现，V2 orchestration submit 面仍然到不了 provider；provider 审计没把这条入口缺失拉进结论，方法面判断不够完整。
