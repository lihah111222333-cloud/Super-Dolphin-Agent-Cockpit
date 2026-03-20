package interaction

import "go.uber.org/fx"

var Module = fx.Module("store.interaction",
	fx.Provide(NewStore),
)
