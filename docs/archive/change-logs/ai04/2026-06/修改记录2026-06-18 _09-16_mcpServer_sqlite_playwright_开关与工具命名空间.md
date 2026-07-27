# 修改记录 2026-06-18 09-16 mcpServer sqlite/playwright 开关与工具命名空间

## 背景

前一轮已经让默认 PostgreSQL MCP server 可以通过 stdio 配置持久化。本轮继续把本地常用 MCP server 做成可显式管理的开关，目标是：

- 在当前工作区配置里按需写入或启用 SQLite MCP server。
- 在当前工作区配置里按需写入或启用 Playwright MCP server。
- 关闭时保留配置，只把该 server 标记为禁用，后续 start 可以重新启用。
- provider 会话只接收启用状态的 MCP server，避免关闭后的本地工具继续注入。
- 多个第三方 MCP server 暴露同名工具时，不再互相覆盖或直接报重复，而是给冲突工具保留 `mcp__server__tool` 命名空间调用名。
- 前端技能页的“插件”入口改成 MCP 工具管理面板，直接控制 SQLite 和 Playwright 的启停。

## 行为调整

### MCP server 配置契约

`internal/contract/mcp_control.go` 现在集中承载 MCP server 配置契约：

- `MCPServerConfig` 新增 `Enabled *bool` 字段。
- 新增 SQLite 与 Playwright 的 start/stop 请求、结果和 controller 接口。
- `MCPServerConfigStore` 增加 `SetServerEnabled`，让业务层可以关闭配置但不删除记录。
- 原先放在 `internal/contract/prompt.go` 的 MCP server 配置类型被迁出，避免 prompt 契约文件继续承载 MCP 控制面定义。

`enabled == nil` 会按启用处理；持久化和 normalize 后会显式写成 `true`，关闭时写成 `false`。

### 默认 SQLite MCP server

新增 `internal/module/mcp_server/sqlite.go`：

- RPC：`mcpServer/sqlite/start`
- RPC：`mcpServer/sqlite/stop`
- 默认 server 名称：`sqlite`
- 默认 stdio 命令：

```json
{
  "transport": "stdio",
  "command": "npx",
  "args": [
    "-y",
    "@bytebase/dbhub",
    "--dsn=sqlite:///..."
  ],
  "enabled": true
}
```

SQLite 数据库路径解析顺序：

1. 请求里的 `databasePath`。
2. 运行时配置里的 `SQLitePath`。
3. `contract.SQLitePathEnvKey` 对应环境变量。
4. `contract.InternalSQLitePathEnvKey` 对应环境变量。
5. `SUPER_DOLPHIN_HOME/super-dolphin.db`。

如果以上路径都无法得到有效值，会直接返回：

```text
mcp_server: sqlite database path is required
```

同时兼容并迁移旧的默认 SQLite MCP 配置：

- `@modelcontextprotocol/server-sqlite <dbPath>`
- `mcp-server-sqlite --db <dbPath>`
- `mcp-server-sqlite --database <dbPath>`

检测到旧默认配置时，start 会替换成新的 `@bytebase/dbhub --dsn=sqlite:///...` 配置。

### 默认 Playwright MCP server

新增 `internal/module/mcp_server/playwright.go`：

- RPC：`mcpServer/playwright/start`
- RPC：`mcpServer/playwright/stop`
- 默认 server 名称：`playwright`
- 默认 stdio 命令：

```json
{
  "transport": "stdio",
  "command": "npx",
  "args": [
    "@playwright/mcp@latest"
  ],
  "enabled": true
}
```

start 行为：

- 如果不存在配置，写入默认配置。
- 如果已有配置，只把 `enabled` 设为 `true`，不覆盖用户已有参数。

stop 行为：

- 只把 `enabled` 设为 `false`。
- 不删除配置行，方便后续重新启用。

### MCP server 模块拆分

`internal/module/mcp_server/service.go` 做了收敛：

- `Service` 接口新增 SQLite/Playwright start/stop。
- `NewServiceWithStoreAndConfig` 读取运行时配置中的 SQLite 路径。
- `ListMCPServerConfigs` 只返回启用状态的配置。
- `normalizeHTTPServerConfig` 和 `normalizeStdioServerConfig` 都会补齐 enabled 状态。
- 原来的配置文件读写、克隆、workspace 查找逻辑拆到 `internal/module/mcp_server/config_helpers.go`，降低单文件复杂度。

`internal/module/mcp_server/rpc.go` 新增注册：

- `mcpServer/sqlite/start`
- `mcpServer/sqlite/stop`
- `mcpServer/playwright/start`
- `mcpServer/playwright/stop`

### NPX 兼容入口

`internal/module/mcp_server_npx` 继续作为默认 npm MCP server 的兼容入口：

- 原有 `mcpServer/postgres/start` 保持透传。
- 新增 SQLite/Playwright start/stop 透传。
- `defaultServerController` 组合 Postgres、SQLite、Playwright 三类 controller，避免兼容模块自己复制默认配置逻辑。

实际配置写入和启停仍由主 `mcp_server` 模块负责。

### 配置持久化

`internal/store/mcpserver/store.go` 新增 enabled 持久化：

- 表结构新增：

```sql
enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1))
```

- `InsertServer` 写入 enabled。
- `ListServers` 读取 enabled 并还原到 `MCPServerConfig.Enabled`。
- `SetServerEnabled` 更新指定工作区和 server 名称的启用状态。
- `ensureTableShape` 会给旧表补 enabled 列。
- 新建表和迁移后表都包含 enabled 约束。

关闭 MCP server 不再删除配置，provider 注入阶段通过 enabled 过滤。

### Thread / Turn 注入过滤

`internal/module/thread/mcp_server_config.go` 与 `internal/module/turn/service_helpers.go` 都补了 enabled 处理：

- normalize 时补齐 `enabled`。
- disabled 配置不会进入 `Servers` 和 `ServerConfigs`。
- 复制配置时保留 enabled 指针，避免状态在快照里丢失。

重复 server 名处理也有调整：

- HTTP 配置如果与当前活跃 server 重名，继续跳过，避免重复连接同一个可复用 HTTP MCP server。
- stdio 配置即使名字已经出现在当前快照里，也会保留 `ServerConfigs`，确保 sqlite/playwright 这类本地 stdio server 能带上真实启动命令。

### Provider stdio 白名单

`internal/provider/shared/mcp_stdio_config_test.go` 和 `internal/provider/claudecli/transport_config.go` 补齐了默认 stdio MCP 包支持：

- 继续允许 managed sidecar 的 `mcp-*` 命令。
- `npx` 只允许明确白名单里的 MCP 包。
- 新增允许：
  - `@bytebase/dbhub`
  - `@playwright/mcp@latest`

这保持 fail-fast：任意 npm 包不会因为写进 MCP 配置就被 provider 拉起。

### Toolbridge 工具命名空间

`internal/platform/toolbridge/handler_peer_decode_helpers.go` 新增外部 MCP 工具冲突处理：

- LSP 和 orchestration 仍保持严格的内置工具重复检测。
- 普通第三方 MCP server 如果裸工具名已被占用，会回退成：

```text
mcp__<serverName>__<toolName>
```

典型场景：

- `sqlite.query` 和 `postgres.query` 都暴露 `query`。
- 第一个可以继续用短名 `query`。
- 后续冲突工具会保留命名空间调用名，例如 `mcp__postgres__query`。

这样既保留短名体验，也避免同名第三方工具互相挤掉。

### 前端 API facade

`frontend-app/src/shared/api/backendApi.js` 新增 MCP server API：

- `listMCPServers` -> `mcpServer/list`
- `startSQLiteMCPServer` -> `mcpServer/sqlite/start`
- `stopSQLiteMCPServer` -> `mcpServer/sqlite/stop`
- `startPlaywrightMCPServer` -> `mcpServer/playwright/start`
- `stopPlaywrightMCPServer` -> `mcpServer/playwright/stop`

这些前端 facade 目前都使用严格空 payload：

```js
{}
```

如果调用方传入多余参数，会在前端直接报错，不会继续调用后端。

`frontend-app/src/shared/api/backendApi.contractMatrix.js` 同步登记这些 RPC，标注为 `mcpServer` 域。

### 技能页 MCP 工具面板

`frontend-app/src/pages/skills/SkillsPage.jsx` 中，“插件”页改为 MCP 工具管理页：

- 展示 SQLite MCP 和 Playwright MCP 两张控制卡片。
- 通过 `mcpServer/list` 读取当前配置状态。
- 支持“开启”和“关闭”操作。
- 操作完成后用 `queryClient.setQueryData` 合并结果，避免等待下一轮刷新才看到状态。
- 未选择项目时禁用操作并显示提示。
- 读取失败或启停失败时展示可读错误。

`frontend-app/src/pages/skills/SkillsPageHub.css` 新增 MCP 工具卡片样式，移动端会把按钮改为单列，避免文本挤压。

### Mermaid 和附件回归

Mermaid 渲染补了两个稳定性点：

- `MermaidDiagram.jsx` 初始化 Mermaid 时设置 `htmlLabels: false`，避免 HTML label 输出非 XML 兼容的 `<br>` 影响 SVG 清理。
- `markdownMermaidModel.js` 在 SVG 清理阶段，如果 `width` 或 `height` 是百分比，会从 `viewBox` 提取具体尺寸，作为图片 data URL 使用时不再丢失布局尺寸。

同时补充了聊天附件相关回归测试，覆盖：

- Codex 剪贴板临时图片路径。
- 普通本地截图路径。
- path-only 图片附件在时间线里的预览行为。

## 主要文件

后端核心：

- `internal/contract/mcp_control.go`
- `internal/contract/prompt.go`
- `internal/module/mcp_server/config_helpers.go`
- `internal/module/mcp_server/sqlite.go`
- `internal/module/mcp_server/playwright.go`
- `internal/module/mcp_server/service.go`
- `internal/module/mcp_server/rpc.go`
- `internal/module/mcp_server_npx/service.go`
- `internal/module/mcp_server_npx/rpc.go`
- `internal/store/mcpserver/store.go`

provider / 注入链路：

- `internal/module/thread/mcp_server_config.go`
- `internal/module/thread/start_session_helpers.go`
- `internal/module/turn/service_helpers.go`
- `internal/provider/claudecli/transport_config.go`
- `internal/platform/toolbridge/handler_peer_decode.go`
- `internal/platform/toolbridge/handler_peer_decode_helpers.go`

前端：

- `frontend-app/src/shared/api/backendApi.js`
- `frontend-app/src/shared/api/backendApi.contractMatrix.js`
- `frontend-app/src/pages/skills/SkillsPage.jsx`
- `frontend-app/src/pages/skills/SkillsPageHub.css`
- `frontend-app/src/pages/chat/components/MermaidDiagram.jsx`
- `frontend-app/src/pages/chat/components/markdownMermaidModel.js`

## 回归测试覆盖

后端新增或扩展的关键测试：

- `TestStartSQLiteServerAddsDefaultNPXConfigAndEnablesIt`
- `TestStopSQLiteServerDisablesDefaultConfigWithoutDeletingIt`
- `TestStartSQLiteServerResolvesDatabasePathFromSuperDolphinHome`
- `TestStartSQLiteServerMigratesLegacyNPXPackageConfig`
- `TestMCPServerConfigProviderMigratesLegacySQLitePackageForChat`
- `TestStartPlaywrightServerAddsDefaultNPXConfigAndEnablesIt`
- `TestStartPlaywrightServerReenablesExistingConfigWithoutDeletingIt`
- `TestStopPlaywrightServerDisablesDefaultConfigWithoutDeletingIt`
- `TestStartSQLiteRPCCreatesDefaultNPXConfig`
- `TestStopSQLiteRPCDisablesDefaultConfig`
- `TestStartPlaywrightRPCCreatesDefaultNPXConfig`
- `TestStopPlaywrightRPCDisablesDefaultConfig`
- `TestConfigStorePersistsEnabledState`
- `TestMCPServerConfigProviderSkipsDisabledRows`
- `TestMergeConfiguredMCPServersSkipsDisabledSQLiteServer`
- `TestPrepareTurnSkipsDisabledConfiguredMCPServers`
- `TestMergeConfiguredMCPServersKeepsStdioConfigWhenNameAlreadyPresent`
- `TestMergeTurnConfiguredMCPServersKeepsStdioConfigWhenNameAlreadyPresent`
- `TestWriteManifestConfigIncludesAllowedNPXSQLiteStdioServer`
- `TestWriteManifestConfigIncludesAllowedNPXPlaywrightStdioServer`
- `TestConfigMCPBinariesAcceptsNPXSQLiteStdioServer`
- `TestConfigMCPBinariesAcceptsNPXPlaywrightStdioServer`

前端新增或扩展的关键测试：

- `wraps MCP server list and default controls with strict empty payloads`
- `renders default MCP controls and sends the start and stop RPC actions`
- `adds concrete dimensions to Mermaid SVGs that only expose a percentage width`
- `renders flowchart labels that use HTML line breaks`
- `renders Codex clipboard temp images as image previews`
- `renders normal local screenshot paths through the local image route`

## 建议验证

这份日志是根据当前工作区 diff 补写的记录；补日志时未重新执行完整代码验证。建议在提交前按实际改动面执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal\contract\mcp_control.go
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal\module\mcp_server\sqlite.go
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal\module\mcp_server\playwright.go
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal\store\mcpserver\store.go
```

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/mcp_server -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/mcp_server_npx -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/store/mcpserver -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/thread -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/turn -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/provider/claudecli -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/platform/toolbridge -count=1
```

```powershell
cd frontend-app
npm run lint
npm test
npm run build
```

## 使用注意

- SQLite/Playwright 的 start/stop 只修改当前工作区 MCP server 配置，不会立即强制重启已有 provider 会话。
- 关闭后的配置仍会保留在表中，后续 start 会复用或重新启用。
- provider 注入只读取 enabled 状态为 true 的配置。
- 第三方 MCP 工具同名时，优先保留短名；短名冲突的工具需要通过 `mcp__server__tool` 形式调用。
- 前端当前不支持传自定义 SQLite database path；页面入口只发 `{}`，实际路径由后端运行时配置或环境变量解析。
