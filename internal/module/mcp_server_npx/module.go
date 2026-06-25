// Package mcpservernpx 是 mcp_server 包中 postgres、sqlite、playwright 三个默认 npm MCP server
// 显式启停入口的兼容适配层。所有实际配置写入都委托给 mcp_server 模块，避免两个入口产生不同默认值。
package mcpservernpx

import "go.uber.org/fx"

// Module 注册 npm MCP server 的显式启动入口，不再自动改写项目配置 provider。
var Module = fx.Module("mcp_server_npx",
	fx.Provide(
		NewService,
		NewHandlers,
	),
)
