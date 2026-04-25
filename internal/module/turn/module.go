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
			// p20.2 鈯?step 1锛歴kill.Service 鎸?optional 娉ㄥ叆锛汸21 observation.Contract
			// 鍚屾牱 optional銆備緷璧栧浘灏氭湭鍑嗗濂芥椂涓嶄細闃诲 turn 妯″潡鍚姩锛孭repareTurn
			// 鐨?hydrate / observation 姝ラ鍦ㄥ搴斾緷璧?nil 鏃惰嚜鍔ㄨ烦杩囥€?
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
		// P0b Step 2: trajectory collector. observation.Contract is
		// optional so deployments without observation still wire turn
		// successfully; the collector tolerates a nil contract.
		fx.Annotate(
			NewTrajectoryCollector,
			fx.ParamTags(`optional:"true"`, `optional:"true"`),
		),
		fx.Annotate(
			NewTrajectorySubscribers,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`),
		),
		// P0b Step 3: skill evaluator. stateless / pure function; no
		// external dependencies, so plain fx.Provide is sufficient.
		fx.Provide(NewDefaultEvaluator),
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