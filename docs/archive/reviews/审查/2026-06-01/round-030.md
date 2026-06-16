# Round 030 - 模式归纳：dto 层缺少 Validate() 方法

## 系统性问题

`internal/dto/provider/` 中的核心请求 struct（TurnRequest、StartSessionRequest、SteerRequest）没有 Validate() 方法。必填字段用 `omitempty` 标签，允许零值通过 JSON 反序列化。

## 受影响 struct

| Struct | 必填字段 | 当前状态 |
|--------|----------|----------|
| TurnRequest | ThreadID, Inputs | 无校验 |
| StartSessionRequest | Provider, AgentID, CWD | 无校验 |
| SteerRequest | ExpectedTurnID | 无校验 |
| MCPManifest | Binaries | 无校验 |

## 统一精修方案

```go
// 在每个 dto struct 上加 Validate()
func (r TurnRequest) Validate() error {
    if strings.TrimSpace(r.ThreadID) == "" {
        return errors.New("turn_request: thread_id required")
    }
    if len(r.Inputs) == 0 {
        return errors.New("turn_request: inputs required")
    }
    return nil
}
```

### 调用点

- `PrepareTurn` 返回前调用 `req.Validate()`。
- `StartSession` 入口调用 `req.Validate()`。
- RPC handler 反序列化后立即调用 `req.Validate()`。

## 预期影响

- 4 个 dto struct 加 Validate() 方法。
- ~6 个调用点加 `if err := req.Validate(); err != nil { return err }`。
- 现有测试中构造不完整 request 的 case 需要补全字段。
