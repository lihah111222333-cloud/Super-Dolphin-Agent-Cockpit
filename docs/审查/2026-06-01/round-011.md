# Round 011 - 第二梯队：claudecli provider 兜底

## 来源

Round-002 扫雷 agent 报告：provider/claudecli 5 条。

## Findings

### 1. [major] claudecli/session_turn.go:136 — sendRetryLocked 返回 nil 当状态漂移

**证据**：turn 状态已不在预期时返回 `nil`（非 error），turn 被静默孤立。
**影响**：用户发起的 turn 请求"成功"但实际未被执行，无任何错误反馈。
**精修**：返回 `fmt.Errorf("claudecli: turn state drifted, cannot retry")`。

### 2. [moderate] claudecli/session_events.go:405 — dataBool unchecked type assert

**证据**：`value, _ := data[key].(bool)` 失败时返回 false。
**影响**：非 bool 类型的 event data 被当 false 处理，可能影响 streaming 控制逻辑。
**精修**：comma-ok 检查 + log.Warn on type mismatch。

### 3. [moderate] claudecli/session_events.go:328-332 — 未知 stream event 静默丢弃

**证据**：`default: return nil` 不记录未知事件类型。
**影响**：Claude API 新增事件类型时，系统静默忽略，无法发现兼容性问题。
**精修**：`default: s.logger.Debug("unknown stream event", "type", eventType); return nil`。

### 4. [moderate] claudecli/event_map.go:143-144 — 未知 role 返回 nil,nil

**证据**：`decodeMessageBlock` 对未知 role 返回 `(nil, nil)`。
**影响**：消息解析静默丢失未知 role 的 block，conversation history 不完整。
**精修**：返回 error 或保留为 raw block。

### 5. [moderate] claudecli/transport_config.go:310 — fallbackSystemPrompt 静默注入

**证据**：instructions 为空时注入 fallback prompt，无 log。
**影响**：开发者不知道 fallback 被触发，调试时困惑"为什么有这段 system prompt"。
**精修**：log.Info("claudecli: using fallback system prompt")。
