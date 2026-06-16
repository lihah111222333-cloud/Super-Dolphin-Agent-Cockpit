package feedback

import "go.uber.org/fx"

// Module wires feedback storage and RPC handlers into the application graph.
var Module = fx.Module("feedback",
	fx.Provide(NewService),
	fx.Provide(NewHandlers),
)
