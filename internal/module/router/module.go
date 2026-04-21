package router

import "go.uber.org/fx"

var Module = fx.Module("router.preview",
	fx.Provide(
		fx.Annotate(
			NewService,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`),
		),
		NewHandlers,
	),
)
