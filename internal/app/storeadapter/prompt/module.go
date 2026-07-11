package promptadapter

import "go.uber.org/fx"

// Module 提供 prompt 领域拥有的 Store adapter。
var Module = fx.Module("app.storeadapter.prompt",
	fx.Provide(
		providePromptStore,
		providePromptPreferenceReader,
		providePromptSharedFileReader,
	),
)
