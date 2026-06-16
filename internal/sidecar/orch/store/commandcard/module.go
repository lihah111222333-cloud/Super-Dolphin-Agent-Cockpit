package commandcard

import "go.uber.org/fx"

// Module is part of the commandcard package API.
var Module = fx.Module("store.commandcard",
	fx.Provide(NewStore),
)
