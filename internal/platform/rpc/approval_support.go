package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
)

func normalizeApprovalRequest(req ApprovalRequest) (ApprovalRequest, error) {
	callID := approvalCallID(firstNonEmpty(req.CallID, req.ApprovalID), req.RequestID)
	if callID == "" {
		return ApprovalRequest{}, ErrInvalidState("approval call id is required")
	}
	req.CallID = callID
	req.ToolName = strings.TrimSpace(req.ToolName)
	req.ApprovalID = strings.TrimSpace(req.ApprovalID)
	req.Kind = firstNonEmpty(strings.TrimSpace(req.Kind), "tool")
	req.State = firstNonEmpty(strings.TrimSpace(req.State), agentdto.StateAwaitingUserInput)
	req.SourceMethod = strings.TrimSpace(req.SourceMethod)
	req.Reason = strings.TrimSpace(req.Reason)
	req.Payload = cloneMap(req.Payload)
	return req, nil
}

func waitForApproval(ctx context.Context, pending *pendingApproval) (contract.ApprovalDecision, error) {
	if pending == nil {
		return contract.ApprovalDecision{}, ErrInvalidState("approval pending state is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return contract.ApprovalDecision{}, ctx.Err()
	case <-pending.done:
		return pending.decision, pending.err
	}
}

func decodeApprovalDecision(raw json.RawMessage) (contract.ApprovalDecision, error) {
	var approved bool
	if err := json.Unmarshal(raw, &approved); err == nil {
		return contract.ApprovalDecision{Approved: approved}, nil
	}
	var decisionText string
	if err := json.Unmarshal(raw, &decisionText); err == nil {
		if approved, ok := normalizeDecisionString(decisionText); ok {
			return contract.ApprovalDecision{Approved: approved, Reason: decisionText}, nil
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return contract.ApprovalDecision{}, ErrInvalidState("approval callback returned invalid payload")
	}
	if value, ok := payload["approved"].(bool); ok {
		return contract.ApprovalDecision{Approved: value, Reason: stringFromMap(payload, "reason", "decision")}, nil
	}
	decision, ok := normalizeDecisionString(stringFromMap(payload, "decision", "reason"))
	if !ok {
		return contract.ApprovalDecision{}, ErrInvalidState("approval decision is required")
	}
	return contract.ApprovalDecision{Approved: decision, Reason: stringFromMap(payload, "decision", "reason")}, nil
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

func isRecoverableDispatchErr(err error) bool {
	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) {
		return false
	}
	return errors.Is(err, channel.ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)
}

func isApprovalNotFound(err error) bool {
	var rpcErr *jrpc2.Error
	return errors.As(err, &rpcErr) && rpcErr.Code == jrpc2.Code(CodeNotFound)
}

func decisionReason(decision contract.ApprovalDecision, err error) string {
	if decision.Reason != "" {
		return decision.Reason
	}
	if err != nil {
		return err.Error()
	}
	if decision.Approved {
		return "approved"
	}
	return "declined"
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

func cloneApprovalRequest(req ApprovalRequest, requestID *int64) ApprovalRequest {
	req.RequestID = cloneInt64Ptr(requestID)
	req.Payload = cloneMap(req.Payload)
	return req
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func isPendingDone(pending *pendingApproval) bool {
	select {
	case <-pending.done:
		return true
	default:
		return false
	}
}
