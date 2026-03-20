package rpc

import (
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
