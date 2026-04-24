package cachekeepalive

import (
	"context"
	"errors"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

type keepaliveIn struct {
	fx.In

	Dispatcher *event.Dispatcher
	Manager    *Manager
	Logger     *pkglogger.Logger `optional:"true"`
}

var Module = fx.Module("platform.cachekeepalive",
	fx.Provide(NewManager),
	fx.Invoke(registerKeepaliveLifecycle),
)

func registerKeepaliveLifecycle(lc fx.Lifecycle, in keepaliveIn) {
	if in.Dispatcher == nil || in.Manager == nil {
		return
	}

	logger := in.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	var cancel func()
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			cancel = startKeepaliveRelay(in.Dispatcher, in.Manager, logger)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel != nil {
				cancel()
			}
			if err := in.Manager.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Warn("cachekeepalive: manager shutdown drain failed", "error", err)
			}
			return nil
		},
	})
}
