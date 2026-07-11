package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

const (
	// DefaultApprovalCallbackMethod 是通用工具审批回调方法名。
	DefaultApprovalCallbackMethod          = "approval/request"
	approvalCallbackMethodCommandExecution = "item/commandExecution/requestApproval"
	approvalCallbackMethodFileChange       = "item/fileChange/requestApproval"
	approvalCallbackMethodSkillRequest     = "skill/requestApproval"
	legacyApprovalCallbackMethod           = "tool/approval/request"
	legacyApprovalEventMethod              = "tool.approval.requested"
)

// publishRequested 向事件总线发布审批已请求事件，供 UI 和审计订阅。
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

// publishResolved 向事件总线发布审批已完成事件，并保留决策原因。
func (m *ApprovalManager) publishResolved(pending *pendingApproval, decision contract.ApprovalDecision, err error) {
	if pending == nil || pending.dispatcher == nil {
		return
	}
	event.Publish(pending.dispatcher, tooldto.ToolApprovalResolved{
		ToolApprovalHeader: approvalHeader(pending.request, approvalResolvedAt(decision)),
		RequestID:          int64Value(pending.requestID),
		Approved:           decisionApproved(decision),
		Decision:           decisionReason(decision, err),
		Kind:               strings.TrimSpace(pending.request.Kind),
	})
}

// callbackMethod 根据请求来源和兼容别名选择客户端回调方法。
func callbackMethod(req ApprovalRequest) string {
	return approvalMethodCatalog.callback(req)
}

// isRequestUserInputKind 判断审批 kind 是否表示用户输入请求。
func isRequestUserInputKind(kind string) bool {
	return strings.EqualFold(strings.TrimSpace(kind), "request_user_input")
}

// callbackParams 构造客户端审批回调参数，保留原 payload 中的额外字段。
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

// approvalHeader 组装 tool approval 事件头，保证 requested/resolved 使用同一身份字段。
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

// approvalRequestedAt 从 payload 中解析事件时间，缺失时回退到 pending 创建时间。
func approvalRequestedAt(pending *pendingApproval) time.Time {
	if pending == nil {
		return shared.FirstEventTime()
	}
	return shared.ResolveEventTime(context.Background(), pending.request.Payload, pending.createdAt)
}

// approvalResolvedAt 从决策 payload 中解析完成时间，缺失时使用当前事件时间。
func approvalResolvedAt(decision contract.ApprovalDecision) time.Time {
	return shared.ResolveEventTime(context.Background(), approvalDecisionPayload(decision))
}

// approvalDecisionPayload 尝试把决策 detail 解析为事件时间可读取的 map。
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
