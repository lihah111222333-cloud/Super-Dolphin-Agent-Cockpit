package turn

import (
	"github.com/creachadair/jrpc2/handler"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

// NewTurnHandlers 注册 turn/start、steer、interrupt、forceComplete 和 approval/respond RPC handler。
func NewTurnHandlers(
	svc Service,
	resolver contract.SessionResolver,
	approver contract.ApprovalResponder,
	capResolver contract.CapabilityResolver,
	runtimeReader ThreadStateConfigReader,
	spawner contract.PendingLaunchSpawner,
) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		contract.TurnRPCStart: turnStartHandler(svc, resolver, spawner, capResolver, runtimeReader),
		"turn/steer":          turnSteerHandler(svc, resolver, capResolver, runtimeReader),
		"turn/interrupt":      turnInterruptHandler(svc, resolver),
		"turn/forceComplete":  turnForceCompleteHandler(svc, resolver),
		"approval/respond":    approvalRespondHandler(approver),
	}}
}
