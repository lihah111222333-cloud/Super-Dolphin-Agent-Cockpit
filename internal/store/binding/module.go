package binding

import (
	"go.uber.org/fx"
)

// Module 注册 binding Store 及 session 恢复所需的只读和写入适配器。
var Module = fx.Module("store.binding",
	fx.Provide(NewStore),
	fx.Provide(NewSessionBindingLookup),
	fx.Provide(NewSessionBindingUpserter),
)
