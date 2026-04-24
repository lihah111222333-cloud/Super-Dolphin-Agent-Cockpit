package hooks

import (
	"context"
	"errors"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/kelindar/event"
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
	),
	fx.Invoke(registerRecoveryLifecycle),
	fx.Invoke(registerEventRelayLifecycle),
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
			reviews, err := in.Resolver.RecoverOnStartup(ctx)
			if err != nil {
				return err
			}
			if len(reviews) > 0 {
				logger.Info("hooks recovered pending reviews", "count", len(reviews))
			}
			return nil
		},
	})
}

type eventRelayIn struct {
	fx.In

	Dispatcher *event.Dispatcher
	Manager    *Manager
	Logger     *pkglogger.Logger `optional:"true"`
}

func registerEventRelayLifecycle(lc fx.Lifecycle, in eventRelayIn) {
	if in.Dispatcher == nil || in.Manager == nil {
		return
	}
	logger := in.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	// P22 P2 (hooks/event_relay fanout): the worker is constructed ahead of
	// Lifecycle.Append so OnStart can Start it before startEventRelay wires
	// subscriptions. OnStop first unsubscribes, then drains the worker
	// bounded by ctx — in that order, so the worker sees a closed input
	// side before it needs to drain.
	worker := newHookDispatchWorker(in.Manager, logger)
	var cancel func()
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			worker.Start()
			cancel = startEventRelay(in.Dispatcher, worker, logger)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel != nil {
				cancel()
			}
			if err := worker.Stop(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Warn("hooks: event relay worker drain failed", "error", err)
			}
			return nil
		},
	})
}
