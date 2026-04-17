# 06 MCP Server 框架层代码地图

> 阅读范围：`internal/mcpserver/common/` 与 `cmd/mcp-lsp/` 下 **68 个生产源码文件**（不含 `_test.go`）。
> 本次审查按迁移后布局反向核对了 68 个生产 `.go` 文件：`common` 4 个、`common/bootstrap` 7 个、`cmd/mcp-lsp` 根目录 6 个、`cmd/mcp-lsp` 各子包 51 个；重点补齐了 `common` 层抽象、`bootstrap` 生命周期与公开 API、`cmd/mcp-lsp/gopls` 子包职责、中间件真实挂载顺序，以及各工具的实现细节遗漏。

---

## 1. 模块概述：MCP Server 框架整体架构

这一层实际由三条主线组成：

1. **MCP 对外服务层（`common`）**
   - 对外暴露最小 MCP 方法集：`initialize`、`notifications/initialized`、`tools/list`、`tools/call`、`ping`、`shutdown`、`exit`。
   - 有两套入口：
     - `common.Server`：stdio 模式，绑定 `*StdioTransport`
     - `common.HTTPServer`：HTTP 模式，`POST /mcp`
   - **稳定抽象点只有 `ToolProvider`**；`common` 层并没有再抽象出统一 `Transport` interface。
   - `tools/call` 的真实返回值会先被 `json.Marshal`，再包装成 MCP `content[].text` 字符串返回。

2. **控制面生命周期层（`common/bootstrap`）**
   - MCP 子进程通过 TCP + `jrpc2` 反连 core/control plane。
   - 生命周期不只含 `register/heartbeat/reconnect`，还包括：
     - `Context()` 上下文查询
     - `EmitEvent()` 审计事件上报
     - `Log()` 生命周期日志上报
     - `RequestApproval()` 审批请求
     - `Report()` 进度/完成/诊断上报
     - `SubscribeHooks()` / `ResolveHook()` / `PendingHooks()`
     - `OnToolsList` / `OnToolsCall` 反向回调
   - 断线后可重连，并在恢复后 **flush 报告队列 + replay hook 订阅**。

3. **LSP 工具能力层（`cmd/mcp-lsp`）**
   - `tools/`：MCP tool 入口与参数/结果整形。
   - `manager/`：按语言或文件路由 manager。
   - `gopls/`：虽然名字保留了历史包名，但现在是**通用 LSP 子进程客户端/管理骨架**。
   - `search/`、`edit/`、`format/`、`exec/`、`middleware/`、`protocol/`：分别承担搜索、写入、展示、受控执行、中间件和协议模型。
   - `gopls` 包内对 `markdown/json/yaml` 有 fallback-only 分支（主要是 `DocumentSymbol` 解析，部分请求返回空结果而不启动 LSP）；但 `cmd/mcp-lsp/runtime.go` 当前没有把这些语言注册成独立 manager，只有在装配层显式路由到该 manager 时才会触发这些 fallback。

### 1.1 核心调用链

```text
MCP Client
  -> common.Server / common.HTTPServer
  -> ToolProvider
  -> cmd/mcp-lsp/tools/* handler
  -> manager.Registry
  -> 对应语言的 manager（大多数由 gopls.manager 承担）
  -> gopls.transport
  -> LSP Server 子进程（gopls / tsserver / pyright / jdtls ...）
```

### 1.2 Bootstrap 生命周期调用链

```text
MCP 子进程
  -> bootstrap.ReadBootConfig / normalizeConfig
  -> bootstrap.Client.Start
  -> TCP + jrpc2 register
  -> heartbeat / context / event / log / approval / report / hooks
  -> disconnect -> reconnectLoop
  -> flushQueuedReports + replayHookSubscriptions
```

---

## 2. 目录结构：每个子包 / 文件职责

## 2.1 `internal/mcpserver/common/`

- `server.go`
  - stdio 版 MCP Server 主体。
  - 定义 `ToolProvider`、`MCPTool`、JSON-RPC request/response/error 结构。
  - `Server` 直接持有 `*StdioTransport`，没有再抽象统一 transport 接口。
  - 只处理最小 MCP 方法集；未知通知静默忽略，未知 request 返回 `method not found`。
  - `initialize` 会回显请求里的 `protocolVersion`；若为空则默认 `2024-11-05`。
  - `tools/call` 会把 provider 返回值序列化成 JSON 字符串，再包装为 MCP `text` content。
- `stdio.go`
  - `StdioTransport`。
  - 会跳过前导空白后探测输入模式：
    - `{` / `[` 开头：raw JSON
    - 其它：`Content-Length` framed JSON-RPC
  - 输出会沿用探测到的模式；写路径有互斥锁。
  - `StdioTransport` 本身没有 `Flush()` 方法；`WriteMessage()` 会在底层 writer 实现 `Flush() error` 时调用它。
- `http_transport.go`
  - `HTTPServer`，通过 `POST /mcp` 提供 HTTP 入口。
  - 单次请求体限制 10MB；单个 HTTP request 只处理一个 JSON-RPC envelope。
  - 通知请求返回 `202 Accepted`。
  - dispatch 逻辑与 `Server` 基本平行实现，而不是共享一套 transport 抽象。
- `discovery.go`
  - 用 `/tmp/super-agent-mcp-{binary}-{parentPID}.port` 做 peer HTTP 发现。
  - 写文件采用 `tmp + rename`，是原子更新。
  - 除 `WriteDiscoveryFile/ReadDiscoveryAddr/CleanupDiscoveryFile` 外，还提供：
    - `WritePeerDiscovery()` / `CleanupPeerDiscovery()`（按当前进程父 PID 处理）
    - `DiscoverPeerHTTPAddr()`
    - `IsValidHTTPAddr()`
- `server.go`（补记）
  - `ToolProvider` / `MCPTool` 以及 JSON-RPC envelope 类型也定义在这里；`common/` 下不存在独立 `manifest.go`。

### 2.1.1 `common/bootstrap/`

- `client.go`
  - `bootstrap.Client` 与 `Config` 定义。
  - 对外暴露构造函数 `New()`，以及核心方法 `Start / Context / EmitEvent / Log / RequestApproval / Report / Close`。
  - hook 相关公开方法 `SubscribeHooks / ResolveHook / PendingHooks` 定义在 `hooks.go`，不是 `client.go` 文件本体。
  - `Config` 还提供 `FinalReport / OnShutdown / OnConfigChanged / OnToolsList / OnToolsCall / Hooks` 等注入点。
  - 内部维护：lease、当前 `jrpc2.Client`、重连状态、心跳/日志序号、协商后的能力集合、config version、resume generation、boot snapshot、hook 状态、report queue。
  - `AgentID` 允许为空；空值意味着该 MCP 进程可工作在 shared service 模式。
- `env.go`
  - 读取 `GO_AGENT_CTL_*` 环境变量；`RPCAddr` 兼容旧的 `RPC_ADDR`，其它 bootstrap 字段兼容对应 `GO_AGENT_MCP_*` 键（如 `GO_AGENT_MCP_INSTANCE_ID`、`GO_AGENT_MCP_BOOT_CONTEXT`）。
  - 解析 `BootSnapshot`，补齐 `InstanceID / BootID / BinaryName / ClientKind`。
  - `Context()` 在断连时会退化到 `envContext()`，按 scope 组装 boot snapshot 视图。
  - 旧环境变量会打印弃用警告，代码里写死了迁移截止提示 `2026-06-30`。
- `lifecycle.go`
  - `beginStart()`、`dial()`、`registerConn()`、`activateLocked()` 等启动流程。
  - 传输层是 **TCP + `jrpc2/channel.Line`**。
  - `handleCallback()` 会把 `tools/list` / `tools/call` 转发给 `Config.OnToolsList / OnToolsCall`。
  - `dispatchRequest()` 只识别 lifecycle 侧的 `shutdown` / `config_changed`。
- `heartbeat.go`
  - 心跳循环、抖动等待、失败告警、下一次心跳间隔更新。
  - lease 被 core 判为 stale/not found 时，会走 `refreshLease()` 在**同一连接**上重新 register。
  - 心跳 metrics 会携带 `queued_reports` 与 `client_kind`。
- `hooks.go`
  - 定义 `HookBeforeHandler / HookCheckHandler / HookAfterHandler` 与 `HookConfig`。
  - 缺省 handler 的默认决策不是空操作：
    - `before` 默认 `deny`
    - `check` 默认 `continue`
    - `after` 默认 `reject`
  - 提供 `SubscribeHooks / ResolveHook / PendingHooks / replayHookSubscriptions`。
  - `SubscribeHooks()` 断连时会缓存订阅参数但返回 unavailable 错误；`PendingHooks()` 需要 `AgentID`（来自 `Config` 或 boot snapshot）。
  - 订阅参数会缓存到 `hookState`，重连后最多重放 3 次。
- `reconnect.go`
  - `handleStop()` 触发断线标记。
  - `reconnectLoop()` 做指数退避重连，最大退避 30s。
  - 重连成功后会重新激活连接、flush 队列、重放 hook 订阅。
- `report_queue.go`
  - **离线 report queue 是“有界内存队列”，不是磁盘 durable queue。**
  - 以 `report_id` 去重覆盖；默认上限 128。
  - `Config.ReportQueueLimit` 可覆盖默认值；`New()` 只保证小于 1 时回落默认值，并没有额外硬性最大值。
  - 连接恢复时补发；`Close()` 时还可发送 `FinalReport()`。
  - 断网时 `Report()` 返回 `queued_offline` 响应。

## 2.2 `cmd/mcp-lsp/`

> 迁移后 `cmd/mcp-lsp/` 下生产源码分布为：根目录 6 个入口文件，子包合计 51 个文件；其中子包分布为 `edit` 4、`exec` 1、`format` 4、`gopls` 15、`installer` 1、`manager` 2、`middleware` 4、`protocol` 5、`search` 2、`tools` 13；以下逐项覆盖。

### 2.2.0 入口层（`cmd/mcp-lsp/*.go`）
- `main.go`（36 行，入口）
  - 进程入口；限制 `GOMAXPROCS`、重定向 MCP stdout/stderr，并把 `run()` 的错误转换为退出码。
- `fx.go`（237 行，DI 组装）
  - Fx 装配根文件；注册 bootstrap / stdio / HTTP runners，并把 tool manifest 通过 `registryToolProvider` 暴露给 `common` 层。
- `runtime.go`（172 行，生命周期）
  - 运行时资源装配；构建多语言 manager registry、installer 与 stdio runner，并在退出时统一关闭 manager。
- `http_runner.go`（58 行，HTTP transport runner）
  - peer mode 下启动 HTTP MCP server，写入/清理 discovery 文件，并处理优雅停机。
- `schema.go`（138 行，工具 schema 注册）
  - 集中定义 9 个 MCP tool 的 input schema 与字段 helper。
- `tools.go`（87 行，tool handler 绑定）
  - 声明 tool manifest 列表，并把 cmd 层 handler 名称映射到 `cmd/mcp-lsp/tools` 的具体实现。

### `edit/`：补丁与 replace_range 算法层
- `patchparse.go`
  - 解析单/多 hunk patch，限制 patch 尺寸、行数、hunk 数。
- `patchmatch.go`
  - 用上下文去匹配 patch hunk 在当前文件中的真实落点，输出 `resolved offsets / matched_by / edit_context`。
- `replaceutil.go`
  - `replace_range` 的内容大小保护、offset/line 映射、编辑上下文构造。
  - 定义：
    - 内容上限 4MB
    - 替换内容上限 256KB
    - 超过 2MB 走 large-content bypass
    - 最多 20 个 edits
- `seeksequence.go`
  - `SeekSequence()` 是 5-pass **按行序列**匹配器：`exact / trim_right / trim_both / unicode_normalized / escape_normalized`。
  - `substring_exact` 不是 `seeksequence.go` 的一档；它是 `patchmatch.go` 在行序列匹配失败后的 raw substring fallback。

### `exec/`：受控命令执行
- `sandbox.go`
  - `Sandbox` 限定 root 目录，并校验 `work_dir` 必须留在 root 内。
  - 统一收集 stdout/stderr，默认输出上限 256KB。
  - 超时后会 kill 整个进程组（`Setpgid + SIGKILL`）。
  - `ShellRequest()` 默认用 `$SHELL -lc`，无环境变量时退化到 `/bin/sh -lc`。

### `format/`：结果展示与坐标转换
- `compact.go`
  - `compact/full` 结果模式与默认紧凑上限：
    - references 30
    - completion 20
    - workspace symbol 20
  - 定义 `CompactList`、紧凑 completion/workspace symbol 结构。
- `display.go`
  - 把协议内部 0-based 坐标统一转成对外 1-based。
  - 把 `file://` URI 转成相对路径显示。
- `funcrange.go`
  - 提供 `FindEnclosingFunction()`、`ResolveEnclosingFunctionRange()`。
  - 为 grep/xref 结果补 `func_start / func_end`。
  - 也负责 `AbsolutePathFromURI()`。
- `render.go`
  - JSON pretty render、带行号文本 render、grouped location render。
  - `NormalizeForDisplay()` 用反射统一处理多种 LSP 返回结构。

### `gopls/`：通用 LSP 子进程管理骨架
- `client.go`
  - `Client` 接口与默认实现。
  - 负责 `initialize/shutdown/request/notify/didOpen/didChange/didClose`。
  - 组装 client capabilities、workspace folders、默认 init options。
- `transport.go`
  - 子进程 JSON-RPC transport。
  - 维护 pending request map、自增 request id。
  - 区分 response / notification / server request。
  - 为常见 server request 提供默认应答，如：
    - `workspace/configuration`
    - `client/registerCapability`
    - `client/unregisterCapability`
    - `window/workDoneProgress/create`
    - `workspace/semanticTokens/refresh`
    - `workspace/codeLens/refresh`
    - `workspace/inlayHint/refresh`
    - `workspace/diagnostic/refresh`
- `transport_conn.go`
  - 启动子进程、读写 `Content-Length` framed 消息、收集 stderr（8KB ring buffer）、关闭/kill/等待退出。
- `manager.go`
  - `manager` 主结构：workspace root、workspace->client 映射、diagnostics generation、logger、pool。
  - 构造时会规范化 root，并初始化 `ManagerPool`。
- `factory.go`
  - 这里不是“工厂模式注册器”，而是 `gopls` 包内部的**泛型公共管线**。
  - 提供 `requestDocument()`、`queryHierarchy()`、union decode、cache persistence、bootstrap sync 辅助函数。
  - 也是 `bootstrap_doc.go / cache.go / manager_symbols.go` 的共享胶水层。
- `manager_lifecycle.go`
  - `EnsureClient()`、client 创建/复用/关闭。
  - 按 file/language 解析 workspace root。
  - JS/TS 与 Java 在仅按 language 建 client 时，会主动打开首个源文件，为 tsserver/jdtls 建立项目上下文。
- `manager_symbols.go`
  - definition / implementation / typeDefinition / hover / signatureHelp / references / callHierarchy / typeHierarchy / documentSymbol / workspaceSymbol / foldingRange / semanticTokens / completion / rename / codeAction / format 的 LSP 请求封装。
  - `locationQuery()` 还会调用 `format.EnrichLocationResultsWithFuncRange()`。
- `manager_diagnostics.go`
  - diagnostics snapshot 缓存、generation 隔离、稳定等待逻辑。
  - `AdvanceDiagnosticGeneration()` 会清空旧快照。
- `manager_symbols_fallback.go`
  - markdown/json/yaml 的 document symbol fallback；这些语言被路由到此 manager 时无需启动 LSP 进程。
- `bootstrap_doc.go`
  - 文档 bootstrap 协调器。
  - 读取磁盘快照并做 `DidOpen/DidChange` 同步。
  - 可刷新同 workspace 已缓存文档；对 Go 还会预热同目录兄弟 `.go` 文件。
  - 支撑 `BootstrapDocument()` 与 `BootstrapDocumentOpenOnly()` 两条路径。
- `cache.go`
  - `lspCacheStore`：文档 bootstrap cache。
  - 默认 TTL 7 天。
  - 可选持久化：
    - `AGENT_LSP_CACHE_PERSISTENT=1`
    - `AGENT_LSP_CACHE_DIR`
  - 持久化不可用时会自动回退到内存模式。
- `state.go`
  - per-workspace / per-URI bootstrap 状态机：
    - `pending / bootstrapping / ready / stale / error`
  - 决定当前文档是 `skip / wait / run`。
- `gomod.go`
  - 不是只处理 Go：
    - Go workspace root 探测
    - JS/TS project marker 探测与 bootstrap file 搜索
    - Java project marker 探测与 bootstrap file 搜索
  - 同时定义：
    - `shouldUseClientForLanguage()`
    - `fileURIFromPath()`
    - `absolutePathFromURI()`
- `pool.go`
  - `ManagerPool` 会跟踪 client lease 计数并启动 recycler。
  - `AGENT_LSP_POOL_SIZE` 控制 size（默认 10，最大 20），但当前 `snapshotManagers()` 只返回 primary manager，说明多 shard 仍处于预留态。
- `recycler.go`
  - 周期性检查 client RSS，超过阈值（默认 768MB，`AGENT_LSP_RSS_LIMIT_MB` 可调）时回收空闲 client。
  - 回收后会重新 ensure client，并恢复 workspace bootstrap 状态。
  - `recycleWorkspaceClient()` 的重建路径当前把 `workspaceConfig.languageID` 写死成 `"go"`，仍带明显 Go-centric 痕迹。

### `installer/`：LSP 安装器
- `installer.go`
  - `Provider` 维护语言 -> 安装配置映射。
  - `EnsureInstalled()`：
    1. 先 `LookPath`
    2. 若不存在则执行安装命令
    3. 再次校验 binary 是否已进入 PATH

### `manager/`：语言路由层
- `manager.go`
  - 定义对工具层暴露的统一 `Manager` 接口。
- `registry.go`
  - `dynamicRegistry` 按文件扩展名/基础名识别语言。
  - `GetManagerForFile/GetManagerForLanguage` 在返回 manager 前可触发 installer。
  - 聚合 diagnostics，并把 URI 按 manager 分组。
  - `BootstrapDocument()` 是显式 bootstrap 入口。

### `middleware/`：工具调用中间件
- `logging.go`
  - `Handler` / `Middleware` / `Chain()`。
  - 记录压缩后的请求体、响应摘要、耗时。
- `recovery.go`
  - panic recovery，并打印 stack。
- `timeout.go`
  - tier timeout：`Fast/Normal/Slow/Exec`。
  - `ClampTimeout()` 用于把用户给的秒数夹在允许范围内。
- `budget.go`
  - 输出预算裁剪器。
  - 默认预算 64KB。
  - 不在 `wrapToolHandler()` 默认链里，而是由个别工具自行外挂。

### `protocol/`：LSP / JSON-RPC 协议模型
- `codec.go`
  - request/notification/response/envelope 编解码。
  - 对 JSON-RPC envelope 做严格校验。
- `methods.go`
  - LSP method 常量。
- `notification.go`
  - 只分发两类 notification：
    - `textDocument/publishDiagnostics`
    - `window/logMessage`
- `types.go`
  - LSP 常用结构体定义。
- `ext.go`
  - 初始化能力模型、统一 result wrapper、以及 `XRefResultLimit` / `SemanticTokenResultLimit` 等常量。

### `search/`：文件与搜索基础设施
- `fileutil.go`
  - workspace root 规范化、路径安全校验、root containment、二进制探测、symlink 禁止、语言别名推断。
  - `text_search` 的 Go-side walk 会跳过：`.cache`、`.git`、`__pycache__`、`build`、`coverage`、`dist`、`node_modules`、`vendor` 等目录。
- `searchutil.go`
  - 文本搜索：逐行扫描。
  - AST 搜索：依赖 `sg`（ast-grep）。
  - 普通 AST 查询走 `sg run --pattern`。
  - 若 query 看起来像 tree-sitter node kind（如 `function_declaration`），则生成临时 rule 文件走 `sg scan --rule`。
  - `ast_search` 会校验目标路径并拒绝目标 symlink，但实际递归遍历交给 `sg`；Go 侧不会像 `text_search` 一样统一应用二进制/超大文件/跳目录过滤。
  - `FilterAndCapSearchMatches()` 负责去重、排序、截断。

### `tools/`：具体 MCP 工具实现
- `factory.go`
  - `newManagerTool()` / `newSandboxTool()`。
  - 参数解码策略：`decodeRaw / decodeLenient / decodeStrict`。
  - `wrapToolHandler()` 只挂 `Recovery + Logging + Timeout`。
- `tool_file.go`
  - `lsp_file`：`open_file / read_file / diagnostics`。
- `tool_diagnostics.go`
  - diagnostics 子流程：稳定等待、reactive bootstrap、表格化输出。
- `tool_grep.go`
  - `lsp_grep`：`text_search / ast_search`。
- `tool_inspect.go`
  - `lsp_inspect`：`hover / definition / implementation / type_definition / signature_help`。
- `tool_xref.go`
  - `lsp_xref`：`references / call_hierarchy / type_hierarchy`。
- `tool_structure.go`
  - `lsp_structure`：`document_symbol / workspace_symbol / folding_range / semantic_tokens`。
- `tool_completion.go`
  - `lsp_completion`。
- `tool_edit.go`
  - `lsp_edit`：`rename / code_action / format / replace_range` 总入口。
- `tool_edit_replace.go`
  - replace_range 计划生成、写盘、回滚、LSP 同步、函数上下文回显。
- `tool_edit_support.go`
  - patch/hunk 辅助、workspace edit 收集与应用、line ending 保留、rollback 辅助。
- `tool_coderun.go`
  - `code_run`：snippet 运行 / 项目 shell 命令。
- `tool_coderuntest.go`
  - `code_run_test`：指定 Go 测试函数运行。

---

## 3. 核心类型 / 接口

### 3.1 MCP 外层抽象

| 类型 | 位置 | 作用 |
|---|---|---|
| `ToolProvider` | `common/server.go` | MCP Server 与真实工具实现之间的唯一稳定抽象：`ListTools` + `CallTool` |
| `MCPTool` | `common/server.go` | 对外暴露的工具项，包含 `name/description/inputSchema` |
| `Server` | `common/server.go` | stdio MCP Server 主体 |
| `StdioTransport` | `common/stdio.go` | stdio 消息读写器，支持 raw JSON / framed JSON-RPC |
| `HTTPServer` | `common/http_transport.go` | HTTP MCP Server，暴露 `POST /mcp` |
| `jsonRPCRequest` / `jsonRPCResponse` | `common/server.go` | stdio / HTTP 两条入口共享的 JSON-RPC envelope |

**设计观察：**
- `common` 层没有单独的 `Transport` interface。
- `ToolProvider.CallTool()` 返回 `any`，由 `common` 层统一序列化后包装成 MCP text content。

### 3.2 Bootstrap 生命周期抽象

| 类型 | 位置 | 作用 |
|---|---|---|
| `bootstrap.Client` | `bootstrap/client.go` | 通用 lifecycle RPC 客户端，负责 register / heartbeat / reconnect / context / event / log / approval / report / hooks |
| `bootstrap.Config` | `bootstrap/client.go` | bootstrap 配置与回调注入点 |
| `HookConfig` | `bootstrap/hooks.go` | tool 侧 hook 处理器集合 |
| `hookState` | `bootstrap/hooks.go` | 保存 hook 订阅参数，供重连后 replay |

**补充事实：**
- `AgentID` 可以为空，表示 shared service 模式。
- `registerConn()` 当前固定 `RegisterRequest.AgentID` 为空串；`Config.AgentID` 仍会用于 `Context()` 请求与 `PendingHooks()` 的 agent 选择。
- `RequestApproval()` 在断连时不会降级，只会返回 `approval unavailable` 错误。
- report queue 是有界内存队列，不会跨进程持久化。

### 3.3 LSP 路由与管理抽象

| 类型 | 位置 | 作用 |
|---|---|---|
| `manager.Manager` | `cmd/mcp-lsp/manager/manager.go` | 对工具层暴露统一 LSP 能力接口 |
| `manager.Registry` | `cmd/mcp-lsp/manager/registry.go` | 按文件/语言路由 manager，并聚合 diagnostics |
| `installer.Provider` | `cmd/mcp-lsp/installer/installer.go` | 确保语言服务器 binary 可用 |
| `middleware.Handler` | `cmd/mcp-lsp/middleware/logging.go` | 工具处理器统一签名 |
| `middleware.Middleware` | `cmd/mcp-lsp/middleware/logging.go` | 工具中间件签名 |

### 3.4 `gopls/` 内部核心抽象

| 类型 | 位置 | 作用 |
|---|---|---|
| `gopls.Client` | `gopls/client.go` | 单个 LSP 子进程客户端抽象 |
| `ClientFactory` | `gopls/manager.go` | 注入具体 LSP binary 的 client 构造器 |
| `manager` | `gopls/manager.go` | workspace -> client 管理核心 |
| `workspaceClient` | `gopls/manager.go` | 一个 workspace 对应一个 LSP client |
| `transport` | `gopls/transport.go` | 子进程 JSON-RPC 传输层 |
| `lspCacheStore` | `gopls/cache.go` | bootstrap 文档缓存 |
| `bootstrapStateStore` | `gopls/state.go` | bootstrap 状态机 |
| `ManagerPool` | `gopls/pool.go` | client lease 计数与 recycler 容器 |
| `poolRecycler` | `gopls/recycler.go` | 基于 RSS 的 client 回收器 |
| `bootstrapCoordinator` | `gopls/bootstrap_doc.go` | 文档 bootstrap 协调器 |

**关键事实：** `gopls/` 包名带历史色彩，但 `cmd/mcp-lsp/runtime.go` 已把它复用于 Go、JS/TS、Python、CSS、Rust、Java 等语言场景。

---

## 4. 工具实现：`cmd/mcp-lsp` 下各子包如何落成具体能力

## 4.1 工具装配方式

`tools/factory.go` 的核心职责：

- `newManagerTool()`：给依赖 `Registry` 的工具生成统一 handler。
- `newSandboxTool()`：给依赖 `Sandbox` 的工具生成统一 handler。
- 三种解码模式：
  - `decodeRaw`：直接 `json.Unmarshal`
  - `decodeLenient`：把空 / `null` 视为 `{}`
  - `decodeStrict`：同样容忍空 / `null`，但会 `DisallowUnknownFields`，并拒绝 trailing JSON
- `wrapToolHandler()`：统一挂载 `Recovery -> Logging -> Timeout`。
- **注意：`budget.go` 不在默认链里。**
  - `lsp_file`、`lsp_grep` 额外挂了 `WithOutputBudget()`。
  - `lsp_edit` 也没有走 `newManagerTool()`，而是保留了自定义 handler 以支持多文件 apply/rollback 流程。

> 实际 MCP tool name / schema / handler 注册在 `cmd/mcp-lsp/tools.go`；`cmd/mcp-lsp/tools` 负责“工具逻辑”，装配层负责“把逻辑暴露成 MCP tool”。

## 4.2 工具能力总表

| 工具 | 实现入口 | 动作/能力 | 主要依赖 |
|---|---|---|---|
| `lsp_file` | `tool_file.go` | `open_file / read_file / diagnostics` | `search` + `registry` + `tool_diagnostics` |
| `lsp_inspect` | `tool_inspect.go` | hover / definition / implementation / type_definition / signature_help | `manager` + `format` |
| `lsp_xref` | `tool_xref.go` | references / call hierarchy / type hierarchy | `manager` + `format` |
| `lsp_grep` | `tool_grep.go` | text_search / ast_search | `search` + `format` + `registry` |
| `lsp_structure` | `tool_structure.go` | document/workspace symbol / folding range / semantic tokens | `manager` + `format` |
| `lsp_completion` | `tool_completion.go` | completion | `manager` + `format` |
| `lsp_edit` | `tool_edit*.go` | rename / code_action / format / replace_range | `manager` + `edit` + `format` |
| `code_run` | `tool_coderun.go` | snippet 运行 / project shell command | `exec.Sandbox` |
| `code_run_test` | `tool_coderuntest.go` | Go 单测函数运行 | `exec.Sandbox` |

## 4.3 各工具实现细节

### A. `lsp_file`

**入口：** `tool_file.go` + `tool_diagnostics.go`

- `open_file`
  - 先经 `search.ReadToolFileContent()` 做 root containment、regular file、非 symlink、非 binary 校验。
  - 若该文件存在 manager，则调用 `manager.DidOpen()` 预热语言服务；这里的 `DidOpen` 失败会被忽略。
- `read_file`
  - 支持单文件与批量读取。
  - `offset/limit` 基于 **1-based 行号**，渲染时带行号。
  - 批量读取并发执行，但有两层限制：
    - 最多 10 个文件
    - 序列化后的总 payload 目标不超过 16KB，超限时会截断内容，必要时丢弃尾部文件
- `diagnostics`
  - 可查询：
    - 单文件
    - 多文件
    - **若不传 `file_path/file_paths`，则返回当前所有 manager 已缓存的 diagnostics**
  - 显式传文件时若当前结果为空，会走 reactive bootstrap（最多 30 个 URI），再等待稳定并重查。
- 额外挂了 `WithOutputBudget()`，超出预算会返回截断 envelope。

### B. `lsp_grep`

**入口：** `tool_grep.go`

- `text_search`
  - 走 `search.SearchText()`，逐文件逐行扫描。
  - 支持 `regex` / `case_sensitive` / `glob` / `path`。
- `ast_search`
  - 走 `search.SearchAST()`，依赖 `sg`（ast-grep）在 PATH 中可用。
  - 普通查询走 `sg run --pattern`。
  - 若 query 形如 `function_declaration` 这类 node kind，则会生成临时 rule 文件并走 `sg scan --rule`。
- 过滤边界：
  - `text_search` 的 Go-side 文件遍历会跳过 symlink、binary、超大文件，以及 `.cache/.git/__pycache__/build/coverage/dist/node_modules/vendor` 等目录。
  - `ast_search` 会校验目标路径并拒绝目标 symlink，但递归搜索本身交给 `sg`，不会在 Go 侧统一套用 `text_search` 的二进制/超大文件/跳目录过滤。
- 结果后处理：
  - `FilterAndCapSearchMatches()` 去重、排序、截断
  - 若有 `Registry`，还会通过 `DocumentSymbol()` 给命中补 `func_start/func_end`
- 也外挂了 `WithOutputBudget()`；预算超限时尽量保留 `total/showing/hint`，清空具体命中表。

### C. `lsp_inspect`

**入口：** `tool_inspect.go`

- 参数走 `decodeStrict`，`file_path/line/column` 必填。
- `hover`
  - 调 `manager.Hover()`。
  - 会兼容 `string / MarkupContent / []any / map[string]any` 等多种 hover 形态，统一抽成文本。
- `definition / implementation / type_definition`
  - 统一走 `runLocationInspect()`。
  - 结果先限流，再做 `format.NormalizeForDisplay()`。
- `signature_help`
  - 调 `manager.SignatureHelp()`；空结果返回 `no signature help found`。

### D. `lsp_xref`

**入口：** `tool_xref.go`

- `references`
  - `include_declaration` 默认 `false`。
  - `compact` 模式默认 30 条，`full` 默认 50 条。
  - `compact` 下按文件聚合，并尽量只在函数区间变化时附加 `func_start/func_end` 提示。
- `call_hierarchy`
  - 方向只允许 `incoming / outgoing / both / ""`。
- `type_hierarchy`
  - 对外支持 `supertypes / subtypes / both`；内部把 `both` 归一成空方向，交给 manager 同时取两侧。

### E. `lsp_structure`

**入口：** `tool_structure.go`

- `document_symbol`
  - 调 `manager.DocumentSymbol()`。
  - `max_results` 不是简单 slice，而是**递归限制 symbol 节点总数**。
- `workspace_symbol`
  - 要求 **`file_path` 与 `language` 二选一且只能选一个**。
  - `file_path` 不能是目录；若是 docs/config 这类 fallback-only 文件，会提示改用 `language` 或 `lsp_file/lsp_grep`。
- `folding_range`
  - 直接调 `manager.FoldingRange()`。
- `semantic_tokens`
  - 结果会同时限制 decoded tokens 数量和原始 `data` 长度（每 token 5 个整数）。

### F. `lsp_completion`

**入口：** `tool_completion.go`

- 走 `decodeStrict`。
- timeout tier 是 `Fast`（5s）。
- `compact` 模式默认 20 条，仅保留 `label/kind/detail`；`full` 模式默认 50 条，返回完整 completion item。

### G. `lsp_edit`

**入口：** `tool_edit.go` + `tool_edit_replace.go` + `tool_edit_support.go`

#### `rename`
- 调 `manager.Rename()` 生成 `WorkspaceEdit`。
- `persist_to_disk` 默认是 `true`；显式传 `false` 时只返回 prepared `workspace_edit`。
- 真正落盘时会：
  1. 收集 `Changes + DocumentChanges` 里的文本编辑
  2. 检查同文件 edit range 是否重叠
  3. 按文件写盘
  4. 调 `syncDocuments()` 同步回 LSP
- 返回值会携带 `DiagnosticGeneration`。

#### `code_action`
- 当前只列出候选动作，不自动 apply。
- 实现里使用的是**零长度 range（光标点）**；虽然 schema 暴露了 `end_line/end_column`，但当前并没有消费这两个字段。

#### `format`
- 调 `manager.Format()`，返回 text edits。
- 不自动写盘。
- 格式化参数固定：`tabSize=4`、`insertSpaces=false`。

#### `replace_range`
- 支持三种输入：
  1. `patch`
  2. `edits: [{old_string,new_string}]`
  3. `line/column[/end_line/end_column] + new_text`；源码里只有 `strings.TrimSpace(new_text) != ""` 时才会进入坐标模式，因此空字符串删除当前不会走这一路径。
- 核心流程：
  1. 读取文件并统一为 LF 处理
  2. 构造 `replacePlan`
  3. patch/edits 走 `edit.MatchContext()`：先用 `seeksequence.go` 的 5-pass 行序列匹配（`exact / trim_right / trim_both / unicode_normalized / escape_normalized`）匹配 `old_text` 与上下文；若没有候选，再在原始字符串上做 `substring_exact` fallback；多候选会报 ambiguous
  4. 写回磁盘，同时保留原文件原始内容和原权限位
  5. 同步到 LSP
  6. 成功时返回替换前后文本、影响区间、函数区间、函数体片段
  7. 失败时返回 `current_content + func_start/func_end/func_body`
- 其它实现细节：
  - 最多 20 个 `edits`
  - 保留原文件 line ending（LF/CRLF）
  - LSP 同步失败时会 rollback 写盘结果

#### `replace_range` 的 LSP 同步策略
- 常规内容：`BootstrapDocument()` 后做 `DidChange(full text)`。
- 超大内容（>2MB）：走 `BootstrapDocumentOpenOnly()`，避免超大 `DidChange`。
- 文本行数超过阈值（200 行）时仍认为同步成功，但会返回 warning。

### H. `code_run`

**入口：** `tool_coderun.go`

- `mode=run`
  - 只支持 `go / javascript / typescript`。
  - Go 默认 `auto_wrap=true`：会自动补 `package main`，并做一层标准库 import 猜测。
  - 在 sandbox root 下创建临时目录写入 snippet 再执行。
- `mode=project_cmd`
  - 走 `Sandbox.ShellRequest()`，底层是 `$SHELL -lc <command>`。
  - `work_dir` 必须留在 sandbox root 内。
- 返回约定：
  - 子进程非零退出码时，不抛 tool error，而返回 `CodeRunResult{success:false, exit_code!=0}`
  - 超时或执行器级错误时，返回 `CodeRunFailure{exit_code:-1}`
  - stdout/stderr 会合并，超过 256KB 会标记 `truncated=true`

### I. `code_run_test`

**入口：** `tool_coderuntest.go`

- 仅支持 Go。
- `test_func` 必填，且只能包含字母、数字、下划线。
- 实际执行命令：
  `go test -run ^<TestFunc>$ <pkg>`
- 默认 `test_pkg=./...`。

---

## 5. 中间件链：middleware 设计

## 5.1 中间件基元

- `middleware.Handler`
  - 签名：`func(context.Context, json.RawMessage) (any, error)`
- `middleware.Middleware`
  - 签名：`func(Handler) Handler`
- `middleware.Chain()`
  - 逆序包裹，因此传入顺序决定“声明顺序”，执行时靠前的 middleware 在最外层。

## 5.2 默认链路

`tools/factory.go` 中 `wrapToolHandler()` 的声明顺序：

```go
middleware.Chain(
    handler,
    middleware.Recovery(...),
    middleware.Logging(...),
    middleware.Timeout(tier),
)
```

实际执行顺序是：

```text
Recovery -> Logging -> Timeout -> 具体 handler
```

含义：
- `Recovery`：兜底 panic，避免整个 MCP 进程被单个工具崩掉。
- `Logging`：记录请求体大小、压缩后的请求/响应、耗时。
- `Timeout`：按工具 tier 注入 deadline。

## 5.3 Timeout tier

- `TierFast = 5s`
- `TierNormal = 30s`
- `TierSlow = 120s`
- `TierExec = 300s`

对应示例：
- `lsp_completion`：Fast
- `lsp_inspect/xref/structure/edit/file`：Normal
- `lsp_grep`：Slow
- `code_run/code_run_test`：Exec

## 5.4 Output Budget

`budget.go` 默认预算是 **64KB**，但只有两类工具显式外挂了 `WithOutputBudget()`：

- `lsp_file`
- `lsp_grep`

行为：
- 先执行真实 handler；
- 若 JSON 编码后的响应超过预算，则返回一个被截断的 envelope；
- grep 结果会尽量保留 `total/showing/hint`，但清空具体命中表。

---

## 6. 通信协议：HTTP / Stdio / Bootstrap / LSP transport

## 6.1 MCP stdio transport（`common/stdio.go`）

`StdioTransport` 同时支持两种输入协议：

1. **Raw JSON**
   - 读取首个非空白字节若是 `{` 或 `[`，则启用 `json.Decoder` 连续解码。
2. **Framed JSON-RPC**
   - 读取 `Content-Length: N\r\n\r\n<body>`。

输出时：
- 若当前模式是 framed，就写 `Content-Length` 头；
- 否则直接写 JSON + `\n`；
- `StdioTransport` 没有公开 `Flush()` 方法；`WriteMessage()` 仅在底层 writer 实现 `Flush() error` 时顺手 flush。

## 6.2 MCP server dispatch（`common/server.go`）

`Server.dispatch()` 当前完整方法列表为：

- `initialize`
- `notifications/initialized`
- `ping`
- `shutdown`
- `exit`
- `tools/list`
- `tools/call`

协议特征：
- `initialize` 默认协议版本是 `2024-11-05`
- 通知（无 id）不回包
- `tools/call` 结果统一包装成 MCP text content：
  `{"content":[{"type":"text","text":"<json-string>"}]}`

## 6.3 MCP HTTP transport（`common/http_transport.go`）

- 入口：`POST /mcp`
- 单请求体上限：10MB
- 每个 HTTP 请求只处理一个 JSON-RPC envelope
- 通知请求不回结果，返回 `202 Accepted`
- `tools/call` 同样把返回值 marshal 成 text content
- 适合 shared service / 多客户端复用场景

## 6.4 Peer HTTP 发现（`common/discovery.go`）

- 把 HTTP 监听地址写到 `/tmp` 下的 discovery file。
- 命名中带 `binary + parentPID`，用于父进程或同伴进程定位 peer MCP HTTP 服务。
- 写入使用 `tmp + rename`，避免半写入文件。

## 6.5 Bootstrap 控制面通道（`common/bootstrap/*.go`）

- 连接方式：TCP + `jrpc2/channel.Line`
- 客户端注册时会上报：
  - `instance_id`
  - `boot_id`
  - `binary_name`
  - `client_kind`
  - `thread_id`
  - `pid`
  - `session_token`
  - `peer_kind=tool`
  - `capabilities_offered/required`
  - `subscriptions`
  - `resume_from_generation`（若存在）
  - 注意：`registerConn()` 当前不把 `Config.AgentID` 写入注册请求，`RegisterRequest.AgentID` 固定为空串。
- 运行时通道包含：
  - request：`register / heartbeat / context / approval / report / hook/subscribe / hook/resolve / hook/pending`
  - notify：`event / log`
  - callback：`tools/list / tools/call / hook/before / hook/check / hook/after / shutdown / config_changed`
- 断连后会：
  - mark disconnected
  - reconnect with exponential backoff
  - flush report queue
  - replay hook subscriptions
- `Context()` 断连时可退化为 boot snapshot；`RequestApproval()` 不退化。

## 6.6 LSP 子进程 transport（`gopls/transport*.go`）

这一套与上面的 MCP transport 是另一层协议：用于本进程与 LSP server 子进程之间通信。

- 永远使用 **Content-Length framed JSON-RPC**。
- `transport.request()`
  - 生成自增 request id
  - 在 `pending map` 中登记 channel
  - 写入子进程 stdin
  - 等待 response 或 context timeout
- `dispatchMessage()`
  - 无 `method`：response
  - 有 `method` 且无 `id`：notification
  - 有 `method` 且有 `id`：server request（反向请求）
- `defaultServerRequestResult()`
  - 对常见 LSP server 反向请求给出默认应答，如：
    - `workspace/configuration`
    - `client/registerCapability`
    - `client/unregisterCapability`
    - `window/workDoneProgress/create`
    - `workspace/semanticTokens/refresh`
    - `workspace/codeLens/refresh`
    - `workspace/inlayHint/refresh`
    - `workspace/diagnostic/refresh`
- `transport_conn.go`
  - 负责启动子进程、读写 framed 消息、汇总 stderr、在异常时清空 pending、kill 进程。

---

## 7. 关键设计观察

1. **`common` 层非常薄，真正复杂度在 `bootstrap/` 与 `cmd/mcp-lsp/`。**
2. **`ToolProvider` 是 MCP server 与工具框架的关键解耦点。**
3. **`common` 没有统一 transport 抽象，而是 `Server + StdioTransport` 与 `HTTPServer` 双实现并存。**
4. **`bootstrap.Client` 已经不只是 register/heartbeat 客户端，而是完整的 control-plane peer。**
5. **`gopls/` 实际上是通用 LSP 进程管理层，不只服务 Go。**
6. **`markdown/json/yaml` 的 fallback 能力存在于 `gopls` manager 内部**，但当前 `cmd/mcp-lsp/runtime.go` 未注册这些语言；要触发 fallback 需要装配层显式把这些语言路由到该 manager。
7. **`replace_range` 是本层最复杂的写路径**：匹配、落盘、LSP 同步、回滚、函数上下文回显都在这里收束。
8. **Output Budget 不是默认全局中间件**，当前仅 `lsp_file` / `lsp_grep` 使用。
9. **`ManagerPool` / `recycler` 基础设施已经存在，但当前仍明显偏 primary-manager 模式。**

---

## 8. 建议从哪里继续读

若要继续深入，推荐顺序：

1. `common/server.go` + `stdio.go`
2. `common/bootstrap/client.go` + `lifecycle.go` + `reconnect.go`
3. `cmd/mcp-lsp/tools/factory.go` + `cmd/mcp-lsp/tools.go`
4. `cmd/mcp-lsp/manager/registry.go` + `cmd/mcp-lsp/gopls/manager*.go`
5. `cmd/mcp-lsp/gopls/client.go` + `cmd/mcp-lsp/gopls/transport*.go`
6. `cmd/mcp-lsp/tools/tool_edit*.go` + `cmd/mcp-lsp/edit/*.go`

这样能最快建立“从 MCP 调用入口到 LSP 子进程，再到控制面生命周期”的完整心智模型。

## 审查补遗

1. **已更正 `report_queue.go` 的性质**：当前实现是**有界内存队列**，并不会落磁盘；原地图里“durable queue”的说法不准确。
2. **已补齐 bootstrap 生命周期缺口**：`Context/EmitEvent/Log/Approval`、`shutdown/config_changed` 回调、hook 默认决策、断连退化/不可退化行为，现在都已写入地图。
3. **已补齐 `gopls/factory.go` 的职责**：它不是对外注册工厂，而是 `gopls` 包内的泛型公共胶水层；原地图缺了这一层。
4. **已更正 `StdioTransport` 描述**：它只有 raw JSON / framed 两种探测模式；没有公开 `Flush()` API，只是在底层 writer 支持时由 `WriteMessage()` 顺手 flush。
5. **已补齐 bootstrap API 与注册细节**：
   - `client.go` 的公开入口补上了 `New()`
   - hook 公开 API 位于 `hooks.go`
   - `ctl/register` 的实际字段、`AgentID` 当前不入注册请求的事实，已按源码写明
6. **已更正中间件链描述**：`wrapToolHandler()` 默认只挂 `Recovery + Logging + Timeout`；`Budget` 是工具级 opt-in，不是全局默认链。
7. **已补齐工具细节遗漏**：
   - `lsp_file diagnostics` 支持“不传路径时读取所有当前 diagnostics”
   - `lsp_grep` 的 `text_search` 与 `ast_search` 过滤边界并不相同；前者走 Go-side 文件筛选，后者主要委托给 `sg`
   - `lsp_edit` 会保留文件权限与行尾风格，并在 LSP 同步失败时回滚
   - `replace_range` 的匹配策略已按 `seeksequence.go + patchmatch.go` 更正，补上了 `substring_exact` fallback 与多候选歧义行为
   - `code_run` 的非零退出与执行器错误是两种不同返回模型
8. **已补齐 `gopls` 子包遗漏职责**：JSTS/Java bootstrap、cache 持久化开关、bootstrap 文档协调器、fallback-only 语言策略都已纳入，并注明当前 runtime 尚未注册 markdown/json/yaml manager。
9. **保留一条实现观察**：`ManagerPool.snapshotManagers()` 当前只返回 primary manager；`recycler` 的重建路径也仍带明显 Go-centric 痕迹，说明池化/回收基础设施仍在演进中。
