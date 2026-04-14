package thread

import (
	"context"

	"github.com/kelindar/event"
	"go.uber.org/fx"
)

type subscriptionParams struct {
	fx.In

	Lifecycle  fx.Lifecycle
	Dispatcher *event.Dispatcher `optional:"true"`
	Service    Service           `optional:"true"`
}

var Module = fx.Module("thread",
	fx.Provide(
		fx.Annotate(
			NewServiceWithPromptAssembly,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`),
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
