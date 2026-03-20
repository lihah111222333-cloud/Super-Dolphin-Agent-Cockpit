package rpc

import (
	"context"
	"log/slog"

	"github.com/creachadair/jrpc2/handler"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

var Module = fx.Module("rpc",
	fx.Provide(
		NewServer,
		NewPushBridge,
		NewApprovalManager,
		NewCapabilityResolver,
		func(m *ApprovalManager) contract.ApprovalResponder { return m },
	),
	fx.Invoke(registerAllHandlers),
	fx.Invoke(bindEventBridge),
)

type Params struct {
	fx.In

	Logger *slog.Logger
	Config *config.Config
}

type HandlerMapResult struct {
	fx.Out

	Handlers handler.Map `group:"rpc_handlers"`
}

type serverParams struct {
	fx.In

	Logger   *slog.Logger
	Config   *config.Config
	Handlers []handler.Map `group:"rpc_handlers"`
}

func registerAllHandlers(server *Server, p serverParams) {
	server.Register(p.Handlers...)
}

func bindEventBridge(lc fx.Lifecycle, bridge *PushBridge, server *Server, logger *slog.Logger) {
	var cancels []context.CancelFunc

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			cancels = subscribeCoreEventPushes(bridge, server, logger)
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
