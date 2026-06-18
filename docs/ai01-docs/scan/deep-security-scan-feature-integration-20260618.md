# feature-integration-20260618 深度安全扫描报告

扫描对象：`D:\project\Super-Dolphin-worktrees\feature-integration-20260618`  
分支：`codex/feature-integration-20260618`  
扫描 commit：`0ed8f0afe429`  
扫描日期：2026-06-18  
方法：使用 `codex-security:deep-security-scan` 做仓库级多轮发现，结合人工复核关键调用链与代码证据。扫描临时产物位于 `C:\tmp\codex-security-scans\feature-integration-20260618\0ed8f0afe429_20260618101949`。

## 结论摘要

| 等级 | 数量 | 结论 |
| --- | ---: | --- |
| P0 | 0 | 未确认无需前置条件即可远程利用的 RCE、持久化绕过或密钥全量泄露链路。 |
| P1 | 3 | 本地 UI/RPC 暴露面、automation 命令模板、stdio MCP 配置执行存在高影响实现逻辑问题。 |
| P2 | 8 | 主要是 SSRF、任意本地文件导入/读取范围扩大、同机本地代理缺少强认证、自动安装脚本执行等需要额外前置条件的问题。 |

优先修复顺序建议：

1. 给 `/wails/ws` 和平台 RPC 增加会话级不可猜 token、Host/Origin allowlist，并把敏感 RPC 方法做最小权限拆分。
2. 禁止 automation command card 使用 `sh -c` 拼接模板；改为命令名 + 参数数组 + schema 强校验。
3. 对项目级 MCP stdio 配置加显式信任确认、命令 allowlist，默认不执行仓库提供的命令。
4. 给所有本地文件导入和 HTTP 出站接口补统一的 workspace root / egress policy。

## P0

本轮未确认 P0。最高风险链条目前需要至少一个前置条件，例如用户访问恶意网页触发 DNS rebinding、项目内存在恶意 MCP 配置、或 agent/automation 输出被攻击者影响。因此本报告不把这些问题升为 P0。

## P1

### P1-01 本地 Wails WebSocket RPC 缺少请求来源绑定，可被 DNS rebinding 访问敏感 RPC

状态：已验证代码路径，未执行动态利用。  
影响：恶意网页可在 DNS rebinding 或同 Host alias 场景下连接本机 UI RPC，调用注册在 `platform/rpc.Server` 的方法。结合 `skill/command/exec`、`skills/remote/read`、datasource import 等接口，可造成本地文件内容泄露、任意 HTTP 出站探测、配置/状态修改。  
根因：服务只校验监听地址是 loopback，没有校验每个 WebSocket 请求的 Host/Origin，也没有每会话 token。

证据：

- `internal/ui/wails/http_server.go:19` 默认监听 `127.0.0.1:4511`。
- `internal/ui/wails/http_server.go:32` 将 `/wails/ws` 直接注册为 `rpc.WSHandler(server, nil)`。
- `internal/ui/wails/http_server.go:61` 只调用 `validateHTTPAssetAddr(s.addr)` 校验绑定地址。
- `internal/platform/rpc/transport_ws.go:21` 使用空 `websocket.Upgrader{}`，没有显式 `CheckOrigin` 或 token 校验。
- `internal/platform/rpc/transport_ws.go:44` 直接 upgrade。
- `internal/platform/rpc/transport_ws.go:56` 建立 jrpc2 server。
- `internal/platform/rpc/transport_ws.go:92` 到 `internal/platform/rpc/transport_ws.go:102` 将客户端传入的 method dispatch 到平台 RPC。

修复建议：

- 启动时生成随机 nonce，WebView 连接 `/wails/ws?token=...`，后端必须校验 token。
- 显式拒绝非 `localhost` / `127.0.0.1` / `::1` Host；对 Origin 做严格 allowlist。
- 将高危 RPC 方法拆出 UI-only capability，不让裸 WS 连接默认拥有全部 handler。
- 增加回归测试：恶意 Origin、rebind Host、缺失 token、错误 token 均应失败。

### P1-02 automation command card 将模板渲染结果直接交给 `sh -c`，存在命令注入

状态：已验证代码路径，未执行动态利用。  
影响：如果 DAG automation 的 `command_template` 引用了来自节点输出、sharedfile 或外部输入的字段，攻击者可通过模板变量把 shell 元字符注入最终命令，实现编排进程权限下的命令执行。  
根因：代码把 `text/template` 渲染后的整条字符串交给 shell，而不是结构化命令名与参数数组；`ArgsSchema` 也没有在执行前形成 shell-safe 约束。

证据：

- `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:40` 存储 `CommandTemplate`。
- `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:66` 到 `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:72` 调用 `renderCommandTemplate(...)` 后执行 `exec.CommandContext(ctx, "sh", "-c", command)`。
- `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:194` 到 `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:204` 从 DAG config 加载 command card、构造 run args 后直接调用 runner。
- `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go:468` 开始的 `renderCommandTemplate` 负责渲染，但没有把变量拆成安全 argv。

修复建议：

- 将 command card 改为 `{ "command": "...", "args": [...] }`，禁止最终路径走 shell。
- 对每个变量按 schema 做类型、枚举、正则、长度校验；路径参数要强制在工作区或允许根内。
- 对必须 shell 的少数场景增加显式 `risk_level=high`、人工确认和不可默认启用策略。
- 增加注入回归测试：变量含 `;`, `&&`, `|`, `$()`, backtick 时必须失败。

### P1-03 项目级 stdio MCP 配置可变成本机进程执行入口

状态：已验证代码路径，触发条件依赖用户打开/启用包含恶意 MCP 配置的工作区。  
影响：仓库内 `.agent/mcp_server/config.json` 或通过 UI 添加的 MCP stdio 配置可指定任意 `command`、`args`、`env`。后续 Codex/toolbridge 准备 dynamic tools 时会启动该命令作为 MCP server。对“不可信仓库”来说，这等价于打开项目后执行项目提供的本机命令。  
根因：配置校验只检查字段非空与格式，不建立信任确认、命令 allowlist、签名或隔离运行边界。

证据：

- `internal/module/mcp_server/config_helpers.go:14` 到 `internal/module/mcp_server/config_helpers.go:31` 读取并规范化本地 MCP server 配置。
- `internal/module/mcp_server/service.go:441` 到 `internal/module/mcp_server/service.go:459` 对 stdio 配置只保留 `Command`、`Args`、`Env`，未限制命令来源。
- `internal/module/mcp_server/config_helpers.go:93` 到 `internal/module/mcp_server/config_helpers.go:108` 将配置转换为跨模块 contract。
- `internal/platform/toolbridge/stdio_mcp_client.go:49` 到 `internal/platform/toolbridge/stdio_mcp_client.go:62` 根据 manifest 创建 stdio client，并用 `exec.Command(...)` 启动 `binary.Command[0]`。

修复建议：

- 默认禁用工作区提供的 stdio MCP；首次启用必须显示命令、路径、env 和来源，并记录用户授权。
- 对内置 MCP 使用固定 registry；非内置命令要求绝对路径、hash pinning 或签名。
- 进程启动前剥离敏感环境变量，并对工作目录、网络、文件访问做最小化隔离。
- 增加测试：未授权项目 MCP 不应被传入 `MCPManifest` 或启动。

## P2

### P2-01 `skills/remote/read` 可向任意 URL 发 GET 并回显正文，存在 SSRF/内网探测

状态：已验证代码路径。  
影响：调用者可让宿主进程访问任意 `http(s)` URL，返回最多 `maxSkillFileBytes` 内容；非 2xx 时还会返回前 8KB 错误体。若与 P1-01 的 UI RPC 暴露面组合，恶意网页可把本机应用变成内网探测与响应泄露代理。  
证据：`internal/module/skill/rpc.go:178`、`internal/module/skill/rpc.go:183` 暴露 `ReadRemote`；`internal/module/skill/skills_fs.go:450` 到 `internal/module/skill/skills_fs.go:468` 构造请求、`s.http.Do(req)` 并返回正文。

修复建议：统一 URL egress policy；禁止 loopback、link-local、RFC1918、metadata IP；限制 redirect；错误响应不要回显正文。

### P2-02 HTTP MCP `tools/list` 接受任意 URL 和 header，可形成 SSRF 与 header 滥用

状态：已验证代码路径。  
影响：保存的 HTTP MCP server 配置可指向任意 `http/https` Host，并带任意 headers。`ListServerTools` 会对该 URL 连续发送 `initialize`、`notifications/initialized`、`tools/list`，错误状态会拼接部分响应体。  
证据：`internal/module/mcp_server/service.go:420` 到 `internal/module/mcp_server/service.go:437` 保存 URL/Headers；`internal/module/mcp_server/service.go:519` 到 `internal/module/mcp_server/service.go:530` 只校验 scheme 和 Host 非空；`internal/module/mcp_server/http_tools.go:46` 到 `internal/module/mcp_server/http_tools.go:65` 发起 MCP HTTP 请求；`internal/module/mcp_server/http_tools.go:140` 到 `internal/module/mcp_server/http_tools.go:147` 处理错误状态。

修复建议：复用 SSRF 防护；禁止敏感 header 名称；对 HTTP MCP URL 做用户确认和 allowlist；错误体只保留摘要。

### P2-03 DAG artifact import 在 `allowed_source_roots` 为空时允许导入任意本地文件

状态：已验证代码路径。  
影响：agent 输出的 artifact plan 可以提供 `SourcePath`。如果 DAG 配置没有显式设置 `allowed_source_roots`，导入器会接受任意本地 regular file，并复制到 sharedfile/artifact 区域，造成本地文件被持久化和后续读取。  
证据：`cmd/mcp-orch/orchestration/dag_subscriber_module.go:269` 到 `cmd/mcp-orch/orchestration/dag_subscriber_module.go:273` 将 plan 中的 `SourcePath` 传给 importer；`cmd/mcp-orch/store/sharedfile/importer.go:64` 到 `cmd/mcp-orch/store/sharedfile/importer.go:90` 接受绝对源路径；`cmd/mcp-orch/store/sharedfile/importer.go:141` 到 `cmd/mcp-orch/store/sharedfile/importer.go:143` 在 roots 为空时直接允许。

修复建议：`allowed_source_roots` 缺失时 fail-fast；默认只允许 run workspace、node output dir 或显式 artifact temp dir。

### P2-04 datasource v1/v2 允许 RPC 导入任意绝对本地文件

状态：已验证代码路径。  
影响：UI RPC 可传 `sourcePath` 读取/复制绝对路径文件。v1 会复制到 `.agent/datasources/uploads` 并提取文本；v2 会读取 UTF-8 文本并分块入库。虽然有扩展名、regular file、UTF-8 等限制，但没有把源路径限制在用户选择目录或 workspace 内。  
证据：`internal/module/datasource/service.go:81` 到 `internal/module/datasource/service.go:92` 处理 upload；`internal/module/datasource/service.go:305` 到 `internal/module/datasource/service.go:314` 只要求绝对路径；`internal/module/datasource/service.go:382` 到 `internal/module/datasource/service.go:403` 打开并复制源文件；`internal/module/datasource_v2/rpc.go:17` 暴露 `datasourceV2/importText`；`internal/module/datasource_v2/service.go:176` 到 `internal/module/datasource_v2/service.go:185` 只要求绝对路径；`internal/module/datasource_v2/service.go:202` 打开源文件。

修复建议：导入应来自文件选择器授予的路径或 workspace allowlist；RPC 入参不要直接接受任意绝对路径。

### P2-05 LSP `work_dir` 可把工具作用域扩大到任意绝对目录

状态：已验证代码路径。  
影响：LSP tools 入参可带 `work_dir`。当它是绝对路径时，代码解析 symlink 后把该目录追加为新的 `WorkspaceRoots`，没有要求其位于原始可信 workspace 下。攻击者若能影响工具参数，可扩大读/搜/编辑作用域。  
证据：`cmd/mcp-lsp/tools/factory.go:515` 到 `cmd/mcp-lsp/tools/factory.go:533` 读取 `work_dir`；`cmd/mcp-lsp/tools/factory.go:540` 到 `cmd/mcp-lsp/tools/factory.go:551` 将 normalized workDir 设为 CWD 并追加到 WorkspaceRoots；`cmd/mcp-lsp/tools/factory.go:555` 到 `cmd/mcp-lsp/tools/factory.go:582` 对绝对路径只做存在和目录校验。

修复建议：绝对 `work_dir` 必须位于当前可信 workspace roots 内；跨 root 需要显式授权。

### P2-06 toolbridge loopback proxy 缺少请求级认证，发现端口和 agentID 的本地进程可转发工具调用

状态：已验证代码路径。  
影响：proxy 绑定随机 loopback 端口，路径中包含 family/agentID 后即可调用 `tools/list` 或 `tools/call`。这主要是同机同用户威胁；端口随机降低可发现性，但不是认证边界。  
证据：`internal/platform/toolbridge/module.go:164` 到 `internal/platform/toolbridge/module.go:175` 监听 `127.0.0.1:0` 并记录地址；`internal/platform/toolbridge/proxy.go:94` 从路径提取 family/agentID；`internal/platform/toolbridge/proxy.go:226` 到 `internal/platform/toolbridge/proxy.go:255` 解码参数、解析 threadID、routeToolCall；`internal/platform/toolbridge/handler.go:120` 到 `internal/platform/toolbridge/handler.go:150` 路由到 host tool 或 peer tool。

修复建议：proxy 地址旁路随机性之外增加 bearer token / per-agent capability；token 只通过子进程环境或 pipe 传递，不写日志。

### P2-07 平台 TCP RPC 连接没有认证，配置错误可扩大为本机或局域网 RPC 面

状态：已验证代码路径。  
影响：`Server.Run` 对 `s.addr` 监听 TCP，并把实际地址写入 `GO_AGENT_CTL_RPC_ADDR`。连接建立后直接启动 jrpc2 over line channel，没有握手 token。若 `s.addr` 被配置成非 loopback，或本机进程读到 env/端口，就能访问注册方法。  
证据：`internal/platform/rpc/server.go:24` 定义 `GO_AGENT_CTL_RPC_ADDR`；`internal/platform/rpc/server.go:399` 到 `internal/platform/rpc/server.go:410` 监听并导出地址；`internal/platform/rpc/server.go:446` 到 `internal/platform/rpc/server.go:447` 对连接启动 RPC server。

修复建议：强制 loopback，除测试外拒绝 `0.0.0.0` / 局域网地址；每连接必须带启动时生成的 token。

### P2-08 JS/TS LSP 依赖准备会自动运行 `pnpm install --frozen-lockfile`

状态：已验证代码路径。  
影响：当 JS/TS workspace 配置启用 pnpm 且缺少 `node_modules` 时，LSP 准备阶段会在项目 install root 执行 `pnpm install --frozen-lockfile`。pnpm install 默认可能执行依赖 lifecycle scripts，不适合作为打开不可信仓库时的自动动作。  
证据：`cmd/mcp-lsp/multilsp/jsts_dependency_prepare.go:58` 到 `cmd/mcp-lsp/multilsp/jsts_dependency_prepare.go:69` 在配置启用时准备依赖；`cmd/mcp-lsp/multilsp/jsts_dependency_prepare.go:76` 到 `cmd/mcp-lsp/multilsp/jsts_dependency_prepare.go:87` 缺少 node_modules 时运行安装；`cmd/mcp-lsp/multilsp/jsts_dependency_prepare.go:131` 到 `cmd/mcp-lsp/multilsp/jsts_dependency_prepare.go:145` 执行 `pnpm install --frozen-lockfile`。

修复建议：默认不自动安装；必须用户确认。若保留自动安装，至少加 `--ignore-scripts`，并把 install root 限制在可信 workspace。

## 未升格或已缓解的候选

- Codex fallback auto-install：`internal/provider/codexapp/codex_autoinstall.go:348` 到 `internal/provider/codexapp/codex_autoinstall.go:357` 要求 pinned SHA-256；`internal/provider/codexapp/codexmanifest/manifest.go` 校验 source/package checksum。未升格为漏洞。
- `/local-image`：`internal/ui/wails/clipboard_assets.go:58` 到 `internal/ui/wails/clipboard_assets.go:77` 允许按绝对路径预览本地图片，但有扩展/内容类型校验。若与 P1-01 组合可造成本地图片泄露，单独不升格为 P1。
- 普通恶意网页直接连 `ws://127.0.0.1:4511/wails/ws` 可能被 gorilla 默认 Origin 检查挡住；P1-01 的核心风险是 DNS rebinding/同 Host alias，因为当前服务没有 Host allowlist 或会话 token。

## 修复验收建议

- 新增安全测试覆盖 Host/Origin/token：普通 cross-origin、DNS rebinding Host、缺失 token、错误 token 都必须拒绝。
- 为 command card 增加注入样例测试，确保模板变量不能改变 argv 边界。
- 为 MCP stdio 增加“未授权工作区配置不启动进程”的测试。
- 为 datasource、artifact import、LSP `work_dir` 增加 workspace root 越界测试。
- 引入统一 `egresspolicy`/`pathpolicy` 包，避免 SSRF 与本地文件边界在多个模块重复实现。

