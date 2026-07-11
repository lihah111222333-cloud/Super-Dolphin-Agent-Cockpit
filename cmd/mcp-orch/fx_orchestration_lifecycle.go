package main

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"go.uber.org/fx"
)

// orchestrationLifecycleOptions 只装配 orchestration service 的 workflow ports 与 lifecycle hook。
func orchestrationLifecycleOptions() fx.Option {
	return fx.Module("orchestration-lifecycle",
		fx.Provide(
			orchestration.ProvideServiceResult,
			orchestration.ProvideHookAfterHandler,
			orchestration.ProvideRPCFacade,
		),
		fx.Invoke(orchestration.RegisterTurnLifecycle),
		fx.Invoke(orchestration.RegisterApprovalLifecycle),
	)
}
