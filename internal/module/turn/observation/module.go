package observation

import "go.uber.org/fx"

// Module wires the observation layer into the core Fx tree.
//
// Guarantees:
//   - Provides *Memory as both *Memory (for tests / direct introspection)
//     and Contract (the read/write facade consumers depend on).
//   - NewObservationSubscribers declares bus subscribers that push canonical
//     facts into the Memory; subscribers never read back.
//   - BusModule owns subscriber registration and shutdown via bus.subscribers.
//
// The module is intentionally single-purpose: it does not import turn /
// tracker packages. turn.Service may receive the Contract as an optional
// sink for PrepareTurn / StartTurn facts; P3 collector and P0b extractor
// consume the same Contract that this module provides.
var Module = fx.Module("module.turn.observation",
	fx.Provide(
		NewMemory,
		func(m *Memory) Contract { return m },
		NewObservationSubscribers,
	),
)
