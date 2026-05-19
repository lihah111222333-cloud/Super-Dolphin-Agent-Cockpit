# Round 013 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:06:34 KST
- 结束：2026-05-17 06:11:42 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 dashboard / insight 聚合 API 的指标口径，重点看 token、approval、duration 等量化指标在列表与 observed 查询中是否混用。

- `internal/module/dashboard/insights_rpc.go`
- `internal/module/dashboard/module.go`
- `internal/module/insight/service.go`
- `internal/module/insight/service_test.go`
- `internal/store/insight/store.go`
- `internal/store/insight/contract.go`
- `internal/store/insight/store_test.go`
- `internal/contract/insight.go`
- `sql/queries/session_insight.sql`

## Findings

1. **[major] dashboard insights list 直接暴露 unobserved token/approval 0 值，调用方很容易把“未观测”当作真实 0**
   - 证据：`dashboard/insights/list` 在未指定 thread 时调用 `reader.ListRecent()`，指定 thread 时调用 `reader.ListByThread()`，不提供 observed-only 参数或默认过滤（`internal/module/dashboard/insights_rpc.go:33-51`）。`InsightSnapshot` 同时暴露 `approval_requests`、`token_total` 以及 observed flags（`internal/contract/insight.go:19-48`），service 只是逐字段透传（`internal/module/insight/service.go:94-127`）。SQL 的普通列表查询不筛选 `approval_requests_observed` 或 `token_snapshot_observed`（`sql/queries/session_insight.sql:105-135`）。
   - 风险：任何 dashboard 前端或外部消费者只要对 list 返回值直接求平均/排行，就会把 provider 未发事件的 0 当作真实 0。量化指标会偏低，尤其 Claude approval 观测缺失与 token thread projection 混用时更明显。
   - 建议：dashboard list 增加 `observed_only` / `metric` 参数，或者提供 token observed 专用 RPC；同时在响应中把 unobserved 指标包装成 nullable，减少误用。

2. **[moderate] 已实现 `ListObservedTokenTurns` store 能力，但没有通过 contract/service/dashboard 暴露**
   - 证据：store interface 包含 `ListObservedTokenTurns()`（`internal/store/insight/contract.go:33-42`），store 实现会调用 `ListObservedTokenTurns` SQL（`internal/store/insight/store.go:192-219`）。SQL 也有 `token_snapshot_observed = TRUE` 的专用查询（`sql/queries/session_insight.sql:152-164`）。但 `contract.InsightService` 只暴露 `ListRecent`、`ListByThread`、`ListObservedApprovalRequests`，没有 token observed 方法（`internal/contract/insight.go:8-14`）；dashboard 也只注册 list 与 approvals 两个 handler（`internal/module/dashboard/insights_rpc.go:15-21`）。
   - 风险：最适合做 token 均值/分位数的 observed-only 数据面无法从 dashboard RPC 访问，迫使调用方使用普通 list 并自己过滤。普通 list 又包含 thread projection、unobserved zero 和 mixed total 等前几轮已发现的问题。
   - 建议：把 `ListObservedTokenTurns` 提升到 `contract.InsightService`，并新增 `dashboard/insights/tokens` RPC；响应中包含 `ui_projection` 或明确仅返回 strict turn projection。

3. **[moderate] duration_ms 使用 GREATEST 合并，后到较短但更准确的完成时间无法修正异常长耗时**
   - 证据：flusher 根据 observation 的 started/completed 或 signal timestamp 计算 duration，缺失 completed 时可回退到 signal timestamp / now（`internal/module/insight/flusher.go:161-181`）。SQL upsert 对 `duration_ms` 使用 `GREATEST(session_insights.duration_ms, EXCLUDED.duration_ms)`（`sql/queries/session_insight.sql:48-50`），测试也把 duration 纳入 no-regression guard（`internal/store/insight/store_test.go:253-260`）。
   - 风险：如果第一次 flush 因事件乱序、fallback timestamp 或系统时间漂移写入过大的 duration，后续更准确的短 duration 无法修正。dashboard 的耗时排行和 SLA 统计会长期保留异常高值。
   - 建议：duration 不应和 token counter 一样单调取大；可按 completed_at 新鲜度、timestamp source priority 或 explicit observed flag 更新，异常值只做上限 clamp 不做永久最大值。

## 误报与已覆盖项

- `dashboard/insights/approvals` 使用 observed-only 查询，能避免未观测 approval 0 进入 approval 专用窗口（`internal/module/dashboard/insights_rpc.go:54-64`、`sql/queries/session_insight.sql:137-150`），本轮不报告 approval 专用接口混入 unobserved。
- store 测试已锁定 `ListObservedApprovalRequests` 与 `ListObservedTokenTurns` SQL 至少包含 observed filter（`internal/store/insight/store_test.go:273-281`），本轮问题是 token observed 查询没有被 service/RPC 暴露。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/dashboard ./internal/module/insight ./internal/store/insight -count=1
```

结果：guard 通过；`internal/archtest`、`internal/module/dashboard`、`internal/module/insight`、`internal/store/insight` 均通过。

## 下一轮建议

- Round 014 审查 command card / prompt routing / dashboard 列表类排序和 limit 默认值，重点看风险等级、更新时间、分页窗口是否会误导高风险项优先级。
