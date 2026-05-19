# Round 004 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 05:27:27 KST
- 结束：2026-05-17 05:29:43 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 prompt classifier 的候选剪枝、fast-path 评分、backend 分类结果接入，以及 skill disclosure tier 与 FBSD 分层的复用关系。

- `internal/module/prompt/classifier/prune.go`
- `internal/module/prompt/classifier/service.go`
- `internal/module/prompt/classifier/claude_cli.go`
- `internal/module/prompt/classifier/classifier_test.go`
- `internal/module/thread/router_resolve.go`
- `internal/module/thread/router_resolve*_test.go`
- `internal/store/sqlc/prompt_template.sql.go`
- `internal/store/prompt/contract.go`
- `internal/module/skill/rpc.go`
- `internal/module/skill/rpc_types_test.go`
- `internal/module/fbsd/tier.go`
- `internal/module/fbsd/tracker.go`

## Findings

1. **[major] classifier backend 返回的 prompt_key 没有限定在剪枝后的候选集内**
   - 证据：router 在 backend 调用前把候选集剪枝到 top K（`internal/module/thread/router_resolve.go:351-358`），然后把剪枝后的 `candidates` 传给 `s.classifier.Classify()`。`claudeCLIClassifier.Classify()` 只解析 stdout JSON 中的 `prompt_key` 并原样返回（`internal/module/prompt/classifier/claude_cli.go:75-84`、`internal/module/prompt/classifier/claude_cli.go:94-108`），没有校验该 key 是否属于输入 candidates。router 接收结果后直接 `req.PromptKey = res.PromptKey`（`internal/module/thread/router_resolve.go:373-380`），随后 `pickRoutedTemplate()` 会在完整 `templates` 列表里解析 pinned key（`internal/module/thread/router_resolve.go:173-190`）。
   - 风险：分类器 prompt 中只给了剪枝后的候选，但 LLM 可能幻觉、记忆或输出一个不在候选集里的 enabled prompt_key。由于后续解析使用完整模板列表，该 key 只要真实存在就会被接受，绕过本轮候选剪枝和候选可解释性。量化链路的 top-K 约束因此不是硬约束，只是提示。
   - 建议：backend 返回后必须检查 `res.PromptKey` 是否在传入 classifier 的候选集合内；不在集合内时按 empty pick 处理并记录日志。测试应覆盖“backend 返回未入候选但存在于模板库”的路径。

2. **[major] 零分候选仍会按 updated_at DESC 的原始列表顺序进入 top-K，近期编辑可挤掉更合适模板**
   - 证据：默认 backend 候选上限是 5（`internal/module/prompt/classifier/service.go:22-39`）；剪枝函数对每个候选只按 tag substring 计分，同分按原始 order 稳定排序（`internal/module/prompt/classifier/prune.go:26-55`）。测试明确锁定“全零分也 top-up 到 max，且保持原顺序”（`internal/module/prompt/classifier/classifier_test.go:148-160`）。原始模板列表来自 SQL `ORDER BY updated_at DESC LIMIT $4`（`internal/store/sqlc/prompt_template.sql.go:128-145`），并非 priority 或语义相关顺序。
   - 风险：当用户输入没有命中 tag，或 tag 覆盖不足时，LLM 分类器只能看到最近更新的前 5 个零分模板。一个刚编辑过但不相关的模板会挤掉更合适但较旧的模板，导致后续分类在错误候选池内做选择。该问题与 Round 003 的 memory manifest 先按 recency 截断类似：相关性评分前置集合已被时间排序污染。
   - 建议：零分候选不应只继承 `updated_at DESC`。可将 prompt priority、default/main 保底、多样化 agent_key、或全文轻量相关性纳入 top-up；也可在全零分时提高候选上限或跳过剪枝。

3. **[moderate] skill disclosure tier 重新实现 FBSD 分数到 hot/warm/cold/frozen 的映射，未复用 FBSD AssignTiers 的预算和 grace 语义**
   - 证据：skill list 的 disclosure tier 通过 `skillsWithDisclosureTiers()` 注入（`internal/module/skill/rpc.go:41-49`）。它读取 FBSD snapshot 后重新计算 effective score（`internal/module/skill/rpc.go:51-83`），再用固定阈值 `>=3 hot, >=1 warm, >0 cold` 映射为小写 tier（`internal/module/skill/rpc.go:104-115`）。FBSD manifest 的真实分层则由 `AssignTiers()` 排序后再按预算贪心分配（`internal/module/fbsd/tier.go:54-120`），并包含 pinned/grace 优先语义（`internal/module/fbsd/tier.go:76-90`）。`Tracker.DisclosureSnapshot()` 只传递 half-life、frozen、workspace 混合参数，不传递 budget、Hot/Warm/Cold 成本或 grace 信息（`internal/module/fbsd/tracker.go:223-235`）。
   - 风险：UI 或 host RPC 看到的 `disclosure_tier` 可能与 Codex manifest 中实际 Hot/Warm/Cold/Frozen 不一致。例如 score>=3 的 skill 在 manifest 预算耗尽后可能被降到 Warm/Cold/Frozen，但 skill/list 仍显示 hot；新装 grace skill 在 manifest 中可优先进入 Hot，但 disclosure tier 因没有调用记录显示 frozen。排障时会误判量化引擎实际决策。
   - 建议：若 disclosure tier 表示“实际 manifest 分层”，应复用 FBSD `AssignTiers()` 或由 tracker 暴露同源 tier snapshot；若只表示“原始频次热度”，字段名和文档应避免与 manifest tier 混淆。

## 误报与已覆盖项

- fast-path 阈值本轮未列为 bug：`FastPath()` 要求 top score >=2 且 gap >=1（`internal/module/prompt/classifier/prune.go:78-129`），测试覆盖单 tag 不命中 fast-path、同分不命中、默认模板不命中。
- classifier backend 不可用会降级为 noop，并在 router 里记录 diagnostic（`internal/module/thread/router_resolve.go:319-325`），本轮不作为风险。
- FBSD `DisclosureSnapshot()` 复用 `EnvTierConfig()` 中 half-life、frozen、workspace 权重配置（`internal/module/fbsd/tracker.go:223-235`），所以本轮不报告这些参数漂移；问题集中在 tier 语义没有同源复用。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/prompt/classifier ./internal/module/thread ./internal/module/skill ./internal/module/fbsd -count=1
```

结果：guard 通过；`internal/archtest`、`internal/module/prompt/classifier`、`internal/module/thread`、`internal/module/skill`、`internal/module/fbsd` 均通过。

## 下一轮建议

- Round 005 审查 `internal/module/memory/auto_dream_intent.go`、memory dedup/merge 评分和 `internal/module/memory/store.go` 删除匹配评分，重点看 substring/containment 分数在多候选、同名、旧条目场景下是否会误删或误合并。
