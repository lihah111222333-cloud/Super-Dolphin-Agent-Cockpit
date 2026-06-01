# Round 008 - 第二梯队：prompt assembler + cache 兜底

## 来源

Round-002 扫雷 agent 报告：prompt assembler 5 条 + prompt cache 3 条。

## Findings

### 1. [major] prompt/service.go:116 — RegisterClaudeMdSourceProvider 接受 nil 不校验

**证据**：存储 nil provider，后续 type assertion 时 panic 或静默跳过。
**精修**：`if provider == nil { return errors.New("prompt: claudemd provider required") }`

### 2. [major] prompt/cache.go:18 — json.Marshal 错误静默 fallback 到 bare section.Name

**证据**：cache key 构建时 marshal 失败 → 用 section.Name 做 key → 不同参数的 section 共享同一 cache entry。
**精修**：marshal 失败 → 不缓存（返回 cache miss），或 panic。

### 3. [major] prompt/dynamic_cache_deps.go:183 — cacheByNameSectionDependency 返回 nil

**证据**：依赖解析失败时返回 nil，caller 静默 fallback 到 bare name key。
**精修**：返回 error，让 cache 层知道依赖解析失败。

### 4. [moderate] prompt/service.go:104 — nil-receiver 返回零 Config

**证据**：`if s == nil || s.cfg == nil { return Config{} }` 掩盖上游 wiring bug。
**精修**：删除 nil-receiver guard；fx 装配期保证 service 非空。

### 5. [moderate] prompt/assembler.go:367 — nil-receiver 静默吞 invalidation

**证据**：`if s == nil { return }` 让 invalidation 通知静默丢失。
**精修**：同上，删除 nil-receiver guard。

### 6. [moderate] prompt/assembler_support.go:34 — nil-receiver 返回空 attachments

**证据**：`if s == nil { return nil }` 让 turn 拿到空 attachments。
**精修**：同上。

### 7. [moderate] prompt/match_when_support.go:18 — unchecked type assert 返回 ""

**证据**：`want.(string)` 失败时返回空字符串，match 条件永远不匹配。
**精修**：comma-ok 检查，不匹配时返回 error 或 log。

### 8. [moderate] prompt/service_surface.go:251 — buildPromptHandlersWithService 无 nil 校验

**证据**：deps 通过 type-switch 提取，未匹配的 dep 为 nil，handler 调用时 panic。
**精修**：type-switch 后校验所有必需 dep 非空。
