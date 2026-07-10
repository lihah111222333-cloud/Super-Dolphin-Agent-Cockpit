package app

import "go.uber.org/fx"

// threadStoreAdaptersModule 为 Thread 与 threadprompt 的 Store adapter 预留独立装配接缝。
func threadStoreAdaptersModule() fx.Option {
	return fx.Options(
		fx.Provide(
			provideThreadStoreAdapter,
			provideThreadBindingStoreAdapter,
			provideThreadPromptStoreAdapter,
			provideThreadPromptRuntimeCatalog,
			provideThreadPromptCatalog,
		),
		fx.Invoke(registerThreadPromptProvidersFromApp),
	)
}
