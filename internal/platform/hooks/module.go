package hooks

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"go.uber.org/fx"
)

var Module = fx.Module("platform.hooks",
	fx.Provide(
		NewHookRegistry,
		NewHookDispatcher,
		NewHookResolver,
		NewManager,
		func(m *Manager) contract.HookManager { return m },
		func(m *Manager) contract.HookLifecycle { return m },
	),
)
