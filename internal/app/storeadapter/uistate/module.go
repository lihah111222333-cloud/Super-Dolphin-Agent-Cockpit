package uistateadapter

import "go.uber.org/fx"

// Module 提供 uistate 领域拥有的 Store adapter。
var Module = fx.Module("app.storeadapter.uistate",
	fx.Provide(
		provideUIStatePreferenceStore,
		provideUIStateSharedFileReader,
		provideUIStateBindingLookup,
	),
)
