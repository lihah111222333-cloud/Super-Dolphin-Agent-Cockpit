# 修改记录 2026-06-15 15-38 HTTP MCP tools 进入 Codex 动态工具请求

## 背景

本次工作补齐了两段链路：

- 在 `internal/module/mcp_server` 中，根据已保存的 HTTP MCP server 地址主动请求远端 `tools/list`，让前端或调用方可以预览 server 暴露的工具。
- 在 Codex provider 启动 thread 前，把配置里的 HTTP MCP server 转成 toolbridge 可消费的工具面，并把远端 tools 放入 Codex `thread/start` 请求的 `dynamicTools`。

随后在真实连接中遇到过以下错误：

```text
thread: establish session: toolbridge: HTTP MCP initialize returned HTTP 406:
{"jsonrpc":"2.0","id":"server-error","error":{"code":-32600,"message":"Not Acceptable: Client must accept both application/json and text/event-stream"}}
```

根因是 HTTP MCP 客户端最初只发送了 `Accept: application/json`。Streamable HTTP MCP 要求 POST 请求的 `Accept` 同时声明 `application/json` 和 `text/event-stream`，否则部分 server 会直接返回 406。

参考规范：

- https://modelcontextprotocol.io/specification/2025-11-25/basic/transports

## 行为变化

### `mcpServer/tools` 拉取远端工具

新增 RPC 方法：

```text
mcpServer/tools
```

请求参数：

```json
{
  "serverName": "my-search"
}
```

后端处理顺序：

1. 按当前工作目录查找 `.agent/mcp_server/config.json`。
2. 读取 `mcpServers[serverName]`。
3. 校验该 server 必须是 HTTP transport，并且 URL、header 都合法。
4. 对远端 MCP endpoint 依次发送：
   - `initialize`
   - `notifications/initialized`
   - `tools/list`
5. 返回远端 `tools/list` 中的工具 schema。

返回示例：

```json
{
  "serverName": "my-search",
  "tools": [
    {
      "name": "remote_search",
      "description": "search remote documents",
      "inputSchema": {
        "type": "object"
      }
    }
  ]
}
```

### HTTP MCP tools 进入 Codex `dynamicTools`

Codex thread 启动前会构建动态工具面。现在 `req.Config` 中的 `mcpConfig` 会被解析为 provider manifest 的 `ExtraBinaries`：

```json
{
  "mcpConfig": {
    "mcpServers": {
      "my-search": {
        "transport": "http",
        "url": "https://example.test/mcp",
        "headers": {
          "Authorization": "Bearer token"
        }
      }
    }
  }
}
```

转换后的 manifest binary：

```json
{
  "name": "my-search",
  "type": "http",
  "url": "https://example.test/mcp",
  "headers": {
    "Authorization": "Bearer token"
  }
}
```

toolbridge 在准备 Codex 工具面时会对 HTTP binary 建立 HTTP MCP client，执行 `tools/list`，再把结果转换成 Codex `dynamicTools`。因此 `thread/start` 请求中可以带上远端 MCP server 暴露的工具。

整体链路：

```text
mcpConfig
  -> providershared.ConfigMCPBinaries
  -> Manifest.ExtraBinaries
  -> contract.BuildManifest
  -> toolbridge.PrepareCodexToolSurface
  -> HTTP MCP initialize / tools/list
  -> Codex thread/start dynamicTools
```

### 工具调用链路

Codex 选择动态工具后，toolbridge 会把调用转发给对应 HTTP MCP server 的 `tools/call`，并注入可信元数据：

```json
{
  "name": "remote_search",
  "arguments": {
    "query": "hello"
  },
  "_agentId": "agent-http",
  "_threadId": "provider-thread-http",
  "_callId": "call-http",
  "_cwd": "D:\\Super-Dolphin",
  "_workspaceRoots": ["D:\\Super-Dolphin"]
}
```

这些 `_agentId`、`_threadId`、`_callId`、`_cwd`、`_workspaceRoots` 由 host 侧注入，不信任模型 payload 中的同名业务字段。

## Streamable HTTP 兼容点

HTTP MCP 客户端现在遵守以下规则：

- POST 请求固定发送：

```http
Accept: application/json, text/event-stream
Content-Type: application/json
```

- `initialize` 成功后，如果响应头里有 `Mcp-Session-Id`，后续请求会继续带上该 header。
- `initialize` 返回的 `protocolVersion` 会作为后续请求的 `MCP-Protocol-Version`。
- 对于普通 JSON 响应，直接解析 JSON-RPC envelope。
- 对于 `Content-Type: text/event-stream` 响应，会读取 SSE `data:` 事件，并选择当前 JSON-RPC request id 对应的响应。
- HTTP 非 2xx、JSON-RPC error、空 result、非法 session id 都会 fail-fast 返回错误。

这修复了只接受 JSON 导致的 406 问题，也避免了某些需要 session header 的 server 在 `notifications/initialized` 或 `tools/list` 阶段返回 400。

## 主要代码位置

### `internal/module/mcp_server`

- `rpc.go`
  - 注册 `mcpServer/tools`。
- `service.go`
  - 增加 `ListServerToolsRequest`、`ListServerToolsResult` 和 `ListServerTools`。
- `http_tools.go`
  - 实现 HTTP MCP 初始化、`tools/list` 请求、HTTP/JSON-RPC 错误处理和 tools 响应解析。
  - `Accept` 使用 `application/json, text/event-stream`。

### `internal/provider/codexapp`

- `support.go`
  - `codexToolSurfaceScope` 从 `req.Config` 中解析 `mcpConfig`。
  - 把 HTTP MCP server 放入 `ManifestContext.ExtraBinaries`。
  - `prepareStartDynamicTools` 在 `thread/start` 前准备动态工具。
- `mcp_config_dynamic_tools_test.go`
  - 覆盖 `mcpConfig` 转 HTTP manifest binary。
  - 覆盖 malformed `mcpConfig` fail-fast。

### `internal/platform/toolbridge`

- `stdio_mcp_client.go`
  - 默认 MCP client factory 识别 HTTP binary，并切到 HTTP MCP client。
- `http_mcp_client.go`
  - 实现 HTTP MCP client。
  - 支持 `initialize`、`notifications/initialized`、`tools/list`、`tools/call`。
  - 支持 Streamable HTTP 的 JSON 与 SSE 响应。
  - 支持 `Mcp-Session-Id` 和 `MCP-Protocol-Version`。
- `http_mcp_client_test.go`
  - 覆盖 HTTP server tools 进入 Codex dynamic tools。
  - 覆盖 HTTP MCP `tools/call` 元数据注入。
  - 覆盖 `Accept` 双类型要求。
  - 覆盖 SSE 格式 `tools/list`。

## 验证命令

实现后执行过以下验证：

```powershell
go run .\scripts\code_size_guard.go -- <changed go files>
```

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/platform/toolbridge ./internal/module/mcp_server ./internal/provider/codexapp -count=1
```

```powershell
git diff --check
```

验证结果：

```text
代码守卫: 全部通过
ok github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge
ok github.com/anthropic-ai/super-agent-v3/internal/module/mcp_server
ok github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp
```

`git diff --check` 仅出现 Windows LF/CRLF 提示，没有 whitespace 错误。

## 使用注意

- HTTP MCP server URL 应直接指向 MCP Streamable HTTP endpoint。
- 当前只支持 `transport: "http"` 的外部 MCP server 配置。
- header key/value 不能为空；鉴权 token 建议通过 header 传入。
- server 名称不能与内置 managed server `lsp`、`orch` 冲突。
- 如果远端 server 返回 `Mcp-Session-Id`，客户端会自动复用，不需要用户手动配置。
- 如果远端 server 只支持旧版 HTTP+SSE 的 separate endpoint 模式，本次实现没有做 legacy endpoint 自动探测。
