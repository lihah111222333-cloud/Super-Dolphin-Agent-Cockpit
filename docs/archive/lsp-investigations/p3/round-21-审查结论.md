# 第 21 轮审查结论

## 审查范围

- `cmd/mcp-lsp/search/fileutil.go`（NormalizeRoot、ResolvePath/InRoots、ReadToolFileContent、readValidatedFile、isBinaryFile、path 校验、语言推断）
- `cmd/mcp-lsp/search/searchutil.go`（SearchText、SearchAST、FilterAndCapSearchMatches、walkSearchEntry、sg 命令调用、glob 校验）

> 与第 19 轮覆盖的 `tools/tool_grep.go`（调用方）不重复。本轮聚焦搜索引擎内部实现。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `fileutil.go:94-112` `NormalizeRoot` | 兜底 | root 为空时 fallback 到 `os.Getwd()`；`EvalSymlinks` 失败时保留 Clean 后的路径 | 空 root 是调用方 bug（workspace root 应由 ctx 提供）；EvalSymlinks 失败（如 dangling symlink）被静默忽略 | 空 root 应 error（不 fallback Getwd）；EvalSymlinks 失败至少 Warn |
| `fileutil.go:108-110` EvalSymlinks 失败 | 静默 | `if resolved, err := filepath.EvalSymlinks(cleaned); err == nil { cleaned = resolved }` | 权限错误、dangling symlink 被当成"不需要 resolve" | 至少 debug log |
| `fileutil.go:118-140` `ResolvePathInRoots` | 兜底 | `resolveCandidateInRoots` 失败后尝试 `resolveAppManagedCandidate`；如果 app-managed 也不匹配，返回原始 error | 合理的 fallback（app-managed 路径是合法的第二源） | OK |
| `fileutil.go:166-175` `resolveCandidateInRoots` | 弱契约 | `len(roots) == 0 \|\| roots[0] == ""` 返回 error | 合理的 fail-fast | OK |
| `fileutil.go:271-297` `readValidatedFileInRoots` | 弱契约 | `maxBytes <= 0` 时不做大小限制（`info.Size() > int64(maxBytes)` 为 false） | maxBytes=0 表示"不限制"是合理的语义；但负值是调用方 bug | 负值应 error |
| `fileutil.go:280-281` Lstat 后判 symlink | 弱契约 | `info.Mode()&os.ModeSymlink != 0` 拒绝 symlink | 合理的安全校验 | OK |
| `fileutil.go:323-336` `isBinaryFile` | 静默 | `defer func() { _ = file.Close() }()` 忽略 close 错误 | 只读 probe，close 失败影响极小 | 可接受 |
| `fileutil.go:389-394` `countNormalizedLines` | 兜底 | 空字符串返回 1 | 空文件有 1 行是合理的（空行） | OK |
| `searchutil.go:67-88` `SearchText` | 弱契约 | `opts.Query` 为空时 `shared.NewLineMatcher("", ...)` 行为依赖 matcher 实现 | 如果 matcher 对空 query 匹配所有行，会返回整个文件的所有行 | 入口校验 `opts.Query == ""` 返回 error（与 round-19 P0 配合） |
| `searchutil.go:90-117` `SearchAST` | 弱契约 | `query == ""` 时返回 error "query is required" | 合理的 fail-fast（正面案例） | OK |
| `searchutil.go:119-151` `FilterAndCapSearchMatches` | 兜底 | `maxResults <= 0` 时不截断 | 合理（0 = 不限制） | OK |
| `searchutil.go:207-218` `findDirEntry` | 兜底 | 找不到 entry 时返回 `(nil, nil)` | nil entry 后续 `shouldSearchPath` 会调用 `isSearchCandidate(path, nil, ...)` → 返回 error "missing dir entry" | 当前合理（error 会在下游报出） |
| `searchutil.go:236-237` `searchTextFile` close | 静默 | `defer func() { _ = file.Close() }()` | 同 isBinaryFile | 可接受 |
| `searchutil.go:281-298` `runSGPatternSearch` | 静默 | `cmd.Output()` 失败时如果是 exit code 1 + 无 stderr → 当成"无匹配"返回空 | 合理（sg 用 exit 1 表示无匹配） | OK |
| `searchutil.go:291-294` sg 无匹配 | 兜底 | `isSGNoMatchExitCodeOneWithoutStderr` 判断 | 合理 | OK |
| `searchutil.go:317-353` `runSGKindSearch` | 静默 | temp rule 文件 `defer func() { _ = os.Remove(tmpFile.Name()) }()` 忽略删除错误 | 清理失败留下孤立 temp 文件 | 至少 debug log |
| `searchutil.go:326-331` tmpFile 写入失败 | 兜底 | 写入失败时尝试 Close + 返回 error | 合理的 fail-fast | OK |
| `searchutil.go:409-420` `validateSearchGlob` | 弱契约 | 用 `path.Match(pattern, "probe")` 校验 glob 语法 | 合理 | OK |
| `searchutil.go:468-476` `normalizeASTLanguage` | 兜底 | raw 为空时尝试从 target/glob 推断；都推断不出返回 error | 合理的 fail-fast | OK |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `fileutil.go:108-110` | EvalSymlinks 失败静默保留 Clean 路径 |
| `fileutil.go:328` | isBinaryFile `_ = file.Close()` |
| `searchutil.go:236` | searchTextFile `_ = file.Close()` |
| `searchutil.go:324` | runSGKindSearch `_ = os.Remove(tmpFile.Name())` |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `fileutil.go:94-112` | NormalizeRoot 空 root fallback Getwd |
| `fileutil.go:271-297` | readValidatedFileInRoots maxBytes 负值不校验 |
| `searchutil.go:67-88` | SearchText opts.Query 为空不校验 |

## 修复优先级

### P0（必须本周修）
1. `searchutil.go:67-88` SearchText `opts.Query` 为空时应返回 error——与 round-19 tool_grep.go P0 配合，defense-in-depth

### P1（本月）
2. `fileutil.go:94-112` NormalizeRoot 空 root 改为 error（不 fallback Getwd）
3. `fileutil.go:108-110` EvalSymlinks 失败加 debug log
4. `fileutil.go:271-297` readValidatedFileInRoots maxBytes 负值 error

### P2（下个 sprint）
5. `searchutil.go:324` runSGKindSearch temp 文件删除失败 debug log
6. `fileutil.go:328` / `searchutil.go:236` file.Close 错误 debug log

## 边界条件

1. **`NormalizeRoot` 空 root fallback Getwd 是有意设计**：`NewFileHandler` 和 `NewGrepHandler` 在 `cfg.WorkspaceRoot` 为空时调用 `resolveRoot("")`，后者调用 `NormalizeRoot("")`。这是为了让 dev 模式下不配置 workspace root 也能工作。改 error 后需要确认所有调用点都保证 root 非空——或者在 `resolveRoot` 层做 fallback 而非在 `NormalizeRoot` 层。
2. **`SearchText` 空 query 的行为**：`shared.NewLineMatcher("", false, false)` 的实现需要确认。如果它对空 pattern 返回 error，则 SearchText 已经 fail-fast。如果它返回一个"匹配所有行"的 matcher，则需要在 SearchText 入口校验。
3. **`EvalSymlinks` 失败的场景**：在 macOS 上 `/var` → `/private/var` 是 symlink，EvalSymlinks 通常成功。失败场景：dangling symlink（目标不存在）、权限不足。当前保留 Clean 路径是合理的 fallback——文件可能确实存在但 parent 有 symlink 问题。改 error 可能过于激进。
4. **`isBinaryFile` 的 close 错误**：只读打开的文件 Close 失败极其罕见（通常只有 NFS 等网络文件系统会报错）。加 debug log 是 defensive，不影响行为。
5. **`runSGKindSearch` 的 temp 文件**：`os.CreateTemp` 创建的文件在 `/tmp` 下，OS 会定期清理。删除失败不影响功能。加 debug log 是为了可观测性。
6. **search 包整体代码质量较高**：路径校验（symlink 拒绝、workspace root 边界检查、binary 检测）都是 fail-fast 风格。主要问题集中在 NormalizeRoot 的 Getwd fallback 和 SearchText 的空 query 不校验。

---

**本轮总结**：search 包代码质量较高，安全校验完善（symlink 拒绝、workspace boundary、binary 检测）。唯一的 P0 是 `SearchText` 空 query 不校验——与 round-19 的 tool_grep.go P0 配合形成 defense-in-depth。

**累计进度**：21 轮完成。cron `da34430c` 继续推进。
