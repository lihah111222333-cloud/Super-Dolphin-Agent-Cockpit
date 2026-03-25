package sharedfile

import "go.uber.org/fx"

var Module = fx.Module("store.sharedfile",
	fx.Provide(NewStore),
)
