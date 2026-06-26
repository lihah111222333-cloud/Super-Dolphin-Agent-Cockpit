package turndedupe

import "go.uber.org/fx"

// Module 注册 turn 去重注册表 store。
// turn.Service 通过可选依赖消费它，缺表部署仍能只依赖进程内去重状态运行。
var Module = fx.Module("store.turndedupe",
	fx.Provide(NewStore),
)
