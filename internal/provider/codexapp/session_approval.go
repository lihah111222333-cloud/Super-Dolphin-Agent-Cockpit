package codexapp

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func (s *session) handleApprovalRequest(method string, params json.RawMessage) {
	if s.approvals == nil {
		return
	}
	payload := append(json.RawMessage(nil), params...)
	go func() {
		if err := s.requestToolApproval(strings.TrimSpace(method), payload); err != nil && s.logger != nil {
			s.logger.Warn("codexapp: approval request failed", "method", method, "error", err)
		}
	}()
}

func (s *session) requestToolApproval(method string, params json.RawMessage) error {
	req, requestID, ok := s.buildApprovalRequest(method, decodeEventPayload(params))
	if !ok {
		return nil
	}
	decision, err := s.approvals.RequestApproval(s.ctx, nil, nil, req)
	if err != nil {
		return err
	}
	return s.sendApprovalDecision(requestID, decision)
}

func (s *session) buildApprovalRequest(method string, payload map[string]any) (rpc.ApprovalRequest, int64, bool) {
	if len(payload) == 0 {
		return rpc.ApprovalRequest{}, 0, false
	}
	requestID := int64Value(payload, "requestId", "request_id")
	if requestID <= 0 {
		return rpc.ApprovalRequest{}, 0, false
	}
	callID := firstNonEmpty(
		stringValue(payload, "callId", "call_id"),
		stringValue(payload, "approvalId", "approval_id"),
		strconv.FormatInt(requestID, 10),
	)
	requestRef := requestID
	return rpc.ApprovalRequest{
		CallID:       callID,
		ApprovalID:   stringValue(payload, "approvalId", "approval_id"),
		ToolName:     firstNonEmpty(stringValue(payload, "toolName", "tool_name", "tool"), stringValue(nestedValue(payload, "item"), "toolName", "tool")),
		AgentID:      s.agentID,
		ThreadID:     firstNonEmpty(stringValue(payload, "threadId", "thread_id"), s.threadID),
		TurnID:       firstNonEmpty(stringValue(payload, "turnId", "turn_id"), stringValue(nestedValue(payload, "turn"), "id")),
		Reason:       stringValue(payload, "reason", "message"),
		SourceMethod: method,
		RequestID:    &requestRef,
		Payload:      payload,
	}, requestID, callID != ""
}

func (s *session) sendApprovalDecision(requestID int64, decision contract.ApprovalDecision) error {
	if requestID <= 0 {
		return errors.New("codexapp: approval request id is required")
	}
	params := map[string]any{"requestId": requestID}
	if decision.Approved != nil {
		params["approved"] = *decision.Approved
	}
	if len(decision.Detail) > 0 {
		params["decision"] = append(json.RawMessage(nil), decision.Detail...)
	} else if reason := strings.TrimSpace(decision.Reason); reason != "" {
		params["decision"] = reason
	}
	callCtx, cancel := withTimeout(s.ctx, 10*time.Second)
	defer cancel()
	_, err := s.transport.Call(callCtx, "approval/respond", params)
	return err
}
