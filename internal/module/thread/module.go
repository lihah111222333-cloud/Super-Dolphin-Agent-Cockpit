package thread

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt/classifier"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

// Compile-time check: thread.Service must satisfy turn.PendingLaunchSpawner
// so the fx graph below can publish it under that interface directly. The
// shared threaddto.SpawnRouting type (in internal/dto/thread) keeps both
// signatures identical without reviving a thread↔turn import cycle.
var _ turn.PendingLaunchSpawner = (Service)(nil)

type subscriptionParams struct {
	fx.In

	Lifecycle   fx.Lifecycle
	Dispatcher  *event.Dispatcher     `optional:"true"`
	Service     Service               `optional:"true"`
	PromptStore promptstore.Store     `optional:"true"`
	Classifier  classifier.Classifier `optional:"true"`
}

var Module = fx.Module("thread",
	fx.Provide(
		fx.Annotate(
			NewServiceWithPromptAssemblyAndSharedFiles,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`),
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
	if p.Dispatcher != nil {
		svc.bindDispatcher(p.Dispatcher)
	}
	svc.bindPromptStore(p.PromptStore)
	svc.bindClassifier(p.Classifier)

	var cancels []context.CancelFunc
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			cancels = registerThreadSubscriptions(svc)
			return nil
		},
		OnStop: func(context.Context) error {
			for _, cancel := range cancels {
				if cancel != nil {
					cancel()
				}
			}
			cancels = nil
			return nil
		},
	})
}
