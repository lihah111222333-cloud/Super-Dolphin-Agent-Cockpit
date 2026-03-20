package turn

import (
	"context"
	"errors"

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
	withSession := func(ctx context.Context, fn func(context.Context, contract.Session) (any, error)) (any, error) {
		if resolver == nil {
			return nil, errors.New("turn rpc: session resolver is not configured")
		}
		threadID := rpc.ThreadIDFrom(ctx)
		session, err := resolver.ResolveSession(ctx, threadID)
		if err != nil {
			return nil, err
		}
		return fn(ctx, session)
	}

	return rpc.HandlerMapResult{Handlers: handler.Map{
		"turn/start": rpc.CapabilityThreadHandler("message_send", capResolver,
			func(ctx context.Context, p turnStartParams) (any, error) {
				return withSession(ctx, func(ctx context.Context, session contract.Session) (any, error) {
					input := buildPrepareInput(p, session)
					req, err := svc.PrepareTurn(ctx, session, input)
					if err != nil {
						return nil, err
					}
					handle, err := svc.StartTurn(ctx, session, req)
					if err != nil {
						return nil, err
					}
					return turnStartResult{TurnID: handle.LocalID()}, nil
				})
			}),

		"turn/steer": rpc.CapabilityThreadHandler("message_send", capResolver,
			func(ctx context.Context, p turnSteerParams) (any, error) {
				return withSession(ctx, func(ctx context.Context, session contract.Session) (any, error) {
					handle, err := svc.SteerTurn(ctx, session, p.Prompt)
					if err != nil {
						return nil, err
					}
					return turnStartResult{TurnID: handle.LocalID()}, nil
				})
			}),

		"turn/interrupt": rpc.ThreadHandler(
			func(ctx context.Context, p turnInterruptParams) (any, error) {
				return withSession(ctx, func(ctx context.Context, session contract.Session) (any, error) {
					return nil, svc.InterruptTurn(ctx, session, p.Source)
				})
			}),

		"turn/forceComplete": rpc.ThreadHandler(
			func(ctx context.Context, p threadIDOnlyParams) (any, error) {
				return withSession(ctx, func(ctx context.Context, session contract.Session) (any, error) {
					return nil, svc.ForceCompleteTurn(ctx, session)
				})
			}),

		"review/start": rpc.ThreadHandler(
			func(ctx context.Context, p threadIDOnlyParams) (any, error) {
				return nil, rpc.ErrNotImplemented("review/start is not yet implemented")
			}),

		"approval/respond": rpc.StrictHandler(
			func(ctx context.Context, p approvalRespondParams) (any, error) {
				if approver == nil {
					return nil, errors.New("turn rpc: approval responder is not configured")
				}
				return nil, approver.Respond(p.CallID, p.RequestID, contract.ApprovalDecision{
					Approved: p.Approved,
					Reason:   p.Decision,
				})
			}),
	}}
}
