//go:build windows

package securefs

import (
	"errors"
	"strings"
	"syscall"
	"testing"
)

// TestCurrentUserSIDPermissionErrorsAreTyped 锁定 OpenCurrentProcessToken/GetTokenUser
// 的 5/1314 错误必须进入 authorization_required 可识别的 typed 链，且不能泄露路径。
func TestCurrentUserSIDPermissionErrorsAreTyped(t *testing.T) {
	const rawPath = `C:\private\super-dolphin\state.db`
	tests := []struct {
		name      string
		operation string
		code      uint32
		kind      WindowsPermissionKind
	}{
		{name: "open_current_process_token_access_denied", operation: "open_current_process_token", code: 5, kind: WindowsPermissionDenied},
		{name: "get_token_user_privilege_not_held", operation: "get_token_user", code: 1314, kind: WindowsPrivilegeNotHeld},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := wrapCurrentUserSIDError(rawPath, tc.operation, syscall.Errno(tc.code))
			if err == nil {
				t.Fatal("wrapCurrentUserSIDError() = nil, want typed permission error")
			}
			var permissionErr *WindowsPermissionError
			if !errors.As(err, &permissionErr) || permissionErr == nil {
				t.Fatalf("error is not WindowsPermissionError: %v", err)
			}
			if got := permissionErr.Win32Code(); got != tc.code {
				t.Fatalf("Win32Code() = %d, want %d", got, tc.code)
			}
			if got := permissionErr.PermissionKind(); got != tc.kind {
				t.Fatalf("PermissionKind() = %v, want %v", got, tc.kind)
			}
			if got, ok := ClassifyWindowsPermissionError(err); !ok || got != tc.kind {
				t.Fatalf("ClassifyWindowsPermissionError() = (%v, %t), want (%v, true)", got, ok, tc.kind)
			}
			if strings.Contains(err.Error(), rawPath) {
				t.Fatalf("typed error leaked raw path: %v", err)
			}
		})
	}
}
