package taskack

import "go.uber.org/fx"

var Module = fx.Module("store.taskack",
	fx.Provide(NewStore),
)
