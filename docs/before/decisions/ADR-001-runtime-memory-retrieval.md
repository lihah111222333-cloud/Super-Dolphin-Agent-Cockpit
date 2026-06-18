# ADR-001：V3 采用 runtime memory 检索（保留对 Claude 原生"模型主动检索"的架构性偏离）

> 状态：✅ Accepted | 日期：2026-04-17 | 决策者：主线 | 相关：P18.4 §有意偏离项 C-4

## 1. 背景与决策窗口

P18.3 已完成 memory 主链，P18.4 C-4 要裁决的不是“做不做检索”，而是 **保留 V3 runtime 检索** 还是 **回切 Claude 的模型主动检索**。

## 2. 候选方案

### 方案 A：Claude 原生"模型主动检索"

由模型决定是否调用 `memory_search`，再配合 `remember`、`forget` 完成读写。落到 V3 需新建 `internal/mcpserver/memory/`、三类 tool，并为 `claudecli`、`codexapp` 两侧补 MCP 桥接，估算 **8-10 天**。优点是更接近 Claude；缺点是首包约 **+2 roundtrip**。

### 方案 B：V3 现有的 runtime 检索

V3 已有完整 runtime 链：`MemoryContextProvider` 实现动态 section、失效感知与 turn context 三类 contract（`rules_provider.go:17-19,161-216,264-282`）；`PrefetchManager.StartRelevantMemoryPrefetch` 在 `prefetch.go:157-185` 负责 turn-scoped 预取；`retrieval.go:192-245` 提供 term-based ranking；`prompt.go:93` 与 `dynamic.go:57` 把 `memory_context` 固定为 `order=125`、`InputScoped`。优点是 **0 roundtrip**、规则确定；缺点是当前 prefetch 绑定 current turn，跨 thread / workspace 深搜较弱。

## 3. 决策

**选择方案 B（runtime 检索）**，并把“未采用 Claude 原生 memory_search tool”登记为 **架构性偏离**。

理由：

- **性能**：V3 是 0 roundtrip；Claude 原生方案会把检索放进 tool 往返。
- **确定性**：runtime gate + prefetch + ranking 可审计；LLM 自主判断有漏检风险。
- **成本**：方案 A 需补 MCP memory 工具面和双 CLI 接线，8-10 天；方案 B 已完成。
- **CLI 约束**：两家 CLI 对 `memory_context` 都是透明消费，不构成回切理由。

结论：C-4 不是“以后补 Claude memory_search”，而是 **V3 保留 runtime retrieval 作为正式架构**。

## 4. 后果

- 不向模型暴露 `memory_search`；模型只消费 runtime 注入上下文。
- runtime 承担本地 digest/ranking 开销，可忽略。
- invalidation 仍由 runtime 显式维护，依赖 `InputScoped` + `OnPromptInvalidate`。
- prompt 文案必须与 runtime gate 同步；`rules.go:154-161` 已改为 runtime-driven 口径。
- C-4 以后按“已裁决偏离”管理，不再按缺口排队。

## 5. 迁移门槛（何时重新评估）

出现以下任一情况时，重新评估是否回切 Claude 原生方案：

1. 引入 agentic meta-cognition，模型需要显式判断“此刻是否该检索记忆”。
2. 出现跨 thread / 跨 workspace 的 on-demand 深度检索需求。
3. runtime 检索的 token cost 被用户持续反馈为显著问题。

## 6. 源码锚点

- `internal/module/memory/rules_provider.go`
  - `17-19`：三类 contract 编译期断言
  - `161-180`：`SectionName` + `Resolve`
  - `191-216`：`PrepareTurnContext`
  - `264-282`：`OnPromptInvalidate`
- `internal/module/memory/prefetch.go:157-185`：`PrefetchManager.StartRelevantMemoryPrefetch`
- `internal/module/memory/retrieval.go`
  - `192-201`：`searchableFields`
  - `203-212`：`matchedTermCount`
  - `236-245`：`flattenSearchFields`
- `internal/contract/prompt.go:93`：`DynamicSectionMemoryContext = "memory_context"`
- `internal/module/prompt/dynamic.go:57`：`memory_context` 的 `order=125`、`CachePolicy=InputScoped`
- `internal/module/memory/rules.go:154-161`：`searching past context` 文案改为 runtime-driven

## 7. 参考

- `docs/plans/迁移/p18/p18.4-claude-parity-gap-closure.md` §C-4
- `docs/plans/迁移/p18/phase-6-memory-retrieval.md`
- `/Users/mac/Desktop/agnet/cluade/claude-code-sourcemap/docs/claude_memory_system_mapping.md`
- `/Users/mac/Desktop/agnet/cluade/claude-code-sourcemap/docs/claude_memory_system_source_refs.md`
- `/Users/mac/Desktop/agnet/cluade/claude-code-sourcemap/docs/claude_system_prompts_mapping.md`
- `/Users/mac/Desktop/agnet/cluade/claude-code-sourcemap/docs/claude_system_prompts_source_refs.md`
