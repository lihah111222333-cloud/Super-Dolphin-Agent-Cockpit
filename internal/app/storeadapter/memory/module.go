package memoryadapter

import "go.uber.org/fx"

// Module 提供 memory 领域拥有的 shared-file Store adapter。
var Module = fx.Module("app.storeadapter.memory",
	fx.Provide(
		provideMemorySharedFileReader,
		provideMemorySharedFileDeleter,
	),
)
