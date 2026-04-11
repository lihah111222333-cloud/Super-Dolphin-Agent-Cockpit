# super-agent-v3 代码地图（03）

## MCP LSP Server 与 IDA Server

> 本文按当前工作树重新审查并修订，实际对照了以下源码：
>
> - `cmd/mcp-lsp/*.go`
> - `cmd/mcp-ida/*.go`
> - `internal/mcpserver/lsp/manager/*.go`
> - `internal/mcpserver/lsp/gopls/*.go`
> - `internal/mcpserver/lsp/tools/*.go`
> - `internal/mcpserver/lsp/search/*.go`
> - `internal/mcpserver/lsp/edit/*.go`
> - `internal/mcpserver/lsp/format/*.go`
> - `internal/mcpserver/lsp/exec/*.go`
> - `internal/mcpserver/lsp/middleware/*.go`
> - `internal/mcpserver/lsp/protocol/*.go`
> - `internal/mcpserver/lsp/installer/*.go`

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
   - `registryToolProvider` 同时被 MCP server 和 bootstrap `OnToolsList` / `OnToolsCall` 复用。

4. **LSP/执行实现层**
   - 真正工具语义落在 `internal/mcpserver/lsp/tools`；
   - 语言服务抽象在 `internal/mcpserver/lsp/manager`；
   - 底层多语言 LSP client/manager 基座在 `internal/mcpserver/lsp/gopls`；
   - 搜索、补丁匹配、显示整形、沙箱执行分别在 `search`、`edit`、`format`、`exec` 子包。

**一句话总结：**`mcp-lsp` 是项目里的多语言代码理解 / 编辑 / 搜索 / 沙箱执行 MCP 侧车。

### 1.2 `mcp-ida`（`cmd/mcp-ida`）

`mcp-ida` 在当前源码范围内仍是一个 **bootstrap/lifecycle 进程**，而不是完整 tool server：

- capability 固定为 `tools/ida`；
- 只装配 `bootstrapRunner`；
- 响应配置变更、关闭请求、退出 final report；
- 没有 `common.Server` / `common.HTTPServer`；
- 没有 tool manifest、schema、handler 映射；
- 没有对 `internal/mcpserver/lsp` 的直接依赖。

另外，`mcp-lsp` 的 bootstrap 注册还要求 `GO_AGENT_PEER_MODE=1`，而 `mcp-ida` 只要 `RPCAddr` 有值就会尝试启动 bootstrap client，这一点与 `mcp-lsp` 不同。

---

## 2. 目录结构（按职责分组）

```text
cmd/
├── mcp-lsp/
│   ├── main.go
│   ├── fx.go
│   ├── runtime.go
│   ├── http_runner.go
│   ├── schema.go
│   └── tools.go
└── mcp-ida/
    ├── main.go
    └── fx.go

internal/mcpserver/lsp/
├── manager/
│   ├── manager.go
│   └── registry.go
├── gopls/
│   ├── manager.go
│   ├── manager_lifecycle.go
│   ├── manager_diagnostics.go
│   ├── manager_symbols.go
│   ├── manager_symbols_fallback.go
│   ├── bootstrap_doc.go
│   ├── state.go
│   ├── cache.go
│   ├── pool.go
│   ├── recycler.go
│   ├── gomod.go
│   ├── factory.go
│   ├── client.go
│   ├── transport.go
│   └── transport_conn.go
├── tools/
│   ├── factory.go
│   ├── tool_file.go
│   ├── tool_diagnostics.go
│   ├── tool_inspect.go
│   ├── tool_xref.go
│   ├── tool_grep.go
│   ├── tool_structure.go
│   ├── tool_edit.go
│   ├── tool_edit_replace.go
│   ├── tool_edit_support.go
│   ├── tool_completion.go
│   ├── tool_coderun.go
│   └── tool_coderuntest.go
├── search/
│   ├── fileutil.go
│   └── searchutil.go
├── edit/
│   ├── patchparse.go
│   ├── patchmatch.go
│   ├── replaceutil.go
│   └── seeksequence.go
├── format/
│   ├── compact.go
│   ├── display.go
│   ├── funcrange.go
│   └── render.go
├── exec/
│   └── sandbox.go
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
└── installer/
    └── installer.go
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
  - 构造 `OnToolsList` / `OnToolsCall`，与 toolbridge/控制面复用同一套 tool definitions；
  - runner 共有 3 个：bootstrap、stdio、http；
  - `bindRuntime()` 用 `platformrunner.RunGroup()` 托管所有 runner 生命周期。

- `runtime.go`
  - `newManager()` 获取工作目录并创建 installer + `manager.NewRegistry()`；
  - 注册语言 ID：`go`、`gomod`、`gosum`、`gowork`、`javascript`、`typescript`、`python`、`css`、`rust`、`java`；
  - `createGenericManager()` 虽然调用的是 `gopls.NewManager()`，但通过切换 binary/args 复用成通用 LSP manager；
  - `newStdioRunner()` 在退出时负责关闭 registry/manager。

- `http_runner.go`
  - 仅在 `GO_AGENT_PEER_MODE=1` 时启动；
  - 启动本地环回地址 HTTP MCP server，并写 peer discovery；
  - 非 peer mode 时返回 `lspBlockRunner`，只阻塞等取消。

- `schema.go`
  - 定义 9 个工具的输入 schema；
  - 所有 schema 根对象统一为 `type=object`、`additionalProperties=false`；
  - 但这只是“声明层约束”，真正严格程度还取决于各 handler 的 decode 模式。

- `tools.go`
  - 维护完整 tool manifest 列表；
  - `newToolHandlers()` 绑定到 `internal/mcpserver/lsp/tools`；
  - 缺 handler 时会回退到 `stubToolHandler`。

### 3.2 `cmd/mcp-ida`

- `main.go`
  - 极简入口，失败直接打印到 `stderr` 并退出。

- `fx.go`
  - 只装配 bootstrap client 与 bootstrap runner；
  - capability 为 `tools/ida`；
  - 有 `FinalReport`、`OnConfigChanged`、`OnShutdown`；
  - 没有 stdio/http server，没有 tools/list/tools/call。

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
              │   └─ createGenericManager(...) x N
              ├─ newToolHandlers
              ├─ newServer
              ├─ newBootstrapRunner
              ├─ newStdioRunner
              └─ newHTTPRunner
          └─ bindRuntime
              └─ platformrunner.RunGroup(runners)
```

### 4.2 `mcp-lsp` 工具调用链

```text
tools/call
  └─ registryToolProvider.CallTool
      └─ handleToolCall
          └─ ToolHandlers[name]
              └─ internal/mcpserver/lsp/tools handler
                  ├─ manager.Registry.GetManagerForFile/GetManagerForLanguage
                  ├─ manager.Manager.*                    # hover/rename/symbol/... 
                  ├─ search.SearchText/SearchAST          # lsp_grep
                  ├─ edit.Parse/MatchContext/...          # replace_range
                  ├─ exec.Sandbox.Run                     # code_run / code_run_test
                  ├─ format.NormalizeForDisplay/...       # 输出整形
                  └─ middleware.Timeout/Logging/Recovery  # 通用包装
```

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

## 5. 工具清单与 schema（按实际实现校准）

### 5.1 完整工具清单

| Tool | 主要 action / 模式 | 对应 handler |
|---|---|---|
| `lsp_file` | `open_file` / `read_file` / `diagnostics` | `tools.NewFileHandler` |
| `lsp_inspect` | `hover` / `definition` / `implementation` / `type_definition` / `signature_help` | `tools.NewInspectHandler` |
| `lsp_xref` | `references` / `call_hierarchy` / `type_hierarchy` | `tools.NewXRefHandler` |
| `lsp_grep` | `text_search` / `ast_search` | `tools.NewGrepHandler` |
| `lsp_structure` | `document_symbol` / `workspace_symbol` / `folding_range` / `semantic_tokens` | `tools.NewStructureHandler` |
| `lsp_edit` | `rename` / `code_action` / `format` / `replace_range` | `tools.NewEditHandler` |
| `lsp_completion` | 单点补全 | `tools.NewCompletionHandler` |
| `code_run` | `run` / `project_cmd` | `tools.NewCodeRunHandler` |
| `code_run_test` | Go 单测函数执行 | `tools.NewCodeRunTestHandler` |

### 5.2 schema 摘要与实现约束

| Tool | schema 必填 | 关键字段 | 实现层补充约束 |
|---|---|---|---|
| `lsp_file` | `action` | `file_path` `file_paths` `offset` `limit` | `read_file` 单文件返回带行号文本；批量最多 10 个文件；单文件读入上限 2 MiB；批量 JSON payload 会压缩到 16 KiB 以内；`open_file` 只有在存在对应 manager 时才会额外 `DidOpen`；路径必须在 workspace root 内、不能是 symlink/binary；`diagnostics` 不传目标时会取所有 manager 的 diagnostics |
| `lsp_inspect` | `action,file_path,line,column` | 单点位置参数 | 严格解码；行列必须是 1-based 正整数 |
| `lsp_xref` | `action,file_path,line,column` | `direction` `include_declaration` `verbosity` `max_results` | `direction` 仅对层级查询生效；`include_declaration` 仅对 `references` 生效；`verbosity` 目前只影响 `references` |
| `lsp_grep` | `action` | `query` `path` `glob` `language` `regex` `case_sensitive` `max_results` | `text_search` 走本地扫描；`ast_search` 依赖 `sg` CLI；语言可由 `path/glob` 推断 |
| `lsp_structure` | `action` | `file_path` `query` `language` `verbosity` `max_results` | `workspace_symbol` 要求 `file_path` 与 `language` 二选一；`verbosity` 只影响 `workspace_symbol`；其余 action 需要可解析的 `file_path` |
| `lsp_edit` | `action,file_path` | `line` `column` `end_line` `end_column` `patch` `edits` `new_name` `new_text` `only` | `rename` 需要 `new_name`；`code_action` 当前只按单点位置查询，未使用 `end_line/end_column`；`replace_range` 需要 `patch/edits/new_text` 之一；`rename` 默认落盘；handler 还接受 schema 未公开的 `persist_to_disk`、`version` |
| `lsp_completion` | `file_path,line,column` | `verbosity` `max_results` | 严格解码；compact/full 两种输出 |
| `code_run` | `mode` | `language` `code` `command` `auto_wrap` `work_dir` `timeout` | `run` 仅支持 Go / JS / TS；Go 默认自动补 `package main` 和 stdlib import；`project_cmd` 走 `$SHELL -lc`; `work_dir` 必须留在 sandbox root 内 |
| `code_run_test` | `test_func` | `test_pkg` `timeout` | `test_func` 必须匹配安全正则；执行 `go test -run ^TestName$ <pkg>`（默认包为 `./...`） |

### 5.3 decode / 校验策略

| 解码模式 | 工具 | 含义 |
|---|---|---|
| `decodeStrict` | `lsp_inspect` `lsp_xref` `lsp_structure` `lsp_completion` | 拒绝未知字段 |
| `decodeLenient` | `lsp_file` `lsp_grep` | `null/空参数` 会归一成 `{}`，未知字段按 Go 默认忽略 |
| `decodeRaw` | `lsp_edit` `code_run` `code_run_test` | 直接反序列化，允许 schema 之外字段进入 handler |

**补充：**`lsp_file` 与 `lsp_grep` 还额外套了 output budget；超出时会返回截断 envelope，而不是完整原始 payload。

### 5.4 `mcp-ida` 的工具面现状

当前 `cmd/mcp-ida` 没有 tool manifest，也没有 `tools/list` / `tools/call` 注册，因此仍应视为 bootstrap/lifecycle 代理，而不是独立 MCP tool server。

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
  - 导航：`Definition` `Implementation` `TypeDefinition` `Hover` `SignatureHelp`
  - 交叉引用：`References` `CallHierarchy` `TypeHierarchy`
  - 结构：`DocumentSymbol` `WorkspaceSymbol` `FoldingRange` `SemanticTokens`
  - 编辑：`Completion` `Rename` `CodeAction` `Format`
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
| `protocol.SemanticTokensResult` | `protocol/ext.go` | 原始 token data + 解码后 token 列表 |

---

## 7. `internal/mcpserver/lsp` 各子包职责

| 子包 | 主要职责 | 关键文件 / 细节 |
|---|---|---|
| `tools` | MCP tool 业务入口；参数解码、action 分发、middleware 包装 | `factory.go` 定义 `decodeRaw/Lenient/Strict`；各 `tool_*.go` 实现具体动作；`tool_edit_replace.go` 和 `tool_edit_support.go` 负责 workspace edit 落盘与 LSP 同步 |
| `manager` | LSP 能力抽象与多语言 registry | `manager.go` 定义统一接口；`registry.go` 做语言识别、installer 触发、diagnostics 聚合 |
| `gopls` | 通用 LSP manager/client 基座，不只服务 Go | `manager*.go` 做 LSP 请求与 workspace/client 生命周期；`bootstrap_doc.go` + `state.go` + `cache.go` 做文档 bootstrap；`pool.go` + `recycler.go` 做 lease/RSS 回收；`client.go` + `transport*.go` 做 stdio JSON-RPC |
| `search` | 安全路径解析、文件读取、文本搜索、AST 搜索 | `fileutil.go` 限制 root、symlink、binary、文件大小；`searchutil.go` 文本搜索走本地扫描，AST 搜索走 `sg` CLI |
| `edit` | `replace_range` 的补丁协议、宽松匹配与上下文生成 | `patchparse.go` 解析单/多 hunk；`seeksequence.go` 提供 exact/trim/unicode/escape 宽松匹配；`patchmatch.go` 决定最终匹配；`replaceutil.go` 管理字节/上下文上限 |
| `format` | LSP 0-based -> 对外 1-based 显示转换，紧凑结果与函数范围增强 | `display.go` 做坐标/URI 规范化；`compact.go` 做 compact list / grouped refs；`funcrange.go` 基于 document symbol 计算 `func_start/func_end` |
| `exec` | workspace 内受限命令执行 | `sandbox.go` 约束 `work_dir` 在 root 内、建立进程组、输出截断、超时杀进程 |
| `middleware` | logging / timeout / recovery / output budget | `timeout.go` 定义 `TierFast/Normal/Slow/Exec`；`budget.go` 控制响应大小 |
| `protocol` | 手写 JSON-RPC codec + LSP DTO + notification 分发 | `codec.go` 构造/解码 request/response；`types.go` 与 `ext.go` 提供所需 LSP 结构；`notification.go` 处理 diagnostics/logMessage |
| `installer` | 语言服务器自动安装 | `installer.go` 先 `LookPath`，缺失时执行安装命令，再二次校验 PATH |

---

## 8. 依赖关系（补全版）

### 8.1 `cmd/mcp-lsp` 的直接依赖

| cmd 文件 | 直接依赖 | 作用 |
|---|---|---|
| `runtime.go` | `manager` `gopls` `installer` `protocol` | 构造 registry、通用 LSP manager、installer、client factory |
| `tools.go` | `internal/mcpserver/lsp/tools` | 绑定所有 tool handler |
| `fx.go` | `common` `common/bootstrap` | MCP server 与 bootstrap 控制面集成 |
| `http_runner.go` | `common` | HTTP MCP server + peer discovery |

### 8.2 `tools` 层的间接依赖

| 工具层能力 | 实际落点 |
|---|---|
| LSP 导航/结构/编辑 | `manager.Manager` + `gopls` |
| 文件读取/路径防护 | `search` |
| 文本/AST 搜索 | `search`（AST 依赖 `sg`） |
| replace_range | `edit` + `format` |
| 输出格式 | `format` |
| 沙箱执行 | `exec` |
| 统一包装 | `middleware` |
| 协议类型 | `protocol` |

### 8.3 外部二进制 / 外部依赖

- LSP servers：`gopls`、`typescript-language-server`、`pyright-langserver`、`vscode-css-language-server`、`rust-analyzer`、`jdtls`
- auto-install commands：`go`、`npm`、`rustup`、`brew`
- AST 搜索：`sg`
- 代码执行：`go`、`node`、当前 shell

### 8.4 `cmd/mcp-ida` 的依赖边界

`cmd/mcp-ida` 当前只依赖：

- `internal/mcpserver/common/bootstrap`
- `internal/platform/config`
- `internal/platform/runner`
- `pkg/logger`

它不在 `internal/mcpserver/lsp` 体系内。

---

## 9. 关键结论

1. **`mcp-lsp` 的 cmd 层主要是装配层。**
   - schema、manifest、runner、bootstrap 都在 cmd 层；
   - 真正工具实现集中在 `internal/mcpserver/lsp/tools`。

2. **`gopls` 包名有误导性，但实现上已经是通用多语言 LSP 基座。**
   - JS/TS、Python、CSS、Rust、Java 都通过同一套 manager/client/transport 运行；
   - 还附带 bootstrap cache、diagnostics generation、RSS recycler。

3. **`search`、`edit`、`exec` 不是零散 helper，而是三个独立子系统。**
   - `search` 负责 workspace 内安全读文件 + text/AST 搜索；
   - `edit` 负责 `replace_range` 的补丁协议和宽松匹配；
   - `exec` 负责 sandbox 内命令与 snippet 运行。

4. **`mcp-ida` 目前仍是“能力占位 + 生命周期代理”。**
   - 代码中没有落地 MCP tool surface；
   - 也没有与 `internal/mcpserver/lsp` 形成直接耦合。

5. **schema 不是全部 contract。**
   - 是否严格拒绝未知字段，取决于 handler 的 decode 模式；
   - 多个关键行为约束是在 handler 中补充的，而不是写在 schema 里。

## 审查补遗

1. **原文遗漏了 `manager.Manager` / `manager.Registry` 的若干关键方法。**
   - `Manager` 还包含 `BootstrapDocumentOpenOnly`、`CurrentDiagnosticGeneration`、`AdvanceDiagnosticGeneration`；
   - `Registry` 还包含 `CurrentDiagnosticGeneration`。

2. **`lsp_file` 的实现约束比原文更严格。**
   - 单文件读取上限 2 MiB；
   - 批量最多 10 个文件；
   - 批量响应会进一步压缩到 16 KiB 以内；
   - 所有路径都受 workspace root、regular file、非 symlink、非 binary 限制。

3. **`lsp_edit.code_action` 的 range 语义在原文里写重了。**
   - schema 虽然暴露了 `end_line` / `end_column`；
   - 但当前实现只使用 `line` + `column` 构造单点 range，`end_line` / `end_column` 实际未被消费。

4. **`verbosity` 不是所有 action 都真的会用。**
   - `lsp_xref` 中，`verbosity` 目前只影响 `references`，`call_hierarchy` / `type_hierarchy` 忽略它；
   - `lsp_structure` 中，`verbosity` 目前只影响 `workspace_symbol`。

5. **`lsp_edit` 的 handler 能力强于公开 schema。**
   - handler 实际接受 `persist_to_disk` 与 `version`；
   - `rename` 默认 `persist_to_disk=true`，也就是默认直接落盘；
   - 只有显式传 `persist_to_disk=false` 才会返回 prepared `workspace_edit` 而不写磁盘。

6. **原文未覆盖 `gopls` 的 bootstrap/cache/recycler 子系统。**
   - `bootstrap_doc.go`、`state.go`、`cache.go`、`pool.go`、`recycler.go` 共同实现了文档 bootstrap、去重状态、缓存、lease 跟踪和 RSS 回收；
   - 这部分不是小优化，而是 runtime 稳定性的一部分。

7. **源码里存在两个“潜在支持已写、但 tool 层未完全打通”的断点。**
   - `registry` 注册了 `gomod/gosum/gowork`，但 installer 只注册了 `go`；当前 `GetManagerForFile(go.mod/go.sum/go.work)` 会先触发 installer 并报 `no installer config found for language: gomod/gosum/gowork`；
   - `DetectLanguageID()` 能识别 `markdown/json/yaml`，`gopls/manager_symbols_fallback.go` 也有 fallback document symbol parser，但 `newManager()` 没有给这些 language ID 注册 manager，因此 tool 层目前拿不到这些 fallback 能力。

8. **`lsp_grep.ast_search` 不是纯 Go 内实现。**
   - 它依赖外部 `sg` CLI；
   - 另外 `code_run` / `code_run_test` 还依赖 `go`、`node` 和本地 shell。

9. **`ManagerPool` 当前更像 lease/recycle 支撑，而不是真正的多 manager 池。**
   - `PoolSizeFromEnv()`、`size` 等字段已经存在；
   - 但当前 `snapshotManagers()` 只返回 primary manager，实际并没有创建多个并行 manager 实例。
