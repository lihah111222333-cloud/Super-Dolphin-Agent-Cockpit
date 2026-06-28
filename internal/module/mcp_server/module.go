// Package mcpserver 管理 MCP server 配置的增删查以及内置 postgres、sqlite、playwright server 的启停。
// 写入路径：配置持久化到 MCPServerConfigStore；读取路径：service 从 store 组装 ListServersResult。
// 该包不启动 MCP server 进程，仅负责生成 provider 可消费的 stdio/http 配置。
package mcpserver

import "go.uber.org/fx"

// Module 将 MCP server 服务、配置 provider 和 RPC handler 注入 Fx 树。
var Module = fx.Module("mcp_server",
	fx.Provide(
		NewServiceWithStoresAndConfig,
		AsMCPServerConfigProvider,
		NewHandlers,
	),
)
