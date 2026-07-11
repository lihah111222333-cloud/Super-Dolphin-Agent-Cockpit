package dashboardadapter

import "go.uber.org/fx"

// Module 提供 dashboard 领域拥有的九个 Store adapter 端口。
var Module = fx.Module("app.storeadapter.dashboard",
	fx.Provide(
		provideDashboardAgentStatusReader,
		provideDashboardAILogReader,
		provideDashboardAuditLogReader,
		provideDashboardBusLogReader,
		provideDashboardSystemLogReader,
		provideDashboardDBQueryExecutor,
		provideDashboardCommandCardReader,
		provideDashboardPromptTemplateReader,
		provideDashboardSharedFileReader,
	),
)
