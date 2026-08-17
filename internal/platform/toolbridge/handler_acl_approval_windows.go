//go:build windows

package toolbridge

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
)

const (
	windowsACLApprovalKind                     = "windows_acl"
	windowsACLAuthorizationRequiredCode        = "authorization_required"
	windowsACLAccessDeniedCode          uint32 = 5
	windowsACLPrivilegeNotHeldCode      uint32 = 1314
)

// windowsACLAuthorization 只保留弹窗需要的稳定分类，不携带路径、错误文本
// 或 peer 自报身份，避免权限错误借审批 payload 泄露本机信息。
type windowsACLAuthorization struct {
	ErrorCode      uint32
	PermissionKind string
}

// windowsACLAuthorizationEnvelope 是 mcp-lsp 类型化错误的最小可信形状。
// Success 必须显式为 false，Code 与 meta 的三个字段也必须完全一致。
type windowsACLAuthorizationEnvelope struct {
	Success   *bool           `json:"success"`
	Error     string          `json:"error,omitempty"`
	Code      string          `json:"code"`
	Retryable bool            `json:"retryable,omitempty"`
	Hint      string          `json:"hint,omitempty"`
	Meta      json.RawMessage `json:"meta"`
}

type windowsACLAuthorizationMeta struct {
	AuthorizationRequired *bool  `json:"authorization_required"`
	WindowsErrorCode      uint32 `json:"windows_error_code"`
	WindowsPermissionKind string `json:"windows_permission_kind"`
}

// windowsACLAuthorizationFromResult 仅解析 structuredContent；模型可见文本即使
// 包含相似字样也不会触发授权弹窗。非 Windows 主机保持原有返回行为。
func windowsACLAuthorizationFromResult(result *ToolCallResult) (windowsACLAuthorization, bool) {
	if result == nil || result.Success {
		return windowsACLAuthorization{}, false
	}
	return parseWindowsACLAuthorization(result.StructuredContent)
}

// parseWindowsACLAuthorization 严格解析类型化 envelope，供跨平台契约测试验证；
// 是否允许弹窗仍由 windowsACLAuthorizationFromResult 的宿主平台门禁决定。
func parseWindowsACLAuthorization(raw json.RawMessage) (windowsACLAuthorization, bool) {
	var envelope windowsACLAuthorizationEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil ||
		envelope.Success == nil || *envelope.Success ||
		envelope.Code != windowsACLAuthorizationRequiredCode {
		return windowsACLAuthorization{}, false
	}
	var meta windowsACLAuthorizationMeta
	if err := json.Unmarshal(envelope.Meta, &meta); err != nil ||
		meta.AuthorizationRequired == nil || !*meta.AuthorizationRequired {
		return windowsACLAuthorization{}, false
	}
	wantKind := ""
	switch meta.WindowsErrorCode {
	case windowsACLAccessDeniedCode:
		wantKind = "access_denied"
	case windowsACLPrivilegeNotHeldCode:
		wantKind = "privilege_not_held"
	default:
		return windowsACLAuthorization{}, false
	}
	if meta.WindowsPermissionKind != wantKind {
		return windowsACLAuthorization{}, false
	}
	return windowsACLAuthorization{
		ErrorCode:      meta.WindowsErrorCode,
		PermissionKind: wantKind,
	}, true
}

// windowsACLApprovalRequest 使用宿主 ToolCallRequest 的可信身份构造审批。
// peer envelope 和模型 arguments 都不能覆盖 AgentID、ThreadID、TurnID 或 CallID。
func (h *Handler) windowsACLApprovalRequest(instance *mcpcontrol.ToolInstance, req ToolCallRequest, result *ToolCallResult) (contract.ApprovalRequest, bool) {
	clientKind := ""
	if instance != nil {
		clientKind = instance.ClientKind
	}
	return h.windowsACLApprovalRequestForClientKind(clientKind, req, result)
}

// windowsACLApprovalRequestForClientKind 供 peer 与 Codex surface schema 两条受信边界
// 共用；clientKind 只能来自已绑定的宿主实例或 surface entry，不能由错误 payload 覆盖。
func (h *Handler) windowsACLApprovalRequestForClientKind(clientKind string, req ToolCallRequest, result *ToolCallResult) (contract.ApprovalRequest, bool) {
	if h == nil || h.approvalRequester == nil ||
		strings.TrimSpace(clientKind) != mcpdto.ClientKindLSP ||
		classifyTool(req.Name) != mcpdto.ClientKindLSP ||
		strings.TrimSpace(req.CallID) == "" {
		return contract.ApprovalRequest{}, false
	}
	authorization, ok := windowsACLAuthorizationFromResult(result)
	if !ok {
		return contract.ApprovalRequest{}, false
	}
	h.info("toolbridge: Windows ACL authorization requested",
		"windows_error_code", authorization.ErrorCode,
		"windows_permission_kind", authorization.PermissionKind,
	)
	return contract.ApprovalRequest{
		CallID:     strings.TrimSpace(req.CallID),
		ApprovalID: strings.TrimSpace(req.CallID),
		ToolName:   strings.TrimSpace(req.Name),
		AgentID:    strings.TrimSpace(req.AgentID),
		ThreadID:   strings.TrimSpace(req.ThreadID),
		TurnID:     strings.TrimSpace(req.TurnID),
		Reason: fmt.Sprintf(
			"Windows 权限不足（错误码 %d，类型 %s）。批准后将使用同一受管 LSP 实例重试一次。",
			authorization.ErrorCode,
			authorization.PermissionKind,
		),
		Kind:         windowsACLApprovalKind,
		SourceMethod: ProxyMethodToolsCall,
		Payload: map[string]any{
			"authorization_required":  true,
			"windows_error_code":      authorization.ErrorCode,
			"windows_permission_kind": authorization.PermissionKind,
		},
	}, true
}

// logWindowsACLApprovalDecision 只记录稳定分类和决策状态；审批错误正文、工具
// arguments 与路径不进入日志。
func (h *Handler) logWindowsACLApprovalDecision(req contract.ApprovalRequest, decision contract.ApprovalDecision, err error) {
	outcome := "no_decision"
	switch {
	case err != nil:
		outcome = "request_failed"
	case decision.Approved != nil && *decision.Approved:
		outcome = "approved_retry_once"
	case decision.Approved != nil:
		outcome = "denied"
	}
	h.info("toolbridge: Windows ACL authorization resolved",
		"outcome", outcome,
		"windows_error_code", req.Payload["windows_error_code"],
		"windows_permission_kind", req.Payload["windows_permission_kind"],
	)
}
