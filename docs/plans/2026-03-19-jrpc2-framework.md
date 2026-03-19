# V3 架构决策：引入 creachadair/jrpc2 替代手写 RPC

> **日期:** 2026-03-19
> **决策:** 引入 creachadair/jrpc2 作为 JSON-RPC 2.0 框架
> **影响:** 砍掉 ~2,300-3,100 行 RPC 样板代码

---

## 1. V2 的 RPC 痛点

### 1.1 手写分发链

```
请求进入
  → server_transport.go: 手动解析 Request envelope
  → server_conn.go: map[string]Handler 路由
  → methods_*.go: typedHandler 包装 + json.Unmarshal
  → 业务逻辑
  → 手动构造 map[string]any{} 响应
  → 手动包装 RPCError
```

每个 handler 里 **40-60% 是样板**：参数解析、错误包装、响应构造。

### 1.2 样板代码量化

| 类别 | 行数 | 说明 |
|---|---|---|
| 参数结构体定义 | ~220 | 44 个 struct × 5 行 |
| handler 绑定代码 | ~260 | 130 个 handler × 2 行 |
| 错误处理包装 | ~780-1,040 | 每个 handler 5-8 行 |
| 响应 map 构造 | ~1,040-1,560 | 每个 handler 8-12 行 |
| **合计样板** | **~2,300-3,100** | |

### 1.3 V2 的 typedHandler 已有雏形

```go
// V2 已有泛型 handler 包装，但功能有限
func typedHandler[P any](fn func(ctx context.Context, p P) (any, error)) Handler {
    return func(ctx context.Context, raw json.RawMessage) (any, error) {
        var p P
        if raw != nil {
            if err := json.Unmarshal(raw, &p); err != nil {
                return nil, pkgerr.Wrap(err, "TypedHandler", "invalid params")
            }
        }
        return fn(ctx, p)
    }
}
```

这只解决了参数解析，没解决：路由、错误码、批量请求、通知推送、中间件。

---

## 2. 框架选型

### 2.1 候选对比

| 框架 | Stars | 类型化 Handler | 批量 | 双向 | 中间件 | 流式 | 代码生成 |
|---|---|---|---|---|---|---|---|
| **creachadair/jrpc2** | ~88 | ✅ 反射 | ✅ | ✅ push+callback | ✅ Assigner 包装 | channel | ❌ |
| sourcegraph/jsonrpc2 | ~244 | ❌ 手动分发 | ❌ | ✅ | ❌ | ❌ | ❌ |
| go-ethereum/rpc | 48k(整库) | ✅ 反射 | ✅ | ✅ reverse call | ❌ | ✅ subscription | ❌ |
| filecoin/go-jsonrpc | ~97 | ✅ 反射 | ? | ✅ reverse call | ❌ | ✅ channel | ❌ |
| semrush/zenrpc | ~172 | ✅ **代码生成** | ✅ | ❌ | ✅ .Use() | ❌ | ✅ |

### 2.2 选择 creachadair/jrpc2

**理由：**

1. **完美匹配需求** — 写普通 Go 函数就是 handler，零样板
2. **全规范实现** — batch、notification、bidirectional push、positional params
3. **中间件支持** — 通过 Assigner 包装实现 logging/auth/metrics
4. **IBM fork** — 企业信任度
5. **轻量无锁** — 不是全框架，只做 JSON-RPC 协议层
6. **测试友好** — `server.NewLocal()` 内存级测试

**唯一缺口：无原生 WebSocket/SSE**
- 解决方案：用 `jhttp` 桥接 HTTP，SSE 通知走自定义 channel
- 或者用 `channel.Channel` 接口适配 WebSocket

### 2.3 淘汰理由

- **sourcegraph/jsonrpc2** — 仍然手动分发，不解决核心痛点
- **go-ethereum/rpc** — 太重，绑定 Ethereum 命名约定（下划线分隔）
- **zenrpc** — 依赖代码生成，增加构建复杂度

---

## 3. V2 → V3 代码对比

### 3.1 Handler 注册

```go
// ════════ V2: 手写绑定（3 步）════════

// 步骤 1: 定义参数结构体
type threadStartParams struct {
    Model            string `json:"model,omitempty"`
    ModelProvider    string `json:"modelProvider,omitempty"`
    Cwd              string `json:"cwd,omitempty"`
    ApprovalPolicy   string `json:"approvalPolicy,omitempty"`
    BaseInstructions string `json:"baseInstructions,omitempty"`
}

// 步骤 2: 写 handler（夹杂样板）
func (s *Server) handleThreadStart(ctx context.Context, p threadStartParams) (any, error) {
    result, err := s.threadService.Start(ctx, p.Model, p.Cwd, ...)
    if err != nil {
        return nil, err
    }
    // 步骤 3: 手动构造响应 map
    return map[string]any{
        "thread":         threadInfo{ID: result.ThreadID, Status: result.Status},
        "model":          result.Model,
        "modelProvider":  result.ModelProvider,
        "cwd":            result.Cwd,
        "approvalPolicy": result.ApprovalPolicy,
    }, nil
}

// 步骤 4: 注册
s.methods["thread/start"] = typedHandler(s.handleThreadStart)


// ════════ V3: jrpc2 一步到位 ════════

// 只需定义请求/响应类型 + 业务函数
type ThreadStartReq struct {
    Model            string `json:"model,omitempty"`
    ModelProvider    string `json:"modelProvider,omitempty"`
    Cwd              string `json:"cwd,omitempty"`
    ApprovalPolicy   string `json:"approvalPolicy,omitempty"`
    BaseInstructions string `json:"baseInstructions,omitempty"`
}

type ThreadStartResp struct {
    Thread         ThreadInfo `json:"thread"`
    Model          string     `json:"model"`
    ModelProvider  string     `json:"modelProvider"`
    Cwd            string     `json:"cwd"`
    ApprovalPolicy string     `json:"approvalPolicy"`
}

func (s *Server) ThreadStart(ctx context.Context, req ThreadStartReq) (ThreadStartResp, error) {
    result, err := s.threadService.Start(ctx, req.Model, req.Cwd, ...)
    if err != nil {
        return ThreadStartResp{}, err
    }
    return ThreadStartResp{
        Thread:         ThreadInfo{ID: result.ThreadID, Status: result.Status},
        Model:          result.Model,
        ModelProvider:  result.ModelProvider,
        Cwd:            result.Cwd,
        ApprovalPolicy: result.ApprovalPolicy,
    }, nil
}

// 注册 — 零样板
mux := handler.ServiceMap{
    "thread": handler.Map{
        "start": handler.New(s.ThreadStart),
    },
}
```

**差异：**
- 无 `typedHandler` 包装 — jrpc2 自动做
- 无 `json.Unmarshal` — jrpc2 反射完成
- 无 `map[string]any{}` — 直接返回类型化 struct
- 无手动 `s.methods[name] =` — ServiceMap 自动路由

### 3.2 错误处理

```go
// ════════ V2: 手动错误码映射 ════════
func normalizeInternalErrorCode(code int, msg string) int {
    if code != CodeInternalError { return code }
    lower := strings.ToLower(msg)
    if strings.Contains(lower, "invalid params") { return CodeInvalidParams }
    if strings.Contains(lower, "is required")    { return CodeInvalidParams }
    // ... 10+ 条消息匹配规则
    return code
}


// ════════ V3: jrpc2 原生错误码 ════════
import "github.com/creachadair/jrpc2/code"

func (s *Server) ThreadStart(ctx context.Context, req ThreadStartReq) (ThreadStartResp, error) {
    if req.Model == "" {
        return ThreadStartResp{}, code.InvalidParams.Err() // 直接返回标准错误码
    }
    // ...
}

// 自定义错误码
var ErrThreadNotFound = code.Register(-32001, "thread not found")
```

### 3.3 通知推送

```go
// ════════ V2: 手动构造 Notification 结构 ════════
notif := &Notification{JSONRPC: "2.0", Method: method, Params: params}
data, _ := json.Marshal(notif)
// 手动广播到 SSE clients


// ════════ V3: jrpc2 内建推送 ════════
func (s *Server) handleSomeEvent(ctx context.Context, event Event) {
    srv := jrpc2.ServerFromContext(ctx)
    // 单行推送，框架处理序列化和传输
    srv.Notify(ctx, "thread/name/updated", map[string]any{
        "threadId": event.ThreadID,
        "name":     event.Name,
    })
}
```

### 3.4 批量请求

```go
// ════════ V2: 完全不支持批量请求 ════════
// server_transport.go 只处理单个 Request


// ════════ V3: jrpc2 原生支持 ════════
// 无需任何额外代码，框架自动处理 JSON 数组形式的批量请求
```

---

## 4. V3 RPC 架构

### 4.1 分层

```
┌──────────────────────────────────────────┐
│              Transport Layer             │
│  HTTP ─→ jhttp.Bridge ─→ jrpc2.Server   │
│  SSE  ─→ custom Channel (通知推送)       │
│  WS   ─→ custom Channel (双向)          │
└──────────────────────────────────────────┘
                    │
┌──────────────────────────────────────────┐
│            Middleware Layer               │
│  LoggingAssigner → AuthAssigner →        │
│  MetricsAssigner → handler.ServiceMap    │
└──────────────────────────────────────────┘
                    │
┌──────────────────────────────────────────┐
│           Service Layer (业务)            │
│  ThreadService   → thread/* methods      │
│  TurnService     → turn/* methods        │
│  SkillService    → skills/* methods      │
│  WorkspaceService→ workspace/* methods   │
│  UIService       → ui/* methods          │
│  ConfigService   → config/* methods      │
│  LspGuiService   → lsp/gui_* methods    │
└──────────────────────────────────────────┘
```

### 4.2 Service 组织方式

每个 Service 是一个 Go struct，方法即 RPC handler：

```go
// internal/apiserver/service_thread.go
type ThreadService struct {
    store   *store.Store
    runner  *runner.Manager
    bus     *bus.MessageBus
}

func (ts *ThreadService) Start(ctx context.Context, req ThreadStartReq) (ThreadStartResp, error) { ... }
func (ts *ThreadService) List(ctx context.Context) (ThreadListResp, error) { ... }
func (ts *ThreadService) Archive(ctx context.Context, req ThreadIDReq) error { ... }
func (ts *ThreadService) Fork(ctx context.Context, req ThreadForkReq) (ThreadForkResp, error) { ... }
```

注册：

```go
mux := handler.ServiceMap{
    "thread":    handler.Map{
        "start":   handler.New(ts.Start),
        "list":    handler.New(ts.List),
        "archive": handler.New(ts.Archive),
        "fork":    handler.New(ts.Fork),
    },
    "turn":      handler.Map{ ... },
    "skills":    handler.Map{ ... },
    "workspace": handler.Map{ ... },
    "ui":        handler.Map{ ... },
    "config":    handler.Map{ ... },
}
```

### 4.3 中间件链

```go
// internal/apiserver/middleware.go

// LoggingMiddleware wraps an Assigner with structured logging.
func LoggingMiddleware(base jrpc2.Assigner, logger *slog.Logger) jrpc2.Assigner { ... }

// MetricsMiddleware wraps an Assigner with latency/error metrics.
func MetricsMiddleware(base jrpc2.Assigner) jrpc2.Assigner { ... }

// AuthMiddleware wraps an Assigner with optional auth checks.
func AuthMiddleware(base jrpc2.Assigner) jrpc2.Assigner { ... }

// 组合
finalMux := LoggingMiddleware(
    MetricsMiddleware(
        AuthMiddleware(serviceMux),
    ),
    logger,
)
```

### 4.4 SSE 通知桥接

jrpc2 没有原生 SSE，但 `Server.Notify()` 是内建的。
V3 方案：SSE 作为独立通知通道，不走 jrpc2 Server：

```go
// internal/apiserver/sse_bridge.go

type SSEBridge struct {
    clients sync.Map // clientID → chan []byte
    bus     *bus.MessageBus
}

func (b *SSEBridge) BroadcastNotification(method string, params any) {
    notif := Notification{JSONRPC: "2.0", Method: method, Params: params}
    data, _ := json.Marshal(notif)
    b.clients.Range(func(_, v any) bool {
        ch := v.(chan []byte)
        select {
        case ch <- data:
        default: // drop if full
        }
        return true
    })
}
```

---

## 5. 迁移影响

### 5.1 文件变化

| V2 文件 | V3 替代 | 行数变化 |
|---|---|---|
| `server_transport.go` (Request/Response 类型) | jrpc2 内建 | **-150 行** |
| `server_conn.go` (dispatchRequest) | jrpc2 自动路由 | **-100 行** |
| `methods.go` (registerMethods + bindRaw/bindTyped) | handler.ServiceMap | **-200 行** |
| `methods_thread.go` (handler 样板) | 纯业务 Service | **-40%** |
| `methods_turn.go` (handler 样板) | 纯业务 Service | **-40%** |
| `methods_command.go` (handler 样板) | 纯业务 Service | **-40%** |
| `methods_config.go` (handler 样板) | 纯业务 Service | **-40%** |
| `methods_ui_state.go` (handler 样板) | 纯业务 Service | **-40%** |
| `methods_ui_sidebar.go` | 纯业务 Service | **-40%** |
| `methods_orchestration.go` | 纯业务 Service | **-40%** |
| 全部 `methods_*.go` | 全部 `service_*.go` | **总计 -2,300~3,100 行** |

### 5.2 新增代码

| 新文件 | 用途 | 行数 |
|---|---|---|
| `internal/apiserver/server_jrpc.go` | jrpc2 Server 配置 + jhttp 桥接 | ~100 |
| `internal/apiserver/middleware.go` | 日志/指标/认证中间件 | ~150 |
| `internal/apiserver/sse_bridge.go` | SSE 通知桥接 | ~80 |
| `internal/apiserver/service_*.go` | 纯业务 Service（已含） | 0（重用） |

### 5.3 净效果

```
样板代码减少:   ~2,300-3,100 行
新增框架适配:   ~330 行
━━━━━━━━━━━━━━━━━━━━━━━━━━━━
净减少:         ~2,000-2,770 行
apiserver 预算: 8,000 → 5,500
```

---

## 6. 风险与缓解

| 风险 | 概率 | 缓解 |
|---|---|---|
| jrpc2 星数低 (~88) | 低 | IBM fork 验证了生产可用性；代码质量极高 |
| 反射性能开销 | 极低 | RPC 本身是 IO bound，反射开销可忽略 |
| SSE 需要自定义桥接 | 中 | 独立实现 SSE 通知层，与 jrpc2 解耦 |
| 方法命名约定差异 | 低 | jrpc2 支持 `/` 分隔（ServiceMap key 自由） |
| 迁移过程不兼容 | 无 | V3 新项目，不存在兼容问题 |

---

## 7. 实施计划

纳入 P5（apiserver 迁移）阶段：

```
P5a: 引入 jrpc2 依赖 + 搭建 Server 骨架
P5b: 实现中间件层（logging, metrics, auth）
P5c: 实现 SSE 通知桥接
P5d: 逐模块迁移 handler → Service
     thread → turn → skills → config → workspace → ui → lsp
P5e: 验证全部 RPC contract test 通过
```
