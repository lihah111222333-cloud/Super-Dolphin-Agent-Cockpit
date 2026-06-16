# Round 003 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 05:19:54 KST
- 结束：2026-05-17 05:26:22 KST
- 说明：按用户 2026-05-17 05:18:05 KST 最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮把“量化引擎”范围扩展到 memory retrieval 相关性评分、预算选择、预取结果消费，以及 thread router 的候选排序回退链路。

- `internal/module/memory/retrieval/manifest.go`
- `internal/module/memory/retrieval/finder.go`
- `internal/module/memory/retrieval/ranking.go`
- `internal/module/memory/retrieval/render.go`
- `internal/module/memory/retrieval/prefetch.go`
- `internal/module/memory/retrieval/*_test.go`
- `internal/module/memory/rules_provider.go`
- `internal/module/memory/gate.go`
- `internal/module/thread/router_resolve.go`
- `internal/module/thread/router_resolve*_test.go`

## Findings

1. **[major] memory manifest 先按更新时间截断，再做相关性评分，旧但高相关的记忆会永久进不了候选池**
   - 证据：`internal/module/memory/retrieval/manifest.go:12` 将默认 manifest 上限定为 200；`BuildManifest()` 在 `ScanHeadersSafe()` 后直接按 `entries[:maxFiles]` 截断（`internal/module/memory/retrieval/manifest.go:20-28`）。而 `ScanHeadersSafe()` 的排序只看 `UpdatedAt` 倒序，时间相同才按路径排序（`internal/module/memory/retrieval/manifest.go:63-68`）。真正的查询相关性评分发生在后续 `rankEntries()`（`internal/module/memory/retrieval/finder.go:97-119`），只会处理已经被 manifest 截断保留下来的条目。
   - 风险：当 memory 文件超过 200 个时，最近更新的低相关条目会把较旧但高度相关的条目挤出 manifest。后续 `scoreMemoryEntry()` 无论打分多高，都没有机会看到这些旧条目，retrieval 的“量化排序”实际退化为“先按新旧硬过滤，再在剩余集合里评分”。对长期项目尤其危险：稳定但不常修改的架构约束、事故复盘、部署规则可能被最近的临时笔记遮蔽。
   - 建议：manifest 阶段保留轻量索引但不要只按更新时间截断；可按 query 对 header 字段先打分，再结合 recency 取 top N，或扩大扫描后在 `rankEntries()` 内统一执行候选截断。

2. **[major] relevant memory 选择预算按原文大小扣减，但最终渲染会截断到 720 runes，导致大条目被错误丢弃**
   - 证据：`SelectRelevantMemoriesWithAlreadySurfaced()` 在选择阶段用 `memoryBudgetBytes(entry)` 扣减预算，若 `size > remaining` 直接跳过（`internal/module/memory/retrieval/finder.go:62-95`）。`memoryBudgetBytes()` 对已有 `Content` 的条目返回完整正文 byte 长度（`internal/module/memory/retrieval/finder.go:161-168`）。但真正进入 prompt 的 attachment 在 `relevantMemoryAttachment()` 中先用 `MaxRenderedMemoryRunes=720` 截断 `MemoryRenderBody(entry)`（`internal/module/memory/retrieval/render.go:111-127`）。
   - 风险：一个高度相关但正文较长的 memory，在实际渲染时最多只占 720 runes，却可能在选择阶段因为原文 byte 长度超过剩余预算被整条跳过。预算模型和真实 prompt 成本不一致，会让量化结果偏向短条目，削弱高价值长文档的召回。
   - 建议：选择阶段按最终可渲染体积估算预算，例如复用 `MemoryRenderBody()` 后按 `MaxRenderedMemoryRunes` 上限计算，或者在超预算时允许“截断后纳入”而不是整条丢弃。

3. **[moderate] prefetch 错误被标记为 ready，消费端无法区分“无结果”和“构建失败”**
   - 证据：`runPrefetch()` 在 manifest 构建失败时调用 `finishHandle(handle, PrefetchStateReady, nil, err)`（`internal/module/memory/retrieval/prefetch.go:176-180`）；查找失败时也把 `entries=nil` 并用 Ready 状态结束（`internal/module/memory/retrieval/prefetch.go:182-190`）。`finishHandle()` 虽然保存 `handle.err`（`internal/module/memory/retrieval/prefetch.go:193-204`），但 `ConsumeIfReady()` 只返回 `snapshot, true`，不返回错误（`internal/module/memory/retrieval/prefetch.go:116-129`）。上层 `consumePrefetchEntries()` 只看 `entries, ok`，随后过滤、标记 surfaced、清理 handle（`internal/module/memory/rules_provider.go:400-419`）。
   - 风险：memory root 权限错误、manifest 解析失败、读取失败等会被折叠成一次“ready 但没有 entries”的正常结果。调用链不会记录错误，也不会触发同步兜底检索；用户看到的只是相关记忆缺失。对依赖 memory retrieval 的上下文选择来说，这是静默降级。
   - 建议：`ConsumeIfReady()` 返回错误或显式状态；上层至少记录错误并避免把失败结果当成已消费成功。对非 context 错误可以同步重试一次或保留 handle 供下一轮观察。

## 误报与已覆盖项

- `hydrateMemoryEntrySafe()` 遇到文件已删除会跳过条目（`internal/module/memory/retrieval/finder.go:143-151`），测试已覆盖 deleted-after-manifest 场景，当前按设计取舍记录，不列为 finding。
- relevant memory 渲染阶段有 untrusted fence 和 fence escape（`internal/module/memory/retrieval/render.go:22-65`），本轮未发现 fence 逃逸缺陷。
- thread router 的 fallback/specific 分池与优先级排序逻辑有明确注释和测试覆盖：specific 先于 `{}` fallback，池内按 Priority 降序稳定排序（`internal/module/thread/router_resolve.go:511-569`）。本轮未形成新的 router finding。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/memory/retrieval ./internal/module/memory ./internal/module/thread -count=1
```

结果：guard 通过；`internal/archtest`、`internal/module/memory/retrieval`、`internal/module/memory`、`internal/module/thread` 均通过。

## 下一轮建议

- Round 004 继续审查 `internal/module/prompt/classifier/` 的候选剪枝与 tag overlap 评分，重点看“低分候选 top-up”“同分回退”“backend classifier 失败”是否会造成路由量化偏差。
- 同时抽查 `internal/module/skill/rpc.go` 中 skill disclosure tier 的重复实现，判断是否与 FBSD 分层阈值漂移。
