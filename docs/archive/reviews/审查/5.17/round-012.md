# Round 012 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:00:19 KST
- 结束：2026-05-17 06:06:33 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 provider unified token event mapping、Codex/Claude raw event 翻译、event surface 和 RPC raw push，重点看不同 provider 的 usage 字段是否会被错误归一化或重复推送。

- `internal/provider/unified/ui_tokens.go`
- `internal/provider/unified/ui_tokens_test.go`
- `internal/provider/unified/event_map.go`
- `internal/provider/codexapp/event_map.go`
- `internal/provider/codexapp/session_dispatch.go`
- `internal/provider/claudecli/event_map.go`
- `internal/provider/claudecli/session_log_watcher.go`
- `internal/provider/claudecli/session_log_watcher_integration.go`
- `internal/platform/eventsurface/bind.go`
- `internal/platform/rpc/push.go`
- `internal/platform/rpc/push_test.go`

## Findings

1. **[major] Codex tokenUsage.last 的 input/output 与 tokenUsage.total 的 totalTokens 混用，单个事件内部口径不一致**
   - 证据：`tokensUpdatedEvent()` 优先从 `tokenUsage.last` 读取 input/output，但 total 也按 `lastUsage,totalUsage` 顺序读取；如果 last 缺少 total，就回退到 `tokenUsage.total.totalTokens`（`internal/provider/unified/ui_tokens.go:30-57`）。现有测试明确锁定这种行为：last input/output 为 7/5，total totalTokens 为 120，最终事件 `InputTokens=7`、`OutputTokens=5`、`TotalTokens=120`（`internal/provider/unified/ui_tokens_test.go:9-39`）。
   - 风险：同一 token event 中 `input+output != total`，下游 state、insight 和 UI patch 会在不同位置优先使用 total 或 input+output，导致单轮成本、上下文百分比和增量输出统计互相矛盾。对于量化引擎，这会让“当前 turn 成本”和“累计线程成本”混在同一个结构体里。
   - 建议：当使用 `tokenUsage.last` 的 input/output 时，只能使用 `last.totalTokens`；若 last 缺失 total，就计算 `input+output`，并把 cumulative total 单独发布为 thread projection。

2. **[major] typed UITokensUpdated 和 raw `thread/tokenUsage/updated` 会同时进入前端，且 method 大小写不同导致去重边界分裂**
   - 证据：event dispatcher 先发布 `BusRawProviderEvent`，再运行 translators 发布 typed events（`internal/provider/unified/event_map.go:102-123`）。Claude/Codex translators 都会无条件尝试 `PublishUITokensUpdated()`（`internal/provider/claudecli/event_map.go:25-41`、`internal/provider/codexapp/event_map.go:46-61`）。event surface 对 typed `UITokensUpdated` 发布规范化的 `thread/tokenusage/updated`（全小写 usage）（`internal/platform/eventsurface/bind.go:192-196`、`internal/platform/eventsurface/bind.go:269-280`）。与此同时 raw push allowlist 仍允许 raw `"thread/tokenUsage/updated"` 和 `"thread/tokenusage/updated"` 直推（`internal/platform/rpc/push.go:97-142`），测试也锁定 raw token usage remains available（`internal/platform/rpc/push_test.go:56-62`）。
   - 风险：同一 provider 原始 token event 可能先以 raw camelCase method 推送，再以 typed lowercase method 推送。前端虽然会 normalize method，但 payload 字段和到达顺序不同，可能造成 usedTokens 短暂回退、重复日志、压缩等待器误判状态变化，且 raw/typed 事件没有共享 event id 可去重。
   - 建议：raw push 对 tokenUsage 应改为 typed-only，或在 typed/raw payload 中带相同 event id 并由前端去重；保留 legacy method 时也应只作为 fallback，避免双发。

3. **[moderate] Codex context window patch 只改已存在的字段，不会给缺失 window 的 tokenUsage 注入权威窗口**
   - 证据：Codex session dispatch 只有在 payload 中已有 `contextWindowTokens`、`contextWindow`、`modelContextWindow` 或 `context_window` 字段时才覆盖为模型权威值（`internal/provider/codexapp/session_dispatch.go:25-30`、`internal/provider/codexapp/session_dispatch.go:55-76`）。unified mapper 的 `contextWindowValue()` 只读取 payload / tokenUsage / usage 中已存在的 window 字段（`internal/provider/unified/ui_tokens.go:108-123`）。
   - 风险：如果 Codex token event 只有 usage/tokenUsage 计数而没有 window 字段，系统已经知道 model context window 也不会注入，UI 与自动继续只能沿用旧 window 或显示 0。上下文百分比会缺失或滞后，影响 critical 阈值触发。
   - 建议：当 `contextWindowForModel()` 有权威值时，在 token event payload 缺失 window 的情况下也应注入一个统一字段，而不是只覆盖已有字段。

## 误报与已覆盖项

- Claude session log watcher 会把 `cache_creation_input_tokens` 和 `cache_read_input_tokens` 加入 input token，并测试覆盖追加、去重、truncate replay（`internal/provider/claudecli/session_log_watcher.go:300-326`、`internal/provider/claudecli/session_log_watcher_poll_test.go:11-70`），本轮不报告 Claude cache token 漏加。
- Codex unknown raw event warning 会对纯 usage payload 静默，避免 token event 被误报为未知事件（`internal/provider/codexapp/event_map.go:101-130`），本轮不报告日志噪声。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/provider/unified ./internal/provider/codexapp ./internal/provider/claudecli ./internal/platform/rpc ./internal/platform/eventsurface -count=1
```

结果：guard 通过；`internal/archtest`、`internal/provider/unified`、`internal/provider/codexapp`、`internal/provider/claudecli`、`internal/platform/rpc`、`internal/platform/eventsurface` 均通过。

## 下一轮建议

- Round 013 审查 dashboard / insight 聚合 API，重点看 token、approval、duration 等量化指标在列表和统计中是否混用 observed/unobserved 数据。
