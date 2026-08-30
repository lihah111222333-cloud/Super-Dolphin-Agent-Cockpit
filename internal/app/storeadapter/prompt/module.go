package promptadapter

import "go.uber.org/fx"

// Module 提供 prompt runtime 所需的偏好与共享文件读取 adapter。
var Module = fx.Module("app.storeadapter.prompt",
	fx.Provide(
		providePromptPreferenceReader,
		providePromptSharedFileReader,
	),
)
