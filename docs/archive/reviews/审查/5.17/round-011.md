# Round 011 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 05:53:49 KST
- 结束：2026-05-17 06:00:18 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 thread history compaction、token 估算和前端自动继续触发之间的关系，重点看压缩结果的量化指标是否会误导后续控制流。

- `internal/module/thread/history.go`
- `internal/module/thread/lifecycle_helpers.go`
- `internal/dto/thread/event.go`
- `internal/provider/codexapp/session_history.go`
- `internal/provider/claudecli/session_history.go`
- `internal/module/thread/compact_event_test.go`
- `cmd/agent-terminal/frontend/vue-app/stores/thread-sync-helpers.js`
- `cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js`
- `cmd/agent-terminal/frontend/vue-app/composables/useAutoContinue.js`
- `cmd/agent-terminal/frontend/vue-app/utils/format-utils.js`

## Findings

1. **[major] `/compact` 事件的 before/after token 只是 4-rune 估算，但下游可能把它当作 provider token 成本**
   - 证据：`Compact()` 在执行 provider compaction 前后都调用 `estimateThreadTokens()`，结果写入 `BeforeTokens`、`AfterTokens`，并固定 `Estimated=true`（`internal/module/thread/history.go:423-466`）。估算函数只读取历史消息并按 message content + metadata 累加（`internal/module/thread/history.go:469-529`），文本 token 估算采用 `utf8.RuneCountInString(text)` 后 `(runes+3)/4`（`internal/module/thread/history.go:531-542`）。DTO 虽有 `Estimated` 字段，但 `BeforeTokens`/`AfterTokens` 字段名未区分真实 provider usage 与粗估值（`internal/dto/thread/event.go:53-62`）。
   - 风险：CJK、代码、JSON、工具输出、base64、重复符号等内容的真实 tokenizer 代价与 4-rune 估算偏差很大。若 dashboard、自动压缩策略或审计报表把该结果作为真实 token 降幅，会错误评估压缩收益。
   - 建议：字段命名或事件 payload 明确为 `estimated_before_tokens` / `estimated_after_tokens`；需要真实量化时读取 provider token telemetry 或 tokenizer library，不要复用当前启发式估算。

2. **[major] 压缩成功判定只比较估算历史长度，可能与前端实际 tokenUsage 不一致**
   - 证据：后端 `Compacted` 由 `afterTokens < beforeTokens` 决定（`internal/module/thread/history.go:454-461`）。前端压缩流程则记录 `tokenUsageSignature()`，其中包含 `usedTokens/contextWindowTokens/usedPercent`，并在压缩后只把签名变化作为日志字段，不作为成功条件（`cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js:164-171`、`cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js:788-829`）。实时 token push 会直接更新 `tokenUsageByThread` 并保留旧 used 值直到收到新正数（`cmd/agent-terminal/frontend/vue-app/stores/thread-sync-helpers.js:382-400`）。
   - 风险：provider 返回 `/compact` 完成且历史文本变短时，后端会发布成功；但如果 provider tokenUsage 未下降、仍处 critical，前端仍会显示“上下文压缩完成”。自动继续 watch 只看 token level 跨入 critical（`cmd/agent-terminal/frontend/vue-app/composables/useAutoContinue.js:314-338`），不会把“压缩成功但 token 仍 critical”作为失败或重试信号。
   - 建议：前端压缩完成后把 provider `tokenUsageSignature` 是否下降纳入结果态；后端事件也可携带真实 token usage delta，避免只凭估算历史判断成功。

3. **[moderate] Codex history 不可用时返回空历史，压缩前估算可能为 0 并掩盖历史读取失败**
   - 证据：Codex rollout reader 本地读取失败或无消息后，只记录 warning 并返回空 history、nil error（`internal/provider/codexapp/session_history.go:27-37`）。`session.ReadHistory()` 若 primary 无错且 fallback 不可用，会把空 messages 返回给调用者（`internal/provider/codexapp/session_history.go:39-59`）。`Compact()` 对 empty history 的估算结果为 0，仍继续调用 provider compact 并发布估算结果（`internal/module/thread/history.go:438-462`）。
   - 风险：当 rollout 文件路径错、绑定未更新或历史 API 不可用时，压缩前后估算可能都是 0，结果会显示 `Compacted=false` 或无有效收益，但实际原因是历史观测失败而不是压缩无效。量化审计会把数据缺失误读为真实 0。
   - 建议：`ReadHistory` 应区分“确认空历史”和“历史不可观测”；`ThreadCompactResult` 增加 history observation status，避免把缺失量化为 0。

## 误报与已覆盖项

- Claude history 会在请求目标无历史时 fallback 到 session resolved thread id，并按 limit 修剪（`internal/provider/claudecli/session_history.go:12-35`），本轮不报告 Claude 目标 ID fallback 缺失。
- 前端 token push 已允许 usedTokens 下降来修正错误高值（`cmd/agent-terminal/frontend/vue-app/token-push-regression.test.js` 覆盖），本轮问题不是前端不允许回退，而是压缩结果成功态没有绑定真实 token usage 下降。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/thread ./internal/provider/codexapp ./internal/provider/claudecli -count=1
cd cmd/agent-terminal/frontend
npx vitest run token-push-regression.test.js use-auto-continue.test.js thread-store.actions.test.js
```

结果：Go guard 与相关 Go 包通过；前端 size guard 通过，3 个 vitest 文件共 79 个测试通过。

## 下一轮建议

- Round 012 审查 provider unified token event mapping 与 raw event bridge，重点看不同 provider 的 usage 字段是否会被错误归一化。
