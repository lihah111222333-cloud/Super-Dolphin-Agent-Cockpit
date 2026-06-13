package datasource

import "go.uber.org/fx"

var Module = fx.Module("datasource",
	fx.Provide(
		NewService,
		NewHandlers,
	),
)
