# P5 波次 1 审查 A（thread rpc）

## 1-2. 编译+守卫

- `go build ./...`：按任务前提，主 Agent 已验证通过；本线程未复跑。
- `go test ./internal/archtest/...`：按任务前提，主 Agent 已验证通过；本线程未复跑。

## 3. 方法完整性（29 个逐一核对）

- 结论：`internal/module/thread/rpc.go:18-75` 中 `handler.Map` 共注册 `29` 个 `thread/*` 方法；与 `go-agent-v2/internal/apiserver/methods_thread_turn.go:8-113` 的 `29` 个 `thread/*` 方法一一对应，名称无遗漏。

| 方法 | V2 | R1 | 结果 |
|---|---|---|---|
| `thread/start` | 有 | 有 | 一致 |
| `thread/resume` | 有 | 有 | 一致 |
| `thread/recover` | 有 | 有 | 一致 |
| `thread/fork` | 有 | 有 | 一致 |
| `thread/archive` | 有 | 有 | 一致 |
| `thread/unarchive` | 有 | 有 | 一致 |
| `thread/delete` | 有 | 有 | 一致 |
| `thread/name/set` | 有 | 有 | 一致 |
| `thread/compact/start` | 有 | 有 | 一致 |
| `thread/rollback` | 有 | 有 | 一致 |
| `thread/list` | 有 | 有 | 一致 |
| `thread/loaded/list` | 有 | 有 | 一致 |
| `thread/read` | 有 | 有 | 一致 |
| `thread/resolve` | 有 | 有 | 一致 |
| `thread/config/get` | 有 | 有 | 一致 |
| `thread/config/set` | 有 | 有 | 一致 |
| `thread/messages` | 有 | 有 | 一致 |
| `thread/backgroundTerminals/clean` | 有 | 有 | 一致 |
| `thread/realtime/start` | 有 | 有 | 一致 |
| `thread/realtime/appendAudio` | 有 | 有 | 一致 |
| `thread/realtime/appendText` | 有 | 有 | 一致 |
| `thread/realtime/stop` | 有 | 有 | 一致 |
| `thread/undo` | 有 | 有 | 一致 |
| `thread/model/set` | 有 | 有 | 一致 |
| `thread/personality/set` | 有 | 有 | 一致 |
| `thread/approvals/set` | 有 | 有 | 一致 |
| `thread/mcp/list` | 有 | 有 | 一致 |
| `thread/skills/list` | 有 | 有 | 一致 |
| `thread/debugMemory` | 有 | 有 | 一致 |

## 4. import 方向

- `internal/module/thread/rpc.go:3-10` 的 import 只有标准库 `context`、`runtime`，第三方 `github.com/creachadair/jrpc2/handler`，以及内部 `internal/platform/rpc`。
- 未 import `store`、`provider` 或其它下游实现包。
- 也未显式 import `contract`/`dto`；原因是 `Service`、`StartRequest`、`ResumeRequest` 等契约类型与 `rpc.go` 同属 `package thread`，直接包内可见。
- 该项通过，依赖方向干净。

## 5. 行数

- `internal/module/thread/rpc.go`：`132` 行，满足 `<= 400`。
- `internal/module/thread/rpc_types.go`：`34` 行，满足 `<= 80`。
- `rpc.go` 内函数长度：

| 函数 | 行段 | 行数 | 结果 |
|---|---|---:|---|
| `NewThreadHandlers` | `18-75` | `58` | 通过 |
| `newThreadCall` | `77-81` | `5` | 通过 |
| `newThreadEffect` | `83-87` | `5` | 通过 |
| `newThreadCommandHandler` | `89-93` | `5` | 通过 |
| `newCapabilityThreadCommandHandler` | `95-104` | `10` | 通过 |
| `newResumeHandler` | `106-119` | `14` | 通过 |
| `newThreadGetHandler` | `121-125` | `5` | 通过 |
| `runtimeMemoryStats` | `127-131` | `5` | 通过 |

## 6. ThreadScope 使用

- `internal/platform/rpc/handler.go:44-96` 证据明确：
- `rpc.ThreadHandler(...)` = `Wrap(ThreadScope())(StrictHandler(...))`
- `rpc.CapabilityThreadHandler(...)` = `Wrap(ThreadScope(), CapabilityGate(...))(StrictHandler(...))`
- `thread/start`：`rpc.StrictHandler(...)`，未套 `ThreadScope`，符合预期。
- `thread/list`：`rpc.StrictHandler(...)`，未套 `ThreadScope`，符合预期。
- `thread/loaded/list`：`rpc.StrictHandler(...)`，同样未套 `ThreadScope`。如果审查口径严格限定“只有 `thread/start`、`thread/list` 例外”，则此项不满足；如果按 V2 参数语义看，V2 `threadLoadedList` 也不要求 `threadId`，因此这一点与 V2 一致。
- 其余 `26` 个当前 handler 都经由 `rpc.ThreadHandler(...)` 或 `rpc.CapabilityThreadHandler(...)`，都会套 `ThreadScope`。
- 但与 V2 对照后，当前存在明显“过度 ThreadScope”：
- `thread/undo`：V2 用 `SendSlashCommandFromRawParams(..., "/undo")`，不是 `RequireThreadID` 版本。
- `thread/model/set`：V2 用 `SendSlashCommandWithArgs(..., "/model", "model")`，不是 `RequireThreadID` 版本。
- `thread/personality/set`：V2 用 `SendSlashCommandWithArgs(..., "/personality", "personality")`，不是 `RequireThreadID` 版本。
- `thread/approvals/set`：V2 用 `SendSlashCommandWithArgs(..., "/approvals", "policy")`，不是 `RequireThreadID` 版本。
- `thread/mcp/list`：V2 用 `SendSlashCommandFromRawParams(..., "/mcp")`，不是 `RequireThreadID` 版本。
- `thread/skills/list`：V2 直接 `ThreadSkillsList()`，不带 `threadId`。
- `thread/debugMemory`：V2 只解析 `action`，不带 `threadId`。
- 该项结论：不通过。当前实现不是“只有 `thread/start`、`thread/list` 不套”，并且对多条 V2 非线程作用域方法施加了额外 `ThreadScope`。

## 7. cmd/capCmd 工厂

- 存在局部工厂，重复度控制到位：
- `newThreadCommandHandler`：`internal/module/thread/rpc.go:89-93`
- `newCapabilityThreadCommandHandler`：`internal/module/thread/rpc.go:95-104`
- 形式上通过；不是每个命令 handler 手写。
- 但该抽象把多条 V2 异构 RPC 契约压扁成了单一 `commandParams`：`internal/module/thread/rpc_types.go:30-33` 只剩 `threadId` + `args`。
- 这直接引入两类回归：

| 方法 | V2 参数/行为 | 当前实现 | 结果 |
|---|---|---|---|
| `thread/config/get` | `threadId` | `args` 命令转发 | 不兼容 |
| `thread/config/set` | `threadId` + `model` + `effort` | `args` 命令转发 | 不兼容 |
| `thread/rollback` | `threadId` + `numTurns/turnIndex` | `args` 命令转发 | 不兼容 |
| `thread/model/set` | `model` 字段 | `args` 字段 | 不兼容 |
| `thread/personality/set` | `personality` 字段 | `args` 字段 | 不兼容 |
| `thread/approvals/set` | `policy` 字段 | `args` 字段 | 不兼容 |
| `thread/realtime/start` | `threadId` + `prompt` + `sessionId` | `args` 字段 | 不兼容 |
| `thread/realtime/appendAudio` | `threadId` + `audio` | `args` 字段 | 不兼容 |
| `thread/realtime/appendText` | `threadId` + `text` | `args` 字段 | 不兼容 |

- 更严重的是，`internal/module/thread/command.go:12-41` 里的 `SendCommand` 目前只支持：
- `/model`
- `/personality`
- `/approvals`
- `/interrupt`
- 而 `rpc.go` 实际注册的命令路由还包括：
- `/config/get`
- `/config/set`
- `/compact`
- `/rollback`
- `/undo`
- `/clean`
- `/mcp`
- `/skills`
- `/realtime/start`
- `/realtime/appendAudio`
- `/realtime/appendText`
- `/realtime/stop`
- 这些路由按当前实现会落到 `unsupported command` 分支。
- 该项结论：工厂“存在”，但实现不完整，且已经破坏 V2 参数契约。

## 8. HandlerMapResult

- `NewThreadHandlers` 签名为 `func NewThreadHandlers(svc Service, capResolver rpc.CapabilityResolver) rpc.HandlerMapResult`，见 `internal/module/thread/rpc.go:18`。
- 返回值为 `rpc.HandlerMapResult{Handlers: handler.Map{...}}`，见 `internal/module/thread/rpc.go:19`。
- `rpc.HandlerMapResult` 在 `internal/platform/rpc/module.go:31-35` 定义为 `fx.Out`，按 `group:"rpc_handlers"` 聚合。
- `internal/module/thread/module.go:8-17` 中确实通过 `fx.Provide(...)` 提供了 `NewThreadHandlers`。
- 该项通过。

## 9. realtime 骨架

- `thread/realtime/start`
- `thread/realtime/appendAudio`
- `thread/realtime/appendText`
- `thread/realtime/stop`
- 以上四个方法均已进入 `handler.Map`，见 `internal/module/thread/rpc.go:70-73`。
- 四者都通过 `newCapabilityThreadCommandHandler(...)` 注册，底层是 `rpc.CapabilityThreadHandler(capabilityRealtime, capResolver, ...)`，因此都带 `CapabilityGate("realtime")`，见 `internal/module/thread/rpc.go:95-104` 与 `internal/platform/rpc/handler.go:93-96`。
- 结构上的“4 个骨架 + realtime capability gate”已到位。
- 但行为上不通过：
- V2 的四个方法分别使用 `prompt`、`sessionId`、`audio`、`text` 等专有参数，见 `go-agent-v2/internal/apiserver/methods_turn.go:91-107`。
- 当前四个方法统一退化为 `commandParams{threadId,args}`。
- 当前 `SendCommand` 不支持 `/realtime/*`，会直接返回 `unsupported command`。
- 该项结论：骨架存在，门禁存在，但功能未打通。

## 10. V2 对照

- 单看“注册名称集合”，无遗漏。
- 但对照 `go-agent-v2/internal/apiserver/methods_thread_turn.go:8-113`、`go-agent-v2/internal/apiserver/methods_thread.go:16-359`、`go-agent-v2/internal/apiserver/methods_turn.go:91-107`，当前 `rpc.go` 仍有实质性语义偏差：
- `thread/start`：V2 参数包含 `modelProvider`、`approvalPolicy`、`baseInstructions`、`developerInstructions`、`sandbox`、`summary`、`effort`、`personality`；当前 `startParams` 只保留 `provider`、`cwd`、`model`、`prompt`。
- `thread/resume`：V2 参数为 `threadId` + `path` + `cwd` + `model`；当前 `resumeParams` 变成 `threadId` + `provider`。
- `thread/read` 与 `thread/resolve`：V2 分别走 `ThreadRead` 与 `ThreadResolve`；当前都汇聚到 `svc.Get(...)`。
- `thread/messages`：V2 `before` 是 `int64`；当前 `messagesParams.Before` 是 `string`。
- `thread/config/get`、`thread/config/set`、`thread/rollback`、`thread/model/set`、`thread/personality/set`、`thread/approvals/set`、`thread/realtime/*`：V2 都有专有参数结构或专有 helper；当前统一压成 `args`。
- `thread/skills/list`：V2 为 provider 级无 `threadId` 查询；当前变成 thread-scoped `SendCommand("/skills")`。
- `thread/debugMemory`：V2 解析 `action=drop|update` 并转 `/debug-m-drop` 或 `/debug-m-update`；当前改成读取本地 `runtime.MemStats`，语义完全不同。
- V2 对照结论：方法名完整，但契约与行为未对齐，不能判定为“无遗漏迁移完成”。

## 结论（通过/需修正）

- 结论：需修正。
- 通过项：
- 方法注册数量 `29/29` 齐全。
- `rpc.go` / `rpc_types.go` / 各函数行数满足约束。
- import 方向干净，无 `store` / `provider` 反向依赖。
- `NewThreadHandlers` 已正确返回 `rpc.HandlerMapResult`，`module.go` 已正确提供。
- realtime 四个方法与 `CapabilityGate("realtime")` 骨架存在。
- 需修正项：
- `ThreadScope` 使用与 V2 真实语义不一致；多条本不要求 `threadId` 的路由被强行 thread-scoped。
- `commandParams{threadId,args}` 过度泛化，破坏多条 V2 RPC 的参数契约。
- `SendCommand` 实际仅支持极少数命令，当前大量已注册路由会在运行时返回 `unsupported command`。
- `thread/read` / `thread/resolve` / `thread/debugMemory` / `thread/skills/list` 等方法存在明显语义折叠或行为漂移。

## 互辩：对 audit-B 的挑剔批判

### 1. turn/start 两步流程判断

- B 第 9 节的主判断基本准确；`internal/module/turn/rpc.go:32-44` 确实是 `buildPrepareInput(...) -> svc.PrepareTurn(...) -> svc.StartTurn(...)`，没有跳过 `PrepareTurn`。
- 且 `internal/module/turn/service.go:37-58` 的 `PrepareTurn` 不是空壳：实际执行了 input assembly、skill resolve 和 manifest build：
- `s.skills.Resolve(...)`：`internal/module/turn/service.go:49`
- `s.assembler.Assemble(input)`：`internal/module/turn/service.go:53`
- `s.manifest.Build(input)`：`internal/module/turn/service.go:57`
- 因此，“若跳过 PrepareTurn 就没有 input assembly/skill resolve/manifest build” 这条担心在当前代码里没有发生。
- 但 B 的证据链仍偏浅：它只读到了 `rpc.go` 和接口定义，没有把 `PrepareTurn` 实现体读穿。结论本身基本正确，论证深度不足。

### 2. withSession 评估是否充分

- B 第 6 节明显不充分。它只证明了 `withSession` helper “存在”，没有证明这个 helper “足够安全”。
- `internal/module/turn/rpc.go:19-29` 的 `withSession` 只做了两件事：
- 检查 `resolver == nil`
- 调用 `resolver.ResolveSession(ctx, threadID)`
- 它没有检查：
- `session == nil`
- session 是否已经 closed
- provider 进程是否已死
- `internal/module/turn/service.go:263-268` 的 `requireSession` 也只是 nil-check，不做 liveness/closed 检查。
- 当前主实现链路里，`internal/provider/unified/session_resolver.go:23-46` 只是从 thread store 查到 `agentID` 后转给 `SessionManager.Get(agentID)`；而 `internal/provider/unified/session.go:48-57` 的 `Get` 只是 map lookup，没有任何健康检查。
- 更关键的是，`internal/provider/unified/session.go:59-67` 的 `SessionManager.Remove` 只有定义，LSP 未找到调用点；这意味着“session 已关闭但仍留在 manager map 中”不是理论风险，而是现有结构就允许的状态。
- 以 codexapp 为例，`internal/provider/codexapp/session.go:195-199` 的 `Close` 会把 session 标成 closed；后续 `StartTurn` 会继续走 transport 调用，若 transport 已关，会在下游报错，见：
- `internal/provider/codexapp/session.go:101-104`
- `internal/provider/codexapp/transport.go:79-81`
- 所以 B 第 6 节把“有 helper”直接判成“通过”过于草率；它忽略了 stale session / closed transport 的运行时风险。

### 3. approval/respond 参数类型验证是否足够

- B 第 7 节验证明显不够，且“通过”结论过宽。
- V2 对外公开契约是 `requestId`，不是 `callId`。证据：
- `go-agent-v2/internal/apiserver/server_approval.go:456-483` 的 `approvalRespondParams` 只有 `RequestID int64`、`Approved *bool`、`Decision any`
- V2 前端直接调用 `approval/respond` 时传的是 `requestId`：`go-agent-v2/cmd/agent-terminal/frontend/vue-app/components/timeline/useApprovalActions.js:51`
- V2 app 层还专门做了 `requestId` 归一化：`go-agent-v2/cmd/agent-terminal/app_handlers.go:381-408`，对应测试在 `go-agent-v2/cmd/agent-terminal/app_contract_test.go:155-163`
- 当前 V3 入参是 `internal/module/turn/rpc_types.go:26-30`：
- `CallID string`
- `RequestID *int64`
- `Approved bool`
- `Decision string`
- B 没有验证“V2 的 `requestId` 调用是否还能打通”。补查后可知：
- `internal/platform/rpc/approval_support.go:18-32` 的 `normalizeApprovalRequest(...)` 会用 `approvalCallID(...)` 把 `requestId` 回退成字符串 call id
- `internal/platform/rpc/approval_support.go:122-130` 的 `approvalCallID(...)` 明确支持 `callId` 为空时退回 `requestId`
- `internal/platform/rpc/approval.go:105-116` 的 `Respond(...)` 也走同样的回退逻辑
- 这说明“数值型 `requestId` 单独传入”在当前后端内部仍可兼容；B 没验证这一点。
- 但真正的问题是，B 同时漏掉了更严重的类型回归：
- `Decision` 从 V2 的 `any` 收窄成了 V3 的 `string`。V2 明确允许结构化 decision payload，见 `go-agent-v2/internal/apiserver/server_conn_protocol_guard_test.go:233`；当前 V3 `StrictHandler` 下这类对象无法解到 `string`。
- `Approved` 从 V2 的 `*bool` 变成 V3 的 `bool`。V2 `approvalRespondTyped` 明确要求“`decision` 或 `approved` 至少有一个”，见 `go-agent-v2/internal/apiserver/server_approval.go:469-477`；当前 V3 丢失了这层 presence validation，缺参时会以 `Approved=false` 零值继续下传。
- 当前仓内除文档外，LSP 未找到任何新的 V3 `approval/respond` 调用点；也就是说，无法证明前端已经从 `requestId` 迁到了 `callId`。现有能证明的调用方仍是 V2 前端，它发的是 `requestId`。
- 当前审批事件确实同时下发了 `requestId` 与 `callId`，见 `internal/platform/rpc/approval_events.go:41-61`，但“事件里带两者”不等于“前端回调入口已经完成 `callId` 迁移”。
- 因此 B 第 7 节不应直接判“通过”；至少应标记为“兼容性未证实，且 `decision`/`approved` 类型已有实质回归”。

### 4. CapabilityResolver 注入问题

- B 第 10 节只检查了 `fx.ParamTags` 顺序对齐，这不足以证明依赖真的可解析。
- 补查 fx 图后，完整应用图中的 provider 链是成立的：
- `internal/platform/rpc/module.go:13-20` 提供 `NewCapabilityResolver`
- 同文件 `:19` 还把 `ApprovalManager` 暴露成 `contract.ApprovalResponder`
- `internal/provider/unified/module.go:16-24` 提供 `NewSessionResolver`
- `internal/app/modules.go:20-33` 同时装配了 `rpc.Module`、`unified.Module`、`turn.Module`
- 所以在完整 `app.Module` 下，`NewTurnHandlers` 的 `resolver` / `approver` / `capResolver` 依赖链是可闭合的。
- 但 B 仍漏掉了一个运行时语义：`internal/module/turn/module.go:10-13` 把 `resolver` 和 `capResolver` 都标成了 optional。
- 这意味着如果有人单独装 `turn.Module` 而没带 `rpc.Module` / `unified.Module`，fx 图不会在构建期失败，而是退化成运行时错误：
- `resolver == nil` 时，`withSession` 返回 `turn rpc: session resolver is not configured`：`internal/module/turn/rpc.go:20-22`
- `capResolver == nil` 时，`rpc.CapabilityGate(...)` 会把 capability set 视为空并直接拒绝，见 `internal/platform/rpc/handler.go:71-85`
- 所以 B 若想判这一项“通过”，至少应把“完整 app 图可解析”和“turn.Module 单独使用会运行时退化”区分开写。

### 5. review/start 的 ErrNotImplemented 错误码

- B 第 8 节只验证了 `review/start` 返回 `rpc.ErrNotImplemented(...)`，没有检查 numeric code。
- 补查后，`internal/platform/rpc/errors_helper.go:5-8` 定义：
- `CodeNotImplemented = -31006`
- `ErrNotImplemented(...)` 用这个 code 封装 jrpc2 error
- 由于 `-31006 > -32000`，它在非保留区间内，满足前述互辩约束。
- 所以这一点的结果是“实际正确，但 B 的验证不完整”。

### 互辩小结

- B 对 `turn/start` 两步流程的核心判断基本成立；`review/start` 的 not-implemented 错误码最终也没踩保留区。
- 但 B 第 6 节和第 10 节只验证了“形状”，没有验证运行时语义。
- B 第 7 节问题最大：它没有核对真实公开契约来源，没有验证 `requestId`/`callId` 兼容路径，也漏掉了 `decision:any -> string`、`approved:*bool -> bool` 这两个实质回归。
- 因此，B 的总评“通过”过宽。以最挑剔口径，应改写为：
- `turn/start` / `review/start` 两项可暂判通过
- `withSession` 需补风险说明
- `approval/respond` 需修正或至少降级为“兼容性未证实，不通过”
