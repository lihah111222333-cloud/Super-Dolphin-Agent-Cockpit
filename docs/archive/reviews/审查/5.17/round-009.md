# Round 009 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 05:40:35 KST
- 结束：2026-05-17 05:47:24 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 prompt assembly 的动态 section 排序、turn attachment 聚合、模板区块合并，以及 token budget 提示与真实上下文预算之间的关系。

- `internal/module/prompt/assembler.go`
- `internal/module/prompt/assembler_support.go`
- `internal/module/prompt/template_sections.go`
- `internal/module/prompt/registry.go`
- `internal/module/prompt/dynamic.go`
- `internal/module/prompt/token_budget_provider.go`
- `internal/contract/prompt_attachment.go`
- `internal/provider/claudecli/session_turn.go`
- `internal/provider/codexapp/session_turn.go`

## Findings

1. **[major] turn attachment 聚合没有总预算、去重或全局上限，多个动态 provider 可以叠加撑爆单轮上下文**
   - 证据：`AssembleTurn()` 先收集动态附件，再追加 ClaudeMd provider 附件，最终原样放入 `TurnAssembly.Attachments`（`internal/module/prompt/assembler.go:122-130`）。动态附件收集对所有非 start-only section 逐个 append provider 返回值，没有按数量、字节、token 或来源去重裁剪（`internal/module/prompt/assembler_support.go:33-51`）。`RenderAttachmentText()` 会渲染 metadata、header 和完整 `Content`，自身只验证 envelope 结构，不执行聚合预算（`internal/contract/prompt_attachment.go:70-93`）。
   - 下游影响：Claude CLI 把所有附件文本拼到 turn text 前面（`internal/provider/claudecli/session_turn.go:347-366`）；Codex app 为每个附件追加一个 text input（`internal/provider/codexapp/session_turn.go:91-100`）。
   - 风险：单个 provider 即使做了局部截断，多个 provider 叠加后仍可能让“评分/预算”链路失控，挤占用户输入和高优先级上下文，或触发 provider 侧 context-too-large。
   - 建议：在 `AssembleTurn()` 后增加全局 attachment budget，按 section 优先级和来源做稳定裁剪，并记录 dropped/truncated metadata；同时用内容 hash 去重同一路径或同一内容附件。

2. **[moderate] prompt_template 的 Ordinal 只在模板块内部排序，不能与内置动态 section 交错**
   - 证据：内置动态 section 由 registry 按 `Order` 排序，`dynamicSectionSpecs` 明确了 session guidance、memory、MCP、token_budget、brief 等固定顺序（`internal/module/prompt/registry.go:43-48`、`internal/module/prompt/dynamic.go:52-66`）。`mergeTemplateSections()` 对 DB blocks 按 `(Region, Ordinal)` 排序后，若 key 不命中内置 section，就 append 为 `tpl:<key>`（`internal/module/prompt/template_sections.go:35-63`）。最终渲染按 `resolved` slice 当前顺序输出同 region 内容（`internal/module/prompt/assembler_support.go:104-114`）。
   - 风险：运营侧可能以为 `Ordinal=0` 能把模板动态块放到 memory 或 MCP 指令之前，但 novel key 实际总是排在已解析内置 section 之后。对量化引擎而言，这会让“高优先级预算/约束/路由提示”低于预期，造成调参不可解释。
   - 建议：明确文档语义，或把 template block 转成带 order 的 resolved section 后统一排序；至少在管理端/API 层区分“替换内置 key”和“追加 novel key”的顺序能力。

3. **[moderate] token budget 是工作量提示和后续 backstop，不是 prompt assembly 的容量约束**
   - 证据：`TokenBudgetProvider.Resolve()` 只在 feature flag 开启时注入固定提示文本（`internal/module/prompt/token_budget_provider.go:50-56`）。`evaluateTokenBudgetBackstop()` 根据已消耗 `totalTokens` 决定是否继续，并不参与 attachment、section 或用户上下文裁剪（`internal/module/prompt/token_budget_provider.go:62-81`）。
   - 风险：名称上同为 budget，但它约束的是输出/继续工作目标，不保护输入上下文体积。调用侧若把 token budget 理解为“本轮 prompt 上限”，会错误评估上面 attachment 与模板 section 的容量风险。
   - 建议：将该能力命名或文档化为 `minimum_work_budget`；真正的 prompt/input budget 应独立实现并在 assembly 边界强制执行。

## 误报与已覆盖项

- `buildBaseUserContext()` 对 untrusted CLAUDE.md 已有单源和总 byte limit，本轮问题不是该路径的本地文件读取上限，而是 `TurnAssembly.Attachments` 聚合后缺少跨来源总上限。
- dynamic section 本身有固定 order；本轮报告的是 DB template novel key 追加语义与 `Ordinal` 直觉不一致，不是 registry 排序不稳定。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/prompt ./internal/provider/claudecli ./internal/provider/codexapp -count=1
```

结果：guard 通过；`internal/archtest`、`internal/module/prompt`、`internal/provider/claudecli`、`internal/provider/codexapp` 均通过。

## 下一轮建议

- Round 010 审查 session insight / token telemetry 的计数聚合、UI projection 和观测标记，重点看 token 量化结果是否会被旧值或部分事件污染。
