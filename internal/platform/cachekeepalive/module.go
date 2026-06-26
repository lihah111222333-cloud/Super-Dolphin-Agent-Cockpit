package cachekeepalive

import (
	"context"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"go.uber.org/fx"
)

var Module = fx.Module("platform.cachekeepalive",
	fx.Provide(NewManager),
	fx.Provide(NewCacheKeepaliveSubscribers),
	fx.Invoke(bindManagerShutdown),
)

// bindManagerShutdown 将 cachekeepalive Manager 作为 fx 资源挂入 OnStop。
// Manager 没有 Start 循环，只有被动 time.AfterFunc；停止时必须先 drain ping 再释放 timer。
func bindManagerShutdown(lc fx.Lifecycle, m *Manager, logger *pkglogger.Logger) {
	if m == nil {
		return
	}
	if logger == nil {
		logger = pkglogger.Get()
	}
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			if err := m.Shutdown(ctx); err != nil {
				logger.Warn("cachekeepalive: manager shutdown drain failed", "error", err)
				return err
			}
			return nil
		},
	})
}
