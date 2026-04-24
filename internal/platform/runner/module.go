package runner

import "go.uber.org/fx"

var Module = fx.Module(
	"runner",
	fx.Provide(NewContract),
)
