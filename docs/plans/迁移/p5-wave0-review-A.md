# P5 波次 0 审查 A（V2 对照 + 可行性）

## 1. V2 对照

### `go-agent-v2/internal/apiserver/server_payload.go`

- `server_payload.go:19-41` 的入口不是纯 codec，而是 `Notify -> UI/runtime 同步 -> legacy mirror 延迟 -> broadcast` 链路。
- `server_payload.go:114-231` 还包含 UI 刷新节流、`ui/thread/changed` / `ui/sidebar/changed` 桥接，以及把通知回放到 UI runtime 的逻辑。
- `server_payload.go:288-412` 才更接近 payload 归一化能力：`parseMapAny`、字段抽取、别名合并、错误字段整理。
- 结论：R0a 的 `codec.go` 只能覆盖 `server_payload.go` 的“payload 归一化”子集，不能把整个 412 行文件等价视为 codec。
- 结论：如果波次 0 不打算保留 V2 的 legacy mirror、Wails/UI runtime 同步、UI 节流刷新，必须在任务定义中显式声明删减；否则 R0a 覆盖不足。

### `go-agent-v2/internal/apiserver/server_conn_ws.go`

- `server_conn_ws.go:15-50` 的核心是连接上限、WS upgrade、读超时、pong 延长、写循环、ping 循环和连接生命周期管理。
- `server_conn_ws.go:72-106` 定义了 `rpcEnvelope`、ID 解析和 `jsonrpc=2.0` 校验。
- `server_conn_ws.go:119-277` 还包含入站分类、通知/请求分流、慢方法异步化、客户端响应相关性恢复和错误响应编码。
- 结论：如果 V3 直接让 `jrpc2` 接管请求分发，`dispatchReadLoopRequest`、`buildClientResponse`、pending response 相关逻辑可以删除；`transport_ws.go` 只需保留 WS channel 适配、upgrade、ping/pong、close 和连接注册。
- 结论：R0a 的 `transport_ws.go` 能覆盖“transport”能力，但其等价替代前提是改为 `channel.Channel` 适配，而不是继续手写 V2 那套 envelope 分发器。

### `go-agent-v2/internal/apiserver/notifications.go`

- `notifications.go:10-100` 的能力很集中：事件类型到 RPC method 名的映射，以及未知事件 fallback 到 `agent/event/` 前缀。
- 结论：R0a 的 `push.go` 只要提供“事件到 method 的稳定映射 + server->client notify”即可覆盖这一块。
- 结论：由于当前 V3 已有强类型事件 DTO 和总线，`push.go` 可以直接按 DTO 类型映射 method，不必保留 V2 的字符串事件表规模。

### `go-agent-v2/internal/apiserver/server_approval.go`

- `server_approval.go:71-132` 的基础能力是 request identity 归一化、payload 容错读取和 sub-agent 检测。
- `server_approval.go:169-310` 的核心复杂度在 decision 标准化，包含 string/bool/structured amendment 三类输入。
- `server_approval.go:339-454` 的核心流程是 sub-agent 自动审批、去重、防 stall heartbeat、前端等待、结果规范化、typed respond / legacy submit 双路径收尾。
- 结论：R0c 文案列出的“去重、子 agent 自动审批、前端等待、结果规范化”覆盖了主流程骨架，但没有覆盖 V2 的 heartbeat、Wails fallback、legacy `yes/no` 提交路径。
- 结论：如果波次 0 只保留 typed approval 通道，删除 Wails/legacy prompt fallback，R0c 是可压缩的；如果要求完整兼容 V2 三条收尾路径，`≤250` 过紧。

## 2. jrpc2 契约

- `docs/契约/jrpc2-convention.md:117-118,248-249,285` 明确要求公共方法在严格参数模式下使用 `handler.Check(...).AllowArray(false).SetStrict(true).Wrap()`。
- 当前 `internal/platform/rpc/handler.go:23-25` 的 `StrictBind` 仅返回 `handler.New(fn)`，不满足对象参数限定，也不做 strict field check。
- `docs/契约/jrpc2-convention.md:581-589` 明确区分协议层标准错误码与业务层自定义错误码，业务码必须避开保留区间 `-32768~-32000`。
- 当前 `internal/platform/rpc/errors.go:5-20` 曾占用保留区间内业务码，与契约冲突；这一项属于波次 0 必修，不宜后移。
- `docs/契约/jrpc2-convention.md:482-492` 规定 server push 走 `Server.Notify`，只有 `ServerOptions.AllowPush=true` 才可用，HTTP bridge 不能承担浏览器 push。
- `docs/契约/jrpc2-convention.md:667-669,713-740` 明确 WebSocket 需要自定义 `channel.Channel` 适配器。
- 当前 `internal/platform/rpc/server.go:46-52` 只用 `channel.Line`，且没有打开 `AllowPush`；R0a 需要同时补 `transport_ws.go` 和 `AllowPush=true`。
- `docs/契约/jrpc2-convention.md:182,1033` 允许 `func(context.Context, *jrpc2.Request)` 形式的 raw handler，并明确只有参数结构确实动态时才应该保留 raw passthrough。
- 结论：R0d 新增 `RawHandler(fn)` 是契约内方案，不需要新 middleware 接口。

## 3. 代码量

### R0a `≤500`

- 源文件总量是 `413 + 278 + 101 = 792` 行；如果按“逐行迁移”理解，`≤500` 不成立。
- 但 V2 的 792 行里有大量可以直接删除的兼容层：
- `server_payload.go` 的 UI runtime 同步、legacy mirror、节流通知不是 codec/transport 的必选能力。
- `server_conn_ws.go` 的 envelope 调度、慢方法异步化、pending response 管理会被 `jrpc2` 服务端接管。
- `notifications.go` 的字符串事件表可被 V3 typed event bridge 压缩。
- 在“只保留 jrpc2 所需最小 transport + payload helper + push bridge”的前提下，R0a 的量级可落在约 `220~380` 行，`≤500` 合理。
- 如果要求同时保留 V2 的 UI mirror、Wails 兼容、非原子视图延迟镜像，`≤500` 不合理。

### R0c `≤250`

- V2 `server_approval.go` 是 484 行，其中高成本部分集中在三类兼容逻辑：Wails pending request、legacy `yes/no` prompt 提交、method-specific decision fallback。
- 如果 V3 只保留：
- requestId 去重
- sub-agent 自动审批
- typed 前端等待
- decision/result 标准化
- typed respond 收尾
- 那么 `≤250` 可以成立。
- 如果还要覆盖 V2 的 Wails fallback、legacy prompt relay、command/network amendment 兼容矩阵，`≤250` 偏紧。
- 结论：R0c 的行数目标成立，但前提是先在任务定义中删掉 V2 fallback 分支。

## 4. 依赖方向

- `internal/archtest/dependency_direction_test.go:90-95` 明确规定 `internal/platform` 不能 import `internal/module/*`。
- 因此 `platform/rpc/approval.go` 不能 import `module/turn`，也不能 import `module/orchestration`。
- 反向依赖同样不合理：module 层不应依赖 transport 细节去实现审批。
- 可行方案是把 `approval.go` 依赖压成窄接口，由 assembly 注入实现。

### `approval.go` 的最小依赖面

- `SubAgentDetector`
- `ApprovalResponder`
- `Awaiter` 或 `PendingApprovalStore`
- `CapabilityResolver` 或 `SessionResolver`

### 当前仓内缺失的端口

- `internal/contract/provider.go:22-50` 的 `Session`/`ToolCallResponder` 没有审批响应接口，只有 tool call result/error responder，无法承载 `request_user_input` 的 approval resolve。
- `internal/sidecar/orch/orchestration/contract.go:10-17` 没有 `UserInputRequested` / `UserInputResolved` 之类状态推进方法。
- `internal/dto/agent/state.go:28-29,90-95` 已经定义了 `user_input_requested` / `user_input_resolved` 触发器，但仓内没有任何使用点。
- 结论：R0c 不只是一个 `platform/rpc/approval.go` 文件问题，还缺一条“审批结果 -> orchestration 状态恢复”的接口链。

### `push.go` 与事件总线

- 当前总线由 `internal/platform/bus/module.go:10-23` 提供 `*event.Dispatcher`。
- 订阅方式在仓内已有现成模式：`internal/platform/bus/sink.go:21-97` 和 `internal/platform/bus/router.go:18-23` 都直接使用 `event.Subscribe(dispatcher, func(ev T) { ... })`。
- 因此 `platform/rpc/push.go` 直接 import `github.com/kelindar/event` 是可行的。
- 如果希望统一取消订阅和 panic 隔离，可复用 `internal/platform/bus.Subscription`、`internal/platform/bus.ResilientSubscribe`。
- 结论：`push.go` 不需要 import module 层，只需依赖 `*event.Dispatcher` 和 DTO 事件类型。

## 5. fx 闭环

- 当前 assembly 在 `internal/app/modules.go:15-25` 只接入了 `config/db/bus/rpc/platformrunner/statemachine/thread`。
- 当前没有接入 `turn`、`orchestration`，也不存在 `skill`、`uistate`、`dashboard` 这三个包的实现。
- 当前 `rpc.Server.Register` 只存在于 `internal/platform/rpc/server.go:29-35`，仓内没有任何调用点。
- 当前仓内也没有 `group:"rpc_methods"` 的实际 provider；只有 `docs/架构决策/架构骨架/skeleton-fx.md:192-215` 给出了设计草图。

### `handler.Map` value-group 是否可行

- 可行。
- `fx` 的 value-group 方案在设计文档里已经固定为：
- 模块侧 `fx.Out` 提供 `handler.Map \`group:"rpc_methods"\``
- 汇总侧 `fx.In` 接收 `[]handler.Map \`group:"rpc_methods"\``
- `rpc.Server.Register(maps...)` 做 merge
- 即使当前没有任何 map provider，group 也可以先空跑，R0b 可以先把骨架落下。

### 各模块 `rpc.go` 还不存在时如何处理

- `turn`、`orchestration` 这两个包已存在，可先接入模块本体，再让 wave1-3 逐步补 `rpc.go` provider。
- `skill`、`uistate`、`dashboard` 当前包不存在，R0b 不能直接按任务文案 import；否则不是“闭环”，而是新增编译路径占位。
- 因此 R0b 在当前仓内只有两种可行落地方式：
- 方式 A：只落 `rpc_methods` 收集骨架，并接入当前真实存在的模块。
- 方式 B：先新增空 module 包作为占位，再在波次 1-3 填具体 handler。

### 当前 assembly 的额外缺口

- `internal/module/thread/service.go:34-49` 依赖 `store/thread`、`store/binding`、`SessionProvider`。
- `internal/app/modules.go:15-25` 当前没有接入 `internal/store/thread.Module`、`internal/store/binding.Module`、`internal/provider/unified.Module`、`internal/provider/claudecli.Module`、`internal/provider/codexapp.Module`。
- 结论：从 runtime graph 视角看，R0b 只处理 `handler.Map` 还不够；若目标真的是“fx 图闭环”，assembly 范围需要补这些 provider/store 模块。

## 6. 工厂模式

### `ThreadScope(fields...)`

- 当前实现 `internal/platform/rpc/handler.go:27-45` 只能接收单字段名。
- 改为 variadic 后可以保持完全兼容：旧调用 `ThreadScope("threadId")` 不需要改。
- 建议默认行为：
- `ThreadScope()` 默认按 `threadId`, `threadID`, `thread_id` 顺序查找。
- `ThreadScope("x")` 只查自定义字段。
- 结论：R0d 的多 field 方案与现有单 field 方案兼容，`≤80` 可行。

### `StrictBind` 与 `json.RawMessage`

- 当前 `StrictBind` 只是 `handler.New` 包装，缺 strict object-only 约束。
- 将其切到 `handler.Check(...).AllowArray(false).SetStrict(true).Wrap()` 后，只要 `json.RawMessage` 是请求 struct 的显式字段，就不会与 strict object-only 模式冲突。
- 真正需要 raw passthrough 的不是“带一个 `json.RawMessage` 字段”的请求，而是“整体参数结构动态”的方法。
- 结论：`StrictBind` 允许 `json.RawMessage` 字段是可行的；整体动态参数应走 `RawHandler`，而不是弱化 `StrictBind`。

### `RawHandler(fn)`

- `docs/契约/jrpc2-convention.md:182` 已明确允许 `func(context.Context, *jrpc2.Request)` 形式。
- 当前 `internal/platform/rpc/handler.go:11-21` 的 middleware 是包在 `handler.Func` 上的，而 `handler.Func` 本身就是 raw request 级别。
- 结论：`RawHandler(fn)` 不需要新的 Middleware 接口，只需返回同一类 `handler.Func`。

### `CapabilityGate`

- 当前 capability 来源只有 `contract.Session.Capabilities()`，定义在 `internal/contract/provider.go:23-26`。
- 当前上下文里只有 `ThreadScope` 注入的 threadID，见 `internal/platform/rpc/handler.go:42,47-60`。
- 当前 session 实际存放在 `internal/provider/unified/session.go:14-57`，按 agentID 键控。
- 当前 thread 模块通过 `bindingStore.GetByAgentID(threadID)` + `sessions.GetSession(agentID)` 拿 session，见 `internal/module/thread/service.go:169-193`。
- 结论：`CapabilityGate` 不能直接从当前 context 取到 capability，必须注入一个 `threadID -> CapabilitySet` 的 resolver。
- 结论：resolver 不应通过 `platform -> module` 直连实现，应该由 assembly 注入适配器。

## 7. 结论

### Blocker

- R0a 的“`server_payload.go -> codec.go`”拆分定义不准确。V2 该文件大部分不是 codec，而是 UI/runtime/legacy mirror 兼容层；若不先声明删减范围，任务验收口径不成立。
- R0b 的“fx 图闭环”在当前仓内定义不足。`skill/uistate/dashboard` 包不存在，`rpc_methods` group 尚未落地，且 `app.Module` 还缺 provider/store 模块，单做 `Register` 收集不能形成真正闭环。
- R0c 缺少两条必要端口：approval resolve 到 provider 的 typed 响应接口，以及 `awaiting_user_input -> turn_running` 的状态恢复接口。当前只有状态定义，没有执行路径。
- 当前 `internal/platform/rpc/errors.go:5-20` 与 `internal/platform/rpc/handler.go:23-25` 已直接违反 jrpc2 契约；若波次 0 不先修，后续波次会继续建立在错误基线之上。

### Improvement

- 把 R0a 验收条件改成“自定义 WS `channel.Channel` + `AllowPush=true` + typed event push bridge + 必要 payload helper”，并明确删除 V2 的 Wails/UI mirror/legacy refresh 兼容层。
- 把 R0b 改成“两段式”：
- 第一段只落 `rpc_methods` value-group、`fx.Invoke` 注册和现有模块接入。
- 第二段等 wave1-3 再让各模块补 `rpc.go` provider；不存在的包不应写进当前验收条件。
- 为 R0c 先补 3 个窄接口：`SubAgentDetector`、`ApprovalResponder`、`UserInputStatePort`；`approval.go` 只编排，不直连 module 包。
- 把业务错误码迁到正区间，例如 `1001+`；这项应并入波次 0，而不是留到波次 1-3。
- `CapabilityGate` 与 `ThreadScope` 一起落工厂层：`ThreadScope(fields...)` 负责上下文归一化，`CapabilityGate(resolver, caps...)` 负责能力判断，二者都不应持有业务模块依赖。

---

## 互辩：对 reviewer-B 的批判

### B 的事实错误

- B 把 `handleWailsModeApproval` 描述成 “Wails/SSE fallback” 不准确。实际进入条件是 `hasNotifyHookState(s)`，只检查 `notifyHook != nil`，见 `go-agent-v2/internal/apiserver/server_approval.go:434-436`、`go-agent-v2/internal/apiserver/server_context.go:390-391`。SSE 客户端只是 `broadcastNotification` 的 fanout 目标之一，不会单独触发这条等待路径，见 `go-agent-v2/internal/apiserver/server_conn.go:136-170`。
- B 的流程图把 `RespondResultFunc(...)` / `SubmitSystemPrompt("yes"/"no")` 直接写成“resume turn”，这不是 `server_approval.go` 里的显式步骤。该文件只执行 `RespondResultFunc`、`DenyFunc` 或 legacy submit，仓内对 `go-agent-v2/internal/apiserver/server_approval.go` 搜索 `resume` 无匹配；相关代码见 `:339-349`、`:441-453`。因此“恢复执行”只能算 provider 侧效果，不能当作 apiserver 内部已实现的状态迁移。
- B 在 `fx` 一节把“空 `handler.Map` 启动后任何 RPC 请求都会 method-not-found”写成绝对结论，也不准确。契约文档明确默认保留内建 `rpc.serverInfo`，见 `docs/契约/jrpc2-convention.md:118`、`:1231-1233`；当前 `internal/platform/rpc/server.go:46-52` 也没有设置 `DisableBuiltin=true`。因此不能断言“任何”请求都会 `method-not-found`。

### B 的遗漏风险

- B 没指出当前 Wave 0 最直接的契约违例在 `StrictBind` 和错误码，而不是 approval/push。`internal/platform/rpc/handler.go:23-25` 的 `StrictBind` 仍是裸 `handler.New`；`internal/platform/rpc/errors.go:6-8` 当时仍占用保留区间内业务码。这与 `docs/契约/jrpc2-convention.md:117-118`、`:581-589` 明确冲突，应优先作为波次 0 blocker。
- B 把 `fx` 问题简化成“空 `handler.Map` 也能启动”，遗漏了真正的 assembly 缺口。`internal/app/modules.go:15-25` 当前只接了 `thread.Module`，但 `internal/module/thread/service.go:34-39` 需要 `threadstore.Store`、`bindingstore.Store`、`SessionProvider`；`app.Module` 并未接入对应 store/provider 模块。因此 R0b 的核心风险不是空路由，而是当前图本身不闭环。
- B 没把 `server_payload.go` 中仍需迁移的副作用单列成风险。该文件不仅是“codec 可删”，还承担 UI runtime 同步、thread/sidebar refresh、legacy mirror 延迟和 payload alias/error shaping，见 `go-agent-v2/internal/apiserver/server_payload.go:32-46`、`:114-231`、`:288-412`。若删减这些语义，必须先在任务定义里明示。
- B 没触及原任务与当前仓库的时序矛盾：`internal/` 下对 `package skill`、`package uistate`、`package dashboard` 的 LSP 搜索均无匹配，而 `turn`、`orchestration` 模块已存在但未接入 `app.Module`。这意味着 R0b 不是简单的 handler 注册问题，而是“任务文案先于代码基实际状态”的装配问题。

### B 的代码量判断问题

- B 对 `R0c <= 250` 的结论偏保守，因为它把 V2 手写 transport 状态也算进了 approval 必保逻辑。V2 的 pending request manager 的确散在 `go-agent-v2/internal/apiserver/server_state_groups.go:21-27`、`:102-137`，`go-agent-v2/internal/apiserver/server_conn.go:196-239`，`go-agent-v2/internal/apiserver/server_conn_ws.go:221-230`；但 V3 契约已经把“反向请求并等待回执”收敛为 `Server.Callback` / `ClientOptions.OnCallback`，见 `docs/契约/jrpc2-convention.md:482-486`。这些代码不应原样计入 `approval.go` 本身。
- B 把 `Wails/notifyHook fallback`、legacy `"yes"/"no"` prompt relay、amendment compatibility、`request_user_input` 桥接全部算作 Wave 0 必保，也偏 V2 守旧。当前 V3 provider 面里，`internal/provider/codexapp/event_map.go:132-144` 有 approval 事件，而 `internal/provider/claudecli/event_map.go:87-102` 没有任何 approval 事件；这说明“保全 V2 legacy path”并不是当前仓内唯一合理目标。
- 更准确的判断应是：若 R0c 保留 `dedup + sub-agent auto-approve + decision normalization + typed resolve + 状态恢复端口`，250 行仍然紧但可做；若 insist 同时保留 V2 的 `notifyHook/Wails + legacy prompt relay + amendment 全兼容`，250 行不成立。B 直接下“不能”过于绝对。

### B 的依赖关系问题

- B 的依赖图仍是 V2 形状：把 pending request manager 并入 R0a，然后得出 `R0a -> R0c`。这只在保留 V2 手写 request/response multiplexer 时成立。若按契约改用 `Server.Callback`，R0c 只依赖一个 `ApprovalAwaiter`/`CallbackPort` 抽象，不必直接依赖 R0a 的整套 transport 实现。
- B 把 R0b 视为可独立并行的空注册闭环，忽略了真实隐藏依赖在 assembly。`internal/app/modules.go:15-25` 当前少接 `turn.Module`、`orchestration.Module`，也少接 thread 所需的 store/provider 模块，所以“fx 图闭环”与 `handler.Map` 注册不是同一层问题，R0b 不能只靠空 slice 自证完成。
- B 没强调 `platform` 不能 import `module`，见 `internal/archtest/dependency_direction_test.go:90-95`。这意味着 `CapabilityGate`、approval 状态恢复、session/capability 查询都必须通过 assembly 注入窄接口。真正的隐藏依赖是接口切面和装配点，而不只是“避免改同一文件”。

### B 的 push 简化问题

- B 把 `WebSocket + jrpc2 Notify/Callback` 当成主线替代方案，但这只覆盖会话化 RPC peer，不等价替代 V2 的三路 fanout。V2 `broadcastNotification` 先 `notifyHook`，再 SSE，再 WS，见 `go-agent-v2/internal/apiserver/server_conn.go:136-170`；其中 `notifyHook` 还是 `handleWailsModeApproval` 的进入条件，见 `go-agent-v2/internal/apiserver/server_approval.go:434-436`。
- `Server.Notify` 只解决“发通知”，不解决 `server_payload.go` 里的副作用：UI runtime 同步、`ui/thread/changed` / `ui/sidebar/changed` 派生、legacy mirror 延迟与 payload shaping，见 `go-agent-v2/internal/apiserver/server_payload.go:32-46`、`:114-231`、`:288-412`。B 讨论了 transport，但没有把这些语义迁移成本算进 push 替代。
- SSE 在 V2 里不是纯降级观察口，而是 `broadcastNotification` 的正式 fanout 目标，见 `go-agent-v2/internal/apiserver/server_conn.go:155-164`。虽然它不能承担 callback，但把它简单降为“兼容桥”会改变现有非 WS 客户端的消费路径。
- 当前 V3 仓内对 `OnNotify`、`OnCallback`、`Server.Callback` 的 `internal/` LSP 搜索均无匹配；所以 B 的推荐主线只是契约级方向，不是现成替代。它还缺 client 端实现、连接对象生命周期和 UI 消费方。

### 综合互辩结论

- B 对 V2 approval“跨文件耦合很深”和“WS transport 不是一行改动”这两点判断基本正确。
- 但 B 的整体结论过于 V2-shaped：它把手写 pending manager、`notifyHook/Wails` fallback、legacy prompt relay 当成 Wave 0 必保核心，从而低估了 jrpc2 迁移带来的删减空间。
- 同时，B 又低估了当前仓内更前置的阻塞：`StrictBind`/错误码违约、assembly 未闭环、以及 `server_payload.go` 中非 transport 副作用的迁移成本。
- 因此不能直接采纳 B 的“R0a 先做 push/pending，R0b 空闭环并行，R0c 必须后置”的拆法。更稳妥的结论是：先明确 R0a 的删减范围，再补真实 assembly 闭环，R0c 以接口化 `ApprovalAwaiter` / `UserInputStatePort` 为前提，避免被 V2 手写 transport 绑死。
