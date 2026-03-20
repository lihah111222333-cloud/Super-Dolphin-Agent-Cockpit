package dbquery

import "go.uber.org/fx"

var Module = fx.Module("store.dbquery",
	fx.Provide(NewStore),
)
