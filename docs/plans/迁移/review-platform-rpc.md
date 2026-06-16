# platform/rpc 审查报告

审查时间：2026-03-21

审查方法：仅使用 LSP `document_symbol`、`diagnostics`、`text_search`、`references(compact)`、`call_hierarchy`、`read_file`。

范围说明：
- 你给出的列表包含 12 个文件，但当前 `internal/platform/rpc/` 目录实际有 15 个 `package rpc` 文件：额外存在 `approval_events.go`、`errors_helper.go`、`registry.go`。证据：`internal/platform/rpc/approval_events.go:1`、`internal/platform/rpc/errors_helper.go:1`、`internal/platform/rpc/registry.go:1`。
- 下面的结论以“目录实际文件集”为准；同时会明确哪些结论只依赖你列出的 12 个文件，哪些结论需要额外 3 个同包文件补足。
- 本次 LSP `diagnostics` 对所审文件未返回诊断错误。

## 主要发现

1. `approval` 生命周期只做到了“内存内 pending + direct callback”闭环，没有把恢复/超时清理接到实际生命周期上。`Cleanup`/`RestorePending`/`PendingSnapshot` 定义完整，但 LSP `references` 为 0；当前长期挂起审批只能依赖调用方 `ctx` 超时或后续显式 `approval/respond`。证据：`internal/platform/rpc/approval_lifecycle.go:10-43`。
2. `registerAllHandlers` 和 `Server.Register` 没有任何 key 去重或冲突报警；重复 method 会被后写入的 map 静默覆盖。证据：`internal/platform/rpc/module.go:47-49`、`internal/platform/rpc/server.go:34-39`、`internal/platform/rpc/registry.go:5-13`。
3. 当前 approval callback method 固定默认到 `tool/approval/request`，与 V2 的 `item/commandExecution/requestApproval` / `item/fileChange/requestApproval` / `skill/requestApproval` 不对齐，而且仓内没有任何地方设置 `CallbackMethod` 覆盖该默认值。证据：`internal/platform/rpc/approval_events.go:13`、`internal/platform/rpc/approval_events.go:37-39`；V2 对照：`go-agent-v2/internal/apiserver/server_approval.go:15-17`。
4. `request_context.go` 只提供 CWD 上下文辅助，且没有任何引用；ThreadID 走的是 `handler.go` 的 `ThreadScope`/`ThreadIDFrom`，AgentID 则没有对应的 context 注入/提取 helper。证据：`internal/platform/rpc/request_context.go:7-14`、`internal/platform/rpc/handler.go:44-68`、`internal/platform/rpc/handler.go:98-101`。
5. `codec.go` 不是 JSON-RPC codec，而是未接线的业务 payload wrapper；实际 JSON-RPC 2.0 编解码由 `jrpc2` 负责。证据：`internal/platform/rpc/codec.go:3-22`、`internal/platform/rpc/server.go:61`、`internal/platform/rpc/transport_ws.go:34`。

## 1. 文件清单与行数

目录实际文件清单如下，全部不超过 400 行：

| 文件 | 是否在用户列表 | 行数 | ≤400 | 行号证据 |
| --- | --- | ---: | --- | --- |
| `internal/platform/rpc/server.go` | 是 | 147 | 是 | `internal/platform/rpc/server.go:147` |
| `internal/platform/rpc/handler.go` | 是 | 131 | 是 | `internal/platform/rpc/handler.go:131` |
| `internal/platform/rpc/strict.go` | 是 | 23 | 是 | `internal/platform/rpc/strict.go:23` |
| `internal/platform/rpc/push.go` | 是 | 93 | 是 | `internal/platform/rpc/push.go:93` |
| `internal/platform/rpc/approval.go` | 是 | 296 | 是 | `internal/platform/rpc/approval.go:296` |
| `internal/platform/rpc/approval_support.go` | 是 | 229 | 是 | `internal/platform/rpc/approval_support.go:229` |
| `internal/platform/rpc/approval_lifecycle.go` | 是 | 44 | 是 | `internal/platform/rpc/approval_lifecycle.go:44` |
| `internal/platform/rpc/codec.go` | 是 | 23 | 是 | `internal/platform/rpc/codec.go:23` |
| `internal/platform/rpc/transport_ws.go` | 是 | 106 | 是 | `internal/platform/rpc/transport_ws.go:106` |
| `internal/platform/rpc/errors.go` | 是 | 32 | 是 | `internal/platform/rpc/errors.go:32` |
| `internal/platform/rpc/request_context.go` | 是 | 15 | 是 | `internal/platform/rpc/request_context.go:15` |
| `internal/platform/rpc/module.go` | 是 | 70 | 是 | `internal/platform/rpc/module.go:70` |
| `internal/platform/rpc/approval_events.go` | 否 | 81 | 是 | `internal/platform/rpc/approval_events.go:81` |
| `internal/platform/rpc/errors_helper.go` | 否 | 10 | 是 | `internal/platform/rpc/errors_helper.go:10` |
| `internal/platform/rpc/registry.go` | 否 | 14 | 是 | `internal/platform/rpc/registry.go:14` |

判断：
- 以“目录实际文件集”看，拆分粒度总体健康，没有超出 400 行的文件。
- 但用户给定列表漏了 3 个同包文件，后续迁移审查如果只盯 12 个文件，会漏掉 approval 事件发布、额外错误码、registry 合并行为。

## 2. 函数复杂度

按 LSP `document_symbol` 的函数起止行号，最长的前 5 个函数如下：

| 排名 | 函数 | 起止行 | 行数 | 80 行阈值 | CC 粗估 | 结论 |
| --- | --- | --- | ---: | --- | ---: | --- |
| 1 | `(*ApprovalManager).finishPending` | `internal/platform/rpc/approval.go:235-262` | 28 | 通过 | 约 5 | 通过 |
| 2 | `decodeApprovalDecision` | `internal/platform/rpc/approval_support.go:49-76` | 28 | 通过 | 约 6 | 通过 |
| 3 | `(*ApprovalManager).RequestApproval` | `internal/platform/rpc/approval.go:71-96` | 26 | 通过 | 约 6 | 通过 |
| 4 | `ThreadScope` | `internal/platform/rpc/handler.go:44-68` | 25 | 通过 | 约 6 | 通过 |
| 5 | `(*ApprovalManager).registerPending` | `internal/platform/rpc/approval.go:125-147` | 23 | 通过 | 约 3 | 通过 |

补充：
- 紧随其后的是 `callbackParams`（`internal/platform/rpc/approval_events.go:41-62`，22 行）和 `WSHandler`（`internal/platform/rpc/transport_ws.go:22-43`，22 行）。
- 没有发现超过 80 行的函数；从分支数粗看，也没有超过 CC 10 的实现。

## 3. Server 生命周期

结论：基础生命周期基本正确，活跃会话追踪是线程安全的；但 `platform/rpc` 自身没有显式 `Start/Stop` API，实际启动依赖外层 runtime 装配。

证据：
- `Run` 负责监听 TCP、进入 accept loop，并把 `net.ErrClosed` / `context.Canceled` / channel closing 视为正常退出：`internal/platform/rpc/server.go:53-66`。
- `acceptLoop` 为每个连接起 goroutine，并在退出前 `wg.Wait()`，避免连接 goroutine 泄漏：`internal/platform/rpc/server.go:68-80`。
- `serveConn` 为每个连接创建 `jrpc2.Server` 并 `Start(ch)`，在 `ctx.Done()` 时调用 `srv.Stop()`，随后等待 `WaitStatus()`：`internal/platform/rpc/server.go:83-102`。
- 活跃会话通过 `mu` + `active map[*jrpc2.Server]struct{}` 管理；增删走写锁，快照走读锁：`internal/platform/rpc/server.go:16-23`、`internal/platform/rpc/server.go:104-131`。
- 真正把 `*rpc.Server` 接入 runtime 的不是 `platform/rpc/module.go`，而是应用层 `AsRPCRunner(server *rpc.Server) RunnerResult`：`internal/app/modules.go:40-48`；runtime 通过 `group:"runners"` 调 `RunGroup` 拉起所有 Runner：`internal/app/runner.go:13-23`、`internal/app/runner.go:30-59`、`internal/platform/runner/group.go:18-59`。

判断：
- “active session 追踪线程安全”是通过的。
- “生命周期闭环”是部分通过：上下文取消可正常停服，但 `platform/rpc` 自己不暴露独立 `Stop()`，也不在模块内自注册为 runner，需要外层 app 补全。

## 4. handler.Map 合并

结论：`registerAllHandlers` 正确消费 `group:"rpc_handlers"`，但完全没有 key 去重。

证据：
- group 定义：`HandlerMapResult.Handlers handler.Map \`group:"rpc_handlers"\``，见 `internal/platform/rpc/module.go:33-37`。
- group 消费：`serverParams.Handlers []handler.Map \`group:"rpc_handlers"\``，见 `internal/platform/rpc/module.go:39-45`。
- 聚合入口只做 `server.Register(p.Handlers...)`：`internal/platform/rpc/module.go:47-49`。
- `Server.Register` 逐个 map、逐个 key 直接赋值：`s.methods[name] = handlerFunc`，见 `internal/platform/rpc/server.go:34-39`。
- 同包的 `Registry(parts ...handler.Map)` 也是同样的覆盖式合并：`internal/platform/rpc/registry.go:5-13`。

判断：
- `group:"rpc_handlers"` 的消费路径是对的。
- 没有 key dedupe、没有冲突日志、没有 “重复 method 直接失败” 的保护；一旦两个 producer 输出相同 method，后注册的会静默覆盖先注册的。

## 5. strict binding

结论：当前已接入的 handler producer 全部使用了 strict wrapper，但这个约束是“约定式”的，不是 register 时强制校验。

证据：
- `StrictHandler` 明确走 `handler.Check(fn).AllowArray(false).SetStrict(true).Wrap()`：`internal/platform/rpc/strict.go:11-16`。
- `ThreadHandler` / `CapabilityThreadHandler` 只是在线程/能力中间件外再包一层 `StrictHandler`：`internal/platform/rpc/handler.go:89-96`。
- 当前 `rpc_handlers` producer 只有 5 处：`internal/sidecar/orch/orchestration/rpc.go:15-77`、`internal/module/skill/rpc.go:42-88`、`internal/module/thread/rpc.go:18-84`、`internal/module/turn/rpc.go:14-93`、`internal/module/workspace/rpc.go:13-24`。
- 这 5 处输出的所有 method 都使用 `rpc.StrictHandler(...)`、`rpc.ThreadHandler(...)` 或 `rpc.CapabilityThreadHandler(...)`。例如：
  - orchestration：`internal/sidecar/orch/orchestration/rpc.go:16-76`
  - thread：`internal/module/thread/rpc.go:19-83`、helper `internal/module/thread/rpc.go:86-131`
  - turn：`internal/module/turn/rpc.go:32-92`
- `RawHandler` 存在，但 LSP `references`/`text_search` 没有发现任何调用点：定义见 `internal/platform/rpc/strict.go:20-22`。

判断：
- 现状通过：所有已发现公开 handler 都被 `SetStrict(true)` 覆盖。
- 架构上仍有缺口：`registerAllHandlers` / `Server.Register` 并不校验传入 handler 是否 strict；未来如果有人直接塞裸 `handler.Func`，框架本身拦不住。

## 6. push bridge

结论：核心事件订阅已接通，且这 3 个 method 名基本与 V2 兼容；但桥接面很窄，只覆盖 3 类内部事件。

证据：
- method 常量：`ui/state/changed`、`turn/started`、`turn/completed`，见 `internal/platform/rpc/push.go:16-19`。
- Fx 生命周期里 `bindEventBridge` 在 `OnStart` 注册订阅，在 `OnStop` cancel：`internal/platform/rpc/module.go:51-69`。
- `subscribeCoreEventPushes` 用 `bus.ResilientSubscribe` 把 `agentdto.StateChanged`、`turndto.TurnStarted`、`turndto.TurnCompleted` 分别桥到上述 3 个 method：`internal/platform/rpc/push.go:75-92`。
- `NotifyAll` 会对活跃连接快照逐个发 `server.Notify(...)`：`internal/platform/rpc/server.go:42-51`、`internal/platform/rpc/server.go:122-131`。
- V2 对照：
  - `turn/started`、`turn/completed`：`go-agent-v2/internal/apiserver/notifications.go:14-17`
  - `ui/state/changed` 被 V2 UI payload 层显式识别：`go-agent-v2/internal/apiserver/server_payload.go:145-153`

判断：
- 这 3 个 method 名是兼容的。
- 但 V2 的 event surface 远大于当前实现；当前 bridge 并没有覆盖 item 级事件、tool 级事件、approval 事件、UI replay 事件等。

## 7. approval lifecycle

结论：`RequestApproval -> registerPending -> dispatch -> waitForApproval -> respond` 的 direct jrpc2 callback 链路在代码上是闭合的；但默认 callback method 不兼容 V2，且恢复/超时清理没有接线。

链路证据：
- 入口先 `normalizeApprovalRequest`，再 `registerPending`，owner 分支会 `publishRequested`，然后 `ensureDispatch`，最后 `waitForApproval`：`internal/platform/rpc/approval.go:71-96`。
- `registerPending` 为每个 pending 分配/规范化 `requestID`，并写入 `pending map`：`internal/platform/rpc/approval.go:125-147`。
- 请求事件发布：`publishRequested` 发 `ToolApprovalRequested`：`internal/platform/rpc/approval_events.go:15-24`。
- dispatch 入口：
  - `ensureDispatch`：`internal/platform/rpc/approval.go:149-164`
  - `beginDispatch`：`internal/platform/rpc/approval.go:166-186`
  - callback method/params：`internal/platform/rpc/approval_events.go:37-62`
- 实际 callback 通过 `bridge.CallbackClient(ctx, server, method, params)` 发到客户端：`internal/platform/rpc/approval.go:188-202`、`internal/platform/rpc/push.go:42-58`。
- callback 结果解码与等待：
  - `waitForApproval`：`internal/platform/rpc/approval_support.go:34-47`
  - `decodeApprovalDecision`：`internal/platform/rpc/approval_support.go:49-76`
  - `mapApprovalWaitErr`：`internal/platform/rpc/approval_support.go:89-98`
- 回写响应：`Respond` -> `finishPending` -> `publishResolved`：`internal/platform/rpc/approval.go:105-116`、`internal/platform/rpc/approval.go:235-262`、`internal/platform/rpc/approval_events.go:26-35`。

关键缺口：
- 默认 callback method 固定为 `tool/approval/request`：`internal/platform/rpc/approval_events.go:13`、`internal/platform/rpc/approval_events.go:37-39`。
- 仓内没有任何地方设置 `CallbackMethod:` 覆盖默认值；LSP `text_search` 无匹配。
- V2 approval method family 是：
  - `item/commandExecution/requestApproval`
  - `item/fileChange/requestApproval`
  - `skill/requestApproval`
  证据：`go-agent-v2/internal/apiserver/server_approval.go:15-17`

判断：
- “链路闭合”是通过的。
- “V2 兼容”不通过：approval request method family 没对齐。

## 8. approval 并发安全

结论：pending map 的基本并发安全是成立的；但 timeout/restore 机制只是“定义存在”，没有接入真实生命周期。

证据：
- `ApprovalManager` 持有 `mu sync.Mutex` + `pending map[string]*pendingApproval`：`internal/platform/rpc/approval.go:19-24`。
- 所有核心 map 操作都在锁内：
  - 注册：`internal/platform/rpc/approval.go:125-147`
  - 重置 dispatch：`internal/platform/rpc/approval.go:218-233`
  - 查找：`internal/platform/rpc/approval.go:269-295`
- 完成路径用 `pending.once.Do(...)` 做幂等保护，且只会 `close(pending.done)` 一次：`internal/platform/rpc/approval.go:239-260`。
- `waitForApproval` 通过 `ctx.Done()` 和 `pending.done` 双路等待：`internal/platform/rpc/approval_support.go:34-47`。
- 超时映射存在：`internal/platform/rpc/approval_support.go:89-98`。
- 后台清理/恢复 API 存在：
  - `Cleanup`：`internal/platform/rpc/approval_lifecycle.go:10-21`
  - `RestorePending`：`internal/platform/rpc/approval_lifecycle.go:23-34`
  - `PendingSnapshot`：`internal/platform/rpc/approval_lifecycle.go:36-43`
- 但上述 3 个方法均无引用，LSP `references` 为 0。

判断：
- 线程安全：通过。
- timeout/清理：部分通过。只有调用方主动传 deadline 的 `ctx` 时，`RequestApproval` 才会自然超时；否则 `Cleanup` 没有被调度，pending 可能长期留在内存里。
- dedupe 维度是 `callID`，不是 `(callID, requestID)`；证据：`internal/platform/rpc/approval.go:128-130`。这在 callID 复用时会把后续请求并到旧 pending 上。

## 9. WebSocket transport

结论：`transport_ws.go` 的 adapter 对 `jrpc2/channel.Channel` 是正确的；但它并不是一个 `io.ReadWriteCloser` adapter，而且相比 V2 缺少显式 origin/backpressure/fallback 基础设施。

证据：
- `WSHandler` 把 websocket 升级后包装成 `wsChannel`，再 `jrpc2.NewServer(...).Start(ch)`：`internal/platform/rpc/transport_ws.go:21-43`。
- `wsChannel` 实现的是 `channel.Channel`，不是 `io.ReadWriteCloser`：
  - 定义：`internal/platform/rpc/transport_ws.go:45-49`
  - `Send`：`internal/platform/rpc/transport_ws.go:55-62`
  - `Recv`：`internal/platform/rpc/transport_ws.go:64-74`
  - `Close`：`internal/platform/rpc/transport_ws.go:76-85`
- 发送使用 `sendMu` 串行化，关闭走 `closeOnce`，读写错误有归一化：`internal/platform/rpc/transport_ws.go:55-105`。
- 当前 upgrader 是零值 `websocket.Upgrader{}`：`internal/platform/rpc/transport_ws.go:17`。
- V2 transport 对照：
  - `transportState.upgrader` 显式配置 `CheckOrigin: checkLocalOrigin`：`go-agent-v2/internal/apiserver/server.go:30-34`、`go-agent-v2/internal/apiserver/server.go:206-208`
  - `broadcastNotification` 会走 SSE + WS snapshot，并在 outbox 过载时断开客户端：`go-agent-v2/internal/apiserver/server_conn.go:136-185`

判断：
- 对 jrpc2 而言，这个 channel adapter 本身是正确的。
- 如果按 “V2 传输层基础设施” 的标准看，当前实现明显更薄：没有显式 origin 策略、没有 per-connection outbox/backpressure、没有 SSE/notifyHook fallback。

## 10. codec

结论：运行时协议是标准 JSON-RPC 2.0，但 `codec.go` 本身不是协议 codec，而且当前未被任何调用链使用。

证据：
- 真实协议边界由 `jrpc2.NewServer(...).Start(...)` 承担：
  - TCP line channel：`internal/platform/rpc/server.go:61`
  - per-conn server start：`internal/platform/rpc/server.go:89`
  - websocket channel：`internal/platform/rpc/transport_ws.go:34`
- `codec.go` 仅定义 `PayloadEncoder`，包装成 `{success: true, data: ...}` / `{success: false, error: {...}}`：`internal/platform/rpc/codec.go:3-22`。
- `PayloadEncoder`、`WrapSuccess`、`WrapError` 的 LSP `references` 都为 0。

判断：
- “运行时 JSON-RPC 2.0”是通过的，因为底层委托给了 `jrpc2`。
- “codec.go 是否实现标准 JSON-RPC 2.0 codec”不通过；它既不是 JSON-RPC codec，也没有接线。

## 11. 错误码

结论：自定义错误码都在 `-31xxx`，没有落入 `jrpc2`/JSON-RPC 2.0 的 `-32xxx` 保留区间。

证据：
- `CodeNotFound = -31001`、`CodeInvalidState = -31002`、`CodeConflict = -31003`、`CodeCapabilityGate = -31004`、`CodeApprovalTimeout = -31005`：`internal/platform/rpc/errors.go:5-11`
- `CodeNotImplemented = -31006`：`internal/platform/rpc/errors_helper.go:5`
- 使用点都通过 `jrpc2.Code(...)` 包装：`internal/platform/rpc/errors.go:13-31`、`internal/platform/rpc/handler.go:79-81`

判断：
- 通过。

## 12. request_context

结论：ThreadID 上下文注入/提取是对的；`request_context.go` 本身没有承担这个职责，而且 AgentID 无法从 rpc context 中提取。

证据：
- `request_context.go` 只有 `WithCWD`/`CWDFrom`：`internal/platform/rpc/request_context.go:5-14`
- 这两个 helper 没有任何引用；LSP `references` 为 0。
- 实际的 ThreadID 注入发生在 `ThreadScope`：它从 params 取 `threadId`/`threadID`/`thread_id` 并通过 `withThreadID` 写入 context：`internal/platform/rpc/handler.go:44-68`
- `ThreadIDFrom` 提取逻辑：`internal/platform/rpc/handler.go:98-101`
- 使用方：
  - thread handlers：`internal/module/thread/rpc.go:51-56`、`internal/module/thread/rpc.go:118-131`
  - turn handlers：`internal/module/turn/rpc.go:24-25`
- rpc 包中没有 `AgentIDFrom(ctx)` 一类 helper；`agentId` 只出现在 approval request/payload 中：`internal/platform/rpc/approval.go:49`、`internal/platform/rpc/approval_events.go:52-57`

判断：
- ThreadID：通过。
- AgentID：不通过，当前不可从 rpc context 提取。
- `request_context.go` 文件名有误导性；它管理的是 CWD，不是 request identity context。

## 13. fx 注册

结论：`module.go` 内部的 `fx.Provide/Invoke` 基本完整，但 `rpc.Module` 自身并不闭合 “server 被 runtime 拉起” 这条依赖链；这一步是在 app 层补上的。

证据：
- `rpc.Module` 提供：
  - `NewServer`
  - `NewPushBridge`
  - `NewApprovalManager`
  - `NewCapabilityResolver`
  - `ApprovalResponder` 接口绑定
  见 `internal/platform/rpc/module.go:14-24`
- `rpc.Module` 的 invoke 只有：
  - `registerAllHandlers`
  - `bindEventBridge`
  见 `internal/platform/rpc/module.go:22-24`
- handler producer 来自 5 个业务模块：
  - `internal/sidecar/orch/orchestration/rpc.go:15-77`
  - `internal/module/skill/rpc.go:42-88`
  - `internal/module/thread/rpc.go:18-84`
  - `internal/module/turn/rpc.go:14-93`
  - `internal/module/workspace/rpc.go:13-24`
- 真正把 `*rpc.Server` 放入 `group:"runners"` 的是 app 层 `AsRPCRunner`：`internal/app/modules.go:40-48`
- runtime 再消费 `group:"runners"`：`internal/app/runner.go:13-23`、`internal/app/runner.go:30-59`

判断：
- 如果看“整个 app 装配链”，依赖链是闭合的。
- 如果只看 `internal/platform/rpc/module.go`，它本身并不会把 server 注册成 runner，因此不是“独立可运行”的闭环模块。

## 14. import 方向

结论：没有违禁的 `internal/module` / `internal/provider` 方向依赖；但如果规则是“只允许 platform/ + 标准库 + jrpc2”，当前实现明显超界。

证据：
- LSP `text_search` 在 `internal/platform/rpc/` 内搜索 `internal/module/`、`internal/provider/`，均无匹配。
- 非 `platform/*` / 非标准库 / 非 `jrpc2` 依赖示例：
  - `go.uber.org/fx`：`internal/platform/rpc/module.go:8`
  - `github.com/kelindar/event`：`internal/platform/rpc/push.go:9`、`internal/platform/rpc/approval.go:13`、`internal/platform/rpc/approval_events.go:10`
  - `github.com/gorilla/websocket`：`internal/platform/rpc/transport_ws.go:14`
  - `internal/contract`：`internal/platform/rpc/module.go:10`、`internal/platform/rpc/handler.go:12`、`internal/platform/rpc/approval.go:15`
  - `internal/dto/*`：`internal/platform/rpc/push.go:11-13`、`internal/platform/rpc/handler.go:17-18`、`internal/platform/rpc/approval_events.go:8-9`

判断：
- “禁止 import module/provider”：通过。
- “只依赖 platform/ + std + jrpc2”：不通过。

## 15. V2 对照

结论：当前 `platform/rpc` 已具备 V3 最小可用的 jrpc2 基座，但与 V2 apiserver 的 RPC 基础设施相比，至少缺失 6 项关键能力。

| 缺失能力 | V2 证据 | 当前实现证据 | 影响 |
| --- | --- | --- | --- |
| approval method family / callback 兼容层 | `go-agent-v2/internal/apiserver/server_approval.go:15-17` | `internal/platform/rpc/approval_events.go:13`、`internal/platform/rpc/approval_events.go:37-39` | 现实现默认只会发 `tool/approval/request`，不能直接复用 V2 前端的 method 语义。 |
| `request_user_input` 事件桥接与自动应答策略 | `go-agent-v2/internal/apiserver/server_event_handler.go:221-245` | `internal/platform/rpc/push.go:75-92` | 当前 push bridge 只订阅 3 类 core event，没有把 provider 的 `request_user_input` 收口到统一审批/前端交互链。 |
| 非 approval 事件的 transport `requestId` 透传 | `go-agent-v2/internal/apiserver/server_event_handler.go:135-146` | `internal/platform/rpc/push.go:82-90`、`internal/platform/rpc/server.go:42-51` | 前端更难把 push 事件与请求侧关联起来。 |
| 无 WS 客户端时的 notifyHook/SSE/Wails fallback | `go-agent-v2/internal/apiserver/server.go:233-238`、`go-agent-v2/internal/apiserver/server_conn.go:136-170`、`go-agent-v2/internal/apiserver/server_approval.go:351-382` | `internal/platform/rpc/approval.go:149-202`、`internal/platform/rpc/approval_lifecycle.go:10-34` | 当前 approval 主要依赖 live jrpc2 callback；`Cleanup/RestorePending` 又未接线，离 V2 的前端兜底能力差距较大。 |
| 更宽的 event method map / UI replay 基础设施 | `go-agent-v2/internal/apiserver/notifications.go:10-80`、`go-agent-v2/internal/apiserver/server_payload.go:145-202` | `internal/platform/rpc/push.go:16-19`、`internal/platform/rpc/push.go:75-92` | 当前只有 `ui/state/changed`、`turn/started`、`turn/completed` 三类标准推送。 |
| transport hardening：origin、outbox/backpressure、连接管理 | `go-agent-v2/internal/apiserver/server.go:206-208`、`go-agent-v2/internal/apiserver/server_conn.go:155-185` | `internal/platform/rpc/transport_ws.go:17`、`internal/platform/rpc/transport_ws.go:22-43` | 当前 WS transport 可用，但没有 V2 那套显式 origin 策略和连接退避/断开策略。 |

## 最终判断

- 作为“最小 jrpc2 服务基座”，`platform/rpc` 已经具备：strict handler 包装、handler group 聚合、基本 server 生命周期、active session 追踪、core event push、approval direct callback、WS 接入、错误码封装。
- 作为“V2 apiserver 等价的 RPC 基础设施”，当前还不够。最大的差距集中在：
  1. approval method 兼容层
  2. `request_user_input` 统一桥接
  3. transport `requestId` 透传
  4. fallback transport / pending request allocator / timeout restore
  5. 更完整的 event mapping 与 transport hardening
