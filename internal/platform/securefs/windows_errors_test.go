package securefs

// 本测试故意不加 windows build tag：它验证类型、脱敏和 errors.Is/As 的共享
// wire 契约，不把合成错误当作非 Windows 原生 ACL 证明。

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestWindowsPermissionErrorClassifiesWin32Codes 验证 Win32 5/1314 的公开分类、脱敏和错误链。
func TestWindowsPermissionErrorClassifiesWin32Codes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-cache.db")
	for _, tc := range []struct {
		name     string
		code     uint32
		kind     WindowsPermissionKind
		sentinel error
	}{
		{name: "access denied", code: 5, kind: WindowsPermissionDenied, sentinel: ErrWindowsPermissionDenied},
		{name: "privilege not held", code: 1314, kind: WindowsPrivilegeNotHeld, sentinel: ErrWindowsPrivilegeNotHeld},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := fmt.Errorf("ACL operation %s: %w", path, syscall.Errno(tc.code))
			err := NewWindowsPermissionError("set ACL", path, cause)

			var typed *WindowsPermissionError
			if !errors.As(err, &typed) || typed == nil {
				t.Fatalf("errors.As() failed for %T: %v", err, err)
			}
			if typed.Code != tc.code || typed.Win32Code() != tc.code {
				t.Fatalf("WindowsPermissionError code = %d/%d, want %d", typed.Code, typed.Win32Code(), tc.code)
			}
			if typed.Kind != tc.kind || typed.PermissionKind() != tc.kind {
				t.Fatalf("WindowsPermissionError kind = %v/%v, want %v", typed.Kind, typed.PermissionKind(), tc.kind)
			}
			if got, ok := ClassifyWindowsPermissionError(err); !ok || got != tc.kind {
				t.Fatalf("ClassifyWindowsPermissionError() = %v, %t, want %v, true", got, ok, tc.kind)
			}
			if !errors.Is(err, tc.sentinel) || !errors.Is(err, syscall.Errno(tc.code)) {
				t.Fatalf("error chain did not preserve classification or Win32 cause: %v", err)
			}
			message := err.Error()
			if strings.Contains(message, path) || strings.Contains(message, filepath.Dir(path)) {
				t.Fatalf("error leaked full path: %s", message)
			}
			if !strings.Contains(message, RedactPath(path)) || !strings.Contains(message, fmt.Sprintf("windows_error_code=%d", tc.code)) {
				t.Fatalf("error = %s, want redacted path and code", message)
			}
		})
	}
}

// TestWindowsPermissionErrorRedactsWindowsPathOnNonWindows 验证 Windows 路径在非 Windows 主机上也不会泄露。
func TestWindowsPermissionErrorRedactsWindowsPathOnNonWindows(t *testing.T) {
	path := `C:\Users\alice\Super Dolphin\private-cache.db`
	err := NewWindowsPermissionError(
		"read ACL",
		path,
		fmt.Errorf("open %s: %w", path, syscall.Errno(5)),
	)
	if strings.Contains(err.Error(), path) || strings.Contains(err.Error(), `C:\Users\alice`) {
		t.Fatalf("Windows path leaked on this platform: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted:private-cache.db>") {
		t.Fatalf("error = %v, want redacted basename", err)
	}
}

// TestWindowsPermissionErrorDoesNotClassifyRawNonWindowsErrno 验证未经 Windows 包装的同数值 errno 不会被误分类。
func TestWindowsPermissionErrorDoesNotClassifyRawNonWindowsErrno(t *testing.T) {
	if kind, ok := ClassifyWindowsPermissionError(syscall.Errno(5)); ok || kind != WindowsPermissionUnknown {
		t.Fatalf("raw errno classified as Windows permission: %v, %t", kind, ok)
	}
}
