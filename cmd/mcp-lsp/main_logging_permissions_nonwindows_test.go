//go:build !windows

package main

import (
	"errors"
	"os"
	"testing"
)

// TestWrapSidecarLogPathErrorPreservesNonWindowsCause 锁定公共日志路径在其他平台不被误分成 Windows ACL 错误。
func TestWrapSidecarLogPathErrorPreservesNonWindowsCause(t *testing.T) {
	want := errors.New("non-Windows log failure")
	if got := wrapSidecarLogPathError("create_private_log_file", "/private/path", want); got != want {
		t.Fatalf("wrapSidecarLogPathError() = %v, want original error identity", got)
	}
}

// assertPrivateLogPermissions 在非 Windows 上校验 owner-only POSIX mode。
func assertPrivateLogPermissions(t *testing.T, logDir, logPath string) {
	t.Helper()
	dirInfo, err := os.Stat(logDir)
	if err != nil {
		t.Fatalf("stat log directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("log directory mode = %o, want 700", got)
	}
	fileInfo, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat log file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("log file mode = %o, want 600", got)
	}
}
