package thread

import (
	"go.uber.org/fx"
)

var Module = fx.Module("thread",
	fx.Provide(
		fx.Annotate(
			NewService,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`),
		),
		fx.Annotate(
			NewThreadHandlers,
			fx.ParamTags("", `optional:"true"`),
		),
	),
)
