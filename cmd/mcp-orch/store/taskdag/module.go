package taskdag

import "go.uber.org/fx"

var Module = fx.Module("store.taskdag",
	fx.Provide(
		NewStore,
		ProvideOrchestrationStore,
	),
)

func ProvideOrchestrationStore(store Store) OrchestrationStore { return store }
