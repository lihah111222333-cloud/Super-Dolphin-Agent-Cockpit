package app

import "go.uber.org/fx"

// businessStoreAdaptersModule 为其余业务模块的 Store adapter 预留独立装配接缝。
func businessStoreAdaptersModule() fx.Option {
	return fx.Options()
}
