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
	raw = cloneRawMessage(raw)
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

func boolPtr(value bool) *bool {
	return &value
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
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
