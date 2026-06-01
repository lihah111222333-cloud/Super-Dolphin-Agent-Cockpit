package observability

import "go.uber.org/fx"

var Module = fx.Module("module.observability",
	fx.Provide(NewHandlers),
)
