# Round 006 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 05:33:52 KST
- 结束：2026-05-17 05:35:24 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 memory similarity consolidate：相似对生成、LLM prompt/decision 解析、ignore set、merge 写删链路，以及失败/取消时的统计行为。

- `internal/module/memory/similarity/similarity.go`
- `internal/module/memory/similarity/similarity_test.go`
- `internal/module/memory/ui_rpc.go`
- `internal/module/memory/ui_rpc_mutations.go`
- `internal/module/memory/ui_rpc_merge_shared_shard19_test.go`
- `internal/contract/dream.go`

## Findings

1. **[major] LLM merged_content 只校验非空和长度，不校验是否来自原 pair，可能把幻觉或外部指令写入长期 memory**
   - 证据：`ConsolidateAll()` 读取相似对详情后构造 prompt，并把 `DreamExecute()` 返回的 JSON 解析为 decisions（`internal/module/memory/similarity/similarity.go:309-330`）。prompt 要求 LLM “去除冗余、保留两条共有 + 各自独有的关键信息”（`internal/module/memory/similarity/similarity.go:220-224`），但 `Decision` 结构只有 id、merge、keep、merged_description、merged_content（`internal/module/memory/similarity/similarity.go:255-263`）。`mergeWithDecision()` 仅校验 keep 是 A/B、merged fields 非空，并做 rune 截断（`internal/module/memory/similarity/similarity.go:451-485`）。随后 adapter 调用 `mergeUIMemoryEntries()`，把 LLM 输出作为覆盖内容写入 keep 侧（`internal/module/memory/ui_rpc.go:617-627`、`internal/module/memory/ui_rpc_mutations.go:418-439`）。
   - 风险：这是模型输出直接写入 durable memory 的路径。若 LLM 合并时幻觉新增规则、引入未在 A/B 中出现的事实、或把条目内的 prompt injection 整理成“规则”，当前代码不会做来源约束或差异审计。后续 retrieval 会把该 memory 当作可信历史参考反复注入上下文。
   - 建议：merge 写入前计算 merged_content 与 A/B 的覆盖关系；对新增句子/段落做显式标记并要求人工确认。至少应记录 diff，并拒绝明显不含原文关键 bigram 的 merged_content。

2. **[moderate] 同一 entry 出现在多组相似对时按顺序执行，前一组写删会污染后一组，结果只表现为部分成功和 Failed 计数**
   - 证据：`applyDecisions()` 注释承认 pairs 可能出现同一 entry 跨多对，当前按顺序处理；pair[0] 删除 entry 后，pair[1] 重读失败并计入 Failed（`internal/module/memory/similarity/similarity.go:358-366`）。实际循环遇到 ctx cancel 才停止，否则逐组 `applyOneDecision()`（`internal/module/memory/similarity/similarity.go:367-385`）。`mergeUIMemoryEntries()` 会先读 A/B，再写 A、删 B，删除失败时才尝试回滚（`internal/module/memory/ui_rpc_mutations.go:394-443`）。
   - 风险：一次 consolidate-all 可能对相互依赖的 pair 集合做部分合并。用户看到 `Merged>0`、`Failed>0`，但失败组的根因只是之前的成功删除了它依赖的 entry。该行为不会破坏磁盘一致性，但会让量化整合结果依赖 pair 顺序，可能漏合并更优 pair，或先合并较弱 pair 后阻断更强 pair。
   - 建议：在 `loadPairInputs()` 或 apply 前对 pairs 做冲突图，确保每个 entry 单轮最多参与一个 merge；按 score 降序选最大无冲突集合，剩余 pair 下轮再处理。

3. **[moderate] similarity 子包会原样传播 LLM 错误文本，隐私脱敏完全依赖 RPC 上层包装**
   - 证据：`DreamExecutor` 只有 `ExecuteDream(ctx, prompt) (string, error)`（`internal/contract/dream.go:10-12`）；adapter 原样返回 executor 错误（`internal/module/memory/ui_rpc.go:630-635`）。`ConsolidateAll()` 对该错误只包一层 `fmt.Errorf("LLM consolidate: %w", err)`（`internal/module/memory/similarity/similarity.go:322-324`）。测试明确说明子包不 redact，错误中含路径也会传播，主包 handler 负责 redaction（`internal/module/memory/similarity/similarity_test.go:395-406`）。RPC handler 当前确实通过 `redactIfPathBearing()` 兜底（`internal/module/memory/ui_rpc_mutations.go:500-520`）。
   - 风险：只要未来有非 RPC 调用方直接使用 `similarity.ConsolidateAll()`，或新增 handler 忘记同样包装，就可能把 LLM/provider 错误里的本地路径、secret 片段或 prompt 摘要返回给前端/日志消费者。子包自身没有安全默认值。
   - 建议：在 similarity 层引入错误分类/脱敏边界，或让 `DreamExecute` adapter 返回已脱敏错误。至少把“必须由调用方 redact”写成接口契约并加测试保护所有入口。

## 误报与已覆盖项

- invalid keep、缺失 merged fields、重复 id、缺失 decision 都有失败计数覆盖（`internal/module/memory/similarity/similarity_test.go:295-335`），本轮不报告 schema 基础校验缺失。
- merge 写删链路在删除 absorbed entry 失败时会尝试 rollback kept entry，相关测试覆盖只读删除失败时 entries/index 保持（`internal/module/memory/ui_rpc_merge_shared_shard19_test.go:150-176`），本轮不报告该路径的部分写入。
- UI merge 会重新读取当前 A/B 并校验同类型、相似度 >= `MinMergePairContainment`（`internal/module/memory/ui_rpc_mutations.go:359-370`、`internal/module/memory/ui_rpc_mutations.go:394-425`），所以 stale pair 至少会被重新校验相似度。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/memory/similarity ./internal/module/memory -count=1
```

结果：guard 通过；`internal/archtest`、`internal/module/memory/similarity`、`internal/module/memory` 均通过。

## 下一轮建议

- Round 007 审查 `internal/module/memory/team/` 同步差异计算、batch 限流和失败合并，重点看删除/上传集合的排序、截断和失败回写是否会造成远端状态漂移。
