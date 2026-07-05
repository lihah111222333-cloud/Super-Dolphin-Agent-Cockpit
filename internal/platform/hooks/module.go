package hooks

import (
	"context"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"go.uber.org/fx"
)

var Module = fx.Module("platform.hooks",
	fx.Provide(
		NewHookRegistry,
		NewHookDispatcher,
		NewHookResolver,
		provideManager,
		func(m *Manager) contract.HookManager { return m },
		func(m *Manager) contract.HookLifecycle { return m },
		NewHooksRelaySubscribers,
		newHookDispatchWorkerProvider,
	),
	fx.Provide(
		fx.Annotate(hookWorkerAsRunner, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(registerRecoveryLifecycle),
)

type managerIn struct {
	fx.In

	Registry   *HookRegistry
	Dispatcher *HookDispatcher
	Resolver   *HookResolver
	Logger     *pkglogger.Logger `optional:"true"`
}

func provideManager(in managerIn) (*Manager, error) {
	return NewManager(in.Registry, in.Dispatcher, in.Resolver, WithManagerLogger(in.Logger))
}

type recoveryLifecycleIn struct {
	fx.In

	Resolver *HookResolver
	Manager  *Manager
	Logger   *pkglogger.Logger `optional:"true"`
}

// registerRecoveryLifecycle 在 fx 启动阶段恢复未完成的 hook pending review。
// 这里只读出并记录数量，真正的过期收敛由 resolver sweeper 路径处理。
func registerRecoveryLifecycle(lc fx.Lifecycle, in recoveryLifecycleIn) {
	if in.Resolver == nil || in.Manager == nil {
		return
	}
	logger := in.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			count, err := in.Resolver.CountPendingReviews(ctx)
			if err != nil {
				return err
			}
			if count > 0 {
				logger.Info("hooks recovered pending reviews", "count", count)
			}
			return nil
		},
	})
}

func newHookDispatchWorkerProvider(manager *Manager, logger *pkglogger.Logger) *hookDispatchWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return newHookDispatchWorker(manager, logger)
}
