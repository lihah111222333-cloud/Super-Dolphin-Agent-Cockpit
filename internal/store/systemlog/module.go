package systemlog

import "go.uber.org/fx"

var Module = fx.Module("store.systemlog",
	fx.Provide(NewStore),
)
