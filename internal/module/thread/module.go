package thread

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"go.uber.org/fx"
)

// Compile-time check: thread.Service must satisfy
// contract.PendingLaunchSpawner so the fx graph below can publish it
// under that interface directly. P22 P4 S2 moved the interface from the
// turn consumer package to internal/contract so the owner-side contract
// stops leaking through a side-channel interface; the thread module no
// longer needs to import turn just to declare what shape it satisfies.
var _ contract.PendingLaunchSpawner = (Service)(nil)

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
			// contract.PendingLaunchSpawner so NewTurnHandlers can pick it
			// up via optional injection without creating a turn→thread
			// import cycle. fx.As replaces the original output, so we need
			// an explicit fx.As(new(Service)) here, otherwise thread.Service
			// disappears from the DI graph. P22 P4 S2: the second fx.As
			// target used to be turn.PendingLaunchSpawner (side-channel
			// interface owned by the consumer package); it now lives in
			// contract.
			fx.As(new(Service)),
			fx.As(new(contract.PendingLaunchSpawner)),
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
