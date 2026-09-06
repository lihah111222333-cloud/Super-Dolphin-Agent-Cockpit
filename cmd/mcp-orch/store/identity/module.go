package identity

import "go.uber.org/fx"

// Module 注册 identity owner 的具体 Store 与跨模块窄接口。
var Module = fx.Module("store.identity",
	fx.Provide(
		NewStore,
		ProvideRepository,
	),
)

// ProvideRepository 把具体 identity Store 收窄为唯一身份 owner 接口。
func ProvideRepository(store *Store) IdentityRepository { return store }
