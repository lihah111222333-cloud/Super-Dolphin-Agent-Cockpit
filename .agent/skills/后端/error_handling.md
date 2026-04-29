# Go 错误处理规范 (V3 契约合规版)

> **加载条件**: 错误包装、哨兵错误、自定义业务错误代码、RPC 错误响应时加载。

---

## 核心原则: 三层错误体系 + 日志系统统一处理

> [!IMPORTANT]
> 项目禁止在每个调用点用 `fmt.Errorf("xxx: %w", err)` 滥用包装。
> 错误上下文应由日志系统 (`pkg/logger`) 在边界层/中间件层统一记录。

| 层级 | 概念与类型 | 用途 |
|------|------|------|
| **L1 协议层** | `*jrpc2.Error` / 框架内置 Code | 描述请求解析、路由找不到、参数非法的底层 RPC 错误。 |
| **L2 业务层** | 带有自定义 Code 的 `*jrpc2.Error` | `CodeUnauthorized (1001)`, `CodeCapabilityDenied (1002)` 等应用级业务错误。 |
| **L3 哨兵层** | `errors.ErrNotFound` 等内置哨兵 | 内部逻辑流转与判断。不要把这些错误赤裸裸地通过 RPC 抛出，需映射为 L2 或 L1 错误。 |

### 禁止 ❌

```go
// ❌ 禁止: 滥用跨层 fmt.Errorf 嵌套，导致日志中看到 "a: b: c: actual error"
return fmt.Errorf("step3: %w", fmt.Errorf("step2: %w", fmt.Errorf("step1: %w", err)))

// ❌ 禁止: 将底层 sql.ErrNoRows 或原生 panic 报错直接透传给客户端，暴露技术细节。
```

### 正确 ✅

```go
// ✅ 同包内部或私有函数: 直接返回
if err != nil {
    return err
}

// ✅ 业务边界 (返回给 RPC 客户端时): 转化为明确业务 Code 的 jrpc2.Error
if !hasBudget {
    return jrpc2.Errorf(CodeRateLimited, "agent computation budget exceeded").WithData(map[string]any{
        "agentId": agentID,
    })
}

// ✅ 日志记录 (Handler / Middleware 层): 使用预留字段常量
logger.FromContext(ctx).Error("task processing failed",
    logger.String(logger.FieldAgentID, agentID),
    logger.Any(logger.FieldError, err),
)
```

---

## jrpc2 错误映射契约

根据 `jrpc2-convention.md` 契约，V3 规定所有的 RPC Handler 必须返回能被框架转换为 JSON-RPC 的错误。

- 协议错误：如入参验证失败 `req.Validate()`，应返回 `jrpc2.Errorf(jrpc2.InvalidParams, "%s", err.Error())`
- `context.Canceled` 会被底层框架自动映射，无需人工干预
- 业务错误：使用自定义业务 code，并且**必须避开 jrpc2 的保留区间 `-32768` 到 `-32000`**。

### 示例

```go
const CodeCapabilityDenied jrpc2.Code = 1002
const CodeInvalidState jrpc2.Code = 1003

func (s *agentService) StartTurn(ctx context.Context, req TurnRequest) (TurnResponse, error) {
    if err := req.Validate(); err != nil {
        // 参数错误：使用标准库定义的 InvalidParams
        return TurnResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "validation failed: %v", err)
    }

    if s.state != StateIdle {
        // 业务冲突：使用自定义 Code
        return TurnResponse{}, jrpc2.Errorf(CodeInvalidState, "agent is not idle").WithData(map[string]any{
            "current_state": s.state,
        })
    }

    // ...
}
```

---

## 日志系统关键能力

> [!NOTE]
> `logger.FromContext(ctx)` 是 V3 获取结构化日志的基础，它能够自动串联 `trace_id`。

| 能力 | 说明 | 使用方式 |
|------|------|---------|
| Context 感知 | 自动注入 `trace_id`/`span_id` | `logger.FromContext(ctx)` |
| 预留字段常量 | MUST 使用常量，避免键名错乱 | `logger.FieldAgentID`, `logger.FieldTaskID` 等 |
| 自动 Stacktrace | Error+ 级别自动附加调用栈 | 无需手动处理 |

### 预留字段常量

**Attr 风格 (推荐)**: Handler/入口层 MUST 使用常量键名

```go
// ✅ MUST 使用 FieldXxx 常量
log.Error("turn failed",
    logger.String(logger.FieldAgentID, agentID),
    logger.String(logger.FieldTaskID, taskID),
    logger.Any(logger.FieldError, err),
)
```

### 常量建议 (按需扩展 pkg/logger)

```go
logger.FieldTraceID    // "trace_id"
logger.FieldAgentID    // "agent_id"
logger.FieldTaskID     // "task_id"
logger.FieldModule     // "module"
logger.FieldError      // "error"
```
