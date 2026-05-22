package e2efixture

import (
	"go.uber.org/fx"
)

var Module = fx.Module("provider.e2efixture",
	fx.Provide(
		fx.Annotate(provideDreamExecutorProvider, fx.ResultTags(`group:"dream_executors"`)),
	),
)
