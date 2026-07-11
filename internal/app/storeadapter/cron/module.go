package cronadapter

import "go.uber.org/fx"

// Module 提供 cron 领域拥有的共享 Store adapter 与两个端口投影。
var Module = fx.Module("app.storeadapter.cron",
	fx.Provide(
		newCronStoreAdapter,
		provideCronStore,
		provideCronSchedulerStore,
	),
)
