package rpc

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// ErrNoUIPeer reports that an approval callback has no authenticated UI peer.
var ErrNoUIPeer = errors.New("rpc no UI peer")

// Module 组装 RPC server、handler、push bridge、审批生命周期和后台 runner。
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
		// 审批清理 ticker 由根 runners 聚合托管；bindApprovalLifecycle 只处理
		// 启动恢复、UI 重连重放和停止时 pending 审批清理。
		fx.Annotate(NewApprovalCleanupRunner, fx.ResultTags(`group:"runners"`)),
	),
	fx.Provide(
		fx.Annotate(pushWorkerAsRunner, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(registerAllHandlers),
	fx.Invoke(bindApprovalLifecycle),
)

// approvalRequester 把 contract 层审批请求桥接到 RPC ApprovalManager。
type approvalRequester struct {
	manager *ApprovalManager
	bridge  *PushBridge
	server  *Server
}

var _ contract.ApprovalRequester = approvalRequester{}
var _ contract.RPCDispatcher = (*Server)(nil)

// RequestApproval 将 contract.ApprovalRequest 转换为 RPC 内部 DTO 并派发给活跃客户端。
func (r approvalRequester) RequestApproval(ctx context.Context, req contract.ApprovalRequest) (contract.ApprovalDecision, error) {
	if r.manager == nil {
		return contract.ApprovalDecision{}, ErrInvalidState("approval manager is nil")
	}
	server := r.activeServer()
	bridge := r.bridge
	if server == nil {
		bridge = nil
	}
	decision, err := r.manager.RequestInternalApproval(ctx, bridge, server, ApprovalRequest{
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
	if err != nil {
		return decision, err
	}
	if server == nil {
		return decision, ErrNoUIPeer
	}
	return decision, nil
}

// activeServer 只选择 UI peer 作为审批回调目标，缺失时由调用方 fail-closed。
func (r approvalRequester) activeServer() *jrpc2.Server {
	if r.server == nil {
		return nil
	}
	for _, current := range r.server.snapshotActive() {
		if r.server.PeerKind(current) == dto.PeerKindUI {
			return current
		}
	}
	return nil
}

// Params 是 Server 构造所需的 fx 输入参数。
type Params struct {
	fx.In

	Logger        *pkglogger.Logger
	Config        *config.Config
	TraceRecorder TraceRecorder `optional:"true"`
}

// HandlerMapResult 是 handler.Map 的 fx 输出包装。
type HandlerMapResult struct {
	fx.Out

	Handlers handler.Map `group:"rpc_handlers"`
}

// HTTPRoute 描述可被外部 router 挂载的可选 HTTP 入口。
type HTTPRoute struct {
	Path    string
	Handler http.Handler
}

// HTTPRouteResult 是 HTTPRoute 的 fx 输出包装。
type HTTPRouteResult struct {
	fx.Out

	Route HTTPRoute `group:"rpc_http_routes"`
}

// serverParams 是注册 handler 时需要的 fx 输入。
type serverParams struct {
	fx.In

	Logger   *pkglogger.Logger
	Config   *config.Config
	Handlers []handler.Map `group:"rpc_handlers"`
}

// registerAllHandlers 把所有分组提供的 handler.Map 注册到 Server。
func registerAllHandlers(server *Server, p serverParams) {
	server.Register(p.Handlers...)
}

// provideWSRoute 暴露默认 WebSocket 路由，供宿主 HTTP server 挂载。
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

// newPushNotificationWorkerProvider 构造 RPC push worker 并补齐默认 logger。
func newPushNotificationWorkerProvider(bridge *PushBridge, server *Server, logger *pkglogger.Logger) *pushNotificationWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return newPushNotificationWorker(server, bridge, logger)
}

// bindApprovalLifecycle 负责审批启动恢复、UI 重连重放和停止时 pending 清理。
// 长期运行的 cleanup ticker 已交给 ApprovalCleanupRunner，避免 fx hook 自行起后台 goroutine。
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

// registerApprovalRestoreOnConnect 在 UI 连接建立时重放仍在等待的审批请求。
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

// 运行边界：审批启动恢复保留在 bindApprovalLifecycle.OnStart，
// 长期 cleanup ticker 由 ApprovalCleanupRunner 托管到根 runners 聚合。

// restoreActiveApprovals 对当前所有活跃连接尝试恢复 pending 审批。
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

// restorePendingApprovals 只在 UI peer 上重新派发 pending 审批。
func restorePendingApprovals(ctx context.Context, approvals *ApprovalManager, bridge *PushBridge, server *Server, current *jrpc2.Server) error {
	if approvals == nil || server == nil || current == nil {
		return nil
	}
	if server.PeerKind(current) != dto.PeerKindUI {
		return nil
	}
	return approvals.RestorePending(ctx, bridge, current)
}

// shutdownPendingApprovals 在停止阶段等待短暂 grace 后清理未完成审批。
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

// waitPendingApprovals 轮询等待 pending 审批清空，直到 ctx 取消或 grace 到期。
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
