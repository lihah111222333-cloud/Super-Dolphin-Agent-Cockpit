# 第 19 轮审查结论

## 审查范围

- `internal/sidecar/lsp/tools/tool_xref.go`（xref 工具：references/call_hierarchy/type_hierarchy、方向校验、limit 处理）
- `internal/sidecar/lsp/tools/tool_grep.go`（grep 工具：text_search/ast_search、结果截断、payload cap、func range 附加）
- `internal/sidecar/lsp/tools/tool_file.go`（file 工具：open_file/read_file/diagnostics、pos 解析、batch read）

> 与第 01 轮覆盖的 `tools/factory.go`、第 11 轮覆盖的 `middleware/` 不重复。本轮聚焦具体工具 handler 的入参校验与错误处理。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `tool_xref.go:60-61` `runReferences` | 兜底 | `req.IncludeDeclaration == nil` 时默认 `true` | nil 指针表示"未传"；默认 include declaration 是合理的语义 | OK（合理默认） |
| `tool_xref.go:64` `ReferencesLimit` | 弱契约 | `format.ReferencesLimit(req.MaxResults, format.VerbosityCompact)` 内部 clamp | MaxResults=0 走默认；负值走默认 | 负值应 error（与 round-11 ClampTimeout 同根） |
| `tool_xref.go:122-129` `normalizeCallHierarchyDirection` | 兜底 | 空字符串返回 `("", nil)` 表示"默认方向" | 合理（空 = 默认 = incoming） | OK |
| `tool_grep.go:56-106` `handleGrep` | 兜底 | `dispatchToolAction` 返回 `(nil, err)` 后继续用外部 `matches` 变量 | 如果 dispatch 返回 nil error 但 `runErr != nil`（text_search/ast_search 内部设置），外层 `if err != nil` 不会触发 | 当前实现中 action handler 返回 `(nil, runErr)`，dispatch 会把 runErr 作为 err 返回——行为正确。但代码结构容易误读 |
| `tool_grep.go:56-106` 变量作用域 | 弱契约 | `matches` 和 `runErr` 在 dispatch 外部声明，action handler 内部赋值 | 闭包捕获外部变量是 Go 惯用法，但如果 dispatch 内部 panic 后 recover，`matches` 可能是半状态 | 改为 action handler 返回 `([]SearchMatch, error)` 而非通过闭包赋值 |
| `tool_grep.go:166-179` `capGrepResponseBytes` | 静默 | `json.Marshal(resp)` 失败时直接 return（不报错） | marshal 失败说明 resp 结构有问题；当前静默返回可能超预算的 resp | marshal 失败应 return error |
| `tool_grep.go:181-205` `dropLastGrepRow` | 兜底 | `maxFile == ""` 时返回 false（无文件可 drop） | 合理的终止条件 | OK |
| `tool_grep.go:199-201` `resp.Showing--` | 兜底 | `if resp.Showing < 0 { resp.Showing = 0 }` | Showing 不应为负；这里是 defensive | OK |
| `tool_grep.go:312-323` `numericRowValue` | 兜底 | 未知类型返回 0 | 调用方拿到 0 无法区分"值为 0"与"类型不符" | 至少 debug log |
| `tool_grep.go:147-164` `attachFuncRanges` | 静默 | `h.registry == nil` 或 `provider == nil` 时静默 return | registry nil 是合法 optional（grep 不强依赖 LSP）；provider nil 是 enricher 构造失败 | 当前合理 |
| `tool_file.go:116-122` `NewFileHandler` | 弱契约 | `cfg.WorkspaceRoot` 为空时 `resolveRoot` 返回空字符串 | 空 root 后续 `toolWorkspaceRoots(ctx)` 会从 ctx 获取；如果 ctx 也没有则报错 | 当前合理（ctx 是 primary source） |
| `tool_file.go:124-153` `handleFile` | 兜底 | `normalizeFileInputFromPos` 失败返回 error（正确）；`ExpandComments == nil` 默认 true | 合理 | OK |
| `tool_file.go:161-177` `normalizeFileInputFromPos` | 兜底 | pos 为空时直接 return nil（不修改 input） | 合理（pos 是 optional） | OK |
| `tool_file.go:170-176` pos 与 legacy 字段优先级 | 兜底 | `input.FilePath == ""` 时用 pos 解析的 filePath；`input.Offset <= 0` 时用 pos 解析的 line | 合理的 migration 兼容 | OK |
| `tool_file.go:179-200` `openFile` | 兜底 | `h.registry == nil` 返回 errManagerUnavailable | 合理的 fail-fast | OK |
| `tool_file.go:95-105` `warnFileCWDTrace` | 静默 | 每次 file 调用都打 Warn 级别日志 | 生产环境日志量大；Warn 级别不合适 | 改为 Debug |
| `tool_grep.go:48-54` `NewGrepHandler` | 弱契约 | `cfg.Registry` 可为 nil（grep 不强依赖 LSP） | 合理 | OK |
| `tool_grep.go:67-106` dispatch 内部 | 弱契约 | `input.Query` 为空时 `search.SearchText` 会搜索什么？ | 依赖 search 包内部校验；如果 search 包不校验空 query，会返回全部文件 | 入口校验 `input.Query == ""` 时返回 error |
| `tool_xref.go:24-50` `NewXRefHandler` | 弱契约 | `req.Action` 为空时 `dispatchToolAction` 会报 "unsupported action" | 合理的 fail-fast | OK |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `tool_grep.go:166-179` | capGrepResponseBytes marshal 失败静默 return |
| `tool_grep.go:312-323` | numericRowValue 未知类型返回 0 |
| `tool_file.go:95-105` | warnFileCWDTrace 每次调用都打 Warn |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `tool_xref.go:64` | MaxResults 负值走默认 |
| `tool_grep.go:56-106` | matches/runErr 通过闭包赋值 |
| `tool_grep.go:67-106` | input.Query 为空不校验 |
| `tool_file.go:116-122` | cfg.WorkspaceRoot 可为空 |
| `tool_file.go:95-105` | warnFileCWDTrace 用 Warn 级别 |

## 修复优先级

### P0（必须本周修）
1. `tool_grep.go:67-106` input.Query 为空时应返回 error——空 query 搜索全部文件是资源浪费且结果无意义

### P1（本月）
2. `tool_grep.go:166-179` capGrepResponseBytes marshal 失败应 return error
3. `tool_file.go:95-105` warnFileCWDTrace 改为 Debug 级别（每次 file 调用都打 Warn 是日志噪声）
4. `tool_grep.go:56-106` matches/runErr 闭包赋值改为 action handler 返回值

### P2（下个 sprint）
5. `tool_grep.go:312-323` numericRowValue 未知类型 debug log
6. `tool_xref.go:64` MaxResults 负值 error

## 边界条件

1. **`input.Query` 为空的场景**：`search.SearchText` 内部可能对空 query 有校验（返回 error 或空结果）。修复前先确认 search 包行为。如果 search 包已经拒绝空 query，则本轮 P0 是冗余的——但 defense-in-depth 仍建议在工具入口校验。
2. **`capGrepResponseBytes` 的 marshal 失败**：`grepResponse` 是纯 struct，所有字段都是 JSON-safe 类型（string、int、bool、map、slice）。marshal 失败理论不会发生。改 error 是 defensive。
3. **`warnFileCWDTrace` 的 Warn 级别**：这是 P22 调试期加的 trace，用于排查 CWD 解析问题。生产环境应降为 Debug 或删除。当前每次 file 工具调用都打一条 Warn，日志量 = 工具调用频率。
4. **`matches/runErr` 闭包赋值**：当前 `dispatchToolAction` 的签名是 `func(...) (any, error)`，action handler 必须返回 `(any, error)`。grep 的 action handler 返回 `(nil, runErr)` 后 dispatch 把 runErr 作为 err 返回给外层。外层再用 `matches` 变量。这个模式虽然工作正确，但代码可读性差。改为让 action handler 返回 `([]SearchMatch, error)` 需要改 dispatch 泛型签名——工作量较大，列为 P1。
5. **`normalizeCallHierarchyDirection` 空字符串 = 默认方向**：LSP 协议中 call hierarchy 的默认方向是 incoming。空字符串在 manager 层被解释为 incoming。这是合理的。
6. **`tool_file.go` 的 batch read**：`readBatch` 限制最多 10 个文件（`lspReadFileBatchMax`），超过时截断。截断有日志（在 readBatch 内部）。本轮未深入 readBatch 实现，下轮可覆盖。
7. **`tool_xref.go` 的 `format.EnrichLocationResultsWithFuncRange`**：enricher 失败时（如 LSP server 不支持 DocumentSymbol），func range 字段为零值。调用方（LLM）看到 `func_start: 0, func_end: 0` 会忽略。不影响正确性。

---

**本轮总结**：这三个工具实现的代码质量相对较高——大部分入参校验已经在 `factory.go` 的 `resolveFilePositionRequest`、`dispatchToolAction`、`decodeToolParams` 中完成。工具层面的问题集中在：
- grep 空 query 不校验（P0）
- capGrepResponseBytes marshal 失败静默（P1）
- warnFileCWDTrace 日志级别不当（P1）

下一轮范围建议：
- `internal/sidecar/lsp/tools/tool_structure.go` + `tool_completion.go`（structure/completion 工具）
- `internal/sidecar/lsp/tools/tool_edit.go` + `tool_edit_replace.go`（edit 工具）
- `internal/sidecar/lsp/tools/tool_coderun.go`（code_run 工具）
- 或 `internal/sidecar/lsp/search/`（搜索引擎实现）
