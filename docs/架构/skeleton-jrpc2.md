# skeleton-jrpc2.md — internal/platform/rpc JSON-RPC 骨架

> **当前库**: `github.com/creachadair/jrpc2`
> **当前平台封装**: `internal/platform/rpc`
> **定位**: 桌面/UI 与后端之间的 JSON-RPC 2.0 注册、分发、推送和审批回调底座。

---

## 0. 一句话定位

jrpc2 = transport 和 handler 适配库；`internal/platform/rpc` = 本仓库的 RPC 平台层。当前项目不再维护单一 `BuildServiceMap`，而是由各模块返回 `rpc.HandlerMapResult`，Fx 按 `group:"rpc_handlers"` 聚合后注册到同一个 `*rpc.Server`。

---

## 1. HandlerMapResult 聚合范式

```go
// internal/platform/rpc/module.go
type HandlerMapResult struct {
    fx.Out
    Handlers handler.Map `group:"rpc_handlers"`
}

type serverParams struct {
    fx.In
    Logger   *pkglogger.Logger
    Config   *config.Config
    Handlers []handler.Map `group:"rpc_handlers"`
}

func registerAllHandlers(server *Server, p serverParams) {
    server.Register(p.Handlers...)
}
```

模块只贡献自己的 handler 片段：

```go
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
    return platformrpc.HandlerMapResult{Handlers: handler.Map{
        "datasource/list": platformrpc.StrictHandler(listHandler(svc)),
    }}
}
```

规则：

- 每个模块只注册自己拥有的方法名。
- 不在模块外追加第二套路由表。
- 共享横切逻辑放在 `internal/platform/rpc` 的 handler/middleware/helper 中。
- 方法名仍使用 `domain/action`，例如 `thread/start`、`turn/submit`、`ctl/register`。

---

## 2. Server 运行时

```go
// internal/platform/rpc/server.go
type Server struct {
    methods handler.Map
    active  map[*jrpc2.Server]string
}

func NewServer(p Params) *Server
func (s *Server) Register(handlerMaps ...handler.Map)
func (s *Server) Dispatch(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
func (s *Server) NotifyAll(ctx context.Context, bridge *PushBridge, method string, params any)
func (s *Server) Run(ctx context.Context) error
```

`Run(ctx)` 由 `internal/app.AsRPCRunner(*rpc.Server)` 接入 platform runner。`Server` 负责监听控制 RPC 地址、接受连接、跟踪 active peer、记录 trace，并在请求结束后清理 pending 状态。

---

## 3. Typed Handler 约束

公共 handler 优先使用：

```go
platformrpc.StrictHandler(func(ctx context.Context, req SomeRequest) (SomeResult, error) {
    if strings.TrimSpace(req.ThreadID) == "" {
        return SomeResult{}, platformrpc.ErrInvalidParams("threadId is required")
    }
    return svc.Do(ctx, req)
})
```

`StrictHandler` 基于 `handler.Check(...).AllowArray(false).SetStrict(true)`，用于阻断未知字段、数组参数和无效入参。跨模块 DTO 缺字段、未知字段、非法状态必须 fail-fast，不在 handler 中静默补默认值。

---

## 4. Push 与 eventsurface

事件推送不直接从业务 handler 调 `Notify`。当前路径是：

```text
module/provider emits typed DTO
  -> internal/platform/bus
  -> internal/platform/eventsurface.Bind / ExpandNotifications
  -> internal/platform/rpc.PushBridge / push worker
  -> rpc.Server.NotifyAll
```

`eventsurface` 负责把内部 DTO 转成客户端方法名与 payload，例如：

- `turn.TurnOutputDelta` -> `item/agentMessage/delta`、`item/reasoning/textDelta`、`item/commandExecution/outputDelta` 或 `turn/output/delta`
- `tool.ToolApprovalRequested` -> command/file/skill 审批方法
- `ui.UIThreadPatch` -> `ui/thread/patch`

---

## 5. 错误与 trace

平台层负责把常见错误映射成稳定 RPC 错误：

- `ErrInvalidParams`：参数缺失、未知字段、格式错误。
- `ErrInvalidState`：依赖未配置、状态不允许。
- capability / approval / session 等错误通过 middleware 或 adapter 保持统一 code。

`Server.Dispatch` 会记录 startedAt、method、param keys、duration、status；慢请求和失败请求进入 trace recorder。handler 不应吞掉错误来伪造成功响应。

---

## 6. 测试范式

最低验证：

```bash
./scripts/test_with_guard.sh ./internal/platform/rpc -count=1
./scripts/test_with_guard.sh ./internal/archtest -count=1
```

代表测试：

- `internal/module/*/rpc_test.go`：模块 handler 输出 `HandlerMapResult`。
- `internal/platform/rpc/*_test.go`：strict 参数、push、trace、active peer。
- `internal/archtest/jrpc2_error_guard_test.go`：错误映射守卫。

---

## 7. 禁止行为

| 规则 | 原因 |
|---|---|
| 不绕过 `HandlerMapResult` 注册公共 RPC 方法 | 避免第二套路由表漂移 |
| 不手写 `json.Unmarshal` 兼容未知字段 | `StrictHandler` 已提供 fail-fast 入口 |
| 不在 handler 内直接启动后台 goroutine | 长跑工作交给 platform runner 或事件链 |
| 不在 handler 中直接依赖数据库连接细节 | 通过 service/store/sqlc contract |
| 不吞掉 `*jrpc2.Error` 或普通 error | 客户端和 trace 需要真实错误 |
