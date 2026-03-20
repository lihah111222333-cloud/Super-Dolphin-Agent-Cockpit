package buslog

import "go.uber.org/fx"

var Module = fx.Module("store.buslog",
	fx.Provide(NewStore),
)
