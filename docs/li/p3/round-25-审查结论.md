# 第 25 轮审查结论

## 审查范围

- `internal/platform/rpc/handler.go`（Middleware 链、ThreadScope、CapabilityGate、CapabilityErrorMapper、InvalidParamsMapper、Logging、TracedMethod、ThreadHandler/CapabilityThreadHandler）
- `internal/platform/rpc/approval.go`（ApprovalManager：RequestApproval、Respond、AutoApprove、registerPending、ensureDispatch）
- `internal/platform/rpc/server.go`（Server 结构、rpcRequestTracker、LogRequest/LogResponse）
- `internal/platform/rpc/strict.go`（StrictHandler、RawHandler）
- `internal/platform/rpc/push.go`（PushBridge：NotifyClient、CallbackClient、subscribeCoreEventPushes）

> 与第 08-10 轮覆盖的 `mcpcontrol/handlers.go`（使用 rpc.StrictHandler 的调用方）不重复。本轮聚焦 rpc 包内部实现。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `handler.go:22-40` `NewCapabilityResolver` | 兜底 | `resolver == nil` 时返回的 closure 内部报 rpcError | 合理的 fail-fast（运行时报错） | OK，但建议构造期 panic |
| `handler.go:42-50` `Wrap` | 弱契约 | nil middleware 不校验（会 panic 在 `mws[i](wrapped)` 调用时） | 调用方传 nil middleware 会 panic | 入口过滤 nil（与 round-11 Chain 同根） |
| `handler.go:178-193` `Logging` | 兜底 | `logger != nil` 时才 log | nil logger 是调用方 bug | 构造期 panic |
| `handler.go:234-239` `RequireSessionCapability` | 弱契约 | `session` 不做 nil 校验；`session.Capabilities()` 在 nil session 时 panic | 调用方必须保证 session 非 nil | 入口 nil 校验 |
| `approval.go:66-76` `NewApprovalManager` | 兜底 | logger nil 兜底全局；dispatcher 可为 nil（optional） | logger nil 是调用方 bug | logger nil 应 panic |
| `approval.go:85-118` `RequestApproval` | 兜底 | `ensureDispatch` 失败时 `failPending` + return error | 合理的 fail-fast | OK |
| `approval.go:102-111` 等待路径 | 兜底 | `waitForApproval` 超时/取消后，owner 尝试 `canceledApprovalDecision`；非 owner 直接返回 error | 合理的 timeout 处理 | OK |
| `approval.go:127-137` `Respond` | 兜底 | `lookupPending` 返回 nil 时报 ErrNotFound | 合理的 fail-fast | OK |
| `server.go:51-59` `newRPCRequestTracker` | 兜底 | `logger == nil` 时返回 nil tracker | nil tracker 后续所有方法都是 nil-safe（`if t == nil { return }`） | 合理（optional 依赖） |
| `server.go:61-79` `LogRequest` | 兜底 | `t == nil \|\| req == nil \|\| req.IsNotification()` 时 return | 合理的 nil-safe | OK |
| `server.go:81-94` `LogResponse` | 兜底 | `t == nil \|\| rsp == nil` 时 return；`rpcErr == nil` 时不 log | 合理（只 log 错误响应） | OK |
| `strict.go:11-17` `StrictHandler` | 弱契约 | `handler.Check(fn)` 失败时 panic | 合理的 fail-fast（构造期 panic） | OK（正面案例） |
| `push.go:23-28` `NewPushBridge` | 兜底 | logger nil 兜底全局；dispatcher 不做 nil 校验 | dispatcher nil 是装配 bug | dispatcher nil 应 panic |
| `push.go:30-35` `NotifyClient` | 兜底 | `server == nil` 返回 ErrInvalidState | 合理的 fail-fast | OK |
| `push.go:37-53` `CallbackClient` | 兜底 | `server == nil` 返回 error；`resp == nil` 返回 ErrInvalidState | 合理的 fail-fast | OK |
| `push.go:63-72` `subscribeCoreEventPushes` | 兜底 | `worker == nil \|\| dispatcher == nil` 返回 nil | nil 是装配 bug | nil 应 panic |
| `push.go:92-99` `subscribeRawProviderEventPushes` | 兜底 | `worker == nil \|\| dispatcher == nil` 返回 noop cancel | 同上 | 同上 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `handler.go:178-193` | Logging logger==nil 时不 log |
| `push.go:63-72` | subscribeCoreEventPushes nil 返回 nil |
| `push.go:92-99` | subscribeRawProviderEventPushes nil 返回 noop |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `handler.go:42-50` | Wrap nil middleware 不过滤 |
| `handler.go:178-193` | Logging logger nil 不 panic |
| `handler.go:234-239` | RequireSessionCapability session nil 不校验 |
| `approval.go:66-76` | NewApprovalManager logger nil 兜底 |
| `push.go:23-28` | NewPushBridge dispatcher nil 不校验 |
| `push.go:63-99` | subscribe* nil 参数兜底 |

## 修复优先级

### P0（必须本周修）
（本轮无 P0——rpc 包整体代码质量较高，fail-fast 模式应用良好）

### P1（本月）
1. `push.go:23-28` NewPushBridge dispatcher nil 改 panic
2. `push.go:63-99` subscribeCoreEventPushes / subscribeRawProviderEventPushes nil 参数改 panic
3. `handler.go:42-50` Wrap 过滤 nil middleware
4. `handler.go:234-239` RequireSessionCapability 入口 nil session 校验
5. `approval.go:66-76` NewApprovalManager logger nil 改 panic

### P2（下个 sprint）
6. `handler.go:178-193` Logging logger nil 改 panic
7. `handler.go:22-40` NewCapabilityResolver resolver nil 构造期 panic

## 边界条件

1. **`Wrap` nil middleware 的影响**：当前 `Wrap(ThreadScope(), Validate(), CapabilityGate(...))` 的调用点都传非 nil middleware。nil middleware 只会在动态构造 middleware 列表时出现（如 optional capability gate）。加 nil 过滤不影响现有行为。
2. **`RequireSessionCapability` nil session**：调用方通常在 `resolveSession` 之后调用，session 已经非 nil。但如果有人直接调用 `RequireSessionCapability(nil, "cap")`，会 panic 在 `session.Capabilities()` 上。加 nil 校验是 defensive。
3. **`NewPushBridge` dispatcher nil**：PushBridge 的 dispatcher 用于 approval events publish。如果 dispatcher 为 nil，approval 事件不会被 publish 到 bus——UI 不会收到 approval 请求通知。这是一个严重的功能缺失但不会 crash。改 panic 让装配错误在启动时暴露。
4. **`subscribeCoreEventPushes` nil 返回 nil**：调用方在 `module.go` 中通过 fx lifecycle 调用。如果 worker 或 dispatcher 为 nil，返回 nil cancels 后 lifecycle 的 OnStop 不会调用任何 cancel——这是合理的（没有订阅就不需要取消）。但 nil 是装配 bug，应在启动时暴露。
5. **rpc 包整体代码质量较高**：`StrictHandler` 的构造期 panic、`NotifyClient`/`CallbackClient` 的 nil server 校验、`ThreadScope` 的 threadId 必填校验都是 fail-fast 正面案例。主要问题集中在 nil 依赖兜底（logger、dispatcher）。
6. **`ApprovalManager` 的并发设计**：双锁（`mu` + `lifecycleMu`）分离了 pending 注册和生命周期操作。`pendingApproval` 用 `sync.Once` 保证 finishPending 幂等。设计合理。

---

**本轮总结**：rpc 包代码质量较高，无 P0。`StrictHandler` 构造期 panic、`NotifyClient` nil server 校验是正面案例。主要问题是 nil 依赖兜底（logger、dispatcher），应在构造期 panic。

**累计进度**：25 轮完成（100 轮的 25%）。cron `da34430c` 继续推进。
