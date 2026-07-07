package cron

import "go.uber.org/fx"

// Module 将 cron Store 接入 Fx 图，供上层调度、续租和恢复模块按接口注入。
var Module = fx.Module("store.cron",
	fx.Provide(newStoreWithDB),
)
