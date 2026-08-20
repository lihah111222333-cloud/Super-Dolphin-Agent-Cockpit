//go:build !windows

package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// TestNonWindowsSidecarFileLoggerRemainsEagerFailFast 锁定非 Windows 沿用启动期初始化，
// Windows 的授权接入不得改变其他平台生命周期。
func TestNonWindowsSidecarFileLoggerRemainsEagerFailFast(t *testing.T) {
	homeFile := filepath.Join(t.TempDir(), "blocked-home")
	if err := os.WriteFile(homeFile, []byte("blocked"), 0o600); err != nil {
		t.Fatalf("create blocked home: %v", err)
	}
	runtime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	t.Cleanup(runtime.ShutdownFileHandler)

	if _, err := prepareSidecarFileLogger(runtime, homeFile, io.Discard); err == nil {
		t.Fatal("prepareSidecarFileLogger() error = nil, want eager non-Windows failure")
	}
}
