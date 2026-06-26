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

// DefaultApprovalTimeout 是审批等待默认超时，测试通过 setApprovalTimeoutForTest 临时覆盖。
var DefaultApprovalTimeout = 5 * time.Minute

type approvalContextKey string

const approvalAutoDeclineOnCancelKey approvalContextKey = "approval_auto_decline_on_cancel"

// normalizeApprovalRequest 校验并标准化审批请求，保证后续索引拥有稳定 callID。
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

// WithApprovalDeadline 在调用方没有显式 deadline 时附加默认审批超时。
func WithApprovalDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx = shared.NonNilContext(ctx)
	if DefaultApprovalTimeout <= 0 {
		return ctx, func() {}
	}
	return platformconfig.WithTimeoutIfNone(ctx, DefaultApprovalTimeout)
}

// WithApprovalAutoDeclineOnCancel 标记 ctx 取消时应自动写入拒绝决策。
func WithApprovalAutoDeclineOnCancel(ctx context.Context) context.Context {
	return context.WithValue(shared.NonNilContext(ctx), approvalAutoDeclineOnCancelKey, true)
}

// waitForApproval 等待 pending 完成或 ctx 取消。
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

// decodeApprovalDecision 兼容 bool、string 和 object 三种客户端决策响应。
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

// normalizeDecisionString 将常见 approve/decline 文本标准化为布尔决策。
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

// mapApprovalWaitErr 把等待错误映射为对外稳定的 RPC 审批错误。
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

// dispatchApprovalDecision 在无需客户端回调或装配不完整时直接给出决策。
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

// canceledApprovalDecision 根据 ctx 标记把取消转换为拒绝决策。
func canceledApprovalDecision(ctx context.Context, err error) (contract.ApprovalDecision, bool) {
	if !shouldAutoDeclineOnCancel(ctx) || !errors.Is(err, context.Canceled) {
		return contract.ApprovalDecision{}, false
	}
	return declinedDecision(""), true
}

// shouldAutoDeclineOnCancel 判断 ctx 是否允许取消时自动拒绝。
func shouldAutoDeclineOnCancel(ctx context.Context) bool {
	enabled, _ := shared.NonNilContext(ctx).Value(approvalAutoDeclineOnCancelKey).(bool)
	return enabled
}

// isRecoverableDispatchErr 判断审批回调错误是否可等待后续连接恢复。
func isRecoverableDispatchErr(err error) bool {
	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) {
		return false
	}
	return isExpectedCloseErr(err)
}

// decisionReason 选择用于事件和日志展示的审批决策原因。
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

// approvalCallID 统一从 callID 或 requestID 生成审批主键文本。
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

// pendingStorageKey 生成 pending 主索引键，requestID 可区分同 callID 的并发请求。
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

// cloneApprovalRequest 复制请求和 payload，避免调用方后续修改影响 pending 状态。
func cloneApprovalRequest(req ApprovalRequest, requestID *int64) ApprovalRequest {
	req.RequestID = cloneInt64Ptr(requestID)
	req.Payload = shared.CloneJSONMap(req.Payload)
	return req
}

// shouldAutoApproveUserInput 判断 request_user_input 是否因策略为 never 而自动批准。
func shouldAutoApproveUserInput(req ApprovalRequest) bool {
	return isRequestUserInputKind(req.Kind) && strings.EqualFold(approvalPolicy(req), "never")
}

// approvalPolicy 从结构字段或 payload 中读取审批策略。
func approvalPolicy(req ApprovalRequest) string {
	return shared.FirstNonEmpty(req.ApprovalPolicy, stringFromMap(req.Payload, "approvalPolicy", "approval_policy"))
}

// cloneInt64Ptr 复制 int64 指针值。
func cloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// boolPtr 返回 bool 指针，便于构造审批 DTO。
func boolPtr(value bool) *bool {
	return &value
}

// int64Value 安全读取 int64 指针，nil 视为 0。
func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// stringFromMap 从多个候选 key 中读取第一个非空字符串。
func stringFromMap(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// decisionApproved 判断审批决策是否明确批准。
func decisionApproved(decision contract.ApprovalDecision) bool {
	return decision.Approved != nil && *decision.Approved
}

// detailReason 从原始决策 detail 中提取用户可读原因。
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

// isPendingDone 非阻塞判断 pending 是否已经完成。
func isPendingDone(pending *pendingApproval) bool {
	select {
	case <-pending.done:
		return true
	default:
		return false
	}
}
