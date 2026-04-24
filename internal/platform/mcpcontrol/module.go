package mcpcontrol

import (
	"context"
	"errors"
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
		// P22 P1b Finding 3: long-running sweep loop owned by run.Group via
		// the root group:"runners" bridge instead of a module-level
		// OnStart-spawned goroutine (the pre-P1b registerSweeperLifecycle
		// path). See sweeper_runner.go for the runner contract.
		fx.Annotate(NewSweeperRunner, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(registerHookLifecycle),
	fx.Invoke(registerRegistryLifecycle),
	fx.Invoke(registerConfigChangeLifecycle),
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

type configChangeIn struct {
	fx.In

	Notifier   contract.ToolNotifier `optional:"true"`
	Versions   configVersionSource   `optional:"true"`
	Dispatcher *event.Dispatcher     `optional:"true"`
	Logger     *pkglogger.Logger     `optional:"true"`
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

func registerConfigChangeLifecycle(lc fx.Lifecycle, in configChangeIn) {
	if in.Notifier == nil || in.Versions == nil || in.Dispatcher == nil {
		return
	}
	logger := in.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}

	// P22 P2 (mcpcontrol config fanout): the worker is constructed ahead of
	// Lifecycle.Append so OnStart can Start it before the bus subscriptions
	// are wired. OnStop first unsubscribes, then drains the worker bounded
	// by ctx so any in-flight peer NotifyConfigChanged observes the
	// cancelled fanoutCtx cleanly.
	worker := newConfigFanoutWorker(in.Notifier, in.Versions, logger)
	var cancels []context.CancelFunc
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			worker.Start()
			cancels = registerConfigChangeSubscriptions(in.Dispatcher, worker, logger)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			for _, cancel := range cancels {
				if cancel != nil {
					cancel()
				}
			}
			cancels = nil
			if err := worker.Stop(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Warn("mcp config change worker drain failed", "error", err)
			}
			return nil
		},
	})
}

// P22 P1b Finding 3: registerSweeperLifecycle was deleted. The sweeper loop is
// now owned by SweeperRunner (sweeper_runner.go) and joined via the root
// `group:"runners"` aggregation; shutdown is driven by root ctx cancel
// rather than a module-scoped cancel paired with a fire-and-forget
// OnStart goroutine. registry final-lease cleanup stays in
// registerRegistryLifecycle.OnStop.

