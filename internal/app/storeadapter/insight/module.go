package insightadapter

import "go.uber.org/fx"

// Module 提供 insight 领域拥有的 Store adapter。
var Module = fx.Module("app.storeadapter.insight",
	fx.Provide(
		provideInsightReader,
		provideInsightWriter,
	),
)
