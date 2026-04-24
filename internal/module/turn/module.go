package turn

import (
	"context"

	"go.uber.org/fx"

	turndedupe "github.com/anthropic-ai/super-agent-v3/internal/store/turndedupe"
)

var Module = fx.Module("turn",
	fx.Provide(
		fx.Annotate(
			NewServiceWithPromptAssemblyAndTurnContext,
			// p20.2 §5 step 1：skill.Service 按 optional 注入；P21 observation.Contract
			// 同样 optional。依赖图尚未准备好时不会阻塞 turn 模块启动，PrepareTurn
			// 的 hydrate / observation 步骤在对应依赖 nil 时自动跳过。
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`),
		),
		fx.Annotate(
			NewOrchestrationTurnStarter,
			fx.ParamTags("", "", `optional:"true"`),
		),
		fx.Annotate(
			NewTurnHandlers,
			fx.ParamTags("", `optional:"true"`, "", `optional:"true"`, `optional:"true"`, `optional:"true"`),
		),
	),
	fx.Invoke(registerTurnServiceLifecycle),
	fx.Invoke(registerTurnDedupeStore),
)

// turnDedupeStoreParams lets the fx.Invoke receive both the Service
// and the optional turndedupe.Store without forcing an order on the
// DI graph. Store==nil means the deployment hasn't wired
// turndedupe.Module; the service keeps the tracker-only behaviour.
type turnDedupeStoreParams struct {
	fx.In

	Service Service
	Store   turndedupe.Store `optional:"true"`
}

// registerTurnDedupeStore installs the optional durable store into
// the already-constructed Service via a package-private setter. The
// setter is guarded by an interface assertion so any non-default
// Service implementation provided in tests is not disturbed.
func registerTurnDedupeStore(p turnDedupeStoreParams) {
	if p.Service == nil || p.Store == nil {
		return
	}
	type dedupeSetter interface {
		setDedupeStore(turndedupe.Store)
	}
	setter, ok := p.Service.(dedupeSetter)
	if !ok {
		return
	}
	setter.setDedupeStore(p.Store)
}

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
