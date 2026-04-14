package memory

import (
	"context"

	"go.uber.org/fx"
)

var Module = fx.Module("memory",
	fx.Provide(
		NewConfig,
		NewService,
		NewAgentMemoryManager,
		NewMemoryRuleEngine,
		NewRulesProvider,
	),
	fx.Invoke(registerLifecycle, registerPromptProvider),
)

func registerLifecycle(lc fx.Lifecycle, svc Service) {
	if svc == nil {
		return
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return svc.EnsureRoot(ctx)
		},
	})
}
