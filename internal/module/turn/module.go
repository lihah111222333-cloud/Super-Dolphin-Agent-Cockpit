package turn

import (
	"context"

	"go.uber.org/fx"
)

var Module = fx.Module("turn",
	fx.Provide(
		fx.Annotate(
			NewServiceWithPromptAssemblyAndTurnContext,
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
	fx.Invoke(registerTurnServiceLifecycle),
)

// registerTurnServiceLifecycle wires the turn Service into fx.Lifecycle so
// its Shutdown hook is called on app stop. Shutdown is discovered via a
// private shutdowner interface assertion so the public Service contract
// stays unchanged.
func registerTurnServiceLifecycle(lc fx.Lifecycle, svc Service) {
	if svc == nil {
		return
	}
	type shutdowner interface{ Shutdown() }
	sd, ok := svc.(shutdowner)
	if !ok {
		return
	}
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			sd.Shutdown()
			return nil
		},
	})
}
