package accesswait

import "go.uber.org/fx"

// Module 注册 access/wait 的唯一持久化 owner；读端口由根图显式绑定。
var Module = fx.Module("store.accesswait",
	fx.Provide(NewStore),
)
