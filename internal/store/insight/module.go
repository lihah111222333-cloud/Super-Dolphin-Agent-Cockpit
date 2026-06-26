package insight

import "go.uber.org/fx"

// Module 将 session insight 存储接入核心 Fx 图。
// 上层采集器和 dashboard API 只依赖 Store 接口，不直接接触 sqlc 查询。
var Module = fx.Module("store.insight",
	fx.Provide(NewStore),
)
