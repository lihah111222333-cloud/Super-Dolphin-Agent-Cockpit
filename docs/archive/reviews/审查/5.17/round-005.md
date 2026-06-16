# Round 005 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 05:31:07 KST
- 结束：2026-05-17 05:32:44 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 memory 删除匹配、自动类型推断、去重/溢出合并评分，以及这些评分如何触发写盘和删除。

- `internal/module/memory/auto_dream_intent.go`
- `internal/module/memory/service.go`
- `internal/module/memory/store.go`
- `internal/module/memory/index.go`
- `internal/module/memory/path.go`
- `internal/module/memory/domain_bridges.go`
- `internal/module/memory/dedup/filter.go`
- `internal/module/memory/dedup/matcher.go`
- `internal/module/memory/dedup/merge.go`
- `internal/module/memory/dedup/overflow.go`
- `internal/module/memory/*_test.go`
- `internal/module/memory/dedup/*_test.go`

## Findings

1. **[major] forget/delete 模糊匹配没有最低置信度和歧义保护，substring 命中即可删除单个最佳项**
   - 证据：`DetectForgetIntent()` 从用户文本中抽取 query 后只过滤 generic target（`internal/module/memory/auto_dream_intent.go:101-117`）。删除链路 `deleteIntent()` 调用 `deleteMemoryAcrossStores()`（`internal/module/memory/service.go:477-487`），后者对 primary/secondary store 逐个执行 `Delete()`（`internal/module/memory/auto_dream_intent.go:219-235`）。store 删除若精确 canonical name 未命中，会进入 `findMatchingMemoryEntry()`（`internal/module/memory/store.go:416-425`）。该函数遍历 entries，只要 `memoryDeleteMatchScore()` 非 0 就可成为最佳项，没有最低分、没有 top1/top2 gap 判断，也没有“多候选同分时拒绝删除”（`internal/module/memory/store.go:427-450`）。模糊分数来自 `strings.Contains(target, query) || strings.Contains(query, target)`，内容字段命中也有 65 分（`internal/module/memory/store.go:453-475`）。
   - 风险：用户说“忘记 deploy”“delete rollback”等短 query 时，只要某条 memory 的名称、描述、hook 或正文包含该词，就可能被删除。多个候选时会按分数字段和 `preferMemoryEntry()` 选一个，而不是要求用户消歧；同分还偏向更新的 entry。对长期记忆库，这是误删高价值上下文的直接风险。
   - 建议：为 fuzzy delete 增加最低长度/最低分、top1/top2 gap、候选数阈值；短 query 或多候选同分时返回需要确认的候选列表。内容字段 substring 命中不应直接删除，可降为“建议候选”。

2. **[major] dedup overflow 达到 16 条后可按 0.40 containment 自动合并并删除一条，阈值偏低且无人工确认**
   - 证据：每类最多 15 条（`internal/module/memory/dedup/overflow.go:9-13`）。`handleDedupOverflow()` 在写入后构造 filter 并调用 `FindOverflowMerge()`（`internal/module/memory/service.go:383-402`）。`FindOverflowMerge()` 只要条目数超过上限，就调用 `FindMostSimilarPair()`；找到后直接返回包含 `DeletePath` 的 `OverflowInstruction`（`internal/module/memory/dedup/filter.go:93-123`）。相似对阈值是 containment >= 0.40（`internal/module/memory/dedup/overflow.go:41-70`），随后 `overflowMergeAndDelete()` 写入 keep path 并 `_ = os.Remove(validatedDel)` 删除 absorbed path（`internal/module/memory/domain_bridges.go:489-512`）。
   - 风险：0.40 containment 对同一类型但不同主题的条目可能偏宽，尤其是含大量模板化 “Why / How to apply” 文本的 memory。超过 15 条后，系统会自动选最高分 pair 合并并删除一条，没有用户确认，也没有 LLM 语义复核。误合并会把独立规则压进同一条，误删除则让原始条目不可见。
   - 建议：overflow 自动删除前提高阈值，或要求名称/search_keys/描述也有相似信号；对 0.40-0.70 区间只给 health warning，不自动删除。删除前应保留 tombstone/backup 或记录可恢复日志。

3. **[moderate] dedup name/search_keys 命中后直接进入 Skip/Merge 决策，不把命中级别用于保护**
   - 证据：`FindDuplicate()` 的匹配顺序是 exact name、search_keys Jaccard >=0.5、content containment >=0.7（`internal/module/memory/dedup/matcher.go:63-84`）。`matchBySearchKeys()` 遇到第一个 Jaccard >=0.5 的 entry 就返回，不寻找最高分或评估歧义（`internal/module/memory/dedup/matcher.go:134-148`）。`Filter.Check()` 对 match level 不做区分，随后只用内容 bigram 的 `Decide()` 结果决定 Skip/Merge/WriteNew（`internal/module/memory/dedup/filter.go:44-83`）。`Decide()` 在新 bigram 90% 已在旧中或独特 bigram <15% 时 Skip（`internal/module/memory/dedup/merge.go:16-38`）。
   - 风险：同名但语义已经分叉的条目、或者 search_keys 只有一半重叠的条目，会被当作 duplicate 进入合并/跳过路径。若新增内容短而关键，可能被 Skip；若被 Merge，`MergeParagraphs()` 对 containment >=0.5 且新段落不更长时保留旧段落（`internal/module/memory/dedup/merge.go:96-138`），会丢掉短但更准确的修正。
   - 建议：把 match level 和 match score 传给决策层；search_keys 命中应选择最高 Jaccard 并要求额外内容相似或名称相似。对短修正类内容，不应只用 bigram novelty 判定是否可跳过。

## 误报与已覆盖项

- cross-scope duplicate 不会自动 merge：`Filter.Check()` 在 scope 不同且都非空时返回 `WriteNew`（`internal/module/memory/dedup/filter.go:57-61`），本轮不报告跨 scope 误合并。
- overflow 删除使用 `ValidateMemoryWritePath()` 包住 keep/delete path（`internal/module/memory/domain_bridges.go:498-510`），本轮不报告路径逃逸。
- auto-dream type 推断使用固定优先顺序处理同分（`internal/module/memory/auto_dream_intent.go:120-142`），可能有分类偏差，但本轮未找到直接破坏性写删证据，暂不列为 finding。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/memory ./internal/module/memory/dedup -count=1
```

结果：guard 通过；`internal/archtest`、`internal/module/memory`、`internal/module/memory/dedup` 均通过。

## 下一轮建议

- Round 006 审查 `internal/module/memory/similarity/` 的 LLM consolidate pair 选择、决策解析、ignore set 与 merge 执行，重点看模型输出是否能越权选择 keep/merge 内容，以及失败时是否会产生部分写入。
