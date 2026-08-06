// Package turn 负责 turn 生命周期管理：输入组装、provider 提交、状态追踪与中断处理。
package turn

import (
	"context"

	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

var Module = fx.Module("turn",
	fx.Provide(
		NewToolResultRuntime,
		fx.Annotate(
			NewServiceWithPromptAssemblyAndTurnContext,
			// 这些跨模块依赖均为可选注入：缺失时 turn 仍能启动，只跳过对应的 skill、observation、dedupe 或 tracing 能力。
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, ""),
			// 同时发布完整 turn.Service 和窄接口 TurnThreadCleaner，避免 thread 模块反向导入 turn 包。
			fx.As(new(Service)),
			fx.As(new(contract.TurnThreadCleaner)),
		),
		// 发布 cron 使用的窄执行器接口，避免 cron 模块直接依赖 turn 包。
		provideCronTurnExecutor,
		NewOrchestrationTurnStarter,
		fx.Annotate(
			NewTurnHandlers,
			fx.ParamTags("", `optional:"true"`, "", `optional:"true"`, "", `optional:"true"`),
		),
		// 轨迹收集器允许缺少 observation 依赖；这种部署仍能启动，只是不补终态/token/skill 快照。
		fx.Annotate(
			NewTrajectoryCollector,
			fx.ParamTags(`optional:"true"`, `optional:"true"`),
		),
		fx.Annotate(
			NewTrajectorySubscribers,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`),
		),
		// skill evaluator 是无外部依赖的纯判断器，直接提供默认实现即可。
		NewDefaultEvaluator,
		func(e *DefaultEvaluator) Evaluator { return e },
		func() Redactor { return NewDefaultRedactor() },
	),
	fx.Invoke(registerTurnServiceLifecycle),
)

// registerTurnServiceLifecycle 把 turn.Service 挂入 fx.Lifecycle，确保应用停止时取消后台 watcher。
// Shutdown 通过私有接口断言发现，避免把生命周期方法暴露进公共 Service 契约。
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

// provideCronTurnExecutor 把 turn.Service 包装为 cron 使用的窄接口，避免 cron 模块直接导入 turn 包。
func provideCronTurnExecutor(svc Service) contract.CronTurnExecutor {
	return NewCronExecutorAdapter(svc)
}
