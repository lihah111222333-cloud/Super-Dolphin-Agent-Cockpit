package datasourcev2adapter

import "go.uber.org/fx"

// Module 提供 datasource_v2 领域拥有的 Store adapter。
var Module = fx.Module("app.storeadapter.datasourcev2",
	fx.Provide(provideDatasourceV2Store),
)
