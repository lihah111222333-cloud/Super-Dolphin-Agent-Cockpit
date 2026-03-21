package uistate

import (
	"context"

	"github.com/kelindar/event"
	"go.uber.org/fx"
)

var Module = fx.Options(
	fx.Provide(NewService),
	fx.Provide(NewUIStateHandlers),
	fx.Provide(NewConfigHandlers),
	fx.Invoke(registerProjections),
)

func registerProjections(lc fx.Lifecycle, dispatcher *event.Dispatcher, svc *service) {
	if svc != nil {
		svc.bindDispatcher(dispatcher)
	}
	var cancels []context.CancelFunc
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			cancels = registerProjectionSubscriptions(dispatcher, svc)
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
