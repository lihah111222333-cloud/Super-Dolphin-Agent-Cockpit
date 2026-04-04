package rpc

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/eventsurface"
)

type approvalMethodCatalogSpec struct {
	defaultCallback     string
	requestUserInput    string
	aliases             map[string]string
	pushEligibleMethods map[string]struct{}
}

var approvalMethodCatalog = approvalMethodCatalogSpec{
	defaultCallback:  DefaultApprovalCallbackMethod,
	requestUserInput: approvalCallbackMethodCommandExecution,
	aliases: map[string]string{
		legacyApprovalCallbackMethod:     DefaultApprovalCallbackMethod,
		legacyApprovalEventMethod:        approvalCallbackMethodCommandExecution,
		"codex/event/request_user_input": approvalCallbackMethodCommandExecution,
		"item/tool/request_user_input":   approvalCallbackMethodCommandExecution,
		"item/tool/requestUserInput":     approvalCallbackMethodCommandExecution,
		"request_user_input":             approvalCallbackMethodCommandExecution,
	},
	pushEligibleMethods: map[string]struct{}{
		DefaultApprovalCallbackMethod:          {},
		approvalCallbackMethodCommandExecution: {},
		approvalCallbackMethodFileChange:       {},
		approvalCallbackMethodSkillRequest:     {},
	},
}

func approvedDecision() contract.ApprovalDecision {
	return contract.ApprovalDecision{
		Approved: boolPtr(true),
		Reason:   "auto_approved",
	}
}

func declinedDecision(reason string) contract.ApprovalDecision {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "decline"
	}
	return contract.ApprovalDecision{
		Approved: boolPtr(false),
		Reason:   reason,
	}
}

func errorDecision(err error) contract.ApprovalDecision {
	return contract.ApprovalDecision{
		Reason: decisionReason(contract.ApprovalDecision{}, err),
	}
}

func (c approvalMethodCatalogSpec) normalize(method string) string {
	method = strings.TrimSpace(method)
	if method == "" {
		return ""
	}
	if alias, ok := c.aliases[method]; ok {
		return alias
	}
	return method
}

func (c approvalMethodCatalogSpec) callback(req ApprovalRequest) string {
	for _, candidate := range []string{req.CallbackMethod, req.SourceMethod} {
		if method := c.normalize(candidate); method != "" {
			return method
		}
	}
	if isRequestUserInputKind(req.Kind) {
		return c.requestUserInput
	}
	return c.defaultCallback
}

func (c approvalMethodCatalogSpec) isPushMethod(method string) bool {
	method = c.normalize(method)
	if method == "" {
		return false
	}
	_, ok := c.pushEligibleMethods[method]
	return ok
}

func baseThreadHandler[Req, Resp any](fn func(context.Context, Req) (Resp, error), extras ...Middleware) handler.Func {
	mws := make([]Middleware, 0, 2+len(extras))
	mws = append(mws, Validate(), ThreadScope())
	mws = append(mws, extras...)
	return Wrap(mws...)(StrictHandler(fn))
}

func broadcastNotifications(ctx context.Context, server *Server, bridge *PushBridge, notifications []eventsurface.Notification) {
	if server == nil || bridge == nil || len(notifications) == 0 {
		return
	}
	ctx = nonNilContext(ctx)
	for _, notification := range notifications {
		server.NotifyAll(ctx, bridge, notification.Method, notification.Payload)
	}
}

func isExpectedCloseErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, channel.ErrClosed) ||
		channel.IsErrClosing(err)
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func rpcError(code int, msg string) error {
	return jrpc2.Errorf(jrpc2.Code(code), "%s", strings.TrimSpace(msg))
}

func rpcErrorData(code int, msg string, data map[string]any) error {
	rpcErr := jrpc2.Errorf(jrpc2.Code(code), "%s", strings.TrimSpace(msg))
	if len(data) == 0 {
		return rpcErr
	}
	return rpcErr.WithData(data)
}
