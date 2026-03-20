package rpc

import (
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/kelindar/event"
)

const DefaultApprovalCallbackMethod = "tool/approval/request"

func (m *ApprovalManager) publishRequested(bridge *PushBridge, pending *pendingApproval) {
	if bridge == nil || bridge.dispatcher == nil || pending == nil {
		return
	}
	event.Publish(bridge.dispatcher, tooldto.ToolApprovalRequested{
		ToolApprovalHeader: approvalHeader(pending.request),
		RequestID:          int64Value(pending.requestID),
		Reason:             pending.request.Reason,
	})
}

func (m *ApprovalManager) publishResolved(pending *pendingApproval, decision ApprovalDecision, err error) {
	if pending == nil || pending.dispatcher == nil {
		return
	}
	event.Publish(pending.dispatcher, tooldto.ToolApprovalResolved{
		ToolApprovalHeader: approvalHeader(pending.request),
		Approved:           decision.Approved,
		Decision:           decisionReason(decision, err),
	})
}

func callbackMethod(req ApprovalRequest) string {
	return firstNonEmpty(strings.TrimSpace(req.CallbackMethod), DefaultApprovalCallbackMethod)
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

func approvalHeader(req ApprovalRequest) shared.ToolApprovalHeader {
	return shared.ToolApprovalHeader{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: shared.TurnHeader{
				AgentHeader: shared.AgentHeader{
					EventHeader: shared.EventHeader{Timestamp: time.Now()},
					AgentID:     req.AgentID,
					ThreadID:    req.ThreadID,
				},
				TurnID: req.TurnID,
			},
			CallID:   req.CallID,
			ToolName: req.ToolName,
		},
		ApprovalID: firstNonEmpty(req.ApprovalID, req.CallID),
	}
}
