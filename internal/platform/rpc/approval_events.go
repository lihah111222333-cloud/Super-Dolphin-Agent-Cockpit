package rpc

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/kelindar/event"
)

const (
	DefaultApprovalCallbackMethod          = "approval/request"
	approvalCallbackMethodCommandExecution = "item/commandExecution/requestApproval"
	approvalCallbackMethodFileChange       = "item/fileChange/requestApproval"
	approvalCallbackMethodSkillRequest     = "skill/requestApproval"
	legacyApprovalCallbackMethod           = "tool/approval/request"
	legacyApprovalEventMethod              = "tool.approval.requested"
)

func (m *ApprovalManager) publishRequested(pending *pendingApproval) {
	if pending == nil || pending.dispatcher == nil {
		return
	}
	event.Publish(pending.dispatcher, tooldto.ToolApprovalRequested{
		ToolApprovalHeader: approvalHeader(pending.request, approvalRequestedAt(pending)),
		RequestID:          int64Value(pending.requestID),
		Reason:             pending.request.Reason,
		Kind:               strings.TrimSpace(pending.request.Kind),
	})
}

func (m *ApprovalManager) publishResolved(pending *pendingApproval, decision contract.ApprovalDecision, err error) {
	if pending == nil || pending.dispatcher == nil {
		return
	}
	event.Publish(pending.dispatcher, tooldto.ToolApprovalResolved{
		ToolApprovalHeader: approvalHeader(pending.request, approvalResolvedAt(decision)),
		Approved:           decisionApproved(decision),
		Decision:           decisionReason(decision, err),
		Kind:               strings.TrimSpace(pending.request.Kind),
	})
}

func callbackMethod(req ApprovalRequest) string {
	for _, candidate := range []string{req.CallbackMethod, req.SourceMethod} {
		if method := normalizeApprovalCallbackMethod(candidate); method != "" {
			return method
		}
	}
	if isRequestUserInputKind(req.Kind) {
		return approvalCallbackMethodCommandExecution
	}
	return DefaultApprovalCallbackMethod
}

func normalizeApprovalCallbackMethod(method string) string {
	switch strings.TrimSpace(method) {
	case "":
		return ""
	case legacyApprovalCallbackMethod:
		return DefaultApprovalCallbackMethod
	case approvalCallbackMethodCommandExecution,
		approvalCallbackMethodFileChange,
		approvalCallbackMethodSkillRequest:
		return strings.TrimSpace(method)
	case legacyApprovalEventMethod:
		return approvalCallbackMethodCommandExecution
	case "codex/event/request_user_input", "item/tool/request_user_input", "item/tool/requestUserInput", "request_user_input":
		return approvalCallbackMethodCommandExecution
	default:
		return strings.TrimSpace(method)
	}
}

func isRequestUserInputKind(kind string) bool {
	return strings.EqualFold(strings.TrimSpace(kind), "request_user_input")
}

func callbackParams(pending *pendingApproval) map[string]any {
	req := pending.request
	params := cloneMap(req.Payload)
	params["requestId"] = int64Value(pending.requestID)
	params["callId"] = pending.callID
	params["toolName"] = pending.toolName
	params["approvalId"] = firstNonEmpty(req.ApprovalID, pending.callID)
	params["reason"] = req.Reason
	params["kind"] = req.Kind
	params["state"] = req.State
	params["sourceMethod"] = req.SourceMethod
	if req.AgentID != "" {
		params["agentId"] = req.AgentID
	}
	if req.ThreadID != "" {
		params["threadId"] = req.ThreadID
	}
	if req.TurnID != "" {
		params["turnId"] = req.TurnID
	}
	return params
}

func approvalHeader(req ApprovalRequest, timestamp time.Time) shared.ToolApprovalHeader {
	return shared.ToolApprovalHeader{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: shared.TurnHeader{
				AgentHeader: shared.AgentHeader{
					ThreadHeader: shared.ThreadHeader{
						EventHeader: shared.EventHeader{Timestamp: timestamp},
						ThreadID:    req.ThreadID,
					},
					AgentID: req.AgentID,
				},
				TurnIDHeader: shared.TurnIDHeader{TurnID: req.TurnID},
			},
			CallID:   req.CallID,
			ToolName: req.ToolName,
		},
		ApprovalID: firstNonEmpty(req.ApprovalID, req.CallID),
	}
}

func approvalRequestedAt(pending *pendingApproval) time.Time {
	if pending == nil {
		return shared.FirstEventTime()
	}
	return shared.ResolveEventTime(nil, pending.request.Payload, pending.createdAt)
}

func approvalResolvedAt(decision contract.ApprovalDecision) time.Time {
	return shared.ResolveEventTime(nil, approvalDecisionPayload(decision))
}

func approvalRequestTime(req ApprovalRequest) time.Time {
	return shared.EventTimeFromPayload(req.Payload)
}

func approvalDecisionTime(decision contract.ApprovalDecision) time.Time {
	return shared.EventTimeFromPayload(approvalDecisionPayload(decision))
}

func approvalDecisionPayload(decision contract.ApprovalDecision) map[string]any {
	if len(decision.Detail) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(decision.Detail, &payload); err != nil {
		return nil
	}
	return payload
}
