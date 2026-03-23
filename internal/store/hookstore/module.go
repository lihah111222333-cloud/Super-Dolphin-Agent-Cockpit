package hookstore

import "go.uber.org/fx"

var Module = fx.Module("store.hookstore",
	fx.Provide(NewStore),
)
