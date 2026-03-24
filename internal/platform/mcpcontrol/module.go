package mcpcontrol

import (
	"context"
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

// Module wires the ctl/* registry, handlers, and sweeper only. MCP binaries are
// started outside the core process and self-register through the control plane.
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
	),
	fx.Invoke(registerHookLifecycle),
	fx.Invoke(registerConfigChangeLifecycle),
	fx.Invoke(registerSweeperLifecycle),
)

type HandlerDeps struct {
	Registry          *ToolRegistry
	Approvals         *rpc.ApprovalManager          `optional:"true"`
	Bridge            *rpc.PushBridge               `optional:"true"`
	HookManager       contract.HookManager          `optional:"true"`
	Logger            *slog.Logger                  `optional:"true"`
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
	Logger            *slog.Logger                  `optional:"true"`
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
	Logger     *slog.Logger          `optional:"true"`
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

func registerConfigChangeLifecycle(lc fx.Lifecycle, in configChangeIn) {
	if in.Notifier == nil || in.Versions == nil || in.Dispatcher == nil {
		return
	}
	logger := in.Logger
	if logger == nil {
		logger = slog.Default()
	}

	var cancels []context.CancelFunc
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			cancels = registerConfigChangeSubscriptions(in.Dispatcher, in.Notifier, in.Versions, logger)
			return nil
		},
		OnStop: func(context.Context) error {
			for _, cancel := range cancels {
				if cancel != nil {
					cancel()
				}
			}
			cancels = nil
			return nil
		},
	})
}

func registerSweeperLifecycle(lc fx.Lifecycle, sweeper *Sweeper) {
	if sweeper == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go sweeper.Run(ctx)
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			return nil
		},
	})
}
