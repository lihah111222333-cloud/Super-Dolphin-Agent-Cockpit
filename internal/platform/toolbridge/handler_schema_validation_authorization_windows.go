//go:build windows

package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// handleSchemaValidationAuthorization 只处理已恢复为 securefs typed 的 5/1314；
// 没有可信 CallID、LSP family 或 approval requester 时保持 fail-fast，不伪造审批。
func (h *Handler) handleSchemaValidationAuthorization(
	ctx context.Context,
	entry codexToolEntry,
	req ToolCallRequest,
	err error,
) schemaValidationAuthorizationDecision {
	if h == nil || h.approvalRequester == nil || entry.executionKind != "stdio" ||
		strings.TrimSpace(entry.family) != mcpdto.ClientKindLSP ||
		strings.TrimSpace(req.CallID) == "" {
		return schemaValidationAuthorizationDecision{}
	}
	authorization, ok := windowsACLAuthorizationFromSchemaError(err)
	if !ok {
		return schemaValidationAuthorizationDecision{}
	}
	result, resultErr := windowsACLAuthorizationToolResult(authorization)
	if resultErr != nil {
		return schemaValidationAuthorizationDecision{err: resultErr, validationDone: true, handled: true}
	}
	approval, ok := h.windowsACLApprovalRequestForClientKind(entry.family, req, result)
	if !ok {
		return schemaValidationAuthorizationDecision{}
	}
	decision, approvalErr := h.approvalRequester.RequestApproval(ctx, approval)
	h.logWindowsACLApprovalDecision(approval, decision, approvalErr)
	if approvalErr != nil || decision.Approved == nil || !*decision.Approved {
		return schemaValidationAuthorizationDecision{result: result, validationDone: true, handled: true}
	}

	// 批准只允许重做当前 schema validation 一次；第二次失败绝不再次申请或执行工具。
	retryErr := h.validateCodexSurfaceEntryArguments(ctx, entry, req.Arguments)
	if retryErr == nil {
		return schemaValidationAuthorizationDecision{handled: true}
	}
	if _, stillPermission := windowsACLAuthorizationFromSchemaError(retryErr); stillPermission {
		return schemaValidationAuthorizationDecision{result: result, validationDone: true, handled: true}
	}
	if recoveryResult, recoverable := toolCallRecoveryFailureResult(retryErr); recoverable {
		return schemaValidationAuthorizationDecision{result: recoveryResult, validationDone: true, handled: true}
	}
	return schemaValidationAuthorizationDecision{err: retryErr, validationDone: true, handled: true}
}

func windowsACLAuthorizationFromSchemaError(err error) (windowsACLAuthorization, bool) {
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(err, &permissionErr) || permissionErr == nil {
		return windowsACLAuthorization{}, false
	}
	switch permissionErr.Win32Code() {
	case windowsACLAccessDeniedCode:
		return windowsACLAuthorization{ErrorCode: windowsACLAccessDeniedCode, PermissionKind: "access_denied"}, true
	case windowsACLPrivilegeNotHeldCode:
		return windowsACLAuthorization{ErrorCode: windowsACLPrivilegeNotHeldCode, PermissionKind: "privilege_not_held"}, true
	default:
		return windowsACLAuthorization{}, false
	}
}

func windowsACLAuthorizationToolResult(authorization windowsACLAuthorization) (*ToolCallResult, error) {
	success := false
	authorizationRequired := true
	meta, err := json.Marshal(windowsACLAuthorizationMeta{
		AuthorizationRequired: &authorizationRequired,
		WindowsErrorCode:      authorization.ErrorCode,
		WindowsPermissionKind: authorization.PermissionKind,
	})
	if err != nil {
		return nil, fmt.Errorf("encode Windows authorization result metadata: %w", err)
	}
	envelope, err := json.Marshal(windowsACLAuthorizationEnvelope{
		Success: &success,
		Error:   "Windows authorization is required.",
		Code:    windowsACLAuthorizationRequiredCode,
		Hint:    "next: obtain required authorization and retry the operation",
		Meta:    meta,
	})
	if err != nil {
		return nil, fmt.Errorf("encode Windows authorization result: %w", err)
	}
	result := toolCallErrorResult("Windows authorization is required.")
	result.StructuredContent = envelope
	return result, nil
}
