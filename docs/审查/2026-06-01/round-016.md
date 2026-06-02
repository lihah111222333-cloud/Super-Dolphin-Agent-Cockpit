# Round 016 - 第二梯队：platform/bus + eventsurface + statemachine

## 来源

Round-002 扫雷 agent 报告：bus+eventsurface 5 条。

## Findings

### 1. [blocker] platform/eventsurface/bind.go:83 — 静默返回 nil（已在 round-004 #4 确认）

### 2. [major] platform/statemachine/factory.go:73 — AllowedTriggers 吞错误返回 nil

**证据**：`PermittedTriggersCtx` 失败时返回 nil slice。
**影响**：状态机查询"当前允许的触发器"失败时，调用方以为"无可用触发器"，UI 禁用所有操作按钮。
**精修**：签名改为 `([]string, error)`。

### 3. [major] platform/bus/sink.go:23 — NewLogSink 静默空 sink（已在 round-006 #10 确认）

### 4. [moderate] platform/bus/router.go:20 — Route 返回 no-op cancel

**证据**：dispatcher/handler nil 时返回空 cancel func。
**影响**：事件路由静默失效。
**精修**：返回 error。

### 5. [moderate] platform/eventsurface/legacy.go:122 — payloadMap 吞 marshal/unmarshal error

**证据**：event payload 转 map 时 marshal 或 unmarshal 失败返回空 map。
**影响**：事件数据丢失，UI 收到空 payload。
**精修**：返回 error，让 publish 层决定是否降级。
