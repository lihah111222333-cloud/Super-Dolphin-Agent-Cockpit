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
		NewSweeper,
		provideHandlers,
	),
	fx.Invoke(registerSweeperLifecycle),
)

type HandlerDeps struct {
	Registry          *ToolRegistry
	Approvals         *rpc.ApprovalManager          `optional:"true"`
	Bridge            *rpc.PushBridge               `optional:"true"`
	Logger            *slog.Logger                  `optional:"true"`
	Dispatcher        *event.Dispatcher             `optional:"true"`
	Orchestration     contract.OrchestrationService `optional:"true"`
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
	Logger            *slog.Logger                  `optional:"true"`
	Dispatcher        *event.Dispatcher             `optional:"true"`
	Orchestration     contract.OrchestrationService `optional:"true"`
	Context           ContextProvider               `optional:"true"`
	Events            EventSink                     `optional:"true"`
	Logs              LogSink                       `optional:"true"`
	RuntimeReports    RuntimeReportHandler          `optional:"true"`
	CompletionReports CompletionReportHandler       `optional:"true"`
}

func provideHandlers(in handlerIn) rpc.HandlerMapResult {
	return NewHandlers(HandlerDeps{
		Registry:          in.Registry,
		Approvals:         in.Approvals,
		Bridge:            in.Bridge,
		Logger:            in.Logger,
		Dispatcher:        in.Dispatcher,
		Orchestration:     in.Orchestration,
		Context:           in.Context,
		Events:            in.Events,
		Logs:              in.Logs,
		RuntimeReports:    in.RuntimeReports,
		CompletionReports: in.CompletionReports,
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
