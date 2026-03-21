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
) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"turn/start":         turnStartHandler(svc, resolver, capResolver),
		"turn/steer":         turnSteerHandler(svc, resolver, capResolver),
		"turn/interrupt":     turnInterruptHandler(svc, resolver),
		"turn/forceComplete": turnForceCompleteHandler(svc, resolver),
		"review/start":       reviewStartHandler(),
		"approval/respond":   approvalRespondHandler(approver),
	}}
}
