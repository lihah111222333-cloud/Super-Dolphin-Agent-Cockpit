package mcpcontrol

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

const activeLeaseCleanupTimeout = 5 * time.Second

// provideRegistry 创建 MCP 控制面注册表，供 fx 以单例形式注入。
func provideRegistry() *ToolRegistry {
	return NewRegistry()
}

// Module 装配 ctl/* 控制面注册表、handler、sweeper 和配置广播 worker。
// MCP 二进制在核心进程外启动，并通过本控制面自注册。
var Module = fx.Module("mcpcontrol",
	fx.Provide(
		provideRegistry,
		provideConfigVersionSource,
		provideToolRegistry,
		provideToolNotifier,
		provideToolHookCallback,
		providePeerCallback,
		provideToolControlPlane,
		NewSweeper,
		provideHandlers,
		newConfigFanoutWorkerProvider,
		NewMCPConfigChangeSubscribers,
		// sweeper 长循环由 root run.Group 管理，避免 fx OnStart 内部再开不可追踪 goroutine。
		fx.Annotate(NewSweeperRunner, fx.ResultTags(`group:"runners"`)),
	),
	fx.Provide(
		fx.Annotate(configFanoutWorkerAsRunner, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(registerRegistryLifecycle),
)

// HandlerDeps 汇总 MCP 控制面 RPC handler 的依赖，测试可以替换各个 sink/handler。
type HandlerDeps struct {
	Registry          *ToolRegistry
	Approvals         *rpc.ApprovalManager          `optional:"true"`
	Bridge            *rpc.PushBridge               `optional:"true"`
	HookManager       contract.HookManager          `optional:"true"`
	Logger            *pkglogger.Logger             `optional:"true"`
	Dispatcher        *event.Dispatcher             `optional:"true"`
	Orchestration     contract.OrchestrationService `optional:"true"`
	AgentSource       AgentContextSource            `optional:"true"`
	Context           ContextProvider               `optional:"true"`
	Events            EventSink                     `optional:"true"`
	Logs              LogSink                       `optional:"true"`
	SystemLogs        SystemLogSink                 `optional:"true"`
	RuntimeReports    RuntimeReportHandler          `optional:"true"`
	CompletionReports CompletionReportHandler       `optional:"true"`
}

// handlerIn 是 fx 注入形态，对应 HandlerDeps 的运行时依赖集合。
type handlerIn struct {
	fx.In

	Registry          *ToolRegistry
	Approvals         *rpc.ApprovalManager          `optional:"true"`
	Bridge            *rpc.PushBridge               `optional:"true"`
	HookManager       contract.HookManager          `optional:"true"`
	Logger            *pkglogger.Logger             `optional:"true"`
	Dispatcher        *event.Dispatcher             `optional:"true"`
	Orchestration     contract.OrchestrationService `optional:"true"`
	AgentSource       AgentContextSource            `optional:"true"`
	Context           ContextProvider               `optional:"true"`
	Events            EventSink                     `optional:"true"`
	Logs              LogSink                       `optional:"true"`
	SystemLogs        SystemLogSink                 `optional:"true"`
	RuntimeReports    RuntimeReportHandler          `optional:"true"`
	CompletionReports CompletionReportHandler       `optional:"true"`
}

// configFanoutWorkerIn 是配置广播 worker 的 fx 注入参数。
type configFanoutWorkerIn struct {
	fx.In

	Notifier contract.ToolNotifier `optional:"true"`
	Versions configVersionSource   `optional:"true"`
	Logger   *pkglogger.Logger     `optional:"true"`
}

// newConfigFanoutWorkerProvider 构造配置广播 worker，缺省 logger 使用包级 logger。
func newConfigFanoutWorkerProvider(in configFanoutWorkerIn) *configFanoutWorker {
	logger := in.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	return newConfigFanoutWorker(in.Notifier, in.Versions, logger)
}

// configVersionSource 是配置变更广播所需的最小版本推进接口。
type configVersionSource interface {
	advanceConfigVersion() int64
}

// provideConfigVersionSource 暴露注册表的配置版本推进能力。
func provideConfigVersionSource(registry *ToolRegistry) configVersionSource {
	return registry
}

// provideToolRegistry 将内部注册表暴露为 contract.ToolRegistry。
func provideToolRegistry(registry *ToolRegistry) contract.ToolRegistry {
	return registry
}

// provideToolNotifier 将内部注册表暴露为通知 fanout 能力。
func provideToolNotifier(registry *ToolRegistry) contract.ToolNotifier {
	return registry
}

// provideToolHookCallback 将内部注册表暴露为 hook callback 能力。
func provideToolHookCallback(registry *ToolRegistry) contract.ToolHookCallback {
	return registry
}

// providePeerCallback 将内部注册表暴露为 peer 定向回调能力。
func providePeerCallback(registry *ToolRegistry) contract.PeerCallback {
	return registry
}

// provideToolControlPlane 将内部注册表暴露为工具控制面聚合接口。
func provideToolControlPlane(registry *ToolRegistry) contract.ToolControlPlane {
	return registry
}

// provideHandlers 将 fx 注入参数转换成 handler 构造参数，保持 NewHandlers 可直接单测。
func provideHandlers(in handlerIn) rpc.HandlerMapResult {
	return NewHandlers(HandlerDeps{
		Registry:          in.Registry,
		Approvals:         in.Approvals,
		Bridge:            in.Bridge,
		HookManager:       in.HookManager,
		Logger:            in.Logger,
		Dispatcher:        in.Dispatcher,
		Orchestration:     in.Orchestration,
		AgentSource:       in.AgentSource,
		Context:           in.Context,
		Events:            in.Events,
		Logs:              in.Logs,
		SystemLogs:        in.SystemLogs,
		RuntimeReports:    in.RuntimeReports,
		CompletionReports: in.CompletionReports,
	})
}

// registerRegistryLifecycle 在 fx 停止阶段清理 active 租约，避免控制面退出后遗留 hook 状态。
func registerRegistryLifecycle(lc fx.Lifecycle, registry *ToolRegistry, hookLifecycle contract.HookLifecycle) {
	if registry == nil {

		return
	}
	registry.setHookLifecycle(hookLifecycle)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {

			return nil
		},
		OnStop: func(ctx context.Context) error {
			stopCtx, cancel := withTimeoutContext(ctx, activeLeaseCleanupTimeout)
			defer cancel()
			return registry.shutdownActiveLeases(stopCtx)
		},
	})
}

// sweeper 循环的生命周期由 SweeperRunner 和 root run.Group 统一管理。
// 注册表最终租约清理仍保留在 registerRegistryLifecycle.OnStop 中，确保退出时 hook 状态被收口。
