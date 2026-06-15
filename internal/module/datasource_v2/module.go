package datasourcev2

import "go.uber.org/fx"

// Module 注册 datasource_v2 的 service 和 JSON-RPC handler。
var Module = fx.Module("datasource_v2",
	fx.Provide(
		NewService,
		NewHandlers,
	),
)
