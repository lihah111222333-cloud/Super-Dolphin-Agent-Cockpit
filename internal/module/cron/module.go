package cron

import "go.uber.org/fx"

// Module wires the cron service + host RPC handlers into the core Fx tree.
// The scheduler / runner actors that actually tick jobs will be added in a
// follow-up PR (Track C phase 2b).
var Module = fx.Module("cron",
	fx.Provide(NewService),
	fx.Provide(NewHandlers),
)
