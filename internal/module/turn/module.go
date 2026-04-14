package turn

import (
	"go.uber.org/fx"
)

var Module = fx.Module("turn",
	fx.Provide(
		fx.Annotate(
			NewServiceWithPromptAssemblyAndMemoryContext,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`),
		),
		fx.Annotate(
			NewOrchestrationTurnStarter,
			fx.ParamTags("", "", `optional:"true"`),
		),
		fx.Annotate(
			NewTurnHandlers,
			fx.ParamTags("", `optional:"true"`, "", `optional:"true"`, `optional:"true"`),
		),
	),
)
