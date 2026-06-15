package mcpservernpx

import "go.uber.org/fx"

// Module 注册 npx MCP server 的显式启动入口，不再自动改写项目配置 provider。
var Module = fx.Module("mcp_server_npx",
	fx.Provide(
		NewService,
		NewHandlers,
	),
)
