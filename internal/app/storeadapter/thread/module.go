package threadadapter

import "go.uber.org/fx"

// Module 提供 Thread 与 threadprompt 领域拥有的 Store adapter，并在根 scope 注册动态 prompt providers。
var Module = fx.Options(
	fx.Module("threadadapter",
		fx.Provide(
			provideThreadStoreAdapter,
			provideThreadBindingStoreAdapter,
			provideThreadPromptStoreAdapter,
			provideThreadPromptRuntimeCatalog,
			provideThreadPromptCatalog,
		),
	),
	fx.Invoke(registerThreadPromptProvidersFromApp),
)
