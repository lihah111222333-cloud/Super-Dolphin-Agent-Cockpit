package turn

import (
	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func NewTurnHandlers(
	svc Service,
	resolver contract.SessionResolver,
	approver contract.ApprovalResponder,
	capResolver rpc.CapabilityResolver,
	runtimeReader ThreadStateConfigReader,
	spawner contract.PendingLaunchSpawner,
) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		contract.TurnRPCStart: turnStartHandler(svc, resolver, spawner, capResolver, runtimeReader),
		"turn/steer":          turnSteerHandler(svc, resolver, capResolver, runtimeReader),
		"turn/interrupt":      turnInterruptHandler(svc, resolver),
		"turn/forceComplete":  turnForceCompleteHandler(svc, resolver),
		"approval/respond":    approvalRespondHandler(approver),
	}}
}
