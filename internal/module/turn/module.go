// Package turn 负责 turn 生命周期管理：输入组装、provider 提交、状态追踪与中断处理。
package turn

import (
	"context"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var Module = fx.Module("turn",
	fx.Provide(
		fx.Annotate(
			NewServiceWithPromptAssemblyAndTurnContext,
			// p20.2 step 1: Skill.Service is optional, contract.Contract is also optional, etc.
			// (Original tag rationale preserved below.)
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`),
			// Publish under both turn.Service (consumed by orchestration,
			// etc.) and contract.TurnThreadCleaner (narrow interface consumed
			// by the thread module to avoid a thread→turn import).
			fx.As(new(Service)),
			fx.As(new(contract.TurnThreadCleaner)),
		),
		// Publish the narrow CronTurnExecutor adapter so the cron module
		// can prepare/start/track turns without importing internal/module/turn.
		provideCronTurnExecutor,
		NewOrchestrationTurnStarter,
		fx.Annotate(
			NewTurnHandlers,
			fx.ParamTags("", `optional:"true"`, "", `optional:"true"`, "", `optional:"true"`),
		),
		// P0b Step 2: trajectory collector. observation.Contract is
		// optional so deployments without observation still wire turn
		// successfully; the collector tolerates a nil contract.
		fx.Annotate(
			NewTrajectoryCollector,
			fx.ParamTags(`optional:"true"`, `optional:"true"`),
		),
		fx.Annotate(
			NewTrajectorySubscribers,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`),
		),
		// P0b Step 3: skill evaluator. stateless / pure function; no
		// external dependencies, so plain fx.Provide is sufficient.
		NewDefaultEvaluator,
		func(e *DefaultEvaluator) Evaluator { return e },
		func() Redactor { return NewDefaultRedactor() },
	),
	fx.Invoke(registerTurnServiceLifecycle),
)

// registerTurnServiceLifecycle 把 turn.Service 挂入 fx.Lifecycle，确保 Shutdown 在应用停止时被调用。
// its Shutdown hook is called on app stop. Shutdown is discovered via a
// private shutdowner interface assertion so the public Service contract
// stays unchanged.
func registerTurnServiceLifecycle(lc fx.Lifecycle, svc Service) {
	if svc == nil {
		return
	}
	type shutdowner interface{ Shutdown() }
	sd, ok := svc.(shutdowner)
	if !ok {
		return
	}
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			sd.Shutdown()
			return nil
		},
	})
}

// provideCronTurnExecutor 把 turn.Service 包装为 contract.CronTurnExecutor，避免 cron 模块直接依赖 turn 包。
// the cron module can prepare/start/track turns via
// contract.CronTurnExecutor without importing this package.
func provideCronTurnExecutor(svc Service) contract.CronTurnExecutor {
	return NewCronExecutorAdapter(svc)
}
