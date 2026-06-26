package commandcard

import "go.uber.org/fx"

// Module 注册命令卡片只读 Store，供内部模块和 orchestration peer 查询可用命令。
var Module = fx.Module("store.commandcard",
	fx.Provide(NewStore),
)
