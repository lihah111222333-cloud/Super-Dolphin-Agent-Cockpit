//go:build windows

package main

import (
	"errors"
	"strings"
	"syscall"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// TestWrapSidecarLogPathErrorPreservesWindowsAuthorizationFailure 锁定日志创建阶段的
// Win32 ACL 错误可被上层识别，且错误文本不会泄漏用户绝对路径。
func TestWrapSidecarLogPathErrorPreservesWindowsAuthorizationFailure(t *testing.T) {
	privatePath := `C:\Users\private-user\sensitive\mcp-lsp.log`
	err := wrapSidecarLogPathError("create_private_log_file", privatePath, syscall.Errno(5))
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(err, &permissionErr) {
		t.Fatalf("wrapped error type = %T, want *securefs.WindowsPermissionError", err)
	}
	if permissionErr.Win32Code() != 5 || !errors.Is(err, securefs.ErrWindowsPermissionDenied) {
		t.Fatalf("wrapped permission error = %#v, want Win32 5/access denied", permissionErr)
	}
	if strings.Contains(err.Error(), privatePath) || strings.Contains(err.Error(), "private-user") {
		t.Fatalf("wrapped permission error leaked private path: %q", err)
	}
}

// assertPrivateLogPermissions 在 Windows 上校验 owner 与 LocalSystem 专用 DACL；
// os.FileMode.Perm 在 Windows 上固定映射为 0777/0666，不能充当 ACL 证明。
func assertPrivateLogPermissions(t *testing.T, logDir, logPath string) {
	t.Helper()
	for _, path := range []string{logDir, logPath} {
		if err := securefs.CheckPrivateOwnerOnly(path, nil); err != nil {
			t.Fatalf("Windows private log ACL %s: %v", path, err)
		}
	}
}
