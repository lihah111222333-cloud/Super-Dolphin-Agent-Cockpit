package codexapp

import (
	"context"
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
	if decision, ok := s.processedApprovalDecision(requestID); ok {
		return s.sendApprovalDecision(requestID, decision)
	}
	decision, err := s.requestApprovalDecision(req)
	if err != nil {
		return err
	}
	s.rememberProcessedApprovalDecision(requestID, decision)
	return s.sendApprovalDecision(requestID, decision)
}

func (s *session) requestApprovalDecision(req rpc.ApprovalRequest) (contract.ApprovalDecision, error) {
	ctx, cancel := approvalDecisionContext(s.ctx)
	defer cancel()
	if isRequestUserInputMethod(req.SourceMethod) {
		req.Kind = "request_user_input"
		return s.approvals.RequestUserInput(ctx, nil, nil, req)
	}
	return s.approvals.RequestApproval(ctx, nil, nil, req)
}

func approvalDecisionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return rpc.WithApprovalDeadline(rpc.WithApprovalAutoDeclineOnCancel(ctx))
}

func (s *session) setApprovalPolicy(policy string) {
	if s == nil {
		return
	}
	s.approvalPolicy.Store(strings.TrimSpace(policy))
}

func (s *session) approvalPolicyValue() string {
	if s == nil {
		return ""
	}
	value, _ := s.approvalPolicy.Load().(string)
	return strings.TrimSpace(value)
}

func (s *session) processedApprovalDecision(requestID int64) (contract.ApprovalDecision, bool) {
	if s == nil || requestID <= 0 {
		return contract.ApprovalDecision{}, false
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if len(s.processedApprovals) == 0 {
		return contract.ApprovalDecision{}, false
	}
	decision, ok := s.processedApprovals[requestID]
	if !ok {
		return contract.ApprovalDecision{}, false
	}
	return cloneApprovalDecision(decision), true
}

func (s *session) rememberProcessedApprovalDecision(requestID int64, decision contract.ApprovalDecision) {
	if s == nil || requestID <= 0 {
		return
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if s.processedApprovals == nil {
		s.processedApprovals = map[int64]contract.ApprovalDecision{}
	}
	s.processedApprovals[requestID] = cloneApprovalDecision(decision)
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
		CallID:         callID,
		ApprovalID:     stringValue(payload, "approvalId", "approval_id"),
		ToolName:       firstNonEmpty(stringValue(payload, "toolName", "tool_name", "tool"), stringValue(nestedValue(payload, "item"), "toolName", "tool")),
		AgentID:        s.agentID,
		ThreadID:       firstNonEmpty(stringValue(payload, "threadId", "thread_id"), s.ThreadID()),
		TurnID:         firstNonEmpty(stringValue(payload, "turnId", "turn_id"), stringValue(nestedValue(payload, "turn"), "id")),
		Reason:         stringValue(payload, "reason", "message"),
		SourceMethod:   method,
		RequestID:      &requestRef,
		ApprovalPolicy: s.approvalPolicyValue(),
		Payload:        payload,
	}, requestID, callID != ""
}

func (s *session) sendApprovalDecision(requestID int64, decision contract.ApprovalDecision) error {
	if requestID <= 0 {
		return errors.New("codexapp: approval request id is required")
	}
	if s == nil || s.transport == nil {
		return errors.New("codexapp: approval transport is not initialized")
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
	_, err := s.callTransport(callCtx, "approval/respond", params)
	return err
}

func cloneApprovalDecision(decision contract.ApprovalDecision) contract.ApprovalDecision {
	decision.Detail = append(json.RawMessage(nil), decision.Detail...)
	return decision
}

func isApprovalBridgeMethod(method string) bool {
	method = strings.TrimSpace(method)
	return method == rpc.DefaultApprovalCallbackMethod ||
		method == "tool/approval/request" ||
		method == "item/commandExecution/requestApproval" ||
		method == "item/fileChange/requestApproval" ||
		method == "skill/requestApproval" ||
		method == "tool.approval.requested" ||
		isRequestUserInputMethod(method)
}

func isRequestUserInputMethod(method string) bool {
	switch strings.TrimSpace(method) {
	case "request_user_input",
		"codex/event/request_user_input",
		"item/commandExecution/requestUserInput",
		"item/commandExecution/request_user_input",
		"item/tool/requestUserInput",
		"item/tool/request_user_input":
		return true
	default:
		return false
	}
}
