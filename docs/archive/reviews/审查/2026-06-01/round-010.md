# Round 010 - 第二梯队：dto/provider 弱契约

## 来源

Round-002 扫雷 agent 报告：dto/provider 5 条。

## Findings

### 1. [major] dto/provider/turn.go:9 — TurnRequest 无 Validate()

**证据**：`ThreadID`、`Inputs`、`TurnAssembly` 是必填字段但无编译期/运行期校验。
**影响**：构造空 TurnRequest 不会报错，provider 收到空 payload 后行为未定义。
**精修**：
```go
func (r TurnRequest) Validate() error {
    if strings.TrimSpace(r.ThreadID) == "" {
        return errors.New("turn request: thread_id required")
    }
    if len(r.Inputs) == 0 {
        return errors.New("turn request: inputs required")
    }
    return nil
}
```
在 `PrepareTurn` 返回前调用。

### 2. [major] dto/provider/session.go:73 — StartSessionRequest 无 Validate()

**证据**：`Provider`、`AgentID`、`CWD` 必填但无校验。
**精修**：同上模式。

### 3. [moderate] dto/provider/session.go:80 — Config map[string]any 无类型约束

**证据**：provider config 是 untyped map，任何 key/value 都能塞进去。
**影响**：provider 实现需要自己做 type assertion，失败时行为不一致。
**精修**：定义 `ProviderConfig` struct per provider，或至少加 `ValidateConfig(provider string) error`。

### 4. [moderate] dto/provider/turn.go:17 — OutputSchema json.RawMessage 无 shape 校验

**证据**：`OutputSchema` 可以是任意 JSON，provider 收到非法 schema 时行为未定义。
**精修**：在 Validate() 中 `json.Valid(r.OutputSchema)` 检查。

### 5. [moderate] dto/provider/message.go:13 — Metadata map[string]any 无 schema

**证据**：消息 metadata 是 catch-all map，无法做编译期类型安全。
**影响**：不同 provider 往 metadata 里塞不同 key，消费方需要 defensive coding。
**精修**：长期应定义 typed metadata struct；短期加 `KnownMetadataKeys` 常量集 + 运行期 warning。
