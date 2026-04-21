package thread

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/router"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

type subscriptionParams struct {
	fx.In

	Lifecycle     fx.Lifecycle
	Dispatcher    *event.Dispatcher `optional:"true"`
	Service       Service           `optional:"true"`
	RouterBackend router.Backend    `optional:"true"`
	PromptStore   promptstore.Store `optional:"true"`
}

var Module = fx.Module("thread",
	fx.Provide(
		fx.Annotate(
			NewServiceWithPromptAssembly,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`),
			// Publish the service under turn.PendingLaunchSpawner too so
			// NewTurnHandlers can pick it up via optional injection without
			// creating a turn→thread import cycle.
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
	svc.bindRouterBackend(p.RouterBackend)
	svc.bindPromptStore(p.PromptStore)

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
