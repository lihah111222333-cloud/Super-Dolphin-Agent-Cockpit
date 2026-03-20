package topologyapproval

import "go.uber.org/fx"

var Module = fx.Module("store.topologyapproval",
	fx.Provide(NewStore),
)
