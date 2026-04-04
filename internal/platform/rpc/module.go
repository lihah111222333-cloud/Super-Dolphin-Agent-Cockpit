package rpc

import (
	"context"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"net/http"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

var Module = fx.Module("rpc",
	fx.Provide(
		NewServer,
		NewPushBridge,
		NewApprovalManager,
		NewCapabilityResolver,
		provideWSRoute,
		func(m *ApprovalManager) contract.ApprovalResponder { return m },
	),
	fx.Invoke(registerAllHandlers),
	fx.Invoke(bindEventBridge),
	fx.Invoke(bindApprovalLifecycle),
)

type Params struct {
	fx.In

	Logger *pkglogger.Logger
	Config *config.Config
}

type HandlerMapResult struct {
	fx.Out

	Handlers handler.Map `group:"rpc_handlers"`
}

// HTTPRoute advertises an optional HTTP binding that an external router may mount.
type HTTPRoute struct {
	Path    string
	Handler http.Handler
}

type HTTPRouteResult struct {
	fx.Out

	Route HTTPRoute `group:"rpc_http_routes"`
}

type serverParams struct {
	fx.In

	Logger   *pkglogger.Logger
	Config   *config.Config
	Handlers []handler.Map `group:"rpc_handlers"`
}

func registerAllHandlers(server *Server, p serverParams) {
	server.Register(p.Handlers...)
}

func provideWSRoute(server *Server) HTTPRouteResult {
	if server == nil {
		return HTTPRouteResult{}
	}
	return HTTPRouteResult{
		Route: HTTPRoute{
			Path:    "/ws",
			Handler: WSHandler(server, nil),
		},
	}
}

func bindEventBridge(lc fx.Lifecycle, bridge *PushBridge, server *Server, logger *pkglogger.Logger) {
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

func bindApprovalLifecycle(lc fx.Lifecycle, approvals *ApprovalManager, bridge *PushBridge, server *Server, logger *pkglogger.Logger) {
	if logger == nil {
		logger = pkglogger.Get()
	}
	registerApprovalRestoreOnConnect(approvals, bridge, server, logger)
	cleanupCancel := func() {}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			var err error
			cleanupCancel, err = startApprovalLifecycle(ctx, approvals, bridge, server, logger)
			return err
		},
		OnStop: func(ctx context.Context) error {
			cleanupCancel()
			return shutdownPendingApprovals(ctx, approvals, logger)
		},
	})
}

func registerApprovalRestoreOnConnect(approvals *ApprovalManager, bridge *PushBridge, server *Server, logger *pkglogger.Logger) {
	if server == nil {
		return
	}
	server.OnConnectUI(func(current *jrpc2.Server) {
		if err := restorePendingApprovals(context.Background(), approvals, bridge, server, current); err != nil {
			logger.Warn("rpc: restore pending approvals on connect failed", "error", err)
		}
	})
}

func startApprovalLifecycle(
	ctx context.Context,
	approvals *ApprovalManager,
	bridge *PushBridge,
	server *Server,
	logger *pkglogger.Logger,
) (context.CancelFunc, error) {
	cleanupCancel := func() {}
	if err := restoreActiveApprovals(ctx, approvals, bridge, server); err != nil {
		return cleanupCancel, err
	}
	if approvals == nil {
		return cleanupCancel, nil
	}
	cleanupCtx, cancel := context.WithCancel(context.Background())
	cleanupCancel = cancel
	go startApprovalCleanupLoop(cleanupCtx, approvals, approvalCleanupInterval, DefaultApprovalTimeout, logger)
	return cleanupCancel, nil
}

func restoreActiveApprovals(ctx context.Context, approvals *ApprovalManager, bridge *PushBridge, server *Server) error {
	if approvals == nil || server == nil {
		return nil
	}
	for _, current := range server.snapshotActive() {
		if err := restorePendingApprovals(ctx, approvals, bridge, server, current); err != nil {
			return err
		}
	}
	return nil
}

func restorePendingApprovals(ctx context.Context, approvals *ApprovalManager, bridge *PushBridge, server *Server, current *jrpc2.Server) error {
	if approvals == nil || server == nil || current == nil {
		return nil
	}
	if server.PeerKind(current) != dto.PeerKindUI {
		return nil
	}
	return approvals.RestorePending(ctx, bridge, current)
}

func shutdownPendingApprovals(ctx context.Context, approvals *ApprovalManager, logger *pkglogger.Logger) error {
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
