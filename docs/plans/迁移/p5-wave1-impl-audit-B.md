# P5 波次 1 审查 B（turn rpc）

## 1-2. 编译+守卫

- 编译：按前置条件，已通过。
- 守卫：按前置条件，已通过。

## 3. 方法完整性

- `internal/module/turn/rpc.go:31-87` 的 `handler.Map` 含 6 个 key：
- `turn/start` (`internal/module/turn/rpc.go:32`)
- `turn/steer` (`internal/module/turn/rpc.go:48`)
- `turn/interrupt` (`internal/module/turn/rpc.go:59`)
- `turn/forceComplete` (`internal/module/turn/rpc.go:66`)
- `review/start` (`internal/module/turn/rpc.go:73`)
- `approval/respond` (`internal/module/turn/rpc.go:78`)
- 结论：6/6 齐全，通过。

## 4. import 方向

- `internal/module/turn/rpc.go:3-11` 仅引入标准库 `context`、`errors`，第三方 `github.com/creachadair/jrpc2/handler`，以及内部 `internal/contract`、`internal/platform/rpc`。
- 未见 `internal/store/*` 或 `internal/provider/*` 依赖，RPC 层未直接反向依赖存储层或 provider 层。
- 该文件未直接引入 dto；dto 细节被 `Service` 合约与 helper 隔离在 RPC 边界之外。
- 结论：通过。

## 5. 行数

- `internal/module/turn/rpc.go` 共 89 行，满足 `<= 250`。
- `internal/module/turn/rpc_types.go` 共 36 行，满足 `<= 60`。
- `internal/module/turn/rpc_helpers.go` 共 15 行，满足 `<= 40`。
- 顶层函数行数：
- `NewTurnHandlers` 位于 `internal/module/turn/rpc.go:13-89`，共 77 行，满足 `<= 80`。
- `buildPrepareInput` 位于 `internal/module/turn/rpc_helpers.go:5-14`，共 10 行，满足 `<= 80`。
- 结论：通过。

## 6. withSession

- `internal/module/turn/rpc.go:19-29` 定义了 `withSession` helper。
- helper 先用 `rpc.ThreadIDFrom(ctx)` 读取 thread ID，再调用 `resolver.ResolveSession(ctx, threadID)` 解析 session。
- `resolver` 类型为 `contract.SessionResolver`，接口定义见 `internal/contract/session_resolver.go:5-7`。
- 结论：存在 threadID -> session 解析，且确实使用 `contract.SessionResolver`，通过。

## 7. approval/respond

- `internal/module/turn/rpc.go:78-87` 使用 `approver.Respond(...)`，未直接依赖平台实现。
- 调用签名为 `approver.Respond(p.CallID, p.RequestID, contract.ApprovalDecision{...})`，包含 `callID + requestID + decision`。
- `approvalRespondParams` 定义于 `internal/module/turn/rpc_types.go:26-31`，包含 `CallID`、`RequestID`、`Approved`、`Decision`。
- `contract.ApprovalResponder` 接口定义于 `internal/contract/approval.go:5-7`，签名为 `Respond(callID string, requestID *int64, decision ApprovalDecision) error`。
- 结论：通过。

## 8. review/start

- `internal/module/turn/rpc.go:73-76` 的 `review/start` handler 直接返回 `rpc.ErrNotImplemented("review/start is not yet implemented")`。
- 未返回空成功结果。
- 结论：通过。

## 9. turn/start 流程

- `internal/module/turn/rpc.go:34-44` 中流程明确为：
- `buildPrepareInput(...)`
- `svc.PrepareTurn(ctx, session, input)`
- `svc.StartTurn(ctx, session, req)`
- 返回值为 `turnStartResult{TurnID: handle.LocalID()}`。
- `Service` 接口在 `internal/module/turn/contract.go:12-19` 中声明了 `PrepareTurn` 与 `StartTurn` 两步。
- `TurnHandle.LocalID()` 定义在 `internal/contract/provider.go:39-45`。
- 结论：满足 `PrepareTurn -> StartTurn` 两步流程，且返回 `TurnHandle.LocalID()`，通过。

## 10. HandlerMapResult

- `NewTurnHandlers` 签名为 `rpc.HandlerMapResult`，见 `internal/module/turn/rpc.go:13-18`。
- 函数返回 `rpc.HandlerMapResult{Handlers: handler.Map{...}}`，见 `internal/module/turn/rpc.go:31-88`。
- `internal/module/turn/module.go:7-15` 通过 `fx.Provide` 注册 `NewTurnHandlers`。
- `internal/module/turn/module.go:10-13` 的 `fx.ParamTags("", optional:"true", "", optional:"true")` 与参数顺序 `svc, resolver, approver, capResolver` 对齐。
- `rpc.HandlerMapResult` 在 `internal/platform/rpc/module.go:31-35` 中定义为 `fx.Out`，`Handlers` 带 `group:"rpc_handlers"`。
- `internal/platform/rpc/module.go:45-46` 的 `registerAllHandlers` 会统一注册该分组中的 handler map。
- 结论：通过。

## 结论（通过/需修正）

- 结论：通过。
- 本轮审查范围内，未发现与第 3-10 项要求冲突的实现缺口。

## 互辩补充：对 audit-A 的挑剔批判

### 1. A 的 29 方法计数

- 用 LSP 重读 `internal/module/thread/rpc.go:20-73`，当前 `handler.Map` 实际注册 `29` 个 key，不多不少。
- 29 个 key 依次为：
- `thread/start`
- `thread/resume`
- `thread/fork`
- `thread/recover`
- `thread/archive`
- `thread/unarchive`
- `thread/delete`
- `thread/list`
- `thread/loaded/list`
- `thread/read`
- `thread/resolve`
- `thread/messages`
- `thread/name/set`
- `thread/config/get`
- `thread/config/set`
- `thread/model/set`
- `thread/personality/set`
- `thread/approvals/set`
- `thread/compact/start`
- `thread/rollback`
- `thread/undo`
- `thread/backgroundTerminals/clean`
- `thread/mcp/list`
- `thread/skills/list`
- `thread/debugMemory`
- `thread/realtime/start`
- `thread/realtime/appendAudio`
- `thread/realtime/appendText`
- `thread/realtime/stop`
- `mock/experimentalMethod` 不在当前 V3 `thread/rpc.go` 中。
- 但 V2 `go-agent-v2/internal/apiserver/methods_thread_turn.go:8-113` 的注册函数并不只有这 29 个 `thread/*`；同一函数还注册了 `turn/*` 4 个、`review/start` 1 个，以及 `mock/experimentalMethod` 1 个，总计 35 个条目。
- 因此，A 的“29 个 `thread/*` 方法齐全”这个数值本身是准确的；不够精确之处在于，它容易被读成“与整个 V2 `registerThreadTurnMethods` 完全对齐”，而 `mock/experimentalMethod` 并未进入当前 V3 `thread/rpc.go`。

### 2. A 对 cmd/capCmd 工厂的评估

- A 抓到了主要问题，但论证还不够细。
- 当前 V3 内部签名是自洽的：
- `newThreadCommandHandler` 与 `newCapabilityThreadCommandHandler` 都读取 `commandParams.Args`，见 `internal/module/thread/rpc.go:89-103`。
- `commandParams` 确实存在 `Args string` 字段，见 `internal/module/thread/rpc_types.go:30-33`。
- `Service.SendCommand` 的第四个参数也是 `args string`，见 `internal/module/thread/contract.go:23`；实现签名同样为 `SendCommand(ctx context.Context, threadID, command, args string)`，见 `internal/module/thread/command.go:12`。
- 因此，不存在“A 所暗示的当前 V3 helper 自身签名可能错位”的问题。当前 helper 在 V3 内部是对齐的。
- 真正的问题是它与 V2 RPC 契约错位：
- V2 `thread/model/set` 走 `SendSlashCommandWithArgs(params, "/model", "model")`，见 `go-agent-v2/internal/apiserver/methods_thread_turn.go:98-100`。
- V2 `thread/personality/set` 用参数 key `personality`，见 `go-agent-v2/internal/apiserver/methods_thread_turn.go:101-103`。
- V2 `thread/approvals/set` 用参数 key `policy`，见 `go-agent-v2/internal/apiserver/methods_thread_turn.go:104-106`。
- V2 `SendSlashCommandWithArgs` 会把传入的 `argKey` 透传给 `sendSlashCommandWithParams(...)`，见 `go-agent-v2/internal/apiserver/codexadapter/adapter_submit.go:434-435`。
- 当前 V3 统一暴露 `args`，因此不是 helper 签名错误，而是 RPC 入参 schema 被过度扁平化。
- 另外，A 对 `SendCommand` 支持面的批评是成立的。`internal/module/thread/command.go:18-35` 当前仅支持 `/model`、`/personality`、`/approvals`、`/interrupt`，其余已注册路由会落入 `unsupported command`。

### 3. A 是否忽略了 thread/start 参数完整性

- A 没有忽略，`audit-A` 第 10 节已经指出当前 `startParams` 丢失多项 V2 字段。
- 但这里可以更精确。
- 用 LSP 读取 V2 `threadStartParams`，见 `go-agent-v2/internal/apiserver/methods_thread.go:16-27`，其字段为：
- `model`
- `modelProvider`
- `cwd`
- `approvalPolicy`
- `baseInstructions`
- `developerInstructions`
- `sandbox`
- `summary`
- `effort`
- `personality`
- 按这份真实 V2 struct，`thread/start` 并不包含 `prompt`，也不包含 `dynamicTools` 参数字段。
- 当前 V3 `startParams` 只有 `provider`、`cwd`、`model`、`prompt`，见 `internal/module/thread/rpc_types.go:7-12`。
- 因此，最严格的结论应是：
- A 关于“字段严重不完整”的方向正确。
- 但更准确的表述应是“当前 V3 不是少了 `prompt/dynamicTools`，而是少了 `modelProvider`、`approvalPolicy`、`baseInstructions`、`developerInstructions`、`sandbox`、`summary`、`effort`、`personality`，并且额外引入了 V2 struct 中并不存在的 `provider`、`prompt` 形状”。

### 4. A 是否验证了 CapabilityResolver 注入来源

- A 没有把这一链路查透。
- `NewThreadHandlers(svc, capResolver)` 的 `capResolver` 由 `internal/platform/rpc/module.go:14-20` 中的 `NewCapabilityResolver` 提供。
- `NewCapabilityResolver` 依赖 `contract.SessionResolver`，见 `internal/platform/rpc/handler.go:20-31`。
- `contract.SessionResolver` 的实际 provider 来自 `internal/provider/unified/module.go:16-24` 的 `NewSessionResolver`。
- 主应用 `internal/app/modules.go:20-38` 同时装配了 `rpc.Module`、`thread.Module` 与 `unified.Module`，因此当前应用图里 provider 是存在的。
- 还有一个 A 没说清的细节：`thread.Module` 对 `NewThreadHandlers` 的第二个参数做了 `optional:"true"`，见 `internal/module/thread/module.go:13-16`。
- 这意味着即使没有 `rpc.CapabilityResolver` provider，FX 也不会因此 panic；handler 会拿到 `nil` resolver。
- `CapabilityGate` 对 `nil` resolver 的行为是取 `nil` capability set，再通过 `CapabilitySet.Has` 返回 `false`，见 `internal/platform/rpc/handler.go:71-86` 与 `internal/dto/provider/capability.go:30-35`。
- 缺失 provider 的结果是 capability-gated 方法一律被拒绝，不是 panic。
- 对这一点，A 的报告存在验证缺口。

### 5. A 对 thread/debugMemory 的实现判断

- 用 LSP 验证，`runtimeMemoryStats()` 确实存在，见 `internal/module/thread/rpc.go:127-131`。
- `thread/debugMemory` handler 也确实调用它并返回结果，见 `internal/module/thread/rpc.go:66-68`。
- 因此，“可能编译不过”或“可能返回 nil”的怀疑不成立。
- A 如果只在第 5 节把它当作存在性检查项，这是成立的。
- 但更高标准下，存在性不是核心，语义才是核心。
- V2 `threadDebugMemory` 解析 `action=drop|update`，再转发为 `/debug-m-drop` 或 `/debug-m-update` slash command，见 `go-agent-v2/internal/apiserver/methods.go:369-388`。
- 当前 V3 则直接返回本地 `runtime.MemStats`，语义完全改写。
- 这一点 A 在第 10 节已指出，因此该项不能批成“A 漏掉 compile 风险”，只能批成“A 没把存在性正确性和语义正确性拆开说”。

### 互辩结论

- A 的强项在于抓到了 `commandParams` 扁平化、`SendCommand` 支持面不足、`thread/start` 契约不完整、`thread/debugMemory` 语义漂移这些主问题。
- A 的薄弱点在于三处：
- 没把“29 个 `thread/*`”与“V2 整个 `registerThreadTurnMethods` 还含 `mock/experimentalMethod` 等非 thread 条目”区分清楚。
- 没核实 `CapabilityResolver` 的真实注入链与缺省行为，因而缺少运行时装配层面的判断。
- 没明确指出当前 cmd/capCmd helper 在 V3 内部签名其实是自洽的，真正断裂点发生在 V2 对外 RPC schema，而不是 helper 调用链本身。
