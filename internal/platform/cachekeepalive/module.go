package cachekeepalive

import (
	"context"

	"go.uber.org/fx"
)

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
