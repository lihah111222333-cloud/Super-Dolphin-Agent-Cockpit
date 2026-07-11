package app

import "go.uber.org/fx"

// businessStoreAdaptersModule 为其余业务模块的 Store adapter 预留独立装配接缝。
func businessStoreAdaptersModule() fx.Option {
	return fx.Options(
		fx.Provide(newCronStoreAdapter),
		fx.Provide(provideCronStore),
		fx.Provide(provideCronSchedulerStore),
		fx.Provide(providePromptStore),
		fx.Provide(providePromptPreferenceReader),
		fx.Provide(providePromptSharedFileReader),
		fx.Provide(provideDashboardAgentStatusReader),
		fx.Provide(provideDashboardAILogReader),
		fx.Provide(provideDashboardAuditLogReader),
		fx.Provide(provideDashboardBusLogReader),
		fx.Provide(provideDashboardSystemLogReader),
		fx.Provide(provideDashboardDBQueryExecutor),
		fx.Provide(provideDashboardCommandCardReader),
		fx.Provide(provideDashboardPromptTemplateReader),
		fx.Provide(provideDashboardSharedFileReader),
	)
}
