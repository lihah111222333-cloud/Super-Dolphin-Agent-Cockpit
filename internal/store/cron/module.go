package cron

import "go.uber.org/fx"

// Module wires Store into the core Fx tree. The scheduler / runner actors
// that consume this store live under internal/module/cron in phase 2.
var Module = fx.Module("store.cron",
	fx.Provide(NewStore),
)
