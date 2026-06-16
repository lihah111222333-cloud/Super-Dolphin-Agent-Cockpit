# P5 波次 0 审查 B（approval + push + 风险）

## 1. approval 状态机

### 流程图

```text
provider JSON-RPC request
  -> AppServerClient.handleRPCEvent
     -> event.RequestID / RequestIDRaw / RespondResultFunc / DenyFunc 挂到 agentcore.Event
  -> server_event_handler.handleAgentEventRouting
     -> request_user_input ? 映射为 approvalMethodCommandExecution
     -> exec/file/skill approval ? 直接进入对应 approval method
  -> handleApprovalRequest
     -> approvalRequestSubAgentState
        -> sub-agent ? handleSubAgentAutoApproval
           -> RespondResultFunc(decisionPayload) || legacy SubmitSystemPrompt("yes")
           -> resume turn
     -> beginApprovalRequestDedup
        -> approvalInFlight sync.Map 去重
     -> StartApprovalStallHeartbeat
     -> resolveApprovalRequestDecision
        -> sendRequestToAll
           -> allocPendingRequest
           -> pending map[requestID]chan *Response
           -> WS client response
           -> handleClientResponse
           -> deliverPendingResponse
        -> or Wails/SSE fallback
           -> allocPendingRequest
           -> broadcastNotification
           -> UI 调 approval/respond
           -> approvalRespondTyped
           -> ResolvePendingRequest
     -> finalizeApprovalRequestDecision
        -> RespondResultFunc(decisionPayload)
           -> resume turn
        -> or legacy SubmitSystemPrompt("yes"/"no")
           -> resume turn
```

### 核心能力清单

- 核心状态不只在 `server_approval.go`。真正承载等待态的是 `connManagerState.pending map[int64]chan *Response`、`nextReqID`，定义在 `go-agent-v2/internal/apiserver/server_state_groups.go:21-27`，分配/投递逻辑在 `:102-137`。
- approval 去重状态不在 pending map，而在 `runtimeGuardState.approvalInFlight sync.Map`，定义在 `go-agent-v2/internal/apiserver/server_state_groups.go:394-420`。
- approval 入口是 `handleApprovalRequest`，主流程在 `go-agent-v2/internal/apiserver/server_approval.go:384-403`。
- request/user-input 不是独立链路，而是被桥接到 approval。`go-agent-v2/internal/apiserver/server_event_handler.go:223-233` 对 `request_user_input` 直接调用 `handleApprovalRequest(..., approvalMethodCommandExecution, ...)`。
- provider 侧恢复 turn 的关键不是 HTTP 回调，而是事件对象上的 `RespondResultFunc` / `DenyFunc`。这些字段定义在 `go-agent-v2/legacy-agentsdk/agentcore/types.go:119-126`，由 `go-agent-v2/legacy-agentsdk/codex/client_appserver_events.go:265-274` 在入站 JSON-RPC request 带 `id` 时挂入 `agentcore.Event`。
- 如果 approval 来自旧 provider 或无 request id，V2 仍保留 legacy 续跑路径：`submitApprovalLegacyDecision` 通过 `SubmitSystemPrompt(..., "approval")` 提交 `"yes"` / `"no"`，见 `go-agent-v2/internal/apiserver/server_approval.go:319-338`、`:449-453`。

### 主要方法列表

- 解析与归一化
  - `approvalRequestIdentity` / `approvalInflightKey` / `normalizeApprovalDecision` / `normalizeApprovalResultPayload`
  - 负责 requestId 提取、决策别名映射、结构化 amendment 决策兼容，见 `go-agent-v2/internal/apiserver/server_approval.go:90-310`
- 子 agent 判定
  - `detectSubAgentForApproval`
  - `approvalRequestSubAgentState`
  - 见 `go-agent-v2/internal/apiserver/server_approval.go:116-132`、`:405-411`
- 主流程
  - `handleApprovalRequest`
  - `beginApprovalRequestDedup`
  - `resolveApprovalRequestDecision`
  - `finalizeApprovalRequestDecision`
  - 见 `go-agent-v2/internal/apiserver/server_approval.go:384-454`
- fallback 与恢复
  - `handleSubAgentAutoApproval`
  - `handleWailsModeApproval`
  - `submitApprovalLegacyDecision`
  - 见 `go-agent-v2/internal/apiserver/server_approval.go:319-382`
- 前端回调入口
  - `approvalRespondTyped`
  - 见 `go-agent-v2/internal/apiserver/server_approval.go:456-483`

### 调用关系

- `handleAgentEventRouting` -> `handleApprovalRequest`
  - `go-agent-v2/internal/apiserver/server_event_handler.go:221-245`
- `handleApprovalRequest`
  - -> `approvalRequestSubAgentState`
  - -> `handleSubAgentAutoApproval`
  - -> `beginApprovalRequestDedup`
  - -> `providerAdapter.StartApprovalStallHeartbeat`
  - -> `resolveApprovalRequestDecision`
  - -> `finalizeApprovalRequestDecision`
  - 调用层次见 `go-agent-v2/internal/apiserver/server_approval.go:384-403`
- `resolveApprovalRequestDecision`
  - -> `sendRequestToAll`
  - -> `handleWailsModeApproval`
  - -> `normalizeApprovalResultPayload`
  - 见 `go-agent-v2/internal/apiserver/server_approval.go:425-439`
- `approvalRespondTyped`
  - -> `ResolvePendingRequest`
  - 见 `go-agent-v2/internal/apiserver/server_approval.go:462-483`

### 去重机制

- approval 主去重键是 `agentID:method:requestID`，由 `approvalInflightKey` 生成，见 `go-agent-v2/internal/apiserver/server_approval.go:104-112`。
- 只有 requestId 存在时才去重。`beginApprovalRequestDedup` 对无 requestId 的请求直接放行，见 `go-agent-v2/internal/apiserver/server_approval.go:413-423`。
- 去重容器是 `runtimeGuardState.approvalInFlight sync.Map`，`tryBeginApprovalState` / `endApprovalState` 实现在 `go-agent-v2/internal/apiserver/server_context.go:376-388` 和 `go-agent-v2/internal/apiserver/server_state_groups.go:394-420`。
- `request_user_input` 还有一套独立的 auto-respond TTL 去重，不复用 approvalInFlight。它依赖 `requestUserInputAutoResponder.seen sync.Map`，TTL 5 分钟，见 `go-agent-v2/internal/apiserver/server_user_input_responder.go:9-41`、`go-agent-v2/internal/apiserver/server_event_handler.go:509-557`。

### 子 agent 自动审批逻辑

- 主判定先查 `agentThreadStore.ExistsRunning(ctx, agentID)`，超时 1.2s，见 `go-agent-v2/internal/apiserver/server_approval.go:18`、`:116-132`。
- 查库失败时不会直接认定 sub-agent，只记录 `lookup_error`。
- 第二判定来自 UI 运行态：`uiRuntime != nil && !uiRuntime.IsMainAgent(agentID)` 时也视为 sub-agent，见 `go-agent-v2/internal/apiserver/server_approval.go:405-410`。
- 一旦判定为 sub-agent，直接 `approve/accept`，不进入 UI 等待。优先走 `RespondResultFunc`，否则退回 legacy `"yes"`，见 `go-agent-v2/internal/apiserver/server_approval.go:339-349`。

### 前端等待机制（channel / timeout）

- WS 主路径
  - `sendRequest` 分配 `reqID + chan *Response`，把 request 放入连接 outbox，然后 `select` 等待 `ch` 或 5 分钟超时，见 `go-agent-v2/internal/apiserver/server_conn.go:196-229`。
  - 前端响应回包后，由 `server_conn_ws.go` 的 `handleClientResponse` -> `deliverPendingResponseState` 投递到 channel，见 `go-agent-v2/internal/apiserver/server_conn_ws.go:221-230`、`go-agent-v2/internal/apiserver/server_context.go:223-228`。
- Wails/SSE fallback
  - `handleWailsModeApproval` 也用 `allocPendingRequest` 拿 channel，然后 `broadcastNotification` 给 UI，最后等待 `approval/respond` 解锁，超时同样是 5 分钟，见 `go-agent-v2/internal/apiserver/server_approval.go:351-382`。
- 结论
  - approval 等待态的抽象中心不是 handler，而是 pending request manager。

### `request_user_input` 如何触发 approval

- provider 侧 `jsonRPCToEvent` 对未映射但命中 `codex/event/`、`item/` 等前缀的方法，直接把原始 method 当 event type，见 `go-agent-v2/legacy-agentsdk/codex/client_appserver_events.go:731-772`。
- `server_event_handler.isRequestUserInputEvent` 通过 method/type 后缀匹配 `request_user_input` / `requestUserInput`，见 `go-agent-v2/internal/apiserver/server_event_handler.go:486-492`。
- 若当前 approval policy 为 `never`，走 auto-respond；否则统一桥接到 `handleApprovalRequest(..., approvalMethodCommandExecution, ...)`，见 `go-agent-v2/internal/apiserver/server_event_handler.go:223-233`。
- 因此 `request_user_input` 在 V2 不是独立状态机，而是 approval 状态机的一个别名入口。

### approval 结果如何恢复 turn 执行

- 首选路径是 `event.RespondResultFunc(decisionPayload)`。这会直接向 provider 返回 JSON-RPC result，见 `go-agent-v2/internal/apiserver/server_approval.go:341-346`、`:441-447`。
- 若没有 `RespondResultFunc`，则退回 legacy 路径，通过 `SubmitSystemPrompt(..., "yes"/"no")` 驱动 provider 继续执行，见 `go-agent-v2/internal/apiserver/server_approval.go:348`、`:449-453`。
- 若响应失败，会调用 `DenyFunc` 做安全拒绝，见 `go-agent-v2/internal/apiserver/server_approval.go:311-318`、`:443-446`。

### 250 行可行性判断

- 结论：不能。
- 原因 1：若覆盖“request_user_input 桥接 + sub-agent auto-approve + requestId 去重 + pending request manager + WS 回执 + SSE/Wails fallback + legacy submit fallback + structured decision normalization”，R0c 实际依赖至少横跨 6 个文件，不是 `approval.go` 单文件 250 行问题。
- 原因 2：`server_approval.go` 本身 483 行里，真正 approval-specific 的一部分只是决策归一化和编排；pending map、notify、response delivery 在 `server_conn.go` / `server_state_groups.go`。
- 原因 3：`request_user_input` 和 legacy path 不是可忽略细节，它们直接决定 turn 是否会卡死。
- 推断：若 R0c 严格限制到 250 行，只能成立于“R0a 先提供 pending request manager + outbound notify/callback abstraction，且本波不做 legacy/Wails fallback、不做 request_user_input 桥接、不做 amendment decision 兼容”。这已经不是 V2 能力覆盖。

## 2. push 通道

### V2 机制

- 事件名到 JSON-RPC method 的映射集中在 `go-agent-v2/internal/apiserver/notifications.go:10-100`。
- push envelope 是标准 JSON-RPC notification：`Notification{JSONRPC, Method, Params}`，定义在 `go-agent-v2/internal/apiserver/server_transport.go:29-33`，版本常量在 `:13`。
- 真正的 fanout 不在 `server_transport.go`，而在 `go-agent-v2/internal/apiserver/server_conn.go:136-170`：
  - 先走 `notifyHook`
  - 再 fanout 到 SSE clients
  - 再 fanout 到所有 WS 连接 outbox
- `go-agent-v2/internal/apiserver/server_payload.go:32-46` 的 `notify()` 还会附带 UI runtime 同步、thread/sidebar refresh、legacy mirror 延迟刷新的副作用。

### V3 技术选型

- 选项 1：jrpc2 原生 notifier 机制
  - 仓库契约验证的接口是 `Server.Notify` / `ClientOptions.OnNotify`，需要回执时用 `Server.Callback` / `ClientOptions.OnCallback`，见 `docs/契约/jrpc2-convention.md:482-486`。
  - 只有 `ServerOptions.AllowPush=true` 时可用，见 `docs/契约/jrpc2-convention.md:490-492`。
  - 这是唯一能自然覆盖“普通通知 + approval 反向请求/回执”的方案。
- 选项 2：SSE bridge
  - 只天然支持 server -> client，契约文档明确写了“若要完整双向 RPC 仍需额外上行通道”，见 `docs/契约/jrpc2-convention.md:494-495`。
  - 可作为波次 0 过渡方案，只承担只读 UI 推送；不能等价替代 approval 的 callback/request-response。
- 选项 3：WebSocket 双向
  - 契约文档明确写了浏览器场景优先 WebSocket，见 `docs/契约/jrpc2-convention.md:492-495`、`:667-747`。
  - 这不是和 jrpc2 原生 notify 对立的方案，而是它的推荐 transport。

### 推荐结论

- 推荐主线：`WebSocket + jrpc2 Server.Notify/Server.Callback + AllowPush=true`。
- SSE 只能作为降级或兼容桥，不应作为 approval 主通道。
- “`server.Push`”不是本仓库已核验的契约表述。本仓库 `go.mod` 锁定 `github.com/creachadair/jrpc2 v1.3.5`，而本地契约文档统一使用 `Server.Notify` / `Server.Callback`，见 `go.mod:5-13`、`docs/契约/jrpc2-convention.md:107-112`、`:482-486`。
- 当前 V3 `internal/platform/rpc/server.go:46-52` 只启用了 `channel.Line` TCP，没有 `AllowPush`、没有 notify/callback、没有浏览器 push 通道。

### jrpc2 契约要求

- push 属于非标准 JSON-RPC 扩展，transport 和 client 必须显式支持，见 `docs/契约/jrpc2-convention.md:488-495`。
- HTTP bridge 不能承担浏览器实时 push。`jhttp.NewBridge` 不会把 server push 转发给远端 HTTP client，见 `docs/契约/jrpc2-convention.md:492-493`、`:571`、`:1038`。
- WebSocket 不是 jrpc2 官方内建 transport，需要自己实现 `channel.Channel` 适配器，见 `docs/契约/jrpc2-convention.md:667-747`。

## 3. transport_ws

### V2 实现

- WebSocket 库是 `github.com/gorilla/websocket`，见 `go-agent-v2/internal/apiserver/server_conn_ws.go:10`、`go-agent-v2/internal/apiserver/server.go:9`。
- 连接模型
  - `connEntry{ws, outbox chan wsOutbound, closeCh}`，见 `go-agent-v2/internal/apiserver/server_conn.go:51-61`
  - 单独 `writeLoop` + ping loop + read deadline，见 `go-agent-v2/internal/apiserver/server_conn.go:90-108`、`go-agent-v2/internal/apiserver/server_conn_ws.go:52-69`
- 协议模型
  - 自己定义 `rpcEnvelope`，手工区分 request / notification / client response，见 `go-agent-v2/internal/apiserver/server_conn_ws.go:72-130`
  - `readLoop` 中直接 `dispatchRequest`，见 `go-agent-v2/internal/apiserver/server_conn_ws.go:133-208`
  - 也就是说，V2 WS 并没有接入某个可复用的 RPC channel 抽象；它本身就是 transport + protocol dispatcher。

### V3 集成方案

- 当前 V3 `internal/platform/rpc/server.go:37-58` 只支持 `net.Listener + server.NetAccepter(listener, channel.Line)`。
- 若扩展到 WS，最低要求是新增 `internal/platform/rpc/transport_ws.go`，实现类似契约文档示例里的 `NewWebSocketChannel(conn) channel.Channel`，见 `docs/契约/jrpc2-convention.md:667-739`。
- 推断：仅在 `server.Run` 里把 `channel.Line` 换掉并不够，因为当前入口是裸 TCP listener，不是 HTTP upgrade 流程；WS 需要独立的 HTTP server / upgrader 接入点。
- 推断：如果 push 需要脱离 handler 主动广播，`server.Loop` 还不够，需要额外保存每个连接对应的 `*jrpc2.Server` 或等价 notifier handle；否则只能在 handler `ctx` 内部调用 `jrpc2.ServerFromContext(ctx).Notify(...)`。
- 因此可行，但不是“小改 `server.go` 一行”的工作量。至少要新增：
  - WS upgrader / HTTP endpoint
  - `channel.Channel` 适配器
  - 连接注册表
  - push bridge
  - `AllowPush=true`

## 4. codec

### V2 实际职责

- `go-agent-v2/internal/apiserver/server_payload.go` 不是 transport codec 主体。
- 前 80 行显示它主要做的是：
  - `notify()` 侧的 payload 归一化
  - UI refresh / legacy mirror 节流与派生事件
  - 见 `go-agent-v2/internal/apiserver/server_payload.go:19-90`
- 真正的“解析”只是在后半段把 `string` / `[]byte` / `json.RawMessage` 解析成 `map[string]any`，并合并 alias、error、nested item 字段，见 `go-agent-v2/internal/apiserver/server_payload.go:316-412`。
- 没有二进制 codec、没有压缩、没有自定义 framing。
- 真正的 JSON-RPC 线协议编解码在：
  - `go-agent-v2/internal/apiserver/server_transport.go`
  - `go-agent-v2/internal/apiserver/server_conn_ws.go`

### V3 是否需要

- 对 jrpc2 主 RPC 通道，不需要再为 Wave 0 复制一个 412 行 `codec.go`。
- 当前 V3 `internal/platform/rpc/codec.go:1-4` 只是占位，这个状态本身没有问题。
- 真正需要迁的是两类东西：
  - JSON-RPC 线协议：jrpc2 已内置
  - V2 的 payload shaping / UI refresh envelope：若仍要保留，应该并入 `push.go` 或 UI bridge，而不是误判成 codec
- 结论：R0a 里“codec”应降级为很薄的 helper 任务；412 行同体量替代没有技术必要。

## 5. fx 闭环时序

- 当前 `internal/platform/rpc/module.go:11-18` 只 `fx.Provide(NewServer)`。
- 当前 `internal/app/modules.go:15-29` 把 `rpc.Module` 作为 runner 注入 runtime。
- 当前 `internal/platform/rpc/server.go:21-27` 初始化的 `methods` 就是空 `handler.Map{}`；`Run()` 没有任何“至少注册一个 handler”校验，见 `:37-58`。
- 当前仓库里没有任何模块向 `rpc.Server.Register` 提供 `handler.Map`，`Register` 也没有被 `fx.Invoke` 驱动，见 `internal/platform/rpc/server.go:29-35` 和仓库检索结果。
- 推断：
  - R0b 完全可以只做 `value-group` 收集 + `fx.Invoke` 注册闭环。
  - 不需要占位 handler，只要 `[]handler.Map` 为空时仍调用 `Register(maps...)` 或不调用都可。
  - server 能正常启动，只是任何 RPC 请求都会变成 method-not-found。
- 因此 R0b 的正确目标应是：
  - 引入 `group:"rpc_handlers"` 的 `handler.Map` 片段
  - 用 `fx.Invoke` 把这些片段注册到 `rpc.Server`
  - 允许空 slice 启动
  - 用 `fx.ValidateApp` 验证图闭环

## 6. 依赖关系

### 真实依赖图

```text
R0a(push + pending manager + WS transport)
  -> R0c(approval)

R0b(fx handler.Map value-group)
  -> R1/R2/R3... 业务模块注册

R0d(ThreadScope/StrictBind 增强)
  -> R1/R2/R3... 业务 handler 参数约束

R0a
  -> R1/R2/R4/R6 中所有需要实时通知的方法
```

### 具体判断

- `R0c` 是否依赖 `R0a`
  - 是，至少依赖其中的 push/callback/pending-request 子集。
  - 原因是 V2 approval 直接依赖 `sendRequestToAll`、`broadcastNotification`、`ResolvePendingRequest`。
  - 若 R0a 只做 codec 占位，不做 push/pending manager，则 R0c 无法闭环。
- `R0d` 是否影响 `R0a/R0c`
  - 基本不影响。
  - 当前 `approval/respond` 参数是 `requestId/approved/decision`，不走 `ThreadScope`，见 `go-agent-v2/internal/apiserver/server_approval.go:456-483`。
  - push bridge 也是事件 fanout，不依赖 thread middleware。
- `R0b` 是否需要 `R0a` 先完成
  - 功能上不需要。
  - 当前 server 已可在空 `handler.Map` 下启动，R0b 只需补 fx 注册闭环。
  - 但实现层面与 `internal/platform/rpc/server.go` 有潜在改动重叠；若 R0a 要改 server 结构，需避免并行改同一文件。

### 建议执行顺序

1. 先做 R0a 的“真正关键子集”：`push.Bridge`、pending request manager、`AllowPush`、WS transport 边界。
2. 并行做 R0b：handler.Map value-group + fx.Invoke 闭环，避免触碰 push 细节。
3. 并行做 R0d：`ThreadScope` 多 field、`StrictBind` 严格绑定。
4. 在 R0a 的 push/pending abstraction 稳定后再做 R0c。
5. 若波次 0 必须强行并行，至少要把 R0c 改写为依赖接口，不直接依赖 transport 实现。

## 7. 结论

### Blocker

- `R0c <= 250` 不能覆盖 V2 approval 实际能力；当前预算低估了跨文件状态机复杂度。
- 当前 V3 没有 `AllowPush`、没有 WS transport、没有 notifier/callback、没有 pending request manager；approval 闭环缺基础设施。
- 当前 `server.Run()` 是 `channel.Line` TCP 单通道实现，不能直接承担浏览器 push。
- 把 `server_payload.go` 整体等量迁成 `codec.go` 是任务定义错误；其中大部分逻辑属于 push/UI bridge，不属于 codec。

### Improvement

- 重新切分 R0a：把 `push + pending manager + transport_ws` 作为关键路径；把 `codec` 降为薄 helper。
- 重新定义 R0c：只保留 approval orchestration 和 decision normalization，把 transport 等待/回执抽到 R0a 提供的接口下。
- 明确浏览器实时通道选型：主线用 `WebSocket + jrpc2 Notify/Callback`；SSE 只做降级，不做等价替代。
- R0b 不需要占位 handler；空 `handler.Map` 启动即可，先把 fx 图打通，再等各模块逐波次注册。

---

## 互辩：对 reviewer-A 的批判

### A 的事实错误

- 未发现 A 存在明显的“引用代码与原文不符”式硬错误；它对 `server_payload.go`、`notifications.go`、`errors.go`、`StrictBind` 的行号描述基本成立。
- 但 A 对 R0a 的源量基线有事实口径偏窄的问题。A 在 `p5-wave0-review-A.md:51-56` 只把 `server_payload.go + server_conn_ws.go + notifications.go` 算成 792 行，却漏掉了直接承载 push / callback / pending wait 的共享逻辑：
  - `go-agent-v2/internal/apiserver/server_conn.go:40-101` 的 `connEntry`、outbox、write loop、ping/write 相关基础设施
  - `go-agent-v2/internal/apiserver/server_conn.go:136-257` 的 `broadcastNotification`、`sendRequest`、`ResolvePendingRequest`、`allocPendingRequest`
  - `go-agent-v2/internal/apiserver/server_state_groups.go:21-137` 的连接表和 `pending map[int64]chan *Response`
- A 在 `p5-wave0-review-A.md:18` 说 `pending response` 相关逻辑可随 jrpc2 接管而删除，这个表述过满。V2 approval 主流程在 `go-agent-v2/internal/apiserver/server_approval.go:351-382`、`:462-483` 明确共用 `allocPendingRequest` / `ResolvePendingRequest` 等等待通道。可以删除的是 V2 手写 envelope 分发器，不是“等待相关能力本身”。

### A 的 approval 低估

- A 在 `p5-wave0-review-A.md:29-33` 概括了 `server_approval.go` 的主流程，但仍漏掉了一个关键入口：`request_user_input` 并不是独立通道，而是由 `go-agent-v2/internal/apiserver/server_event_handler.go:223-233` 直接桥接到 `handleApprovalRequest(..., approvalMethodCommandExecution, ...)`。
- A 在 `p5-wave0-review-A.md:61-70` 接受“删掉 fallback 后 R0c <= 250 可成立”，但这个估计没有把 typed 通道仍必需的三块算进去：
  - provider 侧 callback 绑定。`go-agent-v2/legacy-agentsdk/codex/client_appserver_events.go:265-274` 把 `RespondResultFunc` / `DenyFunc` 挂到 event 上，这是 approval 恢复 turn 的核心，不是 legacy 专属分支。
  - shared awaiter。`go-agent-v2/internal/apiserver/server_conn.go:196-257` 与 `go-agent-v2/internal/apiserver/server_state_groups.go:21-137` 承载了 request/response 相关性和 pending wait。
  - turn 状态恢复。V3 已定义 `awaiting_user_input -> turn_running` 触发器，见 `internal/dto/agent/state.go:28-29`、`:90-95`，但 `internal/sidecar/orch/orchestration` 内没有任何 `TriggerUserInputRequested` / `TriggerUserInputResolved` 使用点。
- 因此 A 在代码量节接受 `R0c <= 250`，与它自己在 `p5-wave0-review-A.md:88-91` 识别出的“缺 user input 恢复接口链”是内在冲突的。缺口既然存在，就不应被当作 250 行内可自然吸收的细节。

### A 的契约解读问题

- A 对 strict binding 和错误码保留区间的解读基本准确：
  - `docs/契约/jrpc2-convention.md:117-118,248-249,285`
  - `docs/契约/jrpc2-convention.md:581-589`
- A 对 push 契约的提炼不够完整。契约第 6 节不是只说 `Server.Notify`，还明确写了“若需要反向请求并等待结果: `Server.Callback` + `ClientOptions.OnCallback`”，见 `docs/契约/jrpc2-convention.md:482-486`、`:567-570`。
- 这个遗漏会直接误导波次拆分。approval 不是纯 notification；如果只按 A 在 `p5-wave0-review-A.md:41-43` 的 `Notify + AllowPush + WS adapter` 去理解，R0a 和 R0c 的耦合会被低估。
- 契约还写明 server push 可用性取决于 transport 和 client 两端支持，见 `docs/契约/jrpc2-convention.md:488-495`。A 主要强调了 server 侧 `AllowPush=true`，但没有把 client 侧 `OnNotify/OnCallback` 作为成对要求提出。

### A 的代码量宽松问题

- A 在 `p5-wave0-review-A.md:51-57` 把 R0a 的压缩判断建立在 792 行直接对照文件之上，这个基线本身偏低。若把 R0a 真实依赖的 V2 共用逻辑也算入，对照基线至少还要加上：
  - `server_conn.go:40-101`
  - `server_conn.go:136-257`
  - `server_state_groups.go:21-137`
  - 合计至少再多约 300 行量级
- A 在 `p5-wave0-review-A.md:54-56` 默认 `jrpc2` 接管后会主要做减法，但 `channel.Line -> WebSocket` 并不是删代码迁移。根据契约文档，`jrpc2` 没有官方 WebSocket transport，需要自己实现 `channel.Channel` 适配器，见 `docs/契约/jrpc2-convention.md:667-747`。
- 当前 V3 `internal/platform/rpc/server.go:37-58` 还是裸 `net.Listener + channel.Line`。要变成浏览器可用的 WS push，至少要新增：
  - HTTP upgrade 入口
  - `wsChannel` 适配器
  - 连接注册表
  - `AllowPush=true`
  - `Notify/Callback` 配套 bridge
- 所以 A 给出的 `220~380` 和 `<=500` 只能在“把一部分能力后移到别的子任务”时成立；如果按任务文案理解为 R0a 本身闭环 `codec + ws + push`，这个估计偏宽松。

### A 的 R0b 难度忽略

- A 在 `p5-wave0-review-A.md:110-115` 说 `handler.Map` group 可以空跑，这一点本身是对的。jrpc2 `handler.Map.Assign` 对缺失方法直接返回 `nil`，见 `/Users/mima0000/go/pkg/mod/github.com/creachadair/jrpc2@v1.3.5/handler/handler.go:22-27`；server 在 assign 失败时返回 `MethodNotFound`，见 `/Users/mima0000/go/pkg/mod/github.com/creachadair/jrpc2@v1.3.5/server.go:332-344`。
- 但 A 对“空 group 可跑”与“fx 图闭环”之间的差别处理得不够尖锐。当前 `internal/app/modules.go:15-25` 已经接入 `thread.Module`，而 `internal/module/thread/service.go:34-39` 构造函数依赖 `threadstore.Store`、`bindingstore.Store`、`SessionProvider`。`app.Module` 里并没有这些 provider/store module。
- 这意味着 R0b 若以“应用级 fx 图闭环”为目标，难点根本不在 `rpc_methods` 空列表，而在当前 app assembly 已经因 thread 依赖缺失而不闭环。A 虽然在 `p5-wave0-review-A.md:127-129` 提到了这一点，但前文对“group 先空跑”的接受容易让人低估真正 blocker。

### 综合互辩结论

- A 的 V2 对照和 strict binding/错误码问题识别总体可靠，没有发现严重的代码事实捏造。
- A 的主要问题不在“看错代码”，而在“把成立前提放得太宽”：
  - 把 R0a 的源量基线算窄了
  - 把 approval typed 主路径所需的 awaiter/callback/state 恢复成本压低了
  - 把 jrpc2 push 契约提炼成了 `Notify`，却没有把 `Callback` 和 client 端契约一起抬出来
  - 把 `rpc_methods` 空跑和“fx 图闭环”混在了一起
- 互辩后的更稳妥结论是：
  - R0a 先做 `push/callback/pending/transport_ws` 真正关键子集
  - R0b 明确区分“RPC 注册骨架闭环”和“app 级 assembly 闭环”
  - R0c 不应再以 `<=250` 作为默认成立目标，除非任务文案显式删除 `request_user_input` 共通入口和状态恢复责任
