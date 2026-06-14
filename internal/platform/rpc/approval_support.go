package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/creachadair/jrpc2"
)

// DefaultApprovalTimeout is the package-level default; tests use setApprovalTimeoutForTest.
var DefaultApprovalTimeout = 5 * time.Minute

type approvalContextKey string

const approvalAutoDeclineOnCancelKey approvalContextKey = "approval_auto_decline_on_cancel"

func normalizeApprovalRequest(req ApprovalRequest) (ApprovalRequest, error) {
	callID := approvalCallID(shared.FirstNonEmpty(req.CallID, req.ApprovalID), req.RequestID)
	if callID == "" {
		return ApprovalRequest{}, ErrInvalidState("approval call id is required")
	}
	req.CallID = callID
	req.ToolName = strings.TrimSpace(req.ToolName)
	req.ApprovalID = strings.TrimSpace(req.ApprovalID)
	req.Kind = shared.FirstNonEmpty(strings.TrimSpace(req.Kind), "tool")
	req.State = shared.FirstNonEmpty(strings.TrimSpace(req.State), string(agentdto.StateAwaitingUserInput))
	req.SourceMethod = strings.TrimSpace(req.SourceMethod)
	req.Reason = strings.TrimSpace(req.Reason)
	req.ApprovalPolicy = strings.TrimSpace(req.ApprovalPolicy)
	req.Payload = shared.CloneJSONMap(req.Payload)
	return req, nil
}

// WithApprovalDeadline applies the default approval timeout when the caller did
// not already provide an explicit deadline.
// WithApprovalDeadline 设置审批截止时间。
func WithApprovalDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx = shared.NonNilContext(ctx)
	if DefaultApprovalTimeout <= 0 {
		return ctx, func() {}
	}
	return platformconfig.WithTimeoutIfNone(ctx, DefaultApprovalTimeout)
}

// WithApprovalAutoDeclineOnCancel 设置审批autodeclineoncancel。
func WithApprovalAutoDeclineOnCancel(ctx context.Context) context.Context {
	return context.WithValue(shared.NonNilContext(ctx), approvalAutoDeclineOnCancelKey, true)
}

func waitForApproval(ctx context.Context, pending *pendingApproval) (contract.ApprovalDecision, error) {
	if pending == nil {
		return contract.ApprovalDecision{}, ErrInvalidState("approval pending state is nil")
	}
	ctx = shared.NonNilContext(ctx)
	select {
	case <-ctx.Done():
		return contract.ApprovalDecision{}, ctx.Err()
	case <-pending.done:
		return pending.decision, pending.err
	}
}

// decodeApprovalDecision 解码审批decision。
func decodeApprovalDecision(raw json.RawMessage) (contract.ApprovalDecision, error) {
	raw = shared.CloneRawMessage(raw)
	var approved bool
	if err := json.Unmarshal(raw, &approved); err == nil {
		return contract.ApprovalDecision{Approved: boolPtr(approved), Detail: raw}, nil
	}
	var decisionText string
	if err := json.Unmarshal(raw, &decisionText); err == nil {
		decision := contract.ApprovalDecision{Reason: strings.TrimSpace(decisionText), Detail: raw}
		if approved, ok := normalizeDecisionString(decisionText); ok {
			decision.Approved = boolPtr(approved)
		}
		return decision, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return contract.ApprovalDecision{}, ErrInvalidState("approval callback returned invalid payload")
	}
	decision := contract.ApprovalDecision{Reason: stringFromMap(payload, "reason", "decision"), Detail: raw}
	if value, ok := payload["approved"].(bool); ok {
		decision.Approved = boolPtr(value)
		return decision, nil
	}
	if approved, ok := normalizeDecisionString(stringFromMap(payload, "decision", "reason")); ok {
		decision.Approved = boolPtr(approved)
	}
	return decision, nil
}

func normalizeDecisionString(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "accept", "acceptforsession", "approve", "approved", "yes", "y", "true", "1":
		return true, true
	case "decline", "deny", "denied", "reject", "rejected", "cancel", "no", "n", "false", "0":
		return false, true
	default:
		return false, false
	}
}

func mapApprovalWaitErr(err error, callID string) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ErrApprovalTimeout("approval timed out for call " + callID)
	case errors.Is(err, context.Canceled):
		return err
	default:
		return err
	}
}

// dispatchApprovalDecision 派发审批decision。
func dispatchApprovalDecision(req ApprovalRequest, bridge *PushBridge, server *jrpc2.Server) (contract.ApprovalDecision, string, bool) {
	switch {
	case shouldAutoApproveUserInput(req):
		return approvedDecision(), "", true
	case bridge == nil && server == nil:
		return declinedDecision(""), "", true
	case bridge == nil:
		return declinedDecision(""), "approval dispatch misconfigured: callback bridge is nil while rpc server is present", true
	case server == nil:
		return declinedDecision(""), "approval dispatch misconfigured: rpc server is nil while callback bridge is present", true
	default:
		return contract.ApprovalDecision{}, "", false
	}
}

func canceledApprovalDecision(ctx context.Context, err error) (contract.ApprovalDecision, bool) {
	if !shouldAutoDeclineOnCancel(ctx) || !errors.Is(err, context.Canceled) {
		return contract.ApprovalDecision{}, false
	}
	return declinedDecision(""), true
}

func shouldAutoDeclineOnCancel(ctx context.Context) bool {
	enabled, _ := shared.NonNilContext(ctx).Value(approvalAutoDeclineOnCancelKey).(bool)
	return enabled
}

func isRecoverableDispatchErr(err error) bool {
	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) {
		return false
	}
	return isExpectedCloseErr(err)
}

// decisionReason 处理decisionreason。
func decisionReason(decision contract.ApprovalDecision, err error) string {
	if decision.Reason != "" {
		return decision.Reason
	}
	if reason := detailReason(decision.Detail); reason != "" {
		return reason
	}
	if err != nil {
		return err.Error()
	}
	switch {
	case decision.Approved == nil:
		return ""
	case *decision.Approved:
		return "approved"
	default:
		return "declined"
	}
}

func approvalCallID(callID string, requestID *int64) string {
	callID = strings.TrimSpace(callID)
	if callID != "" {
		return callID
	}
	if requestID != nil && *requestID > 0 {
		return strconv.FormatInt(*requestID, 10)
	}
	return ""
}

func pendingStorageKey(callID string, requestID *int64) string {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return ""
	}
	if requestID == nil || *requestID <= 0 {
		return callID
	}
	return callID + ":" + strconv.FormatInt(*requestID, 10)
}

func cloneApprovalRequest(req ApprovalRequest, requestID *int64) ApprovalRequest {
	req.RequestID = cloneInt64Ptr(requestID)
	req.Payload = shared.CloneJSONMap(req.Payload)
	return req
}

func shouldAutoApproveUserInput(req ApprovalRequest) bool {
	return isRequestUserInputKind(req.Kind) && strings.EqualFold(approvalPolicy(req), "never")
}

func approvalPolicy(req ApprovalRequest) string {
	return shared.FirstNonEmpty(req.ApprovalPolicy, stringFromMap(req.Payload, "approvalPolicy", "approval_policy"))
}

func cloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func boolPtr(value bool) *bool {
	return &value
}

func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func stringFromMap(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func decisionApproved(decision contract.ApprovalDecision) bool {
	return decision.Approved != nil && *decision.Approved
}

func detailReason(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return stringFromMap(payload, "reason", "decision")
}

func isPendingDone(pending *pendingApproval) bool {
	select {
	case <-pending.done:
		return true
	default:
		return false
	}
}
