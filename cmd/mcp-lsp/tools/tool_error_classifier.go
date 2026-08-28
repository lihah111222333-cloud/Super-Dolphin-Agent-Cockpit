package tools

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	toolErrorCodeAuthorizationRequired = "authorization_required"
	toolErrorMetaAuthorizationRequired = "authorization_required"
	toolErrorMetaWindowsErrorCode      = "windows_error_code"
	toolErrorMetaWindowsPermissionKind = "windows_permission_kind"
)

// windowsAuthorizationMeta 是 Windows ACL 授权错误的稳定 wire 元数据。
// 字段必须同时由 classifier 生产、envelope 传递和 field guard 测试覆盖。
type windowsAuthorizationMeta struct {
	AuthorizationRequired bool   `json:"authorization_required"`
	WindowsErrorCode      uint32 `json:"windows_error_code"`
	WindowsPermissionKind string `json:"windows_permission_kind"`
}

// asMap 将内部字段转换为工具错误 envelope 的动态 meta。
func (m windowsAuthorizationMeta) asMap() map[string]any {
	return map[string]any{
		toolErrorMetaAuthorizationRequired: m.AuthorizationRequired,
		toolErrorMetaWindowsErrorCode:      m.WindowsErrorCode,
		toolErrorMetaWindowsPermissionKind: m.WindowsPermissionKind,
	}
}

// ToolErrorClassifier 将 typed Windows 5/1314 权限错误编码为稳定授权请求。
// 它只沿 errors.As 识别 WindowsPermissionError，不读取错误文本或子进程退出码。
func ToolErrorClassifier(_ string, err error) (common.ToolErrorClassification, bool) {
	if classification, ok := classifyStrictSchemaError(err); ok {
		return classification, true
	}
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(err, &permissionErr) || permissionErr == nil {
		return classifyWindowsJobPolicyError(err)
	}

	var permissionKind securefs.WindowsPermissionKind
	switch permissionErr.Win32Code() {
	case 5:
		permissionKind = securefs.WindowsPermissionDenied
	case 1314:
		permissionKind = securefs.WindowsPrivilegeNotHeld
	default:
		return common.ToolErrorClassification{}, false
	}

	return common.ToolErrorClassification{
		Code:      toolErrorCodeAuthorizationRequired,
		Retryable: false,
		Hint:      "next: obtain required authorization and retry the operation",
		Meta: windowsAuthorizationMeta{
			AuthorizationRequired: true,
			WindowsErrorCode:      permissionErr.Win32Code(),
			WindowsPermissionKind: permissionKind.String(),
		}.asMap(),
	}, true
}

// classifyStrictSchemaError 将严格解码发现的字段漂移编码为不可重试的机器可读错误。
// Unknown/allowed 字段都来自目标 DTO 的真实 JSON tag 集合，避免提示驱动调用方猜字段。
func classifyStrictSchemaError(err error) (common.ToolErrorClassification, bool) {
	var unknownField *strictToolUnknownFieldError
	if !errors.As(err, &unknownField) || unknownField == nil {
		return common.ToolErrorClassification{}, false
	}
	unknown, allowed := unknownField.schemaFields()
	if len(unknown) == 0 {
		return common.ToolErrorClassification{}, false
	}
	return common.ToolErrorClassification{
		Code:      "schema_invalid",
		Retryable: false,
		Hint: fmt.Sprintf(
			"next: remove unknown fields and retry with the documented schema; unknown_fields=%s; allowed_fields=%s; retryable=false",
			strings.Join(unknown, ","), strings.Join(allowed, ","),
		),
		Meta: map[string]any{
			"unknown_fields": unknown,
			"allowed_fields": allowed,
			"retryable":      false,
		},
	}, true
}

// classifyWindowsJobPolicyError 识别受限 Windows Job 的 typed failure。
// 这不是 ACL 授权错误；Win32 5/1314 仍只由 securefs.WindowsPermissionError 分类。
func classifyWindowsJobPolicyError(err error) (common.ToolErrorClassification, bool) {
	var policyErr *hiddenexec.WindowsJobPolicyError
	if !errors.As(err, &policyErr) || policyErr == nil {
		return common.ToolErrorClassification{}, false
	}
	return common.ToolErrorClassification{
		Code:      "windows_job_restricted",
		Retryable: false,
		Hint:      "next: run the approved mcp-lsp broker in a Windows Job that explicitly permits the required breakaway",
		Meta: map[string]any{
			"windows_job_policy":              true,
			"windows_job_limit_flags":         policyErr.LimitFlags,
			"windows_job_kill_on_close":       policyErr.KillOnClose,
			"windows_job_breakaway_ok":        policyErr.BreakawayOK,
			"windows_job_silent_breakaway_ok": policyErr.SilentBreakaway,
		},
	}, true
}
