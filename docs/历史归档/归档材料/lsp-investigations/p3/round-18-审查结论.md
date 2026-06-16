# 第 18 轮审查结论

## 审查范围

- `internal/contract/skill.go`（TrustScope、SkillInfo、SkillLister/InventoryLister 接口、WithSkillCWD/SkillCWDFromContext、SkillHydrationSource）
- `internal/contract/toolbridge.go`（DynamicToolSchema、ToolCallRawMessage、CodexToolSurfaceScope、ToolLifecycleAlreadyPublished）
- `internal/contract/mcp_control.go`（ToolInstance、ToolRegistry/ToolNotifier/ToolHookCallback/PeerCallback/ToolControlPlane 接口）
- `internal/contract/prompt.go`（MCPSnapshot、BuildCtx、StartInput、TurnInput、PromptSection、PromptAssemblyService、FormatUserContextText、WrapSystemReminder、PrepareBaseInstructionBlocks）
- `internal/contract/prompt_attachment.go`（NewRelevantMemoryAttachment、NormalizeAttachmentEnvelope、IsValidAttachmentEnvelope、RenderAttachmentText）

> 与第 16-17 轮覆盖的 `contract/{errors,memory,session,hooks,config,manifest,frc,provider,bus,orchestration}` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `skill.go:106-115` `WithSkillCWD` | 兜底 | `ctx == nil` 兜底 Background；cwd 为空时返回原 ctx 不设值 | nil ctx 是调用方 bug；空 cwd 是合法"不设 scope" | nil ctx 应 panic |
| `skill.go:118-120` `SkillCWDFromContext` | 兜底 | `ctx == nil` 返回 "" | nil ctx 是调用方 bug | panic |
| `toolbridge.go:64-66` `WithToolLifecycleAlreadyPublished` | 弱契约 | 不校验 ctx nil | nil ctx 会 panic 在 `context.WithValue` 内部 | 入口显式 nil 校验 + panic message |
| `toolbridge.go:68-71` `ToolLifecycleAlreadyPublished` | 兜底 | ctx nil 时 `ctx.Value(...)` 会 panic | 同上 | 入口 nil 校验 |
| `mcp_control.go:29-34` `ToolRegistry` 接口 | 弱契约 | `GetInstance` 返回 `(ToolInstance, bool)`；bool=false 时 ToolInstance 是零值 | 调用方必须先判 bool；忘判时用零值 ToolInstance（Lease 为空、Status 为空）继续执行 | 接口文档标注；或改为 `(*ToolInstance, error)` |
| `prompt.go:201-214` `SectionContextCWD` | 兜底 | 三层 fallback：BuildCtx.CWD → Start.CWD → Turn.CWD | 与 round-04/09 firstNonEmpty 同根；但这里是 prompt 组装的合法多源 | 当前合理 |
| `prompt.go:295-300` `NewCriticalPromptSectionError` | 兜底 | `err == nil` 时返回 nil | 合理（nil error = 无错误） | OK |
| `prompt.go:319-339` `PrepareBaseInstructionBlocks` | 兜底 | `enableWhenEval == nil` 时跳过 gate 评估（所有 block 都通过） | nil evaluator 是合法 optional（"不做 gate"） | OK |
| `prompt.go:329-332` enableWhen 评估 | 静默 | `!enableWhenEval(block.EnableWhen, buildCtx, userPrompt)` 为 false 时 continue | block 被 gate 过滤时无日志 | 至少 debug log "block %s gated out" |
| `prompt.go:410-422` `FormatUserContextText` | 兜底 | `normalizeUserContext` 返回 nil 时返回 "" | 合理 | OK |
| `prompt.go:485-489` `appendPromptBlock` | 兜底 | `strings.Contains(base, block)` 时不追加（去重） | 子字符串匹配做去重有误判风险：短 block 可能是 base 的子串但语义不同 | 改为精确匹配（如 hash 或 exact block boundary） |
| `prompt.go:511-528` `normalizeUserContext` | 兜底 | 空 key/value 静默 skip | 合理（空 key/value 无意义） | OK |
| `prompt_attachment.go:12-34` `NewRelevantMemoryAttachment` | 兜底 | `limit < 0` 兜底为 0 | 负 limit 是调用方 bug | 负 limit 应 error/panic |
| `prompt_attachment.go:36-49` `NormalizeAttachmentEnvelope` | 兜底 | `Limit < 0` 兜底 0；`MtimeMs < 0` 兜底 0 | 负值是调用方 bug | 至少 debug log |
| `prompt_attachment.go:51-57` `IsValidAttachmentEnvelope` | 弱契约 | Path/Header/Content 任一为空 → invalid；MtimeMs>0 或 UpdatedAt 非空 → valid | 合理的校验 | OK |
| `prompt_attachment.go:70-94` `RenderAttachmentText` | 兜底 | `!IsValidAttachmentEnvelope` 时返回 "" | 无效 attachment 被静默丢弃 | 至少 debug log "invalid attachment skipped" |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `skill.go:106-115` | WithSkillCWD nil ctx 兜底 Background |
| `skill.go:118-120` | SkillCWDFromContext nil ctx 返回 "" |
| `prompt.go:329-332` | enableWhen gate 过滤无日志 |
| `prompt.go:485-489` | appendPromptBlock 子字符串去重有误判 |
| `prompt_attachment.go:36-49` | NormalizeAttachmentEnvelope 负值兜底 |
| `prompt_attachment.go:70-94` | RenderAttachmentText 无效 attachment 静默丢弃 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `skill.go:106-115` | WithSkillCWD nil ctx 兜底 |
| `skill.go:118-120` | SkillCWDFromContext nil ctx |
| `toolbridge.go:64-71` | WithToolLifecycleAlreadyPublished / ToolLifecycleAlreadyPublished nil ctx |
| `mcp_control.go:29-34` | GetInstance 返回 (value, bool) 而非 (*value, error) |
| `prompt.go:485-489` | appendPromptBlock 子字符串去重 |
| `prompt_attachment.go:12-34` | NewRelevantMemoryAttachment limit<0 兜底 |
| `prompt_attachment.go:36-49` | NormalizeAttachmentEnvelope 负值兜底 |
| `prompt.go:319-339` | PrepareBaseInstructionBlocks enableWhenEval nil 跳过 |
| `toolbridge.go:49-60` | CodexToolSurfaceScope 全字段可为空 |

## 修复优先级

### P0（必须本周修）
1. `prompt.go:485-489` `appendPromptBlock` 的子字符串去重有误判风险——短 block 可能是 base 的子串但语义不同。改为精确匹配或 hash。

### P1（本月）
2. `skill.go:106-120` WithSkillCWD / SkillCWDFromContext nil ctx 改 panic
3. `toolbridge.go:64-71` nil ctx 入口校验
4. `prompt_attachment.go:12-49` limit<0 / MtimeMs<0 至少 debug log
5. `prompt.go:329-332` enableWhen gate 过滤加 debug log
6. `prompt_attachment.go:70-94` RenderAttachmentText 无效 attachment 加 debug log

### P2（下个 sprint）
7. `mcp_control.go:29-34` GetInstance 接口文档标注
8. `prompt.go:485-489` appendPromptBlock 改为 exact block boundary 匹配

## 边界条件

1. **`appendPromptBlock` 的子字符串去重是有意设计**：当 `RenderStartRuntimeContext` 被多次调用时，避免重复追加相同的 system context block。但 `strings.Contains(base, block)` 会在 base 很长时产生 O(n*m) 性能开销，且短 block（如 `"# System Context"` 单行）可能误匹配。修复方向：用 `strings.Contains(base, "\n\n" + block)` 或 `strings.HasSuffix(base, block)` 做更精确的边界匹配。
2. **`WithSkillCWD` nil ctx 兜底 Background**：当前 `context.Background()` 兜底让调用方不会 panic，但丢失了上游 ctx 的 cancel/deadline/values。改 panic 后要确认所有调用点都传了非 nil ctx。
3. **`ToolLifecycleAlreadyPublished` nil ctx**：`ctx.Value(key)` 在 nil ctx 上会 panic（Go runtime 行为）。当前没有显式 nil 校验，依赖 Go 的 panic。加显式校验只是为了更好的 panic message。
4. **`NormalizeAttachmentEnvelope` 负值兜底**：`Limit < 0` 和 `MtimeMs < 0` 在实际调用中不会发生（Limit 来自 int 字段默认 0，MtimeMs 来自 `time.UnixMilli()` 始终 >= 0）。兜底是 defensive，改 debug log 不影响行为。
5. **`PrepareBaseInstructionBlocks` 的 enableWhenEval nil**：这是 fx optional 依赖——当 prompt 模块未注入 evaluator 时，所有 block 都通过。这是合法的"不做 gate"语义。不需要改。
6. **contract 包的审查总结**：经过 round-16/17/18 三轮，`internal/contract/` 包已全面覆盖。该包主要是类型定义和接口声明，实际逻辑较少。发现的问题集中在：Parse* 函数的兜底默认值、nil ctx 兜底、接口返回值约束缺失。大部分是 P1/P2 级别。

---

下一轮范围建议：
- `internal/sidecar/lsp/tools/tool_xref.go`（xref 工具实现：references/call_hierarchy/type_hierarchy）
- `internal/sidecar/lsp/tools/tool_grep.go`（grep 工具实现：text_search/ast_search）
- `internal/sidecar/lsp/tools/tool_file.go`（file 工具实现：open_file/read_file/diagnostics）
- 或 `internal/platform/shared/`（共享工具函数）
