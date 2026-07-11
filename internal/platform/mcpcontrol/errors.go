package mcpcontrol

import (
	"fmt"

	"github.com/creachadair/jrpc2"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

func newMCPError(code int, format string, args ...any) error {
	return jrpc2.Errorf(jrpc2.Code(code), "%s", fmt.Sprintf(format, args...))
}

func errInvalidParams(format string, args ...any) error {
	return newMCPError(dto.ErrCodeInvalidParams, format, args...)
}

func errInternal(format string, args ...any) error {
	return newMCPError(dto.ErrCodeInternal, format, args...)
}

func errLeaseNotFound(format string, args ...any) error {
	return newMCPError(dto.ErrCodeLeaseNotFound, format, args...)
}

func errLeaseStale(format string, args ...any) error {
	return newMCPError(dto.ErrCodeLeaseStale, format, args...)
}

func errCapabilityMismatch(format string, args ...any) error {
	return newMCPError(dto.ErrCodeCapabilityMismatch, format, args...)
}

func errScopeNotAllowed(format string, args ...any) error {
	return newMCPError(dto.ErrCodeScopeNotAllowed, format, args...)
}

func errApprovalUnavailable(format string, args ...any) error {
	return newMCPError(dto.ErrCodeApprovalUnavailable, format, args...)
}

func errAuthFailed(format string, args ...any) error {
	return newMCPError(dto.ErrCodeAuthFailed, format, args...)
}

func errReportConflict(format string, args ...any) error {
	return newMCPError(dto.ErrCodeReportConflict, format, args...)
}

func errPeerUnavailable(format string, args ...any) error {
	return newMCPError(dto.ErrCodePeerUnavailable, format, args...)
}

func errBusy(format string, args ...any) error {
	return newMCPError(dto.ErrCodeBusy, format, args...)
}
