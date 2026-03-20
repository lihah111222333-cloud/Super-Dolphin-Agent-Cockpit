package uipreference

import "go.uber.org/fx"

var Module = fx.Module("store.uipreference",
	fx.Provide(NewStore),
)
