//go:build windows

package main

import (
	"errors"
	"syscall"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// TestWindowsSidecarFileLoggerDefersFailureToToolBoundary 验证 Windows 只延迟 typed
// Win32 5/1314，并由同一个 gate 在批准后的 tools/call 重做原初始化。
func TestWindowsSidecarFileLoggerDefersFailureToToolBoundary(t *testing.T) {
	for _, code := range []syscall.Errno{5, 1314} {
		t.Run(code.Error(), func(t *testing.T) {
			attempts := 0
			gate, err := prepareSidecarFileLoggerWithInitializer(func() error {
				attempts++
				if attempts == 1 {
					return securefs.NewWindowsPermissionError("set private log ACL", `C:\private\mcp-lsp.log`, code)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("prepareSidecarFileLoggerWithInitializer() error = %v", err)
			}
			if err := gate.Ensure(); err != nil {
				t.Fatalf("approved retry Ensure() error = %v", err)
			}
			if attempts != 2 {
				t.Fatalf("initializer attempts = %d, want 2", attempts)
			}
		})
	}
}

// TestWindowsSidecarFileLoggerDoesNotDeferOrdinaryStartupError 锁定路径和配置错误仍立即阻断。
func TestWindowsSidecarFileLoggerDoesNotDeferOrdinaryStartupError(t *testing.T) {
	wantErr := errors.New("invalid log path")
	if _, err := prepareSidecarFileLoggerWithInitializer(func() error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("prepare error = %v, want %v", err, wantErr)
	}
}
