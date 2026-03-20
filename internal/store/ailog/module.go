package ailog

import "go.uber.org/fx"

var Module = fx.Module("store.ailog",
	fx.Provide(NewStore),
)
