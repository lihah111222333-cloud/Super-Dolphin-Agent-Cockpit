package skillcandidate

import "go.uber.org/fx"

// Module wires the skill candidate Store into the core Fx tree.
// Consumers: P0b extractor (Step 4) inserts; review gate (Step 5)
// reads / approves / rejects / promotes.
var Module = fx.Module("store.skillcandidate",
	fx.Provide(NewStore),
)
