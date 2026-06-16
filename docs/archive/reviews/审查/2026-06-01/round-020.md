# Round 020 - 第二梯队：prompt intent + hint 兜底

## 来源

Round-002 扫雷 agent 报告：prompt intent 5 条。

## Findings

### 1. [blocker] prompt/user_context_builder.go:258 — truncateAtRuneBoundary 无 bounds check（已在 round-003 #3 确认）

### 2. [moderate] prompt/prompt_hint.go:45 — store error 吞掉返回空字符串

**证据**：`readPromptHintOverride` 读取失败时返回 ""。
**影响**：prompt hint 配置损坏时系统用默认行为，用户自定义 hint 静默失效。
**精修**：区分 ErrNotFound（合法空）和其他 error（上抛）。

### 3. [moderate] prompt/prompt_hint.go:67 — shared-file read error 同模式

**证据**：`readPromptHintDefault` 同样吞错误。
**精修**：同 #2。

### 4. [moderate] prompt/intent/draftdream/execute.go:22 — 首次 parse error 静默丢弃

**证据**：retry 循环中第一次 parse 失败不 log，只保留最后一次 error。
**影响**：间歇性 parse 失败无法被诊断。
**精修**：每次 parse 失败都 log.Debug。

### 5. [moderate] prompt/intent/repair.go:49 — normalizeKind error 静默 continue

**证据**：invalid kind 的 card 被跳过，不报错。
**影响**：损坏的 intent card 静默消失。
**精修**：收集 errors，最终返回 multierror。
