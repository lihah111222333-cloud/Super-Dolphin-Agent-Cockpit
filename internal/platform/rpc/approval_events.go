package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/eventcore"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
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
	return approvalMethodCatalog.callback(req)
}

func isRequestUserInputKind(kind string) bool {
	return strings.EqualFold(strings.TrimSpace(kind), "request_user_input")
}

func callbackParams(pending *pendingApproval) map[string]any {
	req := pending.request
	params := shared.CloneJSONMap(req.Payload)
	params["requestId"] = int64Value(pending.requestID)
	params["callId"] = pending.callID
	params["toolName"] = pending.toolName
	params["approvalId"] = shared.FirstNonEmpty(req.ApprovalID, pending.callID)
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

func approvalHeader(req ApprovalRequest, timestamp time.Time) shareddto.ToolApprovalHeader {
	return shareddto.ToolApprovalHeader{
		ToolCallHeader: shareddto.ToolCallHeader{
			TurnHeader: shareddto.TurnHeader{
				AgentHeader: shareddto.AgentHeader{
					ThreadHeader: shareddto.ThreadHeader{
						EventHeader: shareddto.EventHeader{Timestamp: timestamp},
						ThreadID:    req.ThreadID,
					},
					AgentID: req.AgentID,
				},
				TurnIDHeader: shareddto.TurnIDHeader{TurnID: req.TurnID},
			},
			CallID:   req.CallID,
			ToolName: req.ToolName,
		},
		ApprovalID: shared.FirstNonEmpty(req.ApprovalID, req.CallID),
	}
}

func approvalRequestedAt(pending *pendingApproval) time.Time {
	if pending == nil {
		return shared.FirstEventTime()
	}
	return shared.ResolveEventTime(context.Background(), pending.request.Payload, pending.createdAt)
}

func approvalResolvedAt(decision contract.ApprovalDecision) time.Time {
	return shared.ResolveEventTime(context.Background(), approvalDecisionPayload(decision))
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
