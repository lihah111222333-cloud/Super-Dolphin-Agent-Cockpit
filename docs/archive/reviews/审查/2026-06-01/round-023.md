# Round 023 - 第二梯队：feedback 模块兜底

## 来源

Round-002 扫雷 agent 报告：module/feedback 3 条。

## Findings

### 1. [major] feedback/service.go:32 — nil-self/nil-store 吞为 soft-disabled error

**证据**：`if s == nil || s.store == nil` 时返回一个"soft disabled"错误，但调用方可能不检查。
**影响**：feedback 功能静默失效，用户提交的反馈被丢弃但无明确提示。
**精修**：fx 装配期强制 store 非空；或返回明确的 `ErrFeedbackDisabled` 让 RPC 层返回 503。

### 2. [moderate] feedback/service.go:23 — 静默 fallback 到 global logger

**证据**：logger 为 nil 时用全局 logger。
**影响**：日志上下文（agent_id, thread_id）丢失。
**精修**：构造期强制 logger 非空。

### 3. [moderate] feedback/service.go:50 — insert 失败 log Warn 而非 Error

**证据**：feedback 写入失败用 Warn 级别。
**影响**：告警系统可能不触发（通常只监控 Error+）。
**精修**：改为 Error 级别。
