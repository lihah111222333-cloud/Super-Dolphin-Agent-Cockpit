package thread

import (
	"go.uber.org/fx"
)

// Module 注册线程持久化 store 及其跨模块只读适配器。
var Module = fx.Module("store.thread",
	fx.Provide(NewStoreWithDB),
	fx.Provide(NewMetadataStore),
	fx.Provide(NewSessionThreadLookup),
)
