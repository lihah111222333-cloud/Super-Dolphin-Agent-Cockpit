# Round 010 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 05:47:25 KST
- 结束：2026-05-17 05:53:48 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 session insight / token telemetry 的采集、合并、持久化和 UI 投影，重点确认 token 量化结果是否会被线程级事件、旧值或局部事件污染。

- `sql/queries/session_insight.sql`
- `internal/module/insight/flusher.go`
- `internal/store/insight/store.go`
- `internal/store/insight/contract.go`
- `internal/dto/observation/types.go`
- `internal/module/turn/observation/memory.go`
- `internal/module/turn/observation/subscribers.go`
- `internal/provider/unified/ui_tokens.go`
- `internal/provider/claudecli/session_log_watcher.go`
- `internal/provider/claudecli/session_log_watcher_integration.go`
- `internal/module/uistate/projector.go`
- `internal/module/uistate/patch.go`

## Findings

1. **[major] per-turn insight 会把 Projection=`thread` 的 token 事件标记为 observed，导致线程累计值进入“单轮观测”查询**
   - 证据：`PublishUITokensUpdated()` 统一把 token event 的 `Projection` 固定为 `"thread"`，即使 payload 带 `turn_id`（`internal/provider/unified/ui_tokens.go:58-75`）。observation subscriber 只要求 `TurnID` 非空就记录 token snapshot，并用 input/output/total 非零设置 `Observed=true`，没有排除 Projection=`thread`（`internal/module/turn/observation/subscribers.go:192-211`）。`flusher.buildParams()` 原样把 `tokens.Observed`、`tokens.Projection` 和 token 值写入 insight params（`internal/module/insight/flusher.go:155-209`）。`ListObservedTokenTurns` 只按 `token_snapshot_observed = TRUE` 过滤，不排除 `ui_projection='thread'`（`sql/queries/session_insight.sql:152-164`）。
   - 风险：查询名和注释承诺“observed token turns”，但线程级累计 token 事件只要带 turn id 就会进入 per-turn 统计窗口。成本均值、单轮 token 排名、自动预算评估会把累计值当作单轮值，形成系统性高估。
   - 建议：`ListObservedTokenTurns` 或写入侧增加 `ui_projection <> 'thread'` 约束；或者将 `Observed` 拆成 `Observed` 与 `PerTurnObserved`，只让严格 turn projection 进入 per-turn 分析。

2. **[major] observation token merge 允许后到小值覆盖先到大值，和 SQL no-regression 语义不一致**
   - 证据：DTO 注释要求 zero 不覆盖已观测非零值，并提示 strict per-turn accounting 应忽略 `Projection="thread"`（`internal/dto/observation/types.go:37-48`）。但 `mergeTokens()` 对 input/output/total 只要 next 非零就直接覆盖 prev，而不是取 max 或按 projection 优先级合并（`internal/module/turn/observation/memory.go:104-124`）。SQL upsert 层则使用 `GREATEST` 保证 token counters only advance（`sql/queries/session_insight.sql:8-15`、`sql/queries/session_insight.sql:70-74`）。
   - 风险：flusher 在 DB 写入前读取的是 memory 中的最后非零值；如果后到的 per-turn last usage 小于先到的累计/较大快照，memory 会短暂或最终降级。DB 层只保护已写过的行，不能保护首次 flush 前的观察态，也不能表达“turn 精度优先于 thread 累计”的来源优先级。
   - 建议：observation merge 应与 SQL invariant 对齐，至少对同 projection 取 max；更稳妥的是按 projection precision 合并，`turn` 事件覆盖 `thread` 事件，但同精度不回退。

3. **[moderate] UI token patch 没有沿用 projector 的 input+output fallback，total 缺失时会向前端推送 0 used**
   - 证据：`applyTokensUpdated()` 在 state 内部如果 `TotalTokens <= 0` 会用 `InputTokens + OutputTokens` 作为 used，并计算百分比（`internal/module/uistate/projector.go:62-83`）。但发给 thread patch 的 `tokenUsagePatch()` 直接使用 `ev.TotalTokens` 作为 `UsedTokens`，不做 fallback（`internal/module/uistate/patch.go:322-330`）。provider unified 只有在 `hasTotal=false` 时才补 `input+output`；如果事件显式带 `total_tokens: 0` 同时带 input/output，patch 仍可能为 0（`internal/provider/unified/ui_tokens.go:46-57`）。
   - 风险：UI state snapshot 和实时 patch 对同一 token 事件给出不同量化结果。前端高优先级 patch 可能短暂显示 0/窗口，影响用户对上下文占用、自动继续和压缩时机的判断。
   - 建议：`tokenUsagePatch()` 复用 projector 的 `usedToken` 计算函数；同时把 `total=0` 且 input/output 非零的事件视作缺失 total，而不是可信 0。

## 误报与已覆盖项

- SQL upsert 对已持久化行的 token non-regression、terminal precedence 和 observed sticky 已有明确防线（`sql/queries/session_insight.sql:1-15`、`sql/queries/session_insight.sql:51-80`），本轮不报告 DB 层回退。
- TurnID 为空的 Claude thread projection 事件会在 observation subscriber 被丢弃，避免误归因到任意 turn（`internal/module/turn/observation/subscribers.go:194-201`），本轮问题限定在带 turn id 的 thread projection。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/insight ./internal/module/turn/observation ./internal/module/uistate ./internal/provider/unified -count=1
```

结果：guard 通过；`internal/archtest`、`internal/module/insight`、`internal/module/turn/observation`、`internal/module/uistate`、`internal/provider/unified` 均通过。

## 下一轮建议

- Round 011 审查 thread history compaction 和 token 估算，重点看估算值、实际 provider token usage、压缩触发条件之间是否存在偏差累积。
