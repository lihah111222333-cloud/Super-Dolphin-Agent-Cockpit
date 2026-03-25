package commandcard

import "go.uber.org/fx"

var Module = fx.Module("store.commandcard",
	fx.Provide(NewStore),
)
