package rpc

import (
	"context"
	"log/slog"
	"time"

	"github.com/creachadair/jrpc2"
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
	fx.Invoke(bindApprovalLifecycle),
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

func bindApprovalLifecycle(lc fx.Lifecycle, approvals *ApprovalManager, bridge *PushBridge, server *Server, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	if server != nil {
		server.OnConnect(func(current *jrpc2.Server) {
			if approvals == nil || current == nil {
				return
			}
			if err := approvals.RestorePending(context.Background(), bridge, current); err != nil {
				logger.Warn("rpc: restore pending approvals on connect failed", "error", err)
			}
		})
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if approvals == nil || server == nil {
				return nil
			}
			for _, current := range server.snapshotActive() {
				if err := approvals.RestorePending(ctx, bridge, current); err != nil {
					return err
				}
			}
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return shutdownPendingApprovals(ctx, approvals, logger)
		},
	})
}

func shutdownPendingApprovals(ctx context.Context, approvals *ApprovalManager, logger *slog.Logger) error {
	const grace = 5 * time.Second
	if approvals == nil {
		return nil
	}
	waitPendingApprovals(ctx, approvals, grace)
	pending := approvals.PendingSnapshot()
	if len(pending) == 0 {
		return nil
	}
	approvals.Cleanup(grace)
	logger.Warn("rpc: cleaned pending approvals on stop", "count", len(approvals.PendingSnapshot()), "grace", grace.String())
	return nil
}

func waitPendingApprovals(ctx context.Context, approvals *ApprovalManager, grace time.Duration) {
	if approvals == nil || grace <= 0 {
		return
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for len(approvals.PendingSnapshot()) != 0 {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			return
		case <-ticker.C:
		}
	}
}
