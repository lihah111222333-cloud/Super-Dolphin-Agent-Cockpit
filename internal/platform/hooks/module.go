package hooks

import (
	"log/slog"

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
	),
)

type managerIn struct {
	fx.In

	Registry   *HookRegistry
	Dispatcher *HookDispatcher
	Resolver   *HookResolver
	Logger     *slog.Logger `optional:"true"`
}

func provideManager(in managerIn) *Manager {
	return NewManager(in.Registry, in.Dispatcher, in.Resolver, WithManagerLogger(in.Logger))
}
