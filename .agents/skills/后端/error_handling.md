# Go 错误处理与日志边界

> **加载条件**：修改领域错误、错误包装、RPC code 映射或结构化日志时加载。

## 三层责任

| 层级 | 错误形态 | 责任 |
|---|---|---|
| `module/service` | 领域错误、哨兵错误、带根因的普通 Go error | 表达业务失败，不依赖 jrpc2 或传输协议。 |
| RPC adapter / middleware | `*jrpc2.Error`、稳定应用 code | 严格解码、Validate，并把已知领域错误映射为客户端契约。当前公共入口在 `internal/platform/rpc`。 |
| transport/runtime | 解析、路由、取消和连接错误 | 保持 JSON-RPC 2.0 语义，统一处理协议级失败。 |

`module/service`禁止直接构造 jrpc2 错误。当前范式是 service 返回领域错误，`rpc.go`通过`platformrpc.StrictHandler`、`ThreadHandler`或相应 middleware 接线，`internal/platform/rpc`集中完成能力错误和参数错误映射。

## 领域错误与包装

```go
var ErrInvalidState = errors.New("turn is not idle")

func (s *service) StartTurn(ctx context.Context, req TurnRequest) (TurnResponse, error) {
    if err := req.Validate(); err != nil {
        return TurnResponse{}, err
    }
    if s.state != StateIdle {
        return TurnResponse{}, ErrInvalidState
    }
    return s.start(ctx, req)
}
```

规则：

- 同一抽象内直接返回已经足够清楚的错误，不机械叠加`fmt.Errorf`。
- 跨 owner 后调用方需要定位上下文时包装一次：`fmt.Errorf("load thread %s: %w", id, err)`。
- 所有包装必须保留`%w`；调用方使用`errors.Is` / `errors.As`，禁止依赖完整字符串判断。
- store 的`sql.ErrNoRows`、SQLite 文本、panic 细节不得直接暴露给客户端。
- 不得通过日志后返回 nil、空 DTO 或默认状态掩盖失败。

## RPC 映射

协议映射只放在 handler、adapter 或 middleware：

```go
func InvalidStateMapper() platformrpc.Middleware {
    return func(next handler.Func) handler.Func {
        return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
            resp, err := next(ctx, req)
            if errors.Is(err, ErrInvalidState) {
                return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidState), "turn is not idle")
            }
            return resp, err
        })
    }
}
```

- 参数绑定和`Validate()`失败使用平台统一的 InvalidParams 路径。
- 能力错误复用`internal/platform/rpc.MapCapabilityError` / `CapabilityErrorMapper`。
- 自定义 code 必须稳定、避开 jrpc2 保留区间，并由测试锁定。
- 客户端消息只包含安全、可行动的信息；根因保留在内部 error chain 和结构化日志中。

## 结构化日志

- 请求/RPC 边界使用`logger.FromContext(ctx)`获取 trace-aware logger。
- 长生命周期 runner、actor 和后台 worker 使用构造函数注入的 logger；不要为获得 logger 人工创建 Background context。
- 现有`logger.FieldXxx`常量覆盖的字段必须复用；新增跨模块稳定字段时先确认 owner 和消费端。
- 失败日志与成功日志互斥。不要在`err != nil`时写“created”“completed”“persisted”等成功事件。
- 同一错误只在拥有恢复/响应责任的边界记录，避免每层重复打印同一根因。

```go
result, err := service.StartTurn(ctx, req)
if err != nil {
    logger.FromContext(ctx).Error("start turn failed",
        logger.String(logger.FieldTaskID, req.TurnID),
        logger.Any(logger.FieldError, err),
    )
    return TurnResponse{}, err
}
return result, nil
```
