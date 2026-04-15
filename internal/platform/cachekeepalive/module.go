package cachekeepalive

import (
	"context"

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

	var cancel func()
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			cancel = startKeepaliveRelay(in.Dispatcher, in.Manager, in.Logger)
			return nil
		},
		OnStop: func(context.Context) error {
			if cancel != nil {
				cancel()
			}
			in.Manager.Shutdown()
			return nil
		},
	})
}
