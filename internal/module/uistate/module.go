package uistate

import (
	"context"
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

type serviceParams struct {
	fx.In

	Logger      *slog.Logger
	Threads     thread.Service
	Agents      contract.OrchestrationService `optional:"true"`
	Preferences uipreference.Store
}

var Module = fx.Options(
	fx.Provide(func(p serviceParams) (*service, Service, error) {
		return NewService(p.Logger, p.Threads, p.Agents, p.Preferences)
	}),
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
