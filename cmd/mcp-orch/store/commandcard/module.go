package commandcard

import "go.uber.org/fx"

// Module 注册 commandcard store 的 fx provider。
var Module = fx.Module("store.commandcard",
	fx.Provide(NewStore),
)
