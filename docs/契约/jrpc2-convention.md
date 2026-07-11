# jrpc2 V3 契约文档

- 目标版本: `github.com/creachadair/jrpc2` `v1.3.5`
- 核验时间: `2026-03-19`
- 核验依据: `README.md`, `doc.go`, `handler/*`, `channel/*`, `jhttp/*`, `server/*`, `v1.3.5` tag 源码
- 说明: 下文所有 Go 示例均已按 `Go 1.25 + jrpc2 v1.3.5` 在临时 module 中完成 `go build` 校验

## 总体结论

`jrpc2` 适合作为 V3 的统一 RPC 底座，不是因为它“功能多”，而是因为它正好覆盖了 V2 的五个核心痛点：

| V2 痛点 | V3 约定 |
|---|---|
| 手写 `typedHandler` 多层闭包 | 用 `handler.New` / `handler.Check(...).Wrap()` 做 typed binding |
| 手写 `withRequiredThreadID` 重复 10+ 次 | 用 typed request 的 `Validate()` 统一校验 |
| 手写 `capabilityGuard` 包装 | 用 `Handler`/`Assigner` 装饰器做中间件链 |
| `dashrpc.Register` 独立注册路径 | 所有方法最终汇总到一个 `handler.Map` |
| 每个 handler 自己做日志/认证/错误 | 统一用装饰器、`RPCLog`、`*jrpc2.Error` 契约 |

V3 的总原则只有五条：

- 公共方法名继续沿用 V2 的斜杠风格，如 `thread/start`、`turn/start`
- 公共参数一律走对象参数，不把数组位置参数暴露为长期契约
- 所有方法最终都进入一个声明式注册表，不再允许第二条平行注册链
- 通用横切逻辑放到装饰器，不再分散到每个 handler
- 错误必须映射成 JSON-RPC 语义明确的 `*jrpc2.Error`

适用范围补充：

- 本文默认约束的是宿主内核心 RPC，也就是 `internal/platform/rpc` 承担的桌面 / UI RPC。
- `cmd/mcp-lsp`、`cmd/mcp-orch`、`cmd/mcp-ida` 也使用 JSON-RPC 2.0 over stdio，但它们是独立 MCP 服务二进制；binary 边界、manifest 和 stdio 生命周期另见 `docs/契约/mcp-service-convention.md`。
- 同一份领域能力如果同时暴露给核心 RPC 和 MCP，只共享 service contract、store contract 和 DTO，不共享 handler registry、notification bridge 或方法命名空间。
- `cmd/` 与 `internal/` 同属模块根 `github.com/lihah111222333-cloud/super-dolphin-agent`，因此 `cmd/mcp-*` 合法 import `internal/*`；这属于 Go `internal` 包规则允许的正常用法。

---

## 1. 框架概述

### jrpc2 是什么

`jrpc2` 是一个完整的 JSON-RPC 2.0 Go 实现，不只是“客户端库”。

它同时提供：

- `jrpc2`: `Server`、`Client`、`Request`、`Response`、标准错误码
- `handler`: typed function 到 `jrpc2.Handler` 的适配
- `channel`: 传输抽象与 framing
- `jhttp`: HTTP bridge / HTTP channel
- `server`: `Loop`、`Local`、多连接服务辅助

它从 `v1.0.0` 起承诺 API 稳定，这一点对 V3 很关键，因为 RPC 契约层不适合建立在频繁破坏式升级的库上。

### 为什么选它

| 方案 | 优点 | 对 V3 的问题 | 结论 |
|---|---|---|---|
| `jrpc2` | 有 client/server、typed handler、batch、transport 抽象、HTTP bridge、本地测试夹具、server push | 没有内建 middleware，需要我们约定装饰器模式 | 选用 |
| `sourcegraph/jsonrpc2` | 连接/stream 抽象成熟，也有 websocket 子包 | 官方 README 明确写了 batch request/response 还不支持；也没有 `handler.New` 这种 typed handler 适配层 | 不选 |
| `ybbus/jsonrpc/v3` | HTTP client 使用方便，支持 batch、自定义 header | 官方定位就是“JSON-RPC over HTTP client”；没有 server、handler、transport 抽象 | 不选 |
| 手写 | 自由度最高 | V2 已证明会把参数解析、错误码、注册、日志、测试全部写散 | 不选 |

### 对 V3 的直接收益

- 80+ 方法可以变成一个声明式 `handler.Map`
- typed request/response 不再需要手写 `json.RawMessage` 解包
- batch、notify、callback、`server.Local` 都是现成能力
- transport 可以和业务逻辑解耦，方法实现不关心底层是 stdio、TCP、HTTP 还是自定义 WebSocket

### 推荐的最小起步形态

```go
package rpc

import (
	"context"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

type PingResponse struct {
	Message string `json:"message"`
}

func NewServer() *jrpc2.Server {
	mux := handler.Map{
		"system/ping": handler.New(func(context.Context) PingResponse {
			return PingResponse{Message: "pong"}
		}),
	}
	return jrpc2.NewServer(mux, &jrpc2.ServerOptions{
		Concurrency: 16,
	})
}
```

### V3 契约结论

- V3 选 `jrpc2`，不是为了“少写代码”，而是为了把方法注册、参数绑定、错误语义、transport、测试夹具收敛成一套稳定模型
- V3 不采用库默认以外的“第二套框架”，尤其不能在 `jrpc2` 外再造一层平行注册/参数体系

---

## 2. 核心概念

### 核心对象

- `Server`: 接收请求、分发 handler、返回响应
- `Client`: 发起 `Call`、`CallResult`、`Batch`、`Notify`
- `Handler`: 实际签名是 `func(context.Context, *jrpc2.Request) (any, error)`
- `handler.Map`: 最简单的 `Assigner`，推荐做 V3 的统一注册表
- `handler.New`: 把 typed function 包成 `jrpc2.Handler`
- `channel.Channel`: 传输抽象，核心方法只有 `Send/Recv/Close`
- 通知机制:
  `jrpc2` 没有名为 `Notifier` 的公开类型
  V3 约定把 `Client.Notify`、`Server.Notify`、`ClientOptions.OnNotify` 统称为“notifier 机制”
- 反向回调机制:
  `Server.Callback` 对应 `ClientOptions.OnCallback`
  这比通知多一个响应

### 需要特别记住的边界

- `handler.New` 是初始化期 API，签名不合法会 panic
- 公共方法如果要严控参数格式，应使用 `handler.Check(...).AllowArray(false).SetStrict(true).Wrap()`
- `rpc.*` 方法默认被框架保留，内建 `rpc.serverInfo`
- server push 不是 JSON-RPC 标准能力，是否可用取决于 transport 和 client 端是否支持

### 核心概念最小示例

```go
package rpc

import (
	"context"

	"github.com/creachadair/jrpc2/handler"
	jrpc2server "github.com/creachadair/jrpc2/server"
)

type EchoRequest struct {
	Text string `json:"text"`
}

func CoreConceptExample(ctx context.Context) error {
	loc := jrpc2server.NewLocal(handler.Map{
		"echo": handler.New(func(context.Context, EchoRequest) string {
			return "ok"
		}),
	}, nil)
	defer loc.Close()

	var out string
	if err := loc.Client.CallResult(ctx, "echo", EchoRequest{Text: "hello"}, &out); err != nil {
		return err
	}
	return loc.Client.Notify(ctx, "echo", EchoRequest{Text: "fire-and-forget"})
}
```

### V3 契约结论

- 统一注册类型是 `handler.Map`
- typed handler 首选 `handler.New`
- transport 只通过 `channel.Channel` 进入业务，不把 HTTP/WebSocket 细节漏到 handler

---

## 3. Handler 注册范式

### 推荐范式

V3 的公共方法名要保持 V2 兼容，因此推荐：

- 公共 API: 用一个扁平 `handler.Map`，方法名继续使用 `thread/start`、`turn/start`
- 内部点号命名空间: 只有确实需要 `ServiceMap` 时才使用，如 `admin.ping`
- 大多数方法: `handler.New`
- 位置参数方法: `handler.NewPos`
  只用于内部小方法或 GET/query 桥接
  不作为公共长期契约

### `handler.New` 能适配的典型签名

- `func(context.Context) error`
- `func(context.Context) T`
- `func(context.Context) (T, error)`
- `func(context.Context, P) error`
- `func(context.Context, P) T`
- `func(context.Context, P) (T, error)`
- `func(context.Context, *jrpc2.Request) ...`

### 推荐注册代码

```go
package rpc

import (
	"context"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

type ThreadStartRequest struct {
	ThreadID string `json:"threadId"`
	Message  string `json:"message"`
}

type ThreadStartResponse struct {
	Accepted bool `json:"accepted"`
}

func threadStart(context.Context, ThreadStartRequest) (ThreadStartResponse, error) {
	return ThreadStartResponse{Accepted: true}, nil
}

func Routes() jrpc2.Assigner {
	return handler.Map{
		"thread/start": handler.New(threadStart),
		"thread/read": handler.New(func(ctx context.Context, req struct {
			ThreadID string `json:"threadId"`
		}) (map[string]string, error) {
			_ = ctx
			return map[string]string{"threadId": req.ThreadID}, nil
		}),
		"math/add": handler.NewPos(func(ctx context.Context, left, right int) int {
			_ = ctx
			return left + right
		}, "left", "right"),
	}
}
```

### V3 契约结论

- 一个服务一个注册表，最后汇总成一个 `handler.Map`
- 不再允许“主注册表 + dashboard 注册表 + 特殊旁路注册表”并存
- 对外方法保持斜杠命名，不为了 `ServiceMap` 去改公共协议

---

## 4. 参数绑定范式

### 推荐范式

V3 对参数绑定采用三层约束：

- 第一层: 所有公共方法都使用 typed request/response struct
- 第二层: 公共参数默认只接受对象参数，不接受数组位置参数
- 第三层: 对关键方法启用 strict field check，拒绝未知字段

`handler.New` 对 struct 参数默认同时接受 object 和 array。
这对内部兼容很好，但对公共协议太宽松。
因此 V3 的公共方法建议使用：

- `handler.Check(fn).AllowArray(false).SetStrict(true).Wrap()`

### 推荐代码

```go
package rpc

import (
	"context"
	"errors"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpc2server "github.com/creachadair/jrpc2/server"
)

type RenameThreadRequest struct {
	ThreadID string `json:"threadId"`
	Title    string `json:"title"`
}

type RenameThreadResponse struct {
	ThreadID string `json:"threadId"`
	Title    string `json:"title"`
}

func StrictObjectOnlyHandler() jrpc2.Handler {
	fi, err := handler.Check(func(ctx context.Context, req RenameThreadRequest) (RenameThreadResponse, error) {
		_ = ctx
		return RenameThreadResponse{
			ThreadID: req.ThreadID,
			Title:    req.Title,
		}, nil
	})
	if err != nil {
		panic(err)
	}
	return fi.AllowArray(false).SetStrict(true).Wrap()
}

func ParameterBindingExample(ctx context.Context) error {
	loc := jrpc2server.NewLocal(handler.Map{
		"thread/name/set": StrictObjectOnlyHandler(),
	}, nil)
	defer loc.Close()

	var rsp RenameThreadResponse
	if err := loc.Client.CallResult(ctx, "thread/name/set", RenameThreadRequest{
		ThreadID: "t-1",
		Title:    "renamed",
	}, &rsp); err != nil {
		return err
	}

	reqs, err := jrpc2.ParseRequests([]byte(`{"jsonrpc":"2.0","id":1,"method":"demo","params":{"threadId":"t-1","title":"ok","extra":true}}`))
	if err != nil {
		return err
	}
	req := reqs[0].ToRequest()
	if req == nil {
		return errors.New("expected request")
	}

	var strict RenameThreadRequest
	if err := req.UnmarshalParams(jrpc2.StrictFields(&strict)); err == nil {
		return errors.New("expected strict decode to reject unknown field")
	}
	return nil
}
```

### V3 契约结论

- 公共请求参数只允许对象格式
- 关键 request struct 开 strict 模式，避免“多传字段被悄悄忽略”
- client 侧默认使用 `CallResult` 直接解到 typed response

---

## 5. 中间件范式

### 事实边界

`jrpc2` 没有内建 middleware 链。
所以 V3 必须显式约定自己的装饰器模式。

### 推荐模式

- 通用横切逻辑:
  用 `type Middleware func(jrpc2.Handler) jrpc2.Handler`
- 面向注册表统一套用:
  用 `WrapAssigner`
- 参数校验:
  放到 typed binder helper 中，不要把 schema 逻辑塞进原始 `json.RawMessage` middleware
- capability/auth/logging:
  放到 middleware

### 推荐代码

```go
package rpc

import (
	"context"
	"errors"
	"io"
	"log"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

type ThreadStartRequest struct {
	ThreadID string `json:"threadId"`
	Message  string `json:"message"`
}

func (r ThreadStartRequest) Validate() error {
	if r.ThreadID == "" {
		return errors.New("threadId is required")
	}
	if r.Message == "" {
		return errors.New("message is required")
	}
	return nil
}

type Principal struct {
	Subject string
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func CurrentPrincipal(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

type Middleware func(jrpc2.Handler) jrpc2.Handler

type AssignerFunc func(context.Context, string) jrpc2.Handler

func (f AssignerFunc) Assign(ctx context.Context, method string) jrpc2.Handler {
	return f(ctx, method)
}

func Chain(mws ...Middleware) Middleware {
	return func(next jrpc2.Handler) jrpc2.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	}
}

func WrapAssigner(next jrpc2.Assigner, mws ...Middleware) jrpc2.Assigner {
	return AssignerFunc(func(ctx context.Context, method string) jrpc2.Handler {
		h := next.Assign(ctx, method)
		if h == nil {
			return nil
		}
		return Chain(mws...)(h)
	})
}

const CodeUnauthorized jrpc2.Code = 1001

func Logging(lg *log.Logger) Middleware {
	return func(next jrpc2.Handler) jrpc2.Handler {
		return func(ctx context.Context, req *jrpc2.Request) (any, error) {
			start := time.Now()
			out, err := next(ctx, req)
			lg.Printf("method=%s id=%s err=%v elapsed=%s", req.Method(), req.ID(), err, time.Since(start))
			return out, err
		}
	}
}

func RequireAuth() Middleware {
	return func(next jrpc2.Handler) jrpc2.Handler {
		return func(ctx context.Context, req *jrpc2.Request) (any, error) {
			if _, ok := CurrentPrincipal(ctx); !ok {
				return nil, jrpc2.Errorf(CodeUnauthorized, "missing principal")
			}
			return next(ctx, req)
		}
	}
}

type ValidatedRequest interface {
	Validate() error
}

func NewValidated[P ValidatedRequest](fn func(context.Context, P) (any, error)) jrpc2.Handler {
	return handler.New(func(ctx context.Context, req P) (any, error) {
		if err := req.Validate(); err != nil {
			return nil, jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())
		}
		return fn(ctx, req)
	})
}

func MiddlewareRoutes() jrpc2.Assigner {
	base := handler.Map{
		"thread/start": NewValidated(func(ctx context.Context, req ThreadStartRequest) (any, error) {
			_ = ctx
			return map[string]bool{"accepted": true}, nil
		}),
	}
	return WrapAssigner(
		base,
		Logging(log.New(io.Discard, "rpc ", log.LstdFlags)),
		RequireAuth(),
	)
}
```

### V3 契约结论

- 日志、认证、capability 走 middleware
- request schema 校验走 `Validate()`，不要重新发明 `withRequiredThreadID`
- 不再允许每个 handler 各自堆闭包做横切逻辑

---

## 6. 通知 / 事件推送范式

### 推荐范式

Server -> Client 的一对多事件推送，V3 约定使用：

- server 侧: `Server.Notify`
- client 侧: `ClientOptions.OnNotify`
- 若需要反向请求并等待结果: `Server.Callback` + `ClientOptions.OnCallback`

### transport 约束

- 这属于非标准 JSON-RPC 扩展
- 只有 `ServerOptions.AllowPush=true` 时可用
- `jhttp.NewBridge` 不会把 server push 转发给远端 HTTP client
  原因是 bridge 内部共享一个本地 client，无法知道应该推给哪个 HTTP 请求方
- 浏览器场景优先 WebSocket
- SSE 只能天然承载 server -> client，若要完整双向 RPC 仍需额外上行通道

### 推荐代码

```go
package rpc

import (
	"context"
	"errors"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpc2server "github.com/creachadair/jrpc2/server"
)

type ThreadStartRequest struct {
	ThreadID string `json:"threadId"`
	Message  string `json:"message"`
}

type ThreadStartResponse struct {
	Accepted bool `json:"accepted"`
}

type EventEnvelope struct {
	Name string `json:"name"`
}

func NotifyExample(ctx context.Context) error {
	received := make(chan EventEnvelope, 1)

	loc := jrpc2server.NewLocal(handler.Map{
		"thread/start": handler.New(func(ctx context.Context, req ThreadStartRequest) (ThreadStartResponse, error) {
			if err := jrpc2.ServerFromContext(ctx).Notify(ctx, "agent/event", EventEnvelope{
				Name: "thread.started",
			}); err != nil {
				return ThreadStartResponse{}, err
			}
			_ = req
			return ThreadStartResponse{Accepted: true}, nil
		}),
	}, &jrpc2server.LocalOptions{
		Server: &jrpc2.ServerOptions{AllowPush: true},
		Client: &jrpc2.ClientOptions{
			OnNotify: func(req *jrpc2.Request) {
				var evt EventEnvelope
				_ = req.UnmarshalParams(&evt)
				received <- evt
			},
		},
	})
	defer loc.Close()

	var rsp ThreadStartResponse
	if err := loc.Client.CallResult(ctx, "thread/start", ThreadStartRequest{
		ThreadID: "t-1",
		Message:  "hello",
	}, &rsp); err != nil {
		return err
	}

	select {
	case <-received:
		return nil
	case <-time.After(time.Second):
		return errors.New("timeout waiting for notification")
	}
}
```

### V3 契约结论

- 事件流优先 `Notify`
- 需要回执时才用 `Callback`
- HTTP bridge 不承担浏览器实时推送契约

---

## 7. 错误处理范式

### 推荐范式

V3 的错误要分三层：

- 协议层:
  用 `jrpc2` 标准错误码
  如 `ParseError`、`InvalidRequest`、`MethodNotFound`、`InvalidParams`
- 运行层:
  `context.Canceled`、`context.DeadlineExceeded` 会自动映射到 `Cancelled` / `DeadlineExceeded`
- 业务层:
  使用应用自定义 code
  必须避开保留区间 `-32768` 到 `-32000`

### 推荐映射

- 参数问题: `jrpc2.InvalidParams`
- 未登录/未授权: 自定义 code，如 `1001`
- capability 不支持: 自定义 code，如 `1002`
- 业务冲突: 自定义 code，如 `1003`

### 推荐代码

```go
package rpc

import (
	"context"
	"errors"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpc2server "github.com/creachadair/jrpc2/server"
)

const CodeCapabilityDenied jrpc2.Code = 1002

type ThreadStartRequest struct {
	ThreadID string `json:"threadId"`
	Message  string `json:"message"`
}

type ThreadStartResponse struct {
	Accepted bool `json:"accepted"`
}

func ErrorExample(ctx context.Context) error {
	loc := jrpc2server.NewLocal(handler.Map{
		"turn/start": handler.New(func(context.Context, ThreadStartRequest) (ThreadStartResponse, error) {
			return ThreadStartResponse{}, jrpc2.Errorf(CodeCapabilityDenied, "provider does not support message_send").WithData(map[string]any{
				"provider":   "offline",
				"capability": "message_send",
			})
		}),
	}, nil)
	defer loc.Close()

	var rsp ThreadStartResponse
	err := loc.Client.CallResult(ctx, "turn/start", ThreadStartRequest{
		ThreadID: "t-1",
		Message:  "hello",
	}, &rsp)

	var jerr *jrpc2.Error
	if !errors.As(err, &jerr) {
		return errors.New("expected *jrpc2.Error")
	}
	if jerr.Code != CodeCapabilityDenied {
		return errors.New("unexpected error code")
	}
	return nil
}
```

### V3 契约结论

- handler 返回的错误必须是 JSON-RPC 可解释错误
- 业务错误要有稳定 code，不能只靠 message 文本
- 可观测上下文放 `WithData`

---

## 8. Transport 范式

### 推荐分层

- stdio / pipe:
  优先用于 `cmd/mcp-*` 这类独立 MCP 服务二进制，或本地进程桥接
- TCP:
  用 `channel.Line`、`channel.LSP` 或其他 framing
- HTTP:
  用 `jhttp.NewBridge` 暴露服务
  用 `jhttp.NewChannel` 做 HTTP client
- WebSocket:
  `jrpc2` 没有官方 transport
  自己实现一个 `channel.Channel` 适配器

### 推荐代码

```go
package rpc

import (
	"context"
	"net"
	"net/http"
	"os"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/jhttp"
	jrpc2server "github.com/creachadair/jrpc2/server"
)

func routes() jrpc2.Assigner {
	return handler.Map{
		"system/ping": handler.New(func(context.Context) string { return "pong" }),
	}
}

func StartStdio() *jrpc2.Server {
	srv := jrpc2.NewServer(routes(), nil)
	return srv.Start(channel.Line(os.Stdin, os.Stdout))
}

func ServeTCP(ctx context.Context, lst net.Listener) error {
	return jrpc2server.Loop(
		ctx,
		jrpc2server.NetAccepter(lst, channel.Line),
		jrpc2server.Static(routes()),
		nil,
	)
}

func HTTPHandler() http.Handler {
	return jhttp.NewBridge(routes(), nil)
}

type websocketConn interface {
	ReadMessage() (messageType int, data []byte, err error)
	WriteMessage(messageType int, data []byte) error
	Close() error
}

type wsChannel struct {
	conn websocketConn
}

const wsTextMessage = 1

func (w *wsChannel) Send(msg []byte) error {
	return w.conn.WriteMessage(wsTextMessage, msg)
}

func (w *wsChannel) Recv() ([]byte, error) {
	_, data, err := w.conn.ReadMessage()
	return data, err
}

func (w *wsChannel) Close() error {
	return w.conn.Close()
}

func NewWebSocketChannel(conn websocketConn) channel.Channel {
	return &wsChannel{conn: conn}
}
```

### V3 契约结论

- handler 不关心 transport
- 核心 `platform/rpc` 与 `cmd/mcp-*` 是两套独立装配根；前者服务宿主 UI，后者服务外部 MCP 宿主
- HTTP 只负责桥接，不承担实时双向推送契约
- WebSocket 适配层是 transport 代码，不是业务代码
- stdio transport 出现在本文时，默认指独立 MCP binary 的通信通道；manifest、tool family 装配和 stdio loop 规则不在核心 RPC 契约里定义

---

## 9. Context 传播范式

### 推荐范式

在 `jrpc2` 里，context 的核心来源有三个：

- handler 入参 `ctx`
- `jrpc2.InboundRequest(ctx)` 取当前请求
- `jrpc2.ServerFromContext(ctx)` 取当前 server

另外，`ServerOptions.NewContext` 可以提供每次请求的基础 context。

### 必须明确的限制

- `NewContext` 没有入参，因此它拿不到当前 HTTP request、WebSocket frame 等 transport 细节
- `jhttp.Bridge` 虽然使用 `req.Context()` 驱动 bridge 侧调用生命周期，但 server handler 的 base context 仍来自 `ServerOptions.NewContext`
- client 端 context 取消不会自动传播到 server
  如果需要远端取消，要定义专门的 cancel RPC

### 推荐代码

```go
package rpc

import (
	"context"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpc2server "github.com/creachadair/jrpc2/server"
)

type tenantKey struct{}

func ContextExample(ctx context.Context) error {
	base := context.WithValue(context.Background(), tenantKey{}, "tenant-a")

	loc := jrpc2server.NewLocal(handler.Map{
		"context/read": handler.New(func(ctx context.Context) (map[string]string, error) {
			req := jrpc2.InboundRequest(ctx)
			tenant, _ := ctx.Value(tenantKey{}).(string)
			_ = jrpc2.ServerFromContext(ctx)
			return map[string]string{
				"method": req.Method(),
				"tenant": tenant,
			}, nil
		}),
	}, &jrpc2server.LocalOptions{
		Server: &jrpc2.ServerOptions{
			NewContext: func() context.Context { return base },
		},
	})
	defer loc.Close()

	var out map[string]string
	return loc.Client.CallResult(ctx, "context/read", nil, &out)
}
```

### V3 契约结论

- 业务上下文通过 `NewContext` 注入
- 请求元信息通过 `InboundRequest` 读取
- transport 原生上下文不自动进入 handler，不能误判

---

## 10. 与 fx 集成

### 推荐范式

fx 负责构造依赖，不负责定义 RPC 契约本身。
V3 推荐：

- 用 `fx.Module` 提供 listener、assigner、`Runner`
- 让 `Runner.Run(ctx)` 真正执行 `server.Loop`
- 用 `fx.Lifecycle` 只做资源生命周期，如关闭 listener

### 推荐代码

```go
package rpc

import (
	"context"
	"net"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/creachadair/jrpc2/handler"
	jrpc2server "github.com/creachadair/jrpc2/server"
	"go.uber.org/fx"
)

type Runner interface {
	Run(context.Context) error
}

type JRPCComponent struct {
	listener net.Listener
	assigner jrpc2.Assigner
	options  *jrpc2.ServerOptions
}

func NewJRPCComponent(lc fx.Lifecycle, listener net.Listener, assigner jrpc2.Assigner) *JRPCComponent {
	c := &JRPCComponent{
		listener: listener,
		assigner: assigner,
		options:  &jrpc2.ServerOptions{Concurrency: 16},
	}
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			_ = ctx
			return c.listener.Close()
		},
	})
	return c
}

func (c *JRPCComponent) Run(ctx context.Context) error {
	return jrpc2server.Loop(
		ctx,
		jrpc2server.NetAccepter(c.listener, channel.Line),
		jrpc2server.Static(c.assigner),
		&jrpc2server.LoopOptions{ServerOptions: c.options},
	)
}

var JRPCModule = fx.Module(
	"jrpc2",
	fx.Provide(
		func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		},
		func() jrpc2.Assigner {
			return handler.Map{
				"system/ping": handler.New(func(context.Context) string { return "pong" }),
			}
		},
		NewJRPCComponent,
		func(c *JRPCComponent) Runner { return c },
	),
)
```

### V3 契约结论

- fx 只负责构造，不要让 fx 直接承载 transport 逻辑分支
- 对外暴露统一 `Runner`，方便和 `oklog/run` 汇合
- `jrpc2.Server` 不应该孤立存在，应该总是挂在一个可运行组件上

---

## 11. 与 oklog/run 集成

### 推荐范式

`oklog/run` 的角色是 goroutine 编排。
V3 推荐把 jrpc2 server 作为一个 actor 放进 `run.Group`。

### 推荐代码

```go
package rpc

import (
	"context"
	"net"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	jrpc2server "github.com/creachadair/jrpc2/server"
	"github.com/oklog/run"
)

func AddJRPCActor(g *run.Group, listener net.Listener, assigner jrpc2.Assigner) {
	ctx, cancel := context.WithCancel(context.Background())
	g.Add(
		func() error {
			return jrpc2server.Loop(
				ctx,
				jrpc2server.NetAccepter(listener, channel.Line),
				jrpc2server.Static(assigner),
				&jrpc2server.LoopOptions{
					ServerOptions: &jrpc2.ServerOptions{Concurrency: 16},
				},
			)
		},
		func(error) {
			cancel()
			_ = listener.Close()
		},
	)
}
```

### V3 契约结论

- `run.Group` 管 goroutine 生命周期
- `server.Loop` 管连接接受与 per-connection server
- fx 和 `run.Group` 不重复管同一层职责

---

## 12. 测试范式

### 单测目标

对 handler 的单测，不需要启动真实 server。
推荐直接测：

- `handler.New(...)` 生成的 handler
- `jrpc2.ParseRequests(...).ToRequest()` 生成的请求
- handler 返回值或 `*jrpc2.Error`

### 推荐代码

```go
package rpc_test

import (
	"context"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

type RenameThreadRequest struct {
	ThreadID string `json:"threadId"`
	Title    string `json:"title"`
}

type RenameThreadResponse struct {
	ThreadID string `json:"threadId"`
	Title    string `json:"title"`
}

func TestRenameHandler(t *testing.T) {
	h := handler.New(func(ctx context.Context, req RenameThreadRequest) (RenameThreadResponse, error) {
		_ = ctx
		return RenameThreadResponse{
			ThreadID: req.ThreadID,
			Title:    req.Title,
		}, nil
	})

	parsed, err := jrpc2.ParseRequests([]byte(`{"jsonrpc":"2.0","id":1,"method":"thread/name/set","params":{"threadId":"t-1","title":"new"}}`))
	if err != nil {
		t.Fatal(err)
	}
	req := parsed[0].ToRequest()
	if req == nil {
		t.Fatal("expected request")
	}

	raw, err := h(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	rsp, ok := raw.(RenameThreadResponse)
	if !ok {
		t.Fatalf("unexpected response type %T", raw)
	}
	if rsp.ThreadID != "t-1" {
		t.Fatalf("unexpected thread id %q", rsp.ThreadID)
	}
}
```

### 补充建议

- 集成测试再用 `server.NewLocal`
- transport 测试和业务 handler 测试分层写
- 不要把“起 server + 走 socket”当成所有测试的起点

---

## 13. 反模式

### 禁止写法

- 禁止为每个方法手写 `json.RawMessage` 解包，除非参数结构确实动态
- 禁止把 `threadId` 必填校验写成 20 个 `withRequiredThreadID(...)` 包装
- 禁止把 capability 判断散落在每个方法里
- 禁止维护第二套注册链，如 `dashrpc.Register` 旁路写入
- 禁止对公共 API 使用位置数组参数作为长期契约
- 禁止假设 HTTP bridge 能透传 server push 给远端浏览器
- 禁止在 handler 内调用 `Server.Wait` / `WaitStatus`
- 禁止在 client callback handler 内关闭 client
- 禁止随意占用 `rpc.*` 命名空间，除非显式 `DisableBuiltin=true`

### 推荐替代写法

```go
package rpc

import (
	"context"
	"errors"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

type ThreadLookupRequest struct {
	ThreadID string `json:"threadId"`
}

func (r ThreadLookupRequest) Validate() error {
	if r.ThreadID == "" {
		return errors.New("threadId is required")
	}
	return nil
}

func GoodRoutes() handler.Map {
	// BAD:
	// routes["thread/read"] = typedHandler(func(ctx context.Context, p threadIDParams) (any, error) {
	//     return withRequiredThreadID("thread/read", p.ThreadID, func(threadID string) (any, error) { ... })
	// })
	//
	// GOOD:
	return handler.Map{
		"thread/read": handler.New(func(ctx context.Context, req ThreadLookupRequest) (map[string]string, error) {
			_ = ctx
			if err := req.Validate(); err != nil {
				return nil, jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())
			}
			return map[string]string{"threadId": req.ThreadID}, nil
		}),
	}
}
```

### V3 契约结论

- 公共方法不再接受“看起来能跑”的临时闭包拼装
- 契约必须是 typed、声明式、可批量审查的

---

## 14. V2 → V3 迁移指南

### 迁移目标

把 V2 的 80+ 个手写 RPC 方法迁成：

- 一个统一 `handler.Map`
- 一套 typed request/response struct
- 一套装饰器链
- 一套错误码契约

### 迁移映射表

| V2 写法 | V3 写法 |
|---|---|
| `typedHandler(s.xxxTyped)` | `handler.New(s.xxx)` 或 `handler.Check(...).Wrap()` |
| `withRequiredThreadID(...)` | `req.Validate()` |
| `capabilityGuard(...)` | `RequireCapability(...)` middleware |
| `dashrpc.Register(...)` | 适配成 `handler.Map` 条目，汇总进主注册表 |
| handler 内自己打日志 | 统一中间件 / `RPCLog` |
| 文本错误 + 猜测码 | `*jrpc2.Error` + 稳定业务 code |

### 迁移步骤

1. 先冻结 V2 的公共方法名，V3 不改协议名字
2. 为每个方法定义 request/response struct
3. 把每个 `typedHandler` 转成普通 Go typed function
4. 把 `withRequiredThreadID` 迁到 `Validate()`
5. 把 capability 判断迁到注册期装饰器
6. 把 dashboard 侧方法适配成 `handler.Map`，不再保留旁路注册
7. 用 `server.NewLocal` 和 direct handler test 补齐回归用例

### 迁移示例

```go
package rpc

import (
	"context"
	"errors"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

const CodeCapabilityDenied jrpc2.Code = 1002

type ThreadLookupRequest struct {
	ThreadID string `json:"threadId"`
}

func (r ThreadLookupRequest) Validate() error {
	if r.ThreadID == "" {
		return errors.New("threadId is required")
	}
	return nil
}

type ThreadStartRequest struct {
	ThreadID string `json:"threadId"`
	Message  string `json:"message"`
}

func (r ThreadStartRequest) Validate() error {
	if r.ThreadID == "" {
		return errors.New("threadId is required")
	}
	if r.Message == "" {
		return errors.New("message is required")
	}
	return nil
}

type CapabilitySet map[string]bool

func MigratedRoutes(caps CapabilitySet) handler.Map {
	// V2:
	// s.methods["thread/read"] = typedHandler(func(ctx context.Context, p threadIDParams) (any, error) {
	//     return withRequiredThreadID("thread/read", p.ThreadID, func(threadID string) (any, error) { ... })
	// })
	// s.methods["turn/start"] = s.capabilityGuard(capabilityMessageSend, "...", typedHandler(s.turnStartTyped))
	//
	// V3:
	return handler.Map{
		"thread/read": handler.New(func(ctx context.Context, req ThreadLookupRequest) (map[string]string, error) {
			_ = ctx
			if err := req.Validate(); err != nil {
				return nil, jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())
			}
			return map[string]string{"threadId": req.ThreadID}, nil
		}),
		"turn/start": handler.New(func(ctx context.Context, req ThreadStartRequest) (map[string]bool, error) {
			_ = ctx
			if !caps["message_send"] {
				return nil, jrpc2.Errorf(CodeCapabilityDenied, "provider does not support message_send")
			}
			if err := req.Validate(); err != nil {
				return nil, jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())
			}
			return map[string]bool{"accepted": true}, nil
		}),
	}
}
```

### 迁移策略建议

- 第一批先迁“纯 typed + 低副作用”方法，快速建立模板
- 第二批迁所有 `threadId` 类方法，统一收敛 `Validate()`
- 第三批迁 capability-gated 方法，统一收敛 middleware
- 第四批迁 dashboard 聚合方法，彻底消灭第二注册路径

### 对 `dashrpc.Register` 的具体约定

- 可以保留 dashboard provider / caller 这些业务实现
- 但注册动作必须产出 `jrpc2.Handler`
- 最终仍然进入主 `handler.Map`
- V3 不再接受“某些方法来自 `handler.Map`，某些方法来自 `dashrpc.Register` 旁路写入”的结构

---

## 补充契约

### 命名约定

- 公共方法名: 继续沿用 V2，如 `thread/start`
- request 类型: `ThreadStartRequest`
- response 类型: `ThreadStartResponse`
- push 事件名: `agent/event` 或业务域专名，如 `thread/updated`

### 并发约定

`jrpc2` server 会并发处理重叠请求。
因此 V3 不能依赖“两个并发 call 的天然执行顺序”。
如果客户端要求 A 一定先于 B 完成，必须等待 A 返回再发 B。

### Builtin 约定

- 默认保留 `rpc.serverInfo`
- 非必要不要关 `DisableBuiltin`
- 若确实要自定义 `rpc.*`，必须在模块边界上显式说明

### 观测性约定

- 协议级 request/response 审计可用 `ServerOptions.RPCLog`
- 业务级日志用 middleware
- 不把 logging 分散到每个 handler 里重复实现

---

## 调研依据

- `jrpc2` README: `https://github.com/creachadair/jrpc2/blob/v1.3.5/README.md`
- `jrpc2` pkg docs: `https://pkg.go.dev/github.com/creachadair/jrpc2@v1.3.5`
- `handler` pkg docs: `https://pkg.go.dev/github.com/creachadair/jrpc2/handler@v1.3.5`
- `channel` pkg docs: `https://pkg.go.dev/github.com/creachadair/jrpc2/channel@v1.3.5`
- `jhttp` pkg docs: `https://pkg.go.dev/github.com/creachadair/jrpc2/jhttp@v1.3.5`
- `server` pkg docs: `https://pkg.go.dev/github.com/creachadair/jrpc2/server@v1.3.5`
- `sourcegraph/jsonrpc2` README: `https://github.com/sourcegraph/jsonrpc2/blob/v0.2.1/README.md`
- `ybbus/jsonrpc/v3` README: `https://github.com/ybbus/jsonrpc/blob/v3.1.7/README.md`

## 最终落地建议

V3 最值得坚持的不是“换成 `jrpc2`”，而是借这次迁移把 RPC 契约彻底收敛成四个固定层次：

- 注册层: 一个 `handler.Map`
- 绑定层: typed request/response
- 横切层: middleware / decorator
- transport 层: `channel.Channel` / `jhttp` / 自定义 adapter

只要这四层不再混写，V2 里 `typedHandler`、`withRequiredThreadID`、`capabilityGuard`、`dashrpc.Register` 的碎片化问题就不会再回到 V3。
