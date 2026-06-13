package mcpserver

import "go.uber.org/fx"

var Module = fx.Module("mcp_server",
	fx.Provide(
		NewMCPServerConfigStore,
		NewServiceWithStore,
		AsMCPServerConfigProvider,
		NewHandlers,
	),
)
