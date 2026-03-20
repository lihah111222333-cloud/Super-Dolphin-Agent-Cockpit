package cwdlock

import "go.uber.org/fx"

var Module = fx.Module("store.cwdlock",
	fx.Provide(NewStore),
)
