package tasktrace

import "go.uber.org/fx"

var Module = fx.Module("store.tasktrace",
	fx.Provide(NewStore),
)
