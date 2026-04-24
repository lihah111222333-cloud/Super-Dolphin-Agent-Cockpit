package cachekeepalive

import (
	"context"
	"errors"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"go.uber.org/fx"
)

var Module = fx.Module("platform.cachekeepalive",
	fx.Provide(NewManager),
	fx.Provide(NewCacheKeepaliveSubscribers),
	fx.Invoke(bindManagerShutdown),
)

// bindManagerShutdown wires the cachekeepalive Manager as a resource whose
// drain runs in fx.OnStop. Manager is not a long-running worker (no Start);
// timers are passive time.AfterFunc. Shutdown drains in-flight pings and
// cancels all timers — strictly resource close per §10.30.
func bindManagerShutdown(lc fx.Lifecycle, m *Manager, logger *pkglogger.Logger) {
	if m == nil {
		return
	}
	if logger == nil {
		logger = pkglogger.Get()
	}
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			if err := m.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Warn("cachekeepalive: manager shutdown drain failed", "error", err)
			}
			return nil
		},
	})
}
