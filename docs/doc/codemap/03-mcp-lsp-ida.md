# super-agent-v3 代码地图（03）

## MCP LSP Server 与 IDA Server

> 本文按当前工作树重新审查并修订，实际对照了以下源码：
>
> - `cmd/mcp-lsp/*.go`
> - `cmd/mcp-ida/*.go`
> - `internal/mcpserver/lsp/{edit,exec,format,gopls,installer,manager,middleware,protocol,search,tools}/*.go`
>
> 目录树中同时列出生产文件和当前存在的 `*_test.go`，避免把测试文件误认为“未覆盖”。

---

## 1. 模块概述

### 1.1 `mcp-lsp`（`cmd/mcp-lsp`）

`mcp-lsp` 是一个已经成型的 **MCP 工具服务进程**，职责分成四层：

1. **MCP 传输层**
   - `stdio` 始终开启，`stdout` 专门保留给 MCP JSON-RPC；
   - `GO_AGENT_PEER_MODE=1` 时再额外开启共享 HTTP MCP 端点；
   - 实际对外 RPC 由 `common.Server` / `common.HTTPServer` 处理，暴露 `initialize`、`ping`、`shutdown`、`tools/list`、`tools/call` 等方法。

2. **工具发布层**
   - `cmd/mcp-lsp/schema.go` 负责 tool input schema；
   - `cmd/mcp-lsp/tools.go` 负责 manifest 与 handler 绑定；
   - 当前固定暴露 9 个工具：`lsp_file`、`lsp_inspect`、`lsp_xref`、`lsp_grep`、`lsp_structure`、`lsp_edit`、`lsp_completion`、`code_run`、`code_run_test`。

3. **运行时装配层**
   - `cmd/mcp-lsp/runtime.go` 构造 `manager.Registry`、installer、stdio runner；
   - `cmd/mcp-lsp/fx.go` 把 bootstrap runner、stdio runner、http runner 纳入 Fx 生命周期；
   - `registryToolProvider` 同时被 MCP stdio/http server 和 bootstrap `OnToolsList` / `OnToolsCall` 复用。

4. **LSP / 搜索 / 编辑 / 执行实现层**
   - 真正工具语义落在 `internal/mcpserver/lsp/tools`；
   - 语言服务抽象在 `internal/mcpserver/lsp/manager`；
   - 底层多语言 LSP client / manager 基座在 `internal/mcpserver/lsp/gopls`；
   - 搜索、补丁匹配、显示整形、沙箱执行分别在 `search`、`edit`、`format`、`exec` 子包。

**一句话总结：**`mcp-lsp` 是项目里的多语言代码理解 / 编辑 / 搜索 / 沙箱执行 MCP 侧车。

### 1.2 `mcp-ida`（`cmd/mcp-ida`）

`mcp-ida` 在当前源码范围内仍是一个 **bootstrap / lifecycle 进程**，而不是完整 tool server：

- capability 固定为 `tools/ida`；
- 只装配 `bootstrapRunner`；
- 响应配置变更、关闭请求、退出 final report；
- 没有 `common.Server` / `common.HTTPServer`；
- 没有 tool manifest、schema、handler 映射；
- 没有对 `internal/mcpserver/lsp` 的直接依赖。

另外，`mcp-lsp` 的 bootstrap 注册还要求 `GO_AGENT_PEER_MODE=1`，而 `mcp-ida` 只要 `RPCAddr` 有值就会尝试启动 bootstrap client，这一点与 `mcp-lsp` 不同。

---

## 2. 目录结构（反向核对所有 `.go` 文件）

```text
cmd/
├── mcp-lsp/
│   ├── fx.go
│   ├── http_runner.go
│   ├── main.go
│   ├── runtime.go
│   ├── schema.go
│   └── tools.go
└── mcp-ida/
    ├── fx.go
    └── main.go

internal/mcpserver/lsp/
├── edit/
│   ├── patchmatch.go
│   ├── patchmatch_test.go
│   ├── patchparse.go
│   ├── patchparse_test.go
│   ├── replaceutil.go
│   ├── replaceutil_test.go
│   ├── seeksequence.go
│   └── seeksequence_test.go
├── exec/
│   └── sandbox.go
├── format/
│   ├── compact.go
│   ├── display.go
│   ├── funcrange.go
│   └── render.go
├── gopls/
│   ├── bootstrap_doc.go
│   ├── cache.go
│   ├── client.go
│   ├── factory.go
│   ├── gomod.go
│   ├── gomod_test.go
│   ├── manager.go
│   ├── manager_diagnostics.go
│   ├── manager_lifecycle.go
│   ├── manager_symbols.go
│   ├── manager_symbols_fallback.go
│   ├── pool.go
│   ├── recycler.go
│   ├── state.go
│   ├── transport.go
│   └── transport_conn.go
├── installer/
│   └── installer.go
├── manager/
│   ├── manager.go
│   ├── registry.go
│   ├── registry_e2e_test.go
│   └── registry_multilang_e2e_test.go
├── middleware/
│   ├── budget.go
│   ├── logging.go
│   ├── recovery.go
│   └── timeout.go
├── protocol/
│   ├── codec.go
│   ├── ext.go
│   ├── methods.go
│   ├── notification.go
│   └── types.go
├── search/
│   ├── fileutil.go
│   ├── language_inference_test.go
│   └── searchutil.go
└── tools/
    ├── factory.go
    ├── tool_coderun.go
    ├── tool_coderuntest.go
    ├── tool_completion.go
    ├── tool_diagnostics.go
    ├── tool_edit.go
    ├── tool_edit_replace.go
    ├── tool_edit_support.go
    ├── tool_edit_support_test.go
    ├── tool_file.go
    ├── tool_grep.go
    ├── tool_inspect.go
    ├── tool_middleware_test.go
    ├── tool_structure.go
    ├── tool_structure_test.go
    └── tool_xref.go
```

---

## 3. `cmd` 层逐文件结论

### 3.1 `cmd/mcp-lsp`

- `main.go`
  - 把 `stdout` 挪给 MCP JSON-RPC，日志全部走 `stderr`；
  - 将 `GOMAXPROCS` 上限压到 2，符合轻量 sidecar / peer 进程定位。

- `fx.go`
  - `run()` 是总装配入口；
  - 读取 bootstrap 配置并声明 capability `tools/lsp`；
  - 构造 `OnToolsList` / `OnToolsCall`，与 toolbridge / 控制面复用同一套 tool definitions；
  - runner 共有 3 个：bootstrap、stdio、http；
  - `bindRuntime()` 用 `platformrunner.RunGroup()` 托管所有 runner 生命周期。

- `runtime.go`
  - `newManager()` 获取工作目录并创建 installer + `manager.NewRegistry()`；
  - `createGenericManager()` 虽然调用的是 `gopls.NewManager()`，但通过切换 binary / args / init options 复用成通用 LSP manager；
  - `newStdioRunner()` 在 stdio server 退出时负责关闭 registry / manager；
  - `setupInstaller()` 只注册了 `javascript`、`typescript`、`python`、`css`、`rust`、`java`、`go` 的安装配置；`gomod`、`gosum`、`gowork` 虽注册了 manager，但没有 installer config。

#### 3.1.1 语言服务器注册矩阵（与 `runtime.go` 的 `createGenericManager` 调用一致）

| language ID | manager binary | args | installer config | 备注 |
|---|---|---|---|---|
| `go` | `gopls` | `nil` | `go install golang.org/x/tools/gopls@latest` | Go 主语言 |
| `gomod` | `gopls` | `nil` | **无** | 与 `go` 共享同一个 `goplsMgr`，但 `GetManagerForFile(go.mod)` 会先触发 installer 并报缺配置 |
| `gosum` | `gopls` | `nil` | **无** | 与 `go` 共享同一个 `goplsMgr`，同样缺 installer config |
| `gowork` | `gopls` | `nil` | **无** | 与 `go` 共享同一个 `goplsMgr`，同样缺 installer config |
| `javascript` | `typescript-language-server` | `--stdio` | `npm install -g typescript-language-server typescript` | 与 `typescript` 共享同一个 `jsMgr` |
| `typescript` | `typescript-language-server` | `--stdio` | `npm install -g typescript-language-server typescript` | 与 `javascript` 共享同一个 `jsMgr` |
| `python` | `pyright-langserver` | `--stdio` | `npm install -g pyright` | 单独 `pyMgr` |
| `css` | `vscode-css-language-server` | `--stdio` | `npm install -g vscode-langservers-extracted` | 单独 `cssMgr` |
| `rust` | `rust-analyzer` | `nil` | `rustup component add rust-analyzer` | 单独 `rustMgr` |
| `java` | `jdtls` | `nil` | `brew install jdtls` | 单独 `javaMgr`，附带 `jdtlsInitOptions()` |

- `http_runner.go`
  - 仅在 `GO_AGENT_PEER_MODE=1` 时启动；
  - 启动本地环回地址 HTTP MCP server，并写 peer discovery；
  - 非 peer mode 时返回 `lspBlockRunner`，只阻塞等取消。

- `schema.go`
  - 定义 9 个工具的输入 schema；
  - 所有 schema 根对象统一为 `type=object`、`additionalProperties=false`；
  - `lsp_edit.edits` 的数组元素是宽松 object（`additionalProperties=true`）；
  - schema 只是“声明层约束”，真正严格程度取决于各 handler 的 decode 模式，当前 server 不会按 schema 自动校验 tool arguments。

- `tools.go`
  - 维护完整 tool manifest 列表；
  - `newToolHandlers()` 绑定到 `internal/mcpserver/lsp/tools`；
  - `toolDefinitions()` 在 handler 缺失时会回退到 `stubToolHandler`，但当前 9 个 manifest 都有实际 handler。

### 3.2 `cmd/mcp-ida`

- `main.go`
  - 极简入口，失败直接打印到 `stderr` 并退出。

- `fx.go`
  - 只装配 bootstrap client 与 bootstrap runner；
  - capability 为 `tools/ida`；
  - 有 `FinalReport`、`OnConfigChanged`、`OnShutdown`；
  - 没有 stdio / http server，没有 `tools/list` / `tools/call` handler。

---

## 4. 总体调用链

### 4.1 `mcp-lsp` 启动链

```text
main
  └─ runMain
      └─ run
          └─ fx.New(...)
              ├─ bootstrap.New
              ├─ newManager
              │   ├─ setupInstaller
              │   ├─ manager.NewRegistry
              │   └─ createGenericManager(...) x 6
              │       └─ registry.Register(...) x 10 language IDs
              ├─ newToolHandlers
              ├─ newServer
              ├─ newBootstrapRunner
              ├─ newStdioRunner
              └─ newHTTPRunner
          └─ bindRuntime
              └─ platformrunner.RunGroup(runners)
```

说明：`createGenericManager(...) x 6` 分别创建 Go、JS/TS、Python、CSS、Rust、Java 6 个 manager 实例；注册到 registry 的 language ID 共 10 个。

### 4.2 `mcp-lsp` 工具调用链

`stdio` 与 `HTTP` 的主调用链：

```text
common.Server / common.HTTPServer
  └─ tools/call
      └─ registryToolProvider.CallTool
          └─ handleToolCall
              └─ toolDefinition.Handler
                  └─ internal/mcpserver/lsp/tools handler
                      ├─ middleware.Recovery / Logging / Timeout
                      ├─ lsp_file 与 lsp_grep 额外套 middleware.WithOutputBudget
                      └─ decode + action dispatch + 具体实现
```

bootstrap / toolbridge 复用路径：

```text
bootstrap.Config.OnToolsCall
  └─ registryToolProvider.CallTool
      └─ handleToolCall
          └─ 同一套 toolDefinition.Handler
```

各 handler 的主要落点：

```text
internal/mcpserver/lsp/tools
  ├─ manager.Registry.GetManagerForFile/GetManagerForLanguage
  ├─ manager.Manager.*                         # hover/rename/symbol/diagnostics/...
  ├─ search.SearchText/SearchAST               # lsp_grep
  ├─ edit.Parse/ParseMulti/MatchContext/...    # replace_range patch/edits/new_text
  ├─ exec.Sandbox.Run                          # code_run / code_run_test
  └─ format.NormalizeForDisplay/...            # 输出整形和 0-based -> 1-based 转换
```

注意：`code_run` / `code_run_test` 不走 `manager.Registry`；`lsp_file.open_file` 只有在成功拿到 manager 时才 best-effort `DidOpen`，拿不到 manager 不会让文件读取本身失败。

### 4.3 `mcp-ida` 启动链

```text
main
  └─ run
      └─ fx.New(...)
          ├─ bootstrap.New
          └─ newBootstrapRunner
      └─ bindRuntime
          └─ platformrunner.RunGroup(runners)
```

---

## 5. 工具清单与 schema（按 `tools.go` / `schema.go` 校准）

### 5.1 完整工具清单与 action / mode

| Tool | schema 中的 action / mode | 对应 handler |
|---|---|---|
| `lsp_file` | `open_file` / `read_file` / `diagnostics` | `tools.NewFileHandler` |
| `lsp_inspect` | `hover` / `definition` / `implementation` / `type_definition` / `signature_help` | `tools.NewInspectHandler` |
| `lsp_xref` | `references` / `call_hierarchy` / `type_hierarchy` | `tools.NewXRefHandler` |
| `lsp_grep` | `text_search` / `ast_search` | `tools.NewGrepHandler` |
| `lsp_structure` | `document_symbol` / `workspace_symbol` / `folding_range` / `semantic_tokens` | `tools.NewStructureHandler` |
| `lsp_edit` | `rename` / `code_action` / `format` / `replace_range` | `tools.NewEditHandler` |
| `lsp_completion` | 无 `action` 字段 | `tools.NewCompletionHandler` |
| `code_run` | `mode=run` / `mode=project_cmd` | `tools.NewCodeRunHandler` |
| `code_run_test` | 无 `mode` 字段；执行 Go test | `tools.NewCodeRunTestHandler` |

补充：`tools.go` 的 manifest 文案把 `lsp_edit.replace_range` 描述为 “single-hunk patch”，但当前实现的 `parsePatchHunks()` 会先走 `edit.ParseMulti()`，显式 `@@ ` 多 hunk patch 也能进入实现层；schema 本身没有限制 hunk 数。

### 5.2 schema 摘要与实现层约束

| Tool | schema 必填 | schema properties | 实现层补充约束 |
|---|---|---|---|
| `lsp_file` | `action` | `action` `file_path` `file_paths` `offset` `limit` | `read_file` 单文件返回带行号文本；批量最多 10 个文件；单文件读取上限 2 MiB；批量 JSON payload 会压缩到 16 KiB 以内；`open_file` / `read_file` 要求路径在 workspace root 内、regular file、非 symlink、非 binary；`diagnostics` 过滤目标要求在 root 内、regular file、非 symlink，但不读取内容做 binary probe；`open_file` 只有在存在对应 manager 时才额外 `DidOpen`；`diagnostics` 不传目标时取 registry 中所有 manager 的 diagnostics，当前没有按共享 manager 去重 |
| `lsp_inspect` | `action,file_path,line,column` | `action` `file_path` `line` `column` | 严格解码；行列必须是 1-based 正整数；先按 `file_path` 找 manager，再解析成绝对路径 / file URI |
| `lsp_xref` | `action,file_path,line,column` | `action` `file_path` `line` `column` `direction` `include_declaration` `verbosity` `max_results` | 严格解码；`include_declaration` 仅对 `references` 生效；`verbosity` 目前只影响 `references`；`direction` 对层级查询生效，schema 枚举为 `incoming/outgoing/both/supertypes/subtypes`，实现还接受空字符串作为默认方向；`max_results` cap 50 |
| `lsp_grep` | `action` | `action` `query` `path` `glob` `language` `regex` `case_sensitive` `max_results` | `query` 在实现层必填；`text_search` 走本地扫描；`ast_search` 依赖外部 `sg` CLI；AST language 可由文件路径或 glob 推断，推断不到则必填；默认 30、最大 50；搜索结果会 best-effort 通过 LSP `DocumentSymbol` 附加 `func_start/func_end` |
| `lsp_structure` | `action` | `action` `file_path` `query` `language` `verbosity` `max_results` | 严格解码；`document_symbol`、`folding_range`、`semantic_tokens` 需要可解析的 `file_path`；`workspace_symbol` 要求 `file_path` 与 `language` **恰好一个**，并且 `query` 必填；`workspace_symbol` 不接受目录 path；`verbosity` 目前只影响 `workspace_symbol`；`semantic_tokens` 默认/上限为 200 token |
| `lsp_edit` | `action,file_path` | `action` `file_path` `line` `column` `end_line` `end_column` `patch` `edits` `new_name` `new_text` `only` | `rename` 需要 `line/column/new_name`，默认直接落盘；显式传隐藏字段 `persist_to_disk=false` 才只返回 prepared `workspace_edit`；`code_action` 需要 `line/column`，当前只构造单点 range，未消费 `end_line/end_column`；`format` 返回 `TextEdit`，不落盘；`replace_range` 需要 `patch` / `edits` / `new_text` 之一，`new_text` 坐标模式会用 `line/column/end_line/end_column`；隐藏字段 `version` 用于 LSP sync；`replace_range` 总是写磁盘并同步 LSP；`resolveFilePath()` 不复用 `search` 的 workspace-root 限制 |
| `lsp_completion` | `file_path,line,column` | `file_path` `line` `column` `verbosity` `max_results` | 严格解码；compact 默认 20，full 默认/最大 50；行列必须 1-based 正整数 |
| `code_run` | `mode` | `mode` `language` `code` `command` `auto_wrap` `work_dir` `timeout` | `run` 需要 `code`，支持 `go`、`javascript`/`js`、`typescript`/`ts`；Go 默认自动补 `package main` 和部分 stdlib import；TS 走 `node --experimental-strip-types`；`project_cmd` 需要 `command`，走 `$SHELL -lc`，`SHELL` 为空时 fallback `/bin/sh`；`work_dir` 必须留在 sandbox root 内；timeout 上限为 `TierExec` |
| `code_run_test` | `test_func` | `test_func` `test_pkg` `timeout` | `test_func` 必须匹配安全正则 `^[A-Za-z0-9_]+$`；执行 `go test -run ^TestName$ <pkg>`；默认包为 `./...`；timeout 上限为 `TierExec` |

### 5.3 decode / 校验策略

| 解码模式 | 工具 | 含义 |
|---|---|---|
| `decodeStrict` | `lsp_inspect` `lsp_xref` `lsp_structure` `lsp_completion` | `null` / 空参数会归一成 `{}`，并拒绝未知字段与 trailing JSON |
| `decodeLenient` | `lsp_file` `lsp_grep` | `null` / 空参数会归一成 `{}`，未知字段按 Go `json.Unmarshal` 默认行为忽略 |
| `decodeRaw` | `lsp_edit` `code_run` `code_run_test` | 直接 `json.Unmarshal`，不归一空参数，未知字段会被忽略，但 schema 之外且结构体声明过的字段（如 `persist_to_disk`、`version`、`test_func`、`test_pkg`）可以进入 handler |

补充：`lsp_file` 与 `lsp_grep` 还额外套了 output budget；超出时会返回截断 envelope，而不是完整原始 payload。

### 5.4 `mcp-ida` 的工具面现状

当前 `cmd/mcp-ida` 没有 tool manifest，也没有 `tools/list` / `tools/call` 注册，因此仍应视为 bootstrap / lifecycle 代理，而不是独立 MCP tool server。

---

## 6. 核心类型 / 接口

### 6.1 `cmd` 层关键类型

| 类型 | 位置 | 作用 |
|---|---|---|
| `Manager` | `cmd/mcp-lsp/runtime.go` | cmd 层运行时容器，持有 `manager.Registry` 与 workspace root |
| `ToolHandler` / `ToolHandlers` | `cmd/mcp-lsp/tools.go` | tool 名到 handler 的映射 |
| `toolDefinition` | `cmd/mcp-lsp/tools.go` | manifest + handler 绑定结果 |
| `registryToolProvider` | `cmd/mcp-lsp/fx.go` | 适配 `common.ToolProvider` |
| `bootstrapRunner` | `cmd/mcp-lsp/fx.go` / `cmd/mcp-ida/fx.go` | 控制面 bootstrap 生命周期 runner |
| `stdioRunner` | `cmd/mcp-lsp/runtime.go` | 运行 stdio MCP server，并在退出时关闭 manager |
| `httpRunner` / `lspBlockRunner` | `cmd/mcp-lsp/http_runner.go` | peer mode HTTP server / 非 peer 占位 runner |
| `runtimeParams` | `cmd/*/fx.go` | Fx 注入的 runners + shutdowner 聚合参数 |

### 6.2 `manager` 包接口

- `manager.Manager`
  - 生命周期：`Close`
  - 导航：`Definition` `Implementation` `TypeDefinition` `Hover` `SignatureHelp`
  - 交叉引用：`References` `CallHierarchy` `TypeHierarchy`
  - 结构：`DocumentSymbol` `WorkspaceSymbol` `FoldingRange` `SemanticTokens`
  - 编辑 / 补全：`Completion` `Rename` `CodeAction` `Format`
  - 文档同步：`DidOpen` `DidChange` `DidClose` `BootstrapDocument` `BootstrapDocumentOpenOnly`
  - 诊断：`Diagnostics` `WaitDiagnosticsStable` `CurrentDiagnosticGeneration` `AdvanceDiagnosticGeneration`

- `manager.Registry`
  - `GetManagerForFile` / `GetManagerForLanguage`
  - `Diagnostics` / `WaitDiagnosticsStable`
  - `CurrentDiagnosticGeneration`
  - `BootstrapDocument`
  - `Close`

- 关键辅助项
  - `ErrUnsupportedLanguage`
  - `DetectLanguageID(path string)`
  - `dynamicRegistry.Register(languageID, manager)`（具体实现有，接口未暴露）

### 6.3 `gopls` 包关键类型

| 类型 | 作用 |
|---|---|
| `Config` | 构造 manager 的配置；除 `WorkspaceRoot`、`ClientFactory` 外，还包含 diagnostics 等待时间与 `Logger` |
| `Client` | LSP client 抽象，封装 `Initialize` / `Shutdown` / `Request` / `Notify` / `DidOpen` / `DidChange` / `DidClose` / `Close` |
| `manager` | 通用多语言 LSP manager 实现；内部维护 workspace->client 映射、diagnostic snapshot、bootstrap 协调器、pool recycler |
| `workspaceClient` | 单个 workspace root 对应的 LSP client 句柄 |
| `ManagerPool` | lease 跟踪与 recycler 容器；当前实现只围绕 primary manager 工作 |
| `bootstrapCoordinator` / `bootstrapStateStore` | 文档 bootstrap 去重、等待、状态流转 |
| `lspCacheStore` | bootstrap 文档缓存，支持内存模式与可选磁盘持久化 |

### 6.4 `tools` / `edit` / `protocol` 关键类型

| 类型 | 位置 | 作用 |
|---|---|---|
| `tools.Config` | `tools/tool_file.go` | `lsp_file` / `lsp_grep` handler 配置 |
| `EditRequest` / `ReplaceEdit` | `tools/tool_edit.go` | `lsp_edit` 请求体；支持隐藏字段 `persist_to_disk`、`version` |
| `CodeRunRequest` | `tools/tool_coderun.go` | `code_run` 与 `code_run_test` 共用请求载体 |
| `SandboxRunner` | `tools/tool_coderun.go` | 沙箱运行抽象 |
| `edit.Hunk` / `edit.Match` / `edit.MatchMode` | `internal/mcpserver/lsp/edit/*` | patch 解析、上下文匹配、宽松匹配模式 |
| `protocol.LocationResult` | `protocol/ext.go` | LSP location union 包装，带 `func_start/func_end` 扩展字段 |
| `protocol.GroupedLocationResult` | `protocol/ext.go` | `references` compact 结果结构 |
| `protocol.WorkspaceSymbolResult` / `CodeActionResult` | `protocol/ext.go` | 兼容 union 返回体 |
| `protocol.SemanticTokensResult` | `protocol/ext.go` | 原始 semantic token data + 可选 decoded token 列表字段；当前 `gopls` manager 路径只填充 `Data` / `ResultID` |

---

## 7. `internal/mcpserver/lsp` 各子包职责

| 子包 | 主要职责 | 关键文件 / 细节 |
|---|---|---|
| `tools` | MCP tool 业务入口；参数解码、action 分发、middleware 包装 | `factory.go` 定义 `decodeRaw/Lenient/Strict`、action dispatch 与 wrapper；`tool_file.go` 分发 `open_file/read_file/diagnostics`，其中 diagnostics 细节在 `tool_diagnostics.go`；`tool_edit.go` 处理 `rename/code_action/format`，`tool_edit_replace.go` + `tool_edit_support.go` 处理 `replace_range`、workspace edit 落盘、LSP 同步、回滚；`tool_coderun*.go` 接沙箱执行 |
| `manager` | LSP 能力抽象与多语言 registry | `manager.go` 定义统一接口；`registry.go` 做语言识别、installer 触发、diagnostics 聚合与 bootstrap 转发；`registry*.go` 测试覆盖多语言路由 / e2e 场景 |
| `gopls` | 通用 LSP manager / client 基座，不只服务 Go | `manager.go` 定义配置、核心状态与接口；`manager_lifecycle.go` 管 client 生命周期、workspace root、DidOpen/DidChange/DidClose；`manager_diagnostics.go` 管 diagnostics generation；`manager_symbols.go` 发送各类 LSP 请求；`manager_symbols_fallback.go` 提供 markdown/json/yaml document symbol fallback（但这些 language ID 当前未在 cmd 层注册）；`factory.go` 放 request/decode/hierarchy 泛型 helper；`gomod.go` 放 Go/JS/TS/Java project-root 和 URI/language helper；`bootstrap_doc.go` + `state.go` + `cache.go` 做文档 bootstrap、去重状态、缓存；`pool.go` + `recycler.go` 做 lease/RSS 回收；`client.go` + `transport*.go` 做 stdio LSP JSON-RPC |
| `search` | 安全路径解析、文件读取、文本搜索、AST 搜索 | `fileutil.go` 限制 root、symlink、regular file、binary、文件大小，并做语言推断；`searchutil.go` 文本搜索走本地扫描，AST 搜索走外部 `sg` CLI；`language_inference_test.go` 覆盖推断行为 |
| `edit` | `replace_range` 的补丁协议、宽松匹配与上下文生成 | `patchparse.go` 解析隐式单 hunk 与显式多 hunk；`seeksequence.go` 提供 exact / trim_right / trim_both / unicode_normalized / escape_normalized 宽松匹配；`patchmatch.go` 决定最终匹配并处理上下文约束；`replaceutil.go` 管理内容 / replacement / context 上限 |
| `format` | LSP 0-based -> 对外 1-based 显示转换，紧凑结果与函数范围增强 | `display.go` 做坐标/URI 规范化；`compact.go` 做 compact list / grouped refs；`funcrange.go` 基于 document symbol 计算 `func_start/func_end`；`render.go` 做 JSON、行号文本、compact/grouped 渲染辅助 |
| `exec` | workspace 内受限命令执行 | `sandbox.go` 约束 `work_dir` 在 root 内、建立进程组、输出截断、超时杀进程 |
| `middleware` | logging / timeout / recovery / output budget | `timeout.go` 定义 `TierFast/Normal/Slow/Exec` 和 timeout clamp；`budget.go` 控制响应大小；`logging.go` / `recovery.go` 做日志与 panic recovery |
| `protocol` | 手写 JSON-RPC codec + LSP DTO + notification 分发 | `codec.go` 构造/解码 JSON-RPC request/notification/response；`methods.go` 集中 LSP method 常量；`types.go` 与 `ext.go` 提供所需 LSP 结构和 union 包装；`notification.go` 处理 diagnostics/logMessage |
| `installer` | 语言服务器自动安装 | `installer.go` 先 `exec.LookPath`，缺失时执行安装命令，再二次校验 PATH |

---

## 8. 依赖关系（补全版）

### 8.1 `cmd/mcp-lsp` 的核心项目内依赖

| cmd 文件 | 核心直接依赖 | 作用 |
|---|---|---|
| `runtime.go` | `common` `pkg/logger` `lsp/gopls` `lsp/installer` `lsp/manager` `lsp/protocol` `platform/runner` | 构造 registry、通用 LSP manager、installer、client factory、stdio runner |
| `tools.go` | `common` `internal/mcpserver/lsp/tools` | 绑定所有 tool handler 与 manifest |
| `fx.go` | `dto/mcp` `common` `common/bootstrap` `platform/config` `platform/runner` `pkg/logger` | MCP stdio server 与 bootstrap 控制面集成，组装 runners |
| `http_runner.go` | `common` `platform/config` `platform/runner` `pkg/logger` | HTTP MCP server + peer discovery |

### 8.2 `tools` 层的间接依赖

| 工具层能力 | 实际落点 |
|---|---|
| LSP 导航 / 结构 / 编辑 / 诊断 / 补全 | `manager.Manager` + `gopls` |
| 文件读取 / 路径防护 | `search`（主要用于 `lsp_file` / `lsp_grep`） |
| 文本 / AST 搜索 | `search`（AST 依赖 `sg`） |
| `replace_range` | `edit` + `format` + `manager.Manager` 文档同步 |
| 输出格式 | `format` |
| 沙箱执行 | `exec` |
| 统一包装 | `middleware` |
| 协议类型 | `protocol` |

### 8.3 外部二进制 / 外部依赖

- LSP servers：`gopls`、`typescript-language-server`、`pyright-langserver`、`vscode-css-language-server`、`rust-analyzer`、`jdtls`
- auto-install commands：`go`、`npm`、`rustup`、`brew`
- AST 搜索：`sg`
- 代码执行：`go`、`node`、`$SHELL`（为空时 `/bin/sh`）

### 8.4 `cmd/mcp-ida` 的依赖边界

`cmd/mcp-ida` 当前只依赖控制面 / 平台层：

- `internal/dto/mcp`
- `internal/mcpserver/common/bootstrap`
- `internal/platform/config`
- `internal/platform/runner`
- `pkg/logger`
- `go.uber.org/fx`

它不在 `internal/mcpserver/lsp` 体系内，也没有本地 MCP tool server。

---

## 9. 关键结论与审查补遗

1. **`mcp-lsp` 的 cmd 层主要是装配层。**
   - schema、manifest、runner、bootstrap 都在 cmd 层；
   - 真正工具实现集中在 `internal/mcpserver/lsp/tools`。

2. **MCP tool 名称、action 列表、schema properties 已与 `tools.go` / `schema.go` 对齐。**
   - 当前 tool 总数为 9；
   - `lsp_completion` 与 `code_run_test` 没有 `action` 字段；
   - `code_run` 使用 `mode` 而不是 `action`；
   - 所有 schema 根对象都是 `additionalProperties=false`。

3. **语言服务器注册列表已按 `runtime.go` 校准。**
   - `createGenericManager()` 实际创建 6 个 manager 实例；
   - registry 实际注册 10 个 language ID；
   - `gomod/gosum/gowork` 共享 `goplsMgr`，但 installer 未配置这些 language ID，这是当前可见断点。

4. **`gopls` 包名有误导性，但实现上已经是通用多语言 LSP 基座。**
   - JS/TS、Python、CSS、Rust、Java 都通过同一套 manager/client/transport 运行；
   - 还附带 bootstrap cache、diagnostics generation、RSS recycler；
   - `ManagerPool` 当前更像 lease/recycle 支撑，而不是真正的多 manager 池，因为 `snapshotManagers()` 只返回 primary manager。

5. **`search`、`edit`、`exec` 不是零散 helper，而是三个独立子系统。**
   - `search` 负责 workspace 内安全读文件 + text/AST 搜索；
   - `edit` 负责 `replace_range` 的补丁协议、宽松匹配、上下文与落盘同步；
   - `exec` 负责 sandbox 内命令与 snippet 运行。

6. **schema 不是全部 contract。**
   - 是否拒绝未知字段，取决于 handler 的 decode 模式；
   - `lsp_edit` 实际接受 schema 未公开的 `persist_to_disk` 与 `version`；
   - `lsp_edit.rename` 默认 `persist_to_disk=true`，显式传 `false` 才会只返回 prepared `workspace_edit`；
   - `lsp_edit.code_action` 暴露了 `end_line/end_column`，但当前实现只使用 `line/column` 构造单点 range；
   - `lsp_edit.format` 当前只返回 `TextEdit`，不写磁盘。

7. **存在“潜在支持已写、但 tool 层未完全打通”的断点。**
   - `DetectLanguageID()` 能识别 `markdown/json/yaml`，`gopls/manager_symbols_fallback.go` 也有 fallback document symbol parser；
   - 但 `newManager()` 没有给这些 language ID 注册 manager，因此 MCP tool 层目前拿不到这些 fallback 能力。

8. **`mcp-ida` 目前仍是“能力占位 + 生命周期代理”。**
   - 代码中没有落地 MCP tool surface；
   - 也没有与 `internal/mcpserver/lsp` 形成直接耦合。
