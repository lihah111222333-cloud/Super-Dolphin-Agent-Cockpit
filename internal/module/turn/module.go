package turn

import "go.uber.org/fx"

var Module = fx.Module("turn",
	fx.Provide(NewService),
)
