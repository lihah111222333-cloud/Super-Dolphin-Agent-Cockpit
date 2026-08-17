//go:build !windows

package toolbridge

import "context"

// handleSchemaValidationAuthorization 在非 Windows 上严格 no-op；schema typed 错误
// 保持原有 fail-fast，绝不创建 Windows approval envelope 或重试。
func (h *Handler) handleSchemaValidationAuthorization(
	context.Context,
	codexToolEntry,
	ToolCallRequest,
	error,
) schemaValidationAuthorizationDecision {
	return schemaValidationAuthorizationDecision{}
}
