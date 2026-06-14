package rpc

import (
	"context"
	"net/http"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

var Module = fx.Module("rpc",
	fx.Provide(
		NewServer,
		NewPushBridge,
		NewApprovalManager,
		NewCapabilityResolver,
		provideWSRoute,
		NewRPCPushSubscribers,
		newPushNotificationWorkerProvider,
		func(s *Server) contract.RPCDispatcher { return s },
		func(m *ApprovalManager) contract.ApprovalResponder { return m },
		func(m *ApprovalManager, bridge *PushBridge, server *Server) contract.ApprovalRequester {
			return approvalRequester{
				manager: m,
				bridge:  bridge,
				server:  server,
			}
		},
		// P22 P1b Finding 4: approval-cleanup ticker owned by run.Group via
		// the root `group:"runners"` aggregation. bindApprovalLifecycle now
		// only handles startup restore + on-connect replay + OnStop shutdown
		// of pending approvals; the long-running cleanup loop lives in
		// ApprovalCleanupRunner (approval_cleanup_runner.go).
		fx.Annotate(NewApprovalCleanupRunner, fx.ResultTags(`group:"runners"`)),
	),
	fx.Provide(
		fx.Annotate(pushWorkerAsRunner, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(registerAllHandlers),
	fx.Invoke(bindApprovalLifecycle),
)

type approvalRequester struct {
	manager *ApprovalManager
	bridge  *PushBridge
	server  *Server
}

var _ contract.ApprovalRequester = approvalRequester{}
var _ contract.RPCDispatcher = (*Server)(nil)

// RequestApproval 处理请求审批。
func (r approvalRequester) RequestApproval(ctx context.Context, req contract.ApprovalRequest) (contract.ApprovalDecision, error) {
	if r.manager == nil {
		return contract.ApprovalDecision{}, ErrInvalidState("approval manager is nil")
	}
	return r.manager.RequestApproval(ctx, r.bridge, r.activeServer(), ApprovalRequest{
		CallID:       req.CallID,
		ApprovalID:   req.ApprovalID,
		ToolName:     req.ToolName,
		AgentID:      req.AgentID,
		ThreadID:     req.ThreadID,
		TurnID:       req.TurnID,
		Reason:       req.Reason,
		Kind:         req.Kind,
		SourceMethod: req.SourceMethod,
		Payload:      req.Payload,
	})
}

func (r approvalRequester) activeServer() *jrpc2.Server {
	if r.server == nil {
		return nil
	}
	for _, current := range r.server.snapshotActive() {
		if r.server.PeerKind(current) == dto.PeerKindUI {
			return current
		}
	}
	for _, current := range r.server.snapshotActive() {
		return current
	}
	return nil
}

type Params struct {
	fx.In

	Logger        *pkglogger.Logger
	Config        *config.Config
	TraceRecorder TraceRecorder `optional:"true"`
}

// HandlerMapResult is the fx-compatible output wrapper for handler.Map.
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

func newPushNotificationWorkerProvider(bridge *PushBridge, server *Server, logger *pkglogger.Logger) *pushNotificationWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return newPushNotificationWorker(server, bridge, logger)
}

// bindApprovalLifecycle owns startup restore + on-connect replay + OnStop
// shutdown for pending approvals. P22 P1b Finding 4 extracted the long
// cleanup loop into ApprovalCleanupRunner; this fx.Hook no longer spawns
// background goroutines.
func bindApprovalLifecycle(lc fx.Lifecycle, approvals *ApprovalManager, bridge *PushBridge, server *Server, logger *pkglogger.Logger) {
	if logger == nil {
		logger = pkglogger.Get()
	}
	registerApprovalRestoreOnConnect(approvals, bridge, server, logger)
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return restoreActiveApprovals(ctx, approvals, bridge, server)
		},
		OnStop: func(ctx context.Context) error {
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

// P22 P1b Finding 4: startApprovalLifecycle was deleted. Its two roles have
// split: startup restore runs inline inside bindApprovalLifecycle.OnStart
// (via restoreActiveApprovals), and the long-running cleanup ticker is now
// owned by ApprovalCleanupRunner (approval_cleanup_runner.go).

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

// waitPendingApprovals 等待待处理approvals。
func waitPendingApprovals(ctx context.Context, approvals *ApprovalManager, grace time.Duration) {
	if approvals == nil || grace <= 0 {
		return
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	poll := 100 * time.Millisecond
	pollTimer := time.NewTimer(timerDelayWithJitter(poll))
	defer pollTimer.Stop()
	for len(approvals.PendingSnapshot()) != 0 {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			return
		case <-pollTimer.C:
			pollTimer.Reset(timerDelayWithJitter(poll))
		}
	}
}
