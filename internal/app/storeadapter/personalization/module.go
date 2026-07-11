package personalizationadapter

import "go.uber.org/fx"

// Module 提供 personalization 领域拥有的 Store adapter。
var Module = fx.Module("app.storeadapter.personalization",
	fx.Provide(providePersonalizationPreferenceStore),
)
