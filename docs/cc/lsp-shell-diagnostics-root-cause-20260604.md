# mcp-lsp shell 脚本 diagnostics 不支持根因分析（2026-06-04）

## 结论

当前 `mcp-lsp` 编译后二进制对 shell 脚本（例如 `.sh`）执行 `file` 工具的 `diagnostics` 动作会返回 `language_unsupported`，根因不是单次启动失败或缺少某个已注册二进制，而是 **shell 语言没有进入 mcp-lsp 的语言支持链路**：

1. 文件语言识别没有把 `.sh` 映射到已注册语言，只会 fallback 成 `sh`。
2. 运行时注册的 language adapter 只有 Go / JSTS / Python / Rust / Java / CSS / 文档 fallback（markdown/json/yaml），没有 shell/sh/bash/shellscript。
3. 非打包模式的 installer 没有 shell 语言服务器配置；打包模式的 LSP bundle manifest 也没有 shell 语言服务器。
4. diagnostics 链路会在 reactive bootstrap 阶段调用 registry 解析目标文件，对 `.sh` 解析不到 manager，于是返回 `unsupported language for LSP toolchain`，并被 MCP tool error envelope 分类为 `language_unsupported`。

因此这是一个“能力未接入”的问题，不是“已接入但诊断结果为空”的问题。

## 复现证据

在 2026-06-04 记录的隔离 worktree 中构建并启动 `cmd/mcp-lsp`，对临时 `.sh` 文件调用 MCP `tools/call`：

```text
worktree: .worktrees/lsp-shell-diagnostics-root-cause-20260604
HEAD: 855a106cb Merge branch 'integration/automation-workflow-fixes-20260604' into 'main'
command surface: go build -o <tmp>/mcp-lsp ./cmd/mcp-lsp
tool call: file action=diagnostics file_path=<worktree>/.tmp-lsp-shell-diagnostics-rootcause.sh
```

关键返回：

```json
{
  "isError": true,
  "structuredContent": {
    "success": false,
    "code": "language_unsupported",
    "error": "file:///.../.tmp-lsp-shell-diagnostics-rootcause.sh: unsupported language for LSP toolchain",
    "hint": "next: choose file_path or language_id with a registered language adapter"
  }
}
```

临时 `.sh` 复现文件已删除，未留下工作区临时文件。

## 代码地图入口

按仓库代码地图阅读边界，`cmd/mcp-lsp` 是 generic multi-language LSP peer：

- `README.md`：项目入口列出 `cmd/mcp-lsp` 为 “MCP generic multi-language LSP peer”。
- `docs/doc/codemap/README.md`：第 03 卷覆盖 `cmd/mcp-lsp` / `cmd/mcp-ida`。
- `docs/doc/codemap/03-mcp-lsp-ida.md`：
  - `mcp-lsp` 工具面是 `file`、`inspect`、`xref`、`grep`、`structure`、`edit`、`completion`。
  - 首个依赖语言服务器的工具调用会走 `registry.GetManagerForFile/GetManagerForLanguage`。
  - 注意：该代码地图对 `file.diagnostics` 的旧摘要已不够精确；当前源码顺序是 URI 收集 → `existingDiagnosticURIs` → `bootstrapDiagnostics/reactiveBootstrap` → `registry.BootstrapDocument` → `waitDiagnosticsWithStartupRecovery/WaitDiagnosticsStable` → `registry.Diagnostics`，不是“空结果后才 reactive bootstrap”。

## diagnostics 调用链

源码链路如下：

```text
internal/sidecar/lsp/tools.go
  newToolHandlers()
    "file" -> tools.NewFileHandler(...)

internal/sidecar/lsp/tools/tool_file.go
  handleFile()
    action "diagnostics" -> h.handleDiagnostics(ctx, input)

internal/sidecar/lsp/tools/tool_diagnostics.go
  handleDiagnostics()
    collectDiagnosticURIs()                         // 显式目标解析为 URI；不存在目标仍保留 URI（194-224）
    fetchDiagnosticsWithRetry()
      existingDiagnosticURIs()                      // 只筛出真实存在、普通文件、非 symlink URI（239-251）
      bootstrapDiagnostics()                        // 先于等待和读取 diagnostics 执行（47-82）
        reactiveBootstrap()
          registry.BootstrapDocument(ctx, uri)      // 显式存在目标在这里解析 manager（333-352）
      waitDiagnosticsWithStartupRecovery()
      registry.Diagnostics()

internal/sidecar/lsp/manager/registry.go
  BootstrapDocument()
    path := strings.TrimPrefix(uri, "file://")
    resolveManagerForTarget(ctx, DetectLanguageID(path), path, uri)
      if lang not in r.managers -> ErrUnsupportedLanguage
```

这个调用链说明 diagnostics 在真正读取诊断前，会先对“显式传入且真实存在”的目标做 reactive bootstrap，并要求目标文件能解析到一个已注册的 language manager。`.sh` 没有 manager，所以显式存在的 `.sh` 会在 bootstrap 阶段即失败。无显式目标时 `collectDiagnosticURIs()` 返回空；显式目标不存在时不会进入 `existingDiagnosticURIs()` 的 bootstrap 列表，因此不会触发这一步。

## 根因细节

### 1. `.sh` 会被识别为 `sh`，但没有注册 `sh`

`internal/sidecar/lsp/manager/registry.go`：

- `languageIDByExtension` 只列出 `.go`、`.js/.jsx/.mjs/.cjs`、`.ts/.tsx`、`.py/.pyi`、`.rs`、`.java`、`.css`、`.md/.markdown`、`.json`、`.yaml/.yml`。
- 没有 `.sh` / `.bash` / `.zsh`。
- `DetectLanguageID()` 找不到扩展映射时，会 `return strings.TrimPrefix(ext, ".")`；所以 `.sh` 最终变成语言 ID `sh`（`internal/sidecar/lsp/manager/registry.go:250-259`）。
- `resolveManagerForTarget()` 用 `sh` 查 `r.managers`，查不到就返回 `ErrUnsupportedLanguage`（`internal/sidecar/lsp/manager/registry.go:147-156`）。
- diagnostics 的下游分组 `groupURIsByManager()` 虽然会跳过 unsupported URI（`internal/sidecar/lsp/manager/registry.go:377-391`），但它不是显式存在 `.sh` 的有效防护：当前顺序是先 reactive bootstrap，`BootstrapDocument()` 已在读取 diagnostics 前失败。

### 2. 默认 adapter 注册不包含 shell

`internal/sidecar/lsp/multilsp/language_service_config.go` 的 `NewLanguageAdapterRegistryFromConfig()` 只注册：

- `goLanguageAdapter`
- JSTS adapter
- Python adapter
- Rust adapter
- Java adapter
- CSS adapter
- `documentFallbackAdapter`

`internal/platform/config/lsp.go` 的默认配置也只包含：

- project adapters：`jsts`、`python`、`rust`、`java`、`css`
- document fallback：`markdown`、`json`、`yaml`

没有 shell adapter、shell root marker、shell source extension、shell diagnostics policy。

### 3. runtime primary language 和 installer 也不包含 shell

`cmd/mcp-lsp/runtime.go`：

- `runtimePrimaryLanguageIDs()` 返回 `go`、`javascript`、`python`、`css`、`rust`、`java`、`markdown`。
- `setupInstaller()` 注册了 `typescript-language-server`、`pyright-langserver`、`vscode-css-language-server`、`rust-analyzer`、`jdtls`、`gopls`，没有 shell/bash language server。

这意味着非打包二进制即使 PATH 中存在某个 shell 语言服务器，也不会被当前 registry 自动选择。

### 4. 打包 LSP bundle 也不包含 shell

打包脚本同样只准备当前支持语言：

- `scripts/prepare_lsp_bundle_macos.sh` / `scripts/prepare_lsp_bundle_linux.sh` 的 `lsp_specs` 包含 `gopls`、`typescript-language-server`、`vscode-css-language-server`、`pyright`、`rust-analyzer`、`sg`、`go`，full profile 额外包含 Java/JDTLS。
- `scripts/package_macos.sh` / `scripts/package_linux.sh` 的 `lsp_server_specs` 同样没有 shell 语言服务器。
- `internal/platform/runtimeenv/runtimeenv.go` 的 `defaultLSPLanguages()` 也没有 shell server ID 到 language ID 的映射。

所以“编译后的二进制”在打包/运行时环境中也没有可用的 shell 诊断后端。

### 5. 错误被稳定分类为 `language_unsupported`

`internal/mcpserver/common/tool_error_envelope.go` 会把包含 “unsupported language” / “unsupported language for lsp toolchain” 的错误归类为：

```text
code: language_unsupported
hint: next: choose file_path or language_id with a registered language adapter
```

这与本次二进制复现返回一致。

## 可达性与防护边界

- 显式传入且真实存在的 `.sh`：`collectDiagnosticURIs()` 会解析为 URI（`internal/sidecar/lsp/tools/tool_diagnostics.go:194-224`），`existingDiagnosticURIs()` 会把普通非 symlink 文件送入 bootstrap（`internal/sidecar/lsp/tools/tool_diagnostics.go:239-251`），随后 `reactiveBootstrap()` 调 `registry.BootstrapDocument()`（`internal/sidecar/lsp/tools/tool_diagnostics.go:333-352`），最终在 manager 解析失败。
- 无显式目标：`collectDiagnosticURIs()` 对空目标直接返回空（`internal/sidecar/lsp/tools/tool_diagnostics.go:194-198`），`bootstrapDiagnostics()` 对空 existing URI 返回 `manager`，不会 reactive bootstrap（`internal/sidecar/lsp/tools/tool_diagnostics.go:71-82`）。
- 显式目标不存在：路径解析后可生成 URI，但 `existingDiagnosticURIs()` 只保留 `os.Lstat` 成功且为普通非 symlink 文件的 URI（`internal/sidecar/lsp/tools/tool_diagnostics.go:239-251`），不存在目标不会进入 bootstrap。
- 下游 `groupURIsByManager()` 跳过 unsupported（`internal/sidecar/lsp/manager/registry.go:377-391`）不是有效防护，因为显式存在目标已先在 `BootstrapDocument()` 的 `resolveManagerForTarget(ctx, DetectLanguageID(path), ...)` 失败（`internal/sidecar/lsp/manager/registry.go:300-306`，`internal/sidecar/lsp/manager/registry.go:147-156`）。

## 非根因

- 不是 `.sh` 文件不存在或路径越界：复现文件在 worktree 内，且 diagnostics 已进入语言解析阶段。
- 不是 shell 脚本语法本身的问题：脚本内容故意有错误，但当前链路在语法诊断前已经因为没有 shell manager 失败。
- 不是单纯的打包遗漏：源码默认 adapter、runtime primary language、installer、bundle manifest 都没有 shell。
- 不是 diagnostics UI 渲染为空：MCP 返回的是 `isError=true` 的 `language_unsupported` 结构化错误。

## 修复方向（未在本次任务中实施）

如需支持 shell diagnostics，需要补齐整条语言接入链路，而不是只加一个扩展名：

1. 语言识别：把 `.sh` / 需要支持的 shell 扩展映射到一个明确 language ID（例如 `shellscript` 或项目确认后的 ID）。
2. adapter：新增 shell language adapter，明确 root 解析、server command、bootstrap policy、cache key。
3. installer：非打包模式注册对应 shell 语言服务器安装/查找策略。
4. bundle：打包脚本、manifest、校验脚本加入 shell 语言服务器及 language 映射。
5. diagnostics 语义：决定是否只依赖 language server，还是需要结合 shellcheck；并为 `.sh` 增加回归测试，覆盖 `file action=diagnostics` 对 shell 文件不再返回 `language_unsupported`。
6. 非回归约束：扩展 shell 支持时，应保留并扩展现有多语言/打包防护，避免破坏 Go、JSTS、Python、Rust、Java、CSS 和文档 fallback 既有链路。

在上述链路补齐前，shell 脚本诊断不支持是当前源码和二进制的预期结果。
