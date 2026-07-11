package turnadapter

import "go.uber.org/fx"

// Module 提供 turn 领域拥有的 optional Store adapter。
var Module = fx.Module("app.storeadapter.turn",
	fx.Provide(
		fx.Annotate(
			provideTurnDedupeStore,
			fx.ParamTags(`optional:"true"`),
		),
	),
)
