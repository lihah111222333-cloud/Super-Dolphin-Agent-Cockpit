package thread

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	"go.uber.org/fx"
)

// Compile-time check: thread.Service must satisfy turn.PendingLaunchSpawner
// so the fx graph below can publish it under that interface directly. The
// shared threaddto.SpawnRouting type (in internal/dto/thread) keeps both
// signatures identical without reviving a thread↔turn import cycle.
var _ turn.PendingLaunchSpawner = (Service)(nil)

// subscriptionParams is an fx.In for the subscription lifecycle hook only.
// P22 P2 (thread) removed the late-setter injection path (bindDispatcher /
// bindPromptStore / bindClassifier): those deps are now constructor params
// on NewServiceWithPromptAssemblyAndSharedFiles, so this struct no longer
// carries them. The bus-callback guard matcher
// `bus_callback_must_not_register_late_setter` enforces that.
type subscriptionParams struct {
	fx.In

	Lifecycle fx.Lifecycle
	Service   Service `optional:"true"`
}

var Module = fx.Module("thread",
	fx.Provide(
		fx.Annotate(
			NewServiceWithPromptAssemblyAndSharedFiles,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`),
			// Publish the service under both thread.Service (its native
			// interface, required by uistate/orchestration) and
			// turn.PendingLaunchSpawner so NewTurnHandlers can pick it up
			// via optional injection without creating a turn→thread import
			// cycle. fx.As replaces the original output, so we need an
			// explicit fx.As(new(Service)) here, otherwise thread.Service
			// disappears from the DI graph.
			fx.As(new(Service)),
			fx.As(new(turn.PendingLaunchSpawner)),
		),
		fx.Annotate(
			NewThreadHandlers,
			fx.ParamTags("", `optional:"true"`),
		),
	),
	fx.Invoke(registerSubscriptions),
)

func registerSubscriptions(p subscriptionParams) {
	svc, ok := p.Service.(*service)
	if !ok || svc == nil {
		return
	}

	var cancels []context.CancelFunc
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			// P22 P2 thread S3: start bus workers before the bus
			// callbacks can fire, so Enqueue never races with a
			// yet-to-be-started runWorker goroutine.
			svc.startBusWorkers()
			cancels = registerThreadSubscriptions(svc)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			// Cancel subscriptions first so the callbacks stop enqueueing,
			// then drain the workers bounded by ctx. Order matters: if
			// draining ran first, in-flight callbacks would hit the closed
			// gate and silently drop — not wrong per contract, but the
			// clean-shutdown contract here is lossless.
			for _, cancel := range cancels {
				if cancel != nil {
					cancel()
				}
			}
			cancels = nil
			svc.stopBusWorkers(ctx)
			return nil
		},
	})
}
