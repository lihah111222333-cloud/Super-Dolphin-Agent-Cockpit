package cwdlock

import "go.uber.org/fx"

// Module 注册 cwd lock Store，供本地实例竞争工作目录所有权。
var Module = fx.Module("store.cwdlock",
	fx.Provide(NewStore),
)
