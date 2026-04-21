package router

import "go.uber.org/fx"

var Module = fx.Module("router.preview",
	fx.Provide(NewService),
	fx.Provide(NewHandlers),
)
