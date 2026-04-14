package turn

import (
	"go.uber.org/fx"
)

var Module = fx.Module("turn",
	fx.Provide(
		fx.Annotate(
			NewServiceWithPromptAssembly,
			fx.ParamTags("", `optional:"true"`),
		),
		NewOrchestrationTurnStarter,
		fx.Annotate(
			NewTurnHandlers,
			fx.ParamTags("", `optional:"true"`, "", `optional:"true"`),
		),
	),
)
