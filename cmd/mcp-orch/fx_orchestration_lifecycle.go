package main

import (
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"go.uber.org/fx"
)

// orchestrationLifecycleOptions 只装配 orchestration service 的 workflow ports 与 lifecycle hook。
func orchestrationLifecycleOptions() fx.Option {
	return fx.Module("orchestration-lifecycle",
		fx.Provide(
			fx.Annotate(
				orchestration.ProvideService,
				fx.As(fx.Self()),
				fx.As(new(contract.AgentLaunchPort)),
				fx.As(new(contract.AgentStateReader)),
				fx.As(new(contract.AgentStopPort)),
				fx.As(new(contract.AgentStopWaitPort)),
				fx.As(new(contract.AgentInterruptPort)),
				fx.As(new(contract.AgentRecoveryPort)),
				fx.As(new(contract.AgentRuntimePort)),
				fx.As(new(contract.AgentReportPort)),
				fx.As(new(contract.TurnSubmissionPort)),
				fx.As(new(contract.DAGCreateRuntime)),
				fx.As(new(contract.DAGRuntime)),
				fx.As(new(contract.DAGDeleteRuntime)),
				fx.As(new(contract.DAGNodeStatusRuntime)),
				fx.As(new(contract.DAGNodeDispatchRuntime)),
				fx.As(new(orchestration.ScheduledDAGStartService)),
				fx.As(new(orchestration.WakeupLauncher)),
				fx.As(new(orchestration.HookConsumerRuntime)),
				fx.As(new(orchestration.HookReportPort)),
				fx.As(new(orchestration.AgentLaunchSnapshotter)),
				fx.As(new(orchestration.StopAgentService)),
				fx.As(new(orchestration.RunnerLifecyclePort)),
				fx.As(new(orchestration.RunnerRuntimePort)),
				fx.As(new(orchestration.TurnLifecyclePort)),
				fx.As(new(orchestration.ApprovalLifecyclePort)),
			),
			orchestration.ProvideHookAfterHandler,
			orchestration.ProvideRPCFacade,
		),
		fx.Invoke(orchestration.RegisterTurnLifecycle),
		fx.Invoke(orchestration.RegisterApprovalLifecycle),
	)
}
