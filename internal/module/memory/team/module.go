// Package team 实现团队记忆的管理、安全守卫和远程同步能力。
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

// registerTeamSyncLifecycle 将 TeamSyncService 的 Shutdown 注册到 fx 生命周期，确保进程退出时同步服务正常关闭。
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
