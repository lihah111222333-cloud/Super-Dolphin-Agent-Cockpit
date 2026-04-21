package feedback

import "go.uber.org/fx"

var Module = fx.Module("feedback",
	fx.Provide(NewService),
	fx.Provide(NewHandlers),
)
