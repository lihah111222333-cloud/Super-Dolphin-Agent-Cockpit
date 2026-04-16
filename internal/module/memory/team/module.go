package team

import (
	"context"

	"go.uber.org/fx"
)

var Module = fx.Module("memory.team",
	fx.Provide(
		NewTeamMemoryManager,
		NewTeamMemoryGuard,
		NewTeamSyncService,
	),
	fx.Invoke(registerTeamSyncLifecycle),
)

func registerTeamSyncLifecycle(lc fx.Lifecycle, svc *TeamSyncService) {
	if svc == nil {
		return
	}
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return svc.Shutdown(ctx)
		},
	})
}
