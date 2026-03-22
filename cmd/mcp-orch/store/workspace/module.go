package workspace

import "go.uber.org/fx"

var Module = fx.Module("store.workspace",
	fx.Provide(NewStore),
)
