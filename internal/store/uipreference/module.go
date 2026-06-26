package uipreference

import "go.uber.org/fx"

// Module 注册 UI preference store 到 Fx 容器。
var Module = fx.Module("store.uipreference",
	fx.Provide(NewStore),
)
