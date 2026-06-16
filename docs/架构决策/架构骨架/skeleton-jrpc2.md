# skeleton-jrpc2.md — github.com/creachadair/jrpc2 v1.3.5 RPC 框架

> **版本**: v1.3.5 | **模块**: `github.com/creachadair/jrpc2`
> **定位**: V3 全部 client↔server 通信的 RPC 骨架

---

## 0. 一句话定位

jrpc2 = V3 的 RPC 骨架。所有 client↔server 通信走 JSON-RPC 2.0，handler 声明式注册，中间件链式组合。消灭 V2 的 God Object Server 和 80+ 手写 RPC 闭包。

---

## 1. Handler 注册范式

### 1.1 声明式路由表

```go
package rpcapi

import (
    "context"

    "github.com/creachadair/jrpc2"
    "github.com/creachadair/jrpc2/handler"
)

// BuildServiceMap 构建完整路由表——所有 RPC 入口在此一目了然
func BuildServiceMap(svc *Service) handler.Map {
    return handler.Map{
        // Thread 域
        "thread/list":   handler.New(svc.ThreadList),
        "thread/create": handler.New(svc.ThreadCreate),
        "thread/get":    handler.New(svc.ThreadGet),
        "thread/delete": handler.New(svc.ThreadDelete),

        // Turn 域
        "turn/submit": handler.New(svc.TurnSubmit),
        "turn/stop":   handler.New(svc.TurnStop),

        // Skills 域
        "skills/list":   handler.New(svc.SkillsList),
        "skills/create": handler.New(svc.SkillsCreate),

        // UI 域
        "ui/state":   handler.New(svc.UIState),
        "ui/sidebar": handler.New(svc.UISidebar),
    }
}
```

### 1.2 Handler 签名

```go
// jrpc2 强制的签名约定：
//   func(ctx context.Context, params *T) (*Result, error)
// jrpc2 自动完成 JSON 反序列化，handler 只写纯业务逻辑

func (s *Service) ThreadList(ctx context.Context, params *ThreadListParams) (*ThreadListResult, error) {
    threads, err := s.queries.ListThreads(ctx, db.ListThreadsParams{
        WorkspaceID: params.WorkspaceID,
        LimitVal:    int32(params.Limit),
    })
    if err != nil {
        return nil, jrpc2.Errorf(CodeStoreFailed, "list threads: %v", err)
    }
    return &ThreadListResult{Threads: threads}, nil
}
```

### 1.3 与 V2 对比：闭包嵌套消除

```
V2: 3-4 层闭包嵌套
  server.go → typedHandler → bindTyped → withRequiredThreadID → capabilityGuard
  每增加一个 handler 要穿过多层抽象，调试困难

V3: 扁平化
  handler.New(svc.ThreadList)   ← 一行注册
  中间件在 Server Options 中全局配置，handler 本身只含业务逻辑
```

---

## 2. 中间件 / 装饰器范式

### 2.1 jrpc2 中间件机制

jrpc2 没有内置 middleware chain，但通过 `handler.Map` 的包装函数和 `jrpc2.ServerOptions` 的 hook 实现等价效果。

```go
// withMiddleware 包装整个 handler.Map，对每个 method 注入前后处理
func withMiddleware(m handler.Map, mws ...Middleware) handler.Map {
    wrapped := make(handler.Map, len(m))
    for name, h := range m {
        wrapped[name] = applyMiddleware(name, h, mws...)
    }
    return wrapped
}

// Middleware 签名
type Middleware func(next jrpc2.Handler) jrpc2.Handler

// applyMiddleware 按顺序包装（从外到内）
func applyMiddleware(method string, h jrpc2.Handler, mws ...Middleware) jrpc2.Handler {
    for i := len(mws) - 1; i >= 0; i-- {
        h = mws[i](h)
    }
    return h
}
```

### 2.2 日志中间件

```go
func LoggingMiddleware(log *slog.Logger) Middleware {
    return func(next jrpc2.Handler) jrpc2.Handler {
        return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
            start := time.Now()
            log.Info("rpc.request", "method", req.Method(), "id", req.ID())

            result, err := next.Handle(ctx, req)

            log.Info("rpc.response",
                "method", req.Method(),
                "duration", time.Since(start),
                "error", err,
            )
            return result, err
        })
    }
}
```

### 2.3 ThreadID 验证中间件（替代 V2 `withRequiredThreadID`）

```go
var methodsRequiringThread = map[string]bool{
    "turn/submit": true,
    "turn/stop":   true,
    "thread/get":  true,
}

func RequireThreadID() Middleware {
    return func(next jrpc2.Handler) jrpc2.Handler {
        return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
            if !methodsRequiringThread[req.Method()] {
                return next.Handle(ctx, req)
            }
            var base struct {
                ThreadID string `json:"threadId"`
            }
            if err := req.UnmarshalParams(&base); err != nil || base.ThreadID == "" {
                return nil, jrpc2.Errorf(jrpc2.InvalidParams, "threadId is required")
            }
            ctx = context.WithValue(ctx, ctxKeyThreadID, base.ThreadID)
            return next.Handle(ctx, req)
        })
    }
}
```

### 2.4 Capability Guard 中间件（替代 V2 `capabilityGuard`）

```go
func CapabilityGuard(caps CapabilityChecker) Middleware {
    return func(next jrpc2.Handler) jrpc2.Handler {
        return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
            if !caps.IsAllowed(req.Method()) {
                return nil, jrpc2.Errorf(CodeCapabilityDenied,
                    "method %q not allowed by current capability set", req.Method())
            }
            return next.Handle(ctx, req)
        })
    }
}
```

### 2.5 Metrics / Tracing 中间件

```go
func MetricsMiddleware(metrics *Metrics) Middleware {
    return func(next jrpc2.Handler) jrpc2.Handler {
        return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
            start := time.Now()
            metrics.RPCInFlight.Add(1)
            defer metrics.RPCInFlight.Add(-1)

            result, err := next.Handle(ctx, req)

            duration := time.Since(start)
            metrics.RPCDuration.Observe(req.Method(), duration)
            if err != nil {
                metrics.RPCErrors.Inc(req.Method())
            }
            return result, err
        })
    }
}
```

### 2.6 错误包装中间件

```go
func ErrorWrapper() Middleware {
    return func(next jrpc2.Handler) jrpc2.Handler {
        return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
            result, err := next.Handle(ctx, req)
            if err != nil {
                var je *jrpc2.Error
                if !errors.As(err, &je) {
                    err = jrpc2.Errorf(CodeInternalError, "%v", err)
                }
            }
            return result, err
        })
    }
}
```

### 2.7 组合使用

```go
mux := BuildServiceMap(svc)
mux = withMiddleware(mux,
    ErrorWrapper(),          // 最外层：兜底错误包装
    MetricsMiddleware(m),    // 指标采集
    LoggingMiddleware(log),  // 请求日志
    RequireThreadID(),       // ThreadID 校验
    CapabilityGuard(caps),   // 权限检查（最内层）
)
```

---

## 3. Server 生命周期与 fx + run.Group 集成

```go
package rpcapi

import (
    "context"
    "fmt"
    "net"

    "github.com/creachadair/jrpc2"
    "github.com/creachadair/jrpc2/channel"
    "github.com/creachadair/jrpc2/server"
    "go.uber.org/fx"

    "github.com/super-agent/super-agent-v3/internal/app"
)

// JRPC2Server 封装 jrpc2 server 实例，实现 app.Runner 接口
type JRPC2Server struct {
    mux    handler.Map
    opts   *jrpc2.ServerOptions
    addr   string
    cancel context.CancelFunc
}

// NewJRPC2Server 构造函数——由 fx 注入所有依赖
func NewJRPC2Server(svc *Service, logger *slog.Logger, cfg Config) *JRPC2Server {
    mux := BuildServiceMap(svc)
    mux = withMiddleware(mux, LoggingMiddleware(logger), ErrorWrapper())

    opts := &jrpc2.ServerOptions{
        Logger:      jrpc2.StdLogger(logger),
        Concurrency: 16,
        AllowPush:   true,
    }
    return &JRPC2Server{mux: mux, opts: opts, addr: cfg.RPCAddr}
}

// Run 实现 app.Runner 接口，由 run.Group 调度
func (s *JRPC2Server) Run(ctx context.Context) error {
    ctx, s.cancel = context.WithCancel(ctx)

    ln, err := net.Listen("tcp", s.addr)
    if err != nil {
        return fmt.Errorf("rpc listen: %w", err)
    }
    defer ln.Close()

    lst := server.NewListener(ln, channel.Line)
    return server.Loop(ctx, lst, server.Static(s.mux), &server.LoopOptions{
        ServerOptions: s.opts,
    })
}

// Stop 由 run.Group 的 interrupt 函数调用
func (s *JRPC2Server) Stop() {
    if s.cancel != nil {
        s.cancel()
    }
}

// --- fx Module 定义 ---
var RPCModule = fx.Module("rpc",
    fx.Provide(NewJRPC2Server),
    fx.Provide(func(s *JRPC2Server) app.RunnerOut {
        return app.RunnerOut{Runner: s}
    }),
)
```

---

## 4. 通知 / 事件推送范式 (Server → Client)

### 4.1 Server 端推送

```go
// jrpc2 支持服务端主动向客户端推送通知
// 前提：ServerOptions.AllowPush = true

func (s *Service) TurnSubmit(ctx context.Context, params *TurnSubmitParams) (*TurnSubmitResult, error) {
    turnID := uuid.NewString()
    // 通过 event bus 解耦——不在 handler 内直接推送
    event.Emit(events.TurnSubmitted{ThreadID: params.ThreadID, TurnID: turnID})
    return &TurnSubmitResult{TurnID: turnID}, nil
}
```

### 4.2 与 kelindar/event 桥接

```go
// NotificationBridge 监听内部事件，转发为 jrpc2 notification
type NotificationBridge struct {
    srv *jrpc2.Server
}

func (b *NotificationBridge) Setup() {
    event.On[events.AgentStateChanged](func(ev events.AgentStateChanged) {
        _ = b.srv.Notify(context.Background(), "event/agentState", ev)
    })
    event.On[events.TurnCompleted](func(ev events.TurnCompleted) {
        _ = b.srv.Notify(context.Background(), "event/turnComplete", ev)
    })
    event.On[events.UIUpdate](func(ev events.UIUpdate) {
        _ = b.srv.Notify(context.Background(), "event/uiUpdate", ev)
    })
}
```

### 4.3 Client 端接收

```go
clientOpts := &jrpc2.ClientOptions{
    OnNotify: func(req *jrpc2.Request) {
        switch req.Method() {
        case "event/agentState":
            var ev events.AgentStateChanged
            _ = req.UnmarshalParams(&ev)
            handleAgentState(ev)
        case "event/turnComplete":
            var ev events.TurnCompleted
            _ = req.UnmarshalParams(&ev)
            handleTurnComplete(ev)
        }
    },
}
client := jrpc2.NewClient(ch, clientOpts)
```

---

## 5. Transport 层范式

### 5.1 Channel 基础

```go
import "github.com/creachadair/jrpc2/channel"

// 基于 stdio 的 transport（适用于 CLI / MCP server）
ch := channel.Line(os.Stdin, os.Stdout)

// 基于 net.Conn 的 transport（TCP / Unix Socket）
conn, _ := net.Dial("tcp", "localhost:9090")
ch = channel.Line(conn, conn)

// 基于内存 pipe 的 transport（测试用，零延迟）
cli, srv := channel.Pipe(channel.Line)
```

### 5.2 WebSocket 桥接（Wails Desktop 场景）

```go
func wsToChannel(ws *websocket.Conn) channel.Channel {
    r, w := io.Pipe()
    go func() {
        for {
            _, msg, err := ws.ReadMessage()
            if err != nil {
                w.CloseWithError(err)
                return
            }
            w.Write(msg)
            w.Write([]byte("\n"))
        }
    }()
    return channel.Line(r, wsWriter{ws})
}

type wsWriter struct{ ws *websocket.Conn }

func (w wsWriter) Write(p []byte) (int, error) {
    return len(p), w.ws.WriteMessage(websocket.TextMessage, p)
}
```

### 5.3 多连接 Server（Loop 模式）

```go
ln, _ := net.Listen("tcp", ":9090")
listener := server.NewListener(ln, channel.Line)

server.Loop(ctx, listener, server.Static(mux), &server.LoopOptions{
    ServerOptions: opts,
})
```

---

## 6. 请求 / 响应类型定义范式

```go
// 所有 RPC params/result 定义在独立包 internal/rpcapi/types.go
package rpcapi

import "time"

// --- Thread 域 ---

type ThreadListParams struct {
    WorkspaceID string `json:"workspaceId"`
    Limit       int    `json:"limit,omitempty"`
}

type ThreadListResult struct {
    Threads []Thread `json:"threads"`
}

type ThreadCreateParams struct {
    WorkspaceID string `json:"workspaceId"`
    Title       string `json:"title"`
}

type ThreadCreateResult struct {
    Thread Thread `json:"thread"`
}

type ThreadGetParams struct {
    ID string `json:"id"`
}

type ThreadGetResult struct {
    Thread Thread `json:"thread"`
}

// --- Turn 域 ---

type TurnSubmitParams struct {
    ThreadID string `json:"threadId"`
    Message  string `json:"message"`
}

type TurnSubmitResult struct {
    TurnID string `json:"turnId"`
}

// --- 公共模型 ---

type Thread struct {
    ID          string    `json:"id"`
    WorkspaceID string    `json:"workspaceId"`
    Title       string    `json:"title"`
    CreatedAt   time.Time `json:"createdAt"`
}
```

### 6.1 命名约定

| 类别 | 格式 | 示例 |
|---|---|---|
| Method 名 | `domain/action` | `thread/list`, `turn/submit` |
| Params 类型 | `DomainActionParams` | `ThreadListParams` |
| Result 类型 | `DomainActionResult` | `ThreadListResult` |
| Notification | `event/noun` | `event/agentState` |

---

## 7. 错误处理范式

### 7.1 错误码定义

```go
package rpcapi

import "github.com/creachadair/jrpc2"

// 自定义错误码（示例使用非保留区间）
const (
    CodeInternalError    jrpc2.Code = -32000
    CodeStoreFailed      jrpc2.Code = -31001
    CodeNotFound         jrpc2.Code = -31002
    CodeCapabilityDenied jrpc2.Code = -31003
    CodeAgentBusy        jrpc2.Code = -31004
    CodeValidation       jrpc2.Code = -31005
    CodeStateConflict    jrpc2.Code = -31006
)

func ErrNotFound(entity, id string) error {
    return jrpc2.Errorf(CodeNotFound, "%s %q not found", entity, id)
}

func ErrValidation(msg string) error {
    return jrpc2.Errorf(CodeValidation, msg)
}

func ErrAgentBusy(agentID string) error {
    return jrpc2.Errorf(CodeAgentBusy, "agent %q is busy", agentID)
}
```

### 7.2 Handler 中使用

```go
func (s *Service) ThreadGet(ctx context.Context, params *ThreadGetParams) (*ThreadGetResult, error) {
    thread, err := s.queries.GetThread(ctx, params.ID)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, ErrNotFound("thread", params.ID)
        }
        return nil, jrpc2.Errorf(CodeStoreFailed, "get thread: %v", err)
    }
    return &ThreadGetResult{Thread: mapThread(thread)}, nil
}
```

### 7.3 客户端错误解析

```go
var result ThreadGetResult
err := client.CallResult(ctx, "thread/get", params, &result)
if err != nil {
    var je *jrpc2.Error
    if errors.As(err, &je) {
        switch je.Code() {
        case CodeNotFound:
            // handle 404
        case CodeCapabilityDenied:
            // handle 403
        default:
            // unknown server error
        }
    }
}
```

---

## 8. 批量请求处理

```go
// jrpc2 原生支持 JSON-RPC 2.0 batch request
specs := []jrpc2.Spec{
    {Method: "thread/list", Params: &ThreadListParams{WorkspaceID: "ws1"}},
    {Method: "ui/state", Params: nil},
    {Method: "ui/sidebar", Params: nil},
}
results, err := client.Batch(ctx, specs)
if err != nil {
    return err // 整个 batch 失败（如连接断开）
}

for i, r := range results {
    if r.Error() != nil {
        log.Error("batch item failed", "index", i, "method", specs[i].Method, "err", r.Error())
        continue
    }
    switch specs[i].Method {
    case "thread/list":
        var tl ThreadListResult
        _ = r.UnmarshalResult(&tl)
    case "ui/state":
        var us UIStateResult
        _ = r.UnmarshalResult(&us)
    }
}
```

---

## 9. 测试范式

### 9.1 Handler 单元测试（无 server / transport）

```go
func TestThreadList(t *testing.T) {
    mockQ := &MockQuerier{
        ListThreadsFunc: func(ctx context.Context, p db.ListThreadsParams) ([]db.AgentThread, error) {
            return []db.AgentThread{{ID: "t1", Title: "Test Thread"}}, nil
        },
    }
    svc := &Service{queries: mockQ}

    result, err := svc.ThreadList(context.Background(), &ThreadListParams{
        WorkspaceID: "ws1",
        Limit:       10,
    })
    require.NoError(t, err)
    require.Len(t, result.Threads, 1)
    assert.Equal(t, "t1", result.Threads[0].ID)
}
```

### 9.2 集成测试（内存 transport）

```go
func TestRPCIntegration(t *testing.T) {
    svc := newTestService(t)
    mux := BuildServiceMap(svc)

    cli, srv := channel.Pipe(channel.Line)
    s := jrpc2.NewServer(mux, nil).Start(srv)
    c := jrpc2.NewClient(cli, nil)
    defer func() { c.Close(); s.Wait() }()

    var result ThreadListResult
    err := c.CallResult(context.Background(), "thread/list", &ThreadListParams{
        WorkspaceID: "ws1",
    }, &result)
    require.NoError(t, err)
    assert.NotEmpty(t, result.Threads)
}
```

### 9.3 中间件测试

```go
func TestRequireThreadID(t *testing.T) {
    inner := handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
        tid := ctx.Value(ctxKeyThreadID).(string)
        return map[string]string{"threadId": tid}, nil
    })

    wrapped := RequireThreadID()(inner)

    req := mustMakeRequest(t, "turn/submit", map[string]string{"message": "hi"})
    _, err := wrapped.Handle(context.Background(), req)
    require.Error(t, err)
    assert.Contains(t, err.Error(), "threadId is required")
}
```

### 9.4 Notification 测试

```go
func TestNotification(t *testing.T) {
    received := make(chan string, 1)
    clientOpts := &jrpc2.ClientOptions{
        OnNotify: func(req *jrpc2.Request) {
            received <- req.Method()
        },
    }

    cli, srv := channel.Pipe(channel.Line)
    s := jrpc2.NewServer(mux, &jrpc2.ServerOptions{AllowPush: true}).Start(srv)
    c := jrpc2.NewClient(cli, clientOpts)
    defer func() { c.Close(); s.Wait() }()

    var result TurnSubmitResult
    _ = c.CallResult(ctx, "turn/submit", &TurnSubmitParams{
        ThreadID: "t1", Message: "hello",
    }, &result)

    select {
    case method := <-received:
        assert.Equal(t, "event/agentState", method)
    case <-time.After(3 * time.Second):
        t.Fatal("notification timeout")
    }
}
```

---

## 10. 对比 V2 改进表

| 维度 | V2 (手写 RPC) | V3 (jrpc2) |
|---|---|---|
| Handler 注册 | 分散在 6 个 `methods_*.go` 文件 | 单一 `handler.Map` 声明式路由表 |
| Handler 签名 | 3-4 层闭包嵌套 | `func(ctx, *Params) (*Result, error)` |
| 序列化 | 手动 `json.Unmarshal` + envelope | jrpc2 自动处理 |
| 中间件 | `withRequiredThreadID` 等特例函数 | 统一 `Middleware` 链 |
| 错误码 | 自定义 JSON envelope | JSON-RPC 2.0 标准 error code |
| 事件推送 | 自建轮询 / WebSocket | `jrpc2.Server.Notify()` |
| 批量请求 | 不支持 | `client.Batch()` 原生支持 |
| 测试 | 需要启动完整 server | `channel.Pipe` 内存测试 |
| 并发控制 | 无 | `ServerOptions.Concurrency` |
| 方法发现 | 全文搜索 | 一个 Map 全部可见 |

---

## 11. 与其他 5 个框架的集成

| 框架 | 集成点 | 说明 |
|---|---|---|
| **fx** (`skeleton-fx.md`) | `RPCModule` 提供 `JRPC2Server` | fx 注入 `*Service`、`db.Querier`、`*slog.Logger` |
| **oklog/run** (`skeleton-rungroup.md`) | `JRPC2Server.Run()` 作为 Actor | run.Group 管理 server 生命周期 |
| **kelindar/event** (`skeleton-event.md`) | `NotificationBridge` | 内部事件 → jrpc2 notification |
| **stateless** (`skeleton-stateless.md`) | handler 查询状态机 | 状态变更通过 event bus → notification 推送 |
| **sqlc** (`skeleton-sqlc.md`) | handler 调用 `db.Querier` | 类型安全的数据库操作，零手写 SQL |

---

## 12. 禁止行为（红线）

| 规则 | 原因 |
|---|---|
| ❌ 在 handler 内部手动 `json.Unmarshal(req.Body)` | jrpc2 已自动处理反序列化 |
| ❌ 在 `handler.Map` 之外注册 RPC 方法 | 所有路由必须在 `BuildServiceMap` 中可见 |
| ❌ 在 handler 中直接返回 `fmt.Errorf()` | 必须使用 `jrpc2.Errorf()` 携带错误码 |
| ❌ 在 handler 中启动不受管理的 goroutine | 使用 event bus 解耦，或由 run.Group 管理 |
| ❌ 在 middleware 中吞掉错误 | 必须向上传播 |
| ❌ 把 `*jrpc2.Server` 存为全局变量 | 通过 fx 注入 |
| ❌ 混用 REST HTTP handler 和 jrpc2 | 所有 client↔server 通信走 JSON-RPC 2.0 |
| ❌ Handler 中直接 import `*pgxpool.Pool` | 只依赖 `db.Querier` 接口 |
