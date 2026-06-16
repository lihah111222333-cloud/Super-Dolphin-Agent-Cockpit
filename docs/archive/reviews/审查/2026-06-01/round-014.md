# Round 014 - 第二梯队：turn lifecycle 兜底

## 来源

Round-002 扫雷 agent 报告：turn lifecycle 3 条 + turn 核心 5 条。

## Findings

### 1. [blocker] turn/tool_result_lifecycle.go:56 — cfg.EnabledForModel 无 nil check

**证据**：`cfg` 可能为 nil，直接调用方法 → panic。
**精修**：`if cfg == nil { return false }`。

### 2. [major] turn/skills.go:323 — applyHydration 丢弃 conflict error（已在 round-005 #8 确认）

### 3. [moderate] turn/skills.go:172 — skillLookup==nil 返回 refs,nil

**证据**：`if s == nil || s.skillLookup == nil { return refs, nil }` 把"未配置"伪装为"无需 hydrate"。
**影响**：调用方无法区分"所有 ref 已完整"和"hydration 被跳过因为依赖缺失"。
**精修**：如果 skillLookup 是 required dep，构造期强制非空；如果 optional，返回 sentinel。

### 4. [moderate] turn/service.go:442 — recordDedupeUpsert log 后丢弃 error

**证据**：`if err != nil { s.logger.Warn(...); return }` 不上抛。
**影响**：dedupe store 写入失败时，turn 继续执行但 idempotency 保证断裂。
**精修**：返回 error，让 PrepareTurn 失败。

### 5. [moderate] turn/service.go:465, :483 — recordDedupeProviderID / recordDedupeTerminal 同模式

**证据**：同 #4，log 后丢弃。
**精修**：同 #4。

### 6. [moderate] turn/tool_result_storage.go:54, :59 — persistToolResult 静默返回 ""

**证据**：dir 创建失败或 WriteFile 失败时返回空字符串，caller 丢失 tool result 数据。
**精修**：返回 error。
