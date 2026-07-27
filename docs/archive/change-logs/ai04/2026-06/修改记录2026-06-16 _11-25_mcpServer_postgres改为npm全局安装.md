# 修改记录 2026-06-16 11-25 mcpServer postgres 改为 npm 全局安装

## 背景

默认 PostgreSQL MCP server 之前写入的是 `npx` 启动配置：

```json
{
  "transport": "stdio",
  "command": "npx",
  "args": [
    "-y",
    "@modelcontextprotocol/server-postgres",
    "postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable"
  ]
}
```

这个方式每次 provider 按配置拉起 stdio server 时，都可能重新走 `npx` 的包解析/拉取流程。目标是改成一次性全局安装 npm 包，后续配置直接启动本地命令，避免重复拉取。

通过 `npm view @modelcontextprotocol/server-postgres bin --json` 确认该包暴露的全局命令为：

```json
{
  "mcp-server-postgres": "dist/index.js"
}
```

## 新行为

调用 `mcpServer/postgres/start` 时：

1. 先检查 `mcp-server-postgres` 是否已经在 `PATH`。
2. 如果不存在，再检查 `npm` 是否可用。
3. 执行：

```powershell
npm install -g @modelcontextprotocol/server-postgres
```

4. 安装后再次检查 `mcp-server-postgres` 是否进入 `PATH`。
5. 全部通过后，写入默认 MCP server 配置。

写入后的默认配置变为：

```json
{
  "transport": "stdio",
  "command": "mcp-server-postgres",
  "args": [
    "postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable"
  ]
}
```

如果 `npm` 不存在、安装失败、或安装后命令仍不可见，接口会直接返回错误，不会静默写入一个后续无法启动的配置。

## 兼容旧配置

如果当前工作区已经存在名为 `postgres` 的 MCP server：

- 如果它是用户自定义配置，例如 `command: "custom-postgres-mcp"`，保持不覆盖，直接返回现有配置。
- 如果它完全匹配旧默认值 `npx -y @modelcontextprotocol/server-postgres <database-url>`，则迁移为新的 `mcp-server-postgres <database-url>` 配置。

这样既能完成默认入口迁移，也避免误改用户手写的 MCP server。

## 主要代码位置

### `internal/module/mcp_server/postgres.go`

- `defaultPostgresServerConfig`
  - 默认命令从 `npx` 改为 `mcp-server-postgres`。
  - args 只保留 PostgreSQL 连接串。
- `StartPostgresServer`
  - 新增安装前置检查。
  - 发现旧默认 npx 配置时，迁移到新配置。
- `isLegacyDefaultPostgresServerConfig`
  - 只识别完全匹配旧默认值的配置。
- `replaceDefaultPostgresServer`
  - 删除旧默认配置并重新插入新配置。

### `internal/module/mcp_server/postgres_installer.go`

新增 npm 全局安装器：

- `LookPath("mcp-server-postgres")` 成功时直接返回。
- 命令不存在时执行 `npm install -g @modelcontextprotocol/server-postgres`。
- 安装后再次 `LookPath("mcp-server-postgres")`，失败则返回错误。

### `internal/module/mcp_server_npx/service.go`

兼容包不再自己拼默认配置，而是委托 `internal/module/mcp_server` 的 `StartPostgresServer` 和 `DefaultPostgresServerConfig`。

这样可以避免 `mcp_server` 和 `mcp_server_npx` 两个入口默认值分叉。

### `internal/store/mcpserver/store.go`

真实持久化层补齐 stdio 配置支持：

- 表字段新增 `command`、`args`、`env`。
- `ListServers` 读取 stdio 字段并还原为 `contract.MCPServerConfig`。
- `normalizeServerConfig` 按 `http` / `stdio` 分支校验。
- `ensureTableShape` 发现旧 HTTP-only 表时重建表结构，并保留已有 HTTP 行。

这一步是必要的，否则默认 postgres stdio 配置即使上层生成正确，也无法真实落库。

### provider / manifest 相关测试

同步更新这些测试中的默认配置形态：

- `internal/provider/shared/mcp_stdio_config_test.go`
- `internal/provider/claudecli/transport_config_test.go`
- `internal/module/turn/mcp_server_npx_config_test.go`
- `internal/module/thread/mcp_server_npx_config_test.go`

`mcp-server-postgres` 以 `mcp-` 开头，仍符合 Claude stdio MCP allowlist 的 managed MCP command 规则。

## 回归测试

新增或更新的重点覆盖：

- 已安装 `mcp-server-postgres` 时不执行 npm install。
- 未安装时执行 `npm install -g @modelcontextprotocol/server-postgres`。
- 安装后命令仍不可见时返回错误。
- 新增默认 postgres 配置前会调用安装器。
- 已有自定义 postgres 配置不会被覆盖。
- 旧默认 npx 配置会迁移到 `mcp-server-postgres`。
- SQLite store 可以保存并读取 stdio 的 `command/args/env`。
- 旧 HTTP-only `mcp_server_configs` 表会迁移到支持 stdio 的结构。

## 已执行验证

使用 Windows PowerShell 入口执行仓库 guard 和受影响包测试：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/mcp_server ./internal/module/mcp_server_npx ./internal/store/mcpserver ./internal/provider/shared ./internal/provider/claudecli ./internal/module/turn ./internal/module/thread -count=1
```

结果摘要：

```text
代码守卫: 全部通过
ok github.com/anthropic-ai/super-agent-v3/internal/module/mcp_server
ok github.com/anthropic-ai/super-agent-v3/internal/module/mcp_server_npx
ok github.com/anthropic-ai/super-agent-v3/internal/store/mcpserver
ok github.com/anthropic-ai/super-agent-v3/internal/provider/shared
ok github.com/anthropic-ai/super-agent-v3/internal/provider/claudecli
ok github.com/anthropic-ai/super-agent-v3/internal/module/turn
ok github.com/anthropic-ai/super-agent-v3/internal/module/thread
```

`internal/archtest/baseline.json` 没有变化。

## 使用注意

- `mcpServer/postgres/start` 只负责安装 npm 全局包并写入/迁移配置。
- 真正的 stdio MCP server 进程仍由后续 provider 会话按配置启动。
- 如果全局 npm bin 目录不在 `PATH`，安装成功后仍会报错，这是预期的 fail-fast 行为。
- 当前默认数据库连接串保持不变：

```text
postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable
```

## 2026-06-16 补充：Codex 每轮 chat 注入 dynamicTools

排查数据库只读查询工具调用失败时确认：

- 本机 `mcp-server-postgres` 已经在 `PATH` 中。
- 全局 npm 包为 `@modelcontextprotocol/server-postgres@0.6.2`。
- 因此当前失败不是命令未安装导致，而是 Codex provider 之前只在 `thread/start` 传 `dynamicTools`，后续 `turn/start` 没有继续把工具 schema 发给 Codex。

本次补充改动：

- `internal/provider/codexapp/session.go`
  - `StartTurn` 会在每次 `turn/start` 前准备 dynamic tools。
  - 返回的 tools 会写入 `turn/start.dynamicTools`，让模型每轮 chat 都能看到 PG MCP 暴露的 `query` 等工具。
- `internal/provider/codexapp/session_turn.go`
  - 使用本轮 `dto.TurnRequest.MCP` 生成工具面 scope，确保项目级 MCP server 配置能进入本轮工具声明。
- `internal/provider/codexapp/toolsurface/turn.go`
  - 新增小包承载 turn 级工具面准备逻辑，避免 `codexapp` 包继续超过代码行数守卫。
- `internal/provider/codexapp/session_turn_test.go`
  - 新增回归测试，覆盖 `postgres -> mcp-server-postgres -> query dynamicTool -> turn/start.dynamicTools` 这条链路。

补充验证命令：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/mcp_server ./internal/module/mcp_server_npx ./internal/store/mcpserver ./internal/provider/shared ./internal/provider/claudecli ./internal/module/turn ./internal/module/thread ./internal/provider/codexapp ./internal/provider/codexapp/toolsurface -count=1
```

结果：代码守卫、archtest、上述所有受影响包测试均通过。
