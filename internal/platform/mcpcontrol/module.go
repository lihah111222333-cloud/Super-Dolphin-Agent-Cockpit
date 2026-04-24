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

// Module wires the ctl/* control-plane registry, handlers, sweeper, and related
// lifecycle hooks. MCP binaries are started outside the core process and
// self-register through the control plane.
var Module = fx.Module("mcpcontrol",
	fx.Provide(
		NewRegistry,
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
		// P22 P1b Finding 3: long-running sweep loop owned by run.Group via
		// the root group:"runners" bridge instead of a module-level
		// OnStart-spawned goroutine (the pre-P1b registerSweeperLifecycle
		// path). See sweeper_runner.go for the runner contract.
		fx.Annotate(NewSweeperRunner, fx.ResultTags(`group:"runners"`)),
	),
	fx.Provide(
		fx.Annotate(configFanoutWorkerAsRunner, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(registerHookLifecycle),
	fx.Invoke(registerRegistryLifecycle),
)

// HandlerDeps bundles the dependencies used to build the MCP control-plane RPC handlers.
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
	RuntimeReports    RuntimeReportHandler          `optional:"true"`
	CompletionReports CompletionReportHandler       `optional:"true"`
}

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
	RuntimeReports    RuntimeReportHandler          `optional:"true"`
	CompletionReports CompletionReportHandler       `optional:"true"`
}

type hookLifecycleIn struct {
	fx.In

	Registry      *ToolRegistry
	HookLifecycle contract.HookLifecycle `optional:"true"`
}

type configFanoutWorkerIn struct {
	fx.In

	Notifier contract.ToolNotifier `optional:"true"`
	Versions configVersionSource   `optional:"true"`
	Logger   *pkglogger.Logger     `optional:"true"`
}

func newConfigFanoutWorkerProvider(in configFanoutWorkerIn) *configFanoutWorker {
	logger := in.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	return newConfigFanoutWorker(in.Notifier, in.Versions, logger)
}

type configVersionSource interface {
	advanceConfigVersion() int64
}

func provideConfigVersionSource(registry *ToolRegistry) configVersionSource {
	return registry
}

func provideToolRegistry(registry *ToolRegistry) contract.ToolRegistry {
	return registry
}

func provideToolNotifier(registry *ToolRegistry) contract.ToolNotifier {
	return registry
}

func provideToolHookCallback(registry *ToolRegistry) contract.ToolHookCallback {
	return registry
}

func providePeerCallback(registry *ToolRegistry) contract.PeerCallback {
	return registry
}

func provideToolControlPlane(registry *ToolRegistry) contract.ToolControlPlane {
	return registry
}

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
		RuntimeReports:    in.RuntimeReports,
		CompletionReports: in.CompletionReports,
	})
}

func registerHookLifecycle(in hookLifecycleIn) {
	if in.Registry == nil {
		return
	}
	in.Registry.setHookLifecycle(in.HookLifecycle)
}

func registerRegistryLifecycle(lc fx.Lifecycle, registry *ToolRegistry) {
	if registry == nil {
		return
	}
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

// P22 P1b Finding 3: registerSweeperLifecycle was deleted. The sweeper loop is
// now owned by SweeperRunner (sweeper_runner.go) and joined via the root
// `group:"runners"` aggregation; shutdown is driven by root ctx cancel
// rather than a module-scoped cancel paired with a fire-and-forget
// OnStart goroutine. registry final-lease cleanup stays in
// registerRegistryLifecycle.OnStop.
