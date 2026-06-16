# 第 20 轮审查结论

## 审查范围

- `internal/sidecar/lsp/tools/tool_coderun.go`（code_run/code_run_test 工具：sandbox 执行、snippet 准备、Go/JS/TS 语言支持、project_cmd）
- `internal/sidecar/lsp/tools/tool_edit.go`（edit 工具：patch 解析、replace_range、LSP sync、editEnvelope）
- `internal/sidecar/lsp/tools/tool_structure.go`（structure 工具：document_symbol/workspace_symbol/folding_range/semantic_tokens）
- `internal/sidecar/lsp/tools/tool_completion.go`（completion 工具：LSP 补全、compact list）

> 与第 01 轮覆盖的 `tools/factory.go`、第 19 轮覆盖的 `tool_xref/grep/file` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `tool_coderun.go:168-189` `handleProjectCommand` | 兜底 | `req.WorkDir == ""` 时 fallback 到 workspace root；非绝对路径时 Join 到 workspace root | 合理的 fallback（project_cmd 需要 cwd） | OK |
| `tool_coderun.go:196-216` `snippetRequest` | 静默 | `os.WriteFile` 失败时 `_ = os.RemoveAll(tempDir)` 忽略清理错误 | 清理失败留下孤立 temp 目录 | 至少 Warn |
| `tool_coderun.go:215` cleanup 函数 | 静默 | `func() { _ = os.RemoveAll(tempDir) }` 忽略清理错误 | 同上 | 至少 Warn |
| `tool_coderun.go:148-166` `handleRun` | 弱契约 | `language` 为空时 `prepareSnippet` 会走 default 分支报错 | 合理的 fail-fast | OK |
| `tool_coderun.go:150-152` | 弱契约 | `req.Code` 全空白时报错 "code is required" | 合理 | OK |
| `tool_coderun.go:65-71` `NewCodeRunHandler` | 弱契约 | `rootDir` 为空时 `lspexec.NewSandbox("")` 行为依赖 sandbox 实现 | 应在入口校验 rootDir 非空 | 入口校验 |
| `tool_coderun.go:272-300` `wrapGoSnippet` | 兜底 | `trimmed == ""` 时返回原 code；`strings.HasPrefix(trimmed, "package ")` 时不 wrap | 合理（已有 package 声明的代码不需要 wrap） | OK |
| `tool_edit.go:51-53` `NewEditHandler` | 弱契约 | `registry` 不做 nil 校验；Handle 内部才判 | 与 round-01 factory.go 同根：延迟到调用时才报错 | 构造期校验 |
| `tool_edit.go:63-72` `Handle` | 兜底 | `h.registry == nil` 返回 errEditManagerNil | 合理的 fail-fast | OK |
| `tool_edit.go:74-79` `normalizeEditVersion` | 兜底 | `version <= 0` 返回 defaultEditVersion (2) | 负值 version 是调用方 bug | 负值应 error |
| `tool_structure.go:26-66` `NewStructureHandler` | 兜底 | `req.FilePath = firstNonEmpty(req.FilePath, req.Path)` 两个字段都为空时 FilePath="" | 后续 `resolveManager()` 会用空 FilePath 调用 `managerForFile`，最终报 "file_path is required" | 当前合理（错误会在 managerForFile 层报出） |
| `tool_structure.go:68-75` `firstNonEmpty` | 兜底 | 又一份 firstNonEmpty 实现（与 round-04/09/13 同根） | 重复代码 | 统一到一处 |
| `tool_structure.go:79-100` `resolveWorkspaceSymbolManager` | 弱契约 | `(filePath == "") == (language == "")` 时报错 "exactly one of file_path or language is required" | 两者都为空或都非空都报错——合理的强契约 | OK（正面案例） |
| `tool_completion.go:17-46` `NewCompletionHandler` | 兜底 | `result == nil \|\| len(result.Items) == 0` 返回 emptyListEnvelope | 合理 | OK |
| `tool_completion.go:30` `CompletionLimit` | 弱契约 | `format.CompletionLimit(req.MaxResults, format.VerbosityCompact)` 内部 clamp | MaxResults 负值走默认 | 负值应 error |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `tool_coderun.go:207-208` | WriteFile 失败后 `_ = os.RemoveAll(tempDir)` |
| `tool_coderun.go:215` | cleanup `_ = os.RemoveAll(tempDir)` |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `tool_coderun.go:65-71` | NewCodeRunHandler rootDir 可为空 |
| `tool_edit.go:51-53` | NewEditHandler registry nil 不校验 |
| `tool_edit.go:74-79` | normalizeEditVersion 负值兜底 |
| `tool_structure.go:68-75` | firstNonEmpty 重复实现 |
| `tool_completion.go:30` | MaxResults 负值走默认 |

## 修复优先级

### P0（必须本周修）
（本轮无 P0——这四个工具实现的代码质量较高，入参校验已在 factory 层完成）

### P1（本月）
1. `tool_coderun.go:65-71` NewCodeRunHandler 入口校验 rootDir 非空
2. `tool_edit.go:51-53` NewEditHandler 构造期校验 registry 非 nil
3. `tool_edit.go:74-79` normalizeEditVersion 负值改 error
4. `tool_structure.go:68-75` firstNonEmpty 统一到 shared 包（与 round-04/09/13 协同）

### P2（下个 sprint）
5. `tool_coderun.go:196-216` snippetRequest 清理失败加 Warn
6. `tool_completion.go:30` / `tool_xref.go:64` MaxResults 负值 error

## 边界条件

1. **`normalizeEditVersion` 负值改 error 的影响**：当前 edit 工具的 `version` 字段是 optional（JSON 中不传时为 0）。0 走默认是合理的。只有显式传负值才是 bug。改 error 后要确认 JSON unmarshal 不会把缺失字段解析为负值（Go 的 int 零值是 0，不是负数）。
2. **`NewCodeRunHandler` rootDir 为空**：`lspexec.NewSandbox("")` 的行为需要确认。如果 sandbox 内部用 `os.Getwd()` 作为 root，空字符串可能是合法的"用当前目录"。修复前先确认 sandbox 实现。
3. **`firstNonEmpty` 重复实现**：本轮在 `tool_structure.go:68` 又发现一份。加上之前发现的 `handler_managed_launch.go:189`、`release_scope.go:51`、`runtimeenv/runtime_resolution.go:351`，至少 4 份重复。统一时建议放在 `internal/platform/shared` 或 `internal/util`。
4. **`tool_structure.go:79-100` 的 "exactly one of" 校验**：这是本轮中的正面案例——强契约、明确错误信息、不兜底。可以作为其它工具入参校验的参考。
5. **`tool_coderun.go` 的 temp 目录清理**：cleanup 函数在 defer 中调用，`os.RemoveAll` 失败通常是因为文件被占用（如 Go build cache）。加 Warn 后日志量取决于 code_run 使用频率。
6. **`tool_edit.go` 的 `decodeStrict`**：edit 工具是唯一使用 `decodeStrict` 模式的工具（其它都用 `decodeLenient`）。这意味着 edit 不接受未知字段——这是正确的 fail-fast 行为。

---

**本轮总结**：这四个工具实现的代码质量较高，无 P0 发现。大部分入参校验已在 factory 层（`resolveFilePositionRequest`、`dispatchToolAction`、`decodeToolParams`）完成。工具层面的问题集中在：
- 构造函数不校验依赖（registry/rootDir）
- `firstNonEmpty` 重复实现
- temp 目录清理错误静默

**累计进度**：20 轮完成。

下一轮范围建议：
- `internal/sidecar/lsp/search/`（搜索引擎实现：text_search、ast_search、path resolution）
- `internal/sidecar/lsp/manager/`（LSP manager 生命周期、语言适配器）
- 或 `internal/platform/shared/`（共享工具函数）
- 或 `internal/store/`（数据库访问层）
