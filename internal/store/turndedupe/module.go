package turndedupe

import "go.uber.org/fx"

// Module wires Store into the core Fx tree. The turn.Service consumes
// this store via an fx optional dependency so deployments without the
// registry table continue operating on the in-memory tracker only.
var Module = fx.Module("store.turndedupe",
	fx.Provide(NewStore),
)
