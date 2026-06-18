# 修改记录 2026-06-15 22-34 mcpServer postgres start 支持 stdio 持久化

## 背景

调用默认 PostgreSQL MCP server 启动入口时，收到错误：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32098,
    "message": "mcp_server: unsupported transport: stdio"
  }
}
```

该入口的正确 JSON-RPC 请求是：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "mcpServer/postgres/start",
  "params": {}
}
```

`params` 不需要传 transport。后端会写入默认 `postgres` MCP server 配置，真实的 `npx @modelcontextprotocol/server-postgres ...` 进程由后续 provider 会话按配置拉起。

## 根因

上层模块已经支持默认 PostgreSQL stdio 配置：

- `internal/module/mcp_server/postgres.go`
  - `defaultPostgresServerConfig()` 返回 `transport: "stdio"`、`command: "npx"` 和 postgres server args。
- `internal/module/mcp_server/service.go`
  - `normalizeServerConfig` 已经支持 `http` 与 `stdio`。

实际失败点在持久化层：

- `internal/store/mcpserver/store.go`
  - 旧实现只允许 `transport == "http"`。
  - 表结构只保存 `url` 和 `headers`。
  - `url` 不能为空的约束会天然排斥 stdio 配置。

所以请求格式本身没错，是 store 层仍停留在 HTTP-only 时代，导致默认 postgres stdio 配置落库前被 fail-fast 拦截。

## 行为调整

`mcp_server` 配置存储现在同时支持两类 transport：

### HTTP

HTTP 配置继续保存：

```json
{
  "transport": "http",
  "url": "https://example.test/mcp",
  "headers": {
    "Authorization": "Bearer token"
  }
}
```

校验规则保持 fail-fast：

- `url` 不能为空。
- `url` 必须是 `http` 或 `https`，且必须带 host。
- header key/value 不能为空。
- HTTP 配置不会保存 stdio 的 `command/args/env`。

### stdio

stdio 配置现在可以保存：

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

校验规则：

- `command` 不能为空。
- `args` 中不能出现空字符串。
- `env` 的 key/value 不能为空。
- stdio 配置不会保存 HTTP 的 `url/headers`。

## 数据库存储变化

`public.mcp_server_configs` 新增 stdio 相关字段：

```sql
command text NOT NULL DEFAULT ''
args jsonb NOT NULL DEFAULT '[]'::jsonb
env jsonb NOT NULL DEFAULT '{}'::jsonb
```

表约束调整为按 transport 检查有效 payload：

```sql
CONSTRAINT mcp_server_configs_transport_payload_check CHECK (
  (transport = 'http' AND url <> '' AND command = '')
  OR (transport = 'stdio' AND url = '' AND command <> '')
)
```

为了兼容旧表，`ensureTable` 会执行幂等迁移：

- 补齐 `command/args/env` 字段。
- 移除旧的 `url <> ''` 单列约束。
- 重新建立 transport payload 约束。
- 为 `args` 和 `env` 添加 jsonb 类型约束。

## 主要代码位置

### `internal/store/mcpserver/store.go`

- `InsertServer`
  - 写入 `transport/url/headers/command/args/env`。
- `ListServers`
  - 读取 `command/args/env` 并还原为 `contract.MCPServerConfig`。
- `normalizeServerConfig`
  - 按 `http` 与 `stdio` 分支校验配置。
- `scanMCPServerConfigRow`
  - 把数据库行还原成可用配置，损坏 JSON 或旧脏数据会 fail-fast。
- `migrateMCPServerConfigsTableSQL`
  - 对已有表执行幂等 schema 补齐。

### `internal/store/mcpserver/store_test.go`

新增回归测试：

- `TestConfigStoreInsertPersistsStdioServerConfig`
  - 覆盖 stdio 配置可以写入，并检查 `command/args/env` 参数。
- `TestConfigStoreListReturnsStdioServerConfig`
  - 覆盖 stdio 配置可以从数据库行还原。

同时测试文件改成外部包 `mcpserver_test`，通过公开构造函数 `NewMCPServerConfigStore` 验证行为，避免单文件守卫只编译测试文件时依赖包内私有类型。

## 已执行验证

先用新增回归测试确认旧逻辑会失败：

```powershell
go test ./internal/store/mcpserver -run 'TestConfigStore(InsertPersistsStdioServerConfig|ListReturnsStdioServerConfig)' -count=1
```

旧逻辑失败原因：

```text
mcp_server: unsupported transport: stdio
```

修复后执行过以下验证：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal\store\mcpserver\store.go
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal\store\mcpserver\store_test.go
```

```powershell
go test ./internal/store/mcpserver -count=1
go test ./internal/module/mcp_server -run 'Test(StartPostgresRPC|StartPostgresServerAddsDefaultStdioConfigOnExplicitCall)' -count=1
```

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/store/mcpserver -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/mcp_server -count=1
git diff --check
```

验证结果：

```text
代码守卫: 全部通过
ok github.com/anthropic-ai/super-agent-v3/internal/store/mcpserver
ok github.com/anthropic-ai/super-agent-v3/internal/module/mcp_server
```

当前环境里 `pwsh` 不在 PATH，因此使用 Windows 自带 `powershell` 跑 `.ps1` 守卫入口。

`git diff --check` 仅出现 Windows LF/CRLF 提示，没有 whitespace 错误。

## 使用注意

- `mcpServer/postgres/start` 只负责写入默认 `postgres` stdio 配置。
- 如果返回 `added: false`，说明当前工作区已经存在名为 `postgres` 的 MCP server 配置。
- 写入后需要启动新的 provider 会话，MCP stdio 进程才会按配置启动。
- 默认数据库地址仍是：

```text
postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable
```
