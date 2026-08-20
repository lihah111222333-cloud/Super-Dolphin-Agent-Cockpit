package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// TestInitSidecarFileLoggerPersistsFatalError 验证 mcp-lsp 的致命错误同时进入 stderr 和私有项目日志。
func TestInitSidecarFileLoggerPersistsFatalError(t *testing.T) {
	homeDir := t.TempDir()

	logRuntime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{
		ServiceName:    binaryName,
		ServiceVersion: binaryVersion,
		LogFilePrefix:  binaryName,
	})
	t.Cleanup(logRuntime.ShutdownFileHandler)

	var stderr bytes.Buffer
	if err := initSidecarFileLogger(logRuntime, homeDir, &stderr); err != nil {
		t.Fatalf("initSidecarFileLogger() error = %v", err)
	}
	logPath := logRuntime.CurrentLogFilePath()
	wantDir := filepath.Join(homeDir, ".super-dolphin", "log", binaryName)
	if filepath.Dir(logPath) != wantDir {
		t.Fatalf("log directory = %q, want %q", filepath.Dir(logPath), wantDir)
	}
	if !strings.HasPrefix(filepath.Base(logPath), binaryName+"-") {
		t.Fatalf("log file = %q, want %q prefix", logPath, binaryName+"-")
	}

	sentinel := errors.New("startup sentinel")
	logRuntime.Get().Error("mcp-lsp failed", pkglogger.FieldError, sentinel)

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read mcp-lsp log: %v", err)
	}
	for outputName, output := range map[string]string{
		"file":   string(content),
		"stderr": stderr.String(),
	} {
		if !strings.Contains(output, "mcp-lsp failed") || !strings.Contains(output, sentinel.Error()) {
			t.Fatalf("%s log missing fatal error: %q", outputName, output)
		}
	}
	assertPrivateLogPermissions(t, wantDir, logPath)
}

func TestSidecarLogDirIsOwnerScopedUnderProductRuntimeState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "user")
	product := filepath.Join(t.TempDir(), "product")
	t.Setenv("SUPER_DOLPHIN_HOME", product)
	got := sidecarLogDir(home, "task-a")
	want := filepath.Join(product, "runtime-state", "sidecars", "task-a", "log", binaryName)
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("sidecarLogDir() = %q, want %q", got, want)
	}
	if strings.Contains(got, filepath.Join(product, "log", binaryName)) {
		t.Fatalf("sidecar log path is not owner scoped: %q", got)
	}
}

// TestInitSidecarFileLoggerFailsClosed 验证持久日志目录不可创建时启动明确失败。
func TestInitSidecarFileLoggerFailsClosed(t *testing.T) {
	homeFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(homeFile, []byte("blocked"), 0o600); err != nil {
		t.Fatalf("create blocking home file: %v", err)
	}
	logRuntime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	err := initSidecarFileLogger(logRuntime, homeFile, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "initialize mcp-lsp file logger") {
		t.Fatalf("initSidecarFileLogger() error = %v, want file logger initialization failure", err)
	}
}

// TestInitSidecarFileLoggerFailsClosedWithoutAlternateDirectory 锁定权限或路径失败不能通过换目录被掩盖。
func TestInitSidecarFileLoggerFailsClosedWithoutAlternateDirectory(t *testing.T) {
	homeFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(homeFile, []byte("blocked"), 0o600); err != nil {
		t.Fatalf("create blocking home file: %v", err)
	}
	logRuntime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	t.Cleanup(logRuntime.ShutdownFileHandler)

	if err := initSidecarFileLogger(logRuntime, homeFile, io.Discard); err == nil {
		t.Fatal("initSidecarFileLogger() accepted a blocked home path through fallback")
	}
	if got := logRuntime.CurrentLogFilePath(); got != "" {
		t.Fatalf("log file path = %q after failure, want empty", got)
	}
}

// TestSidecarFileLoggerGateRetriesFailureAndMemoizesSuccess 验证门只记忆成功：首次权限失败
// 原样返回，批准后的下一次调用重做同一初始化，成功后不再触碰 ACL。
func TestSidecarFileLoggerGateRetriesFailureAndMemoizesSuccess(t *testing.T) {
	wantErr := errors.New("authorization required")
	attempts := 0
	gate, err := newSidecarFileLoggerGate(func() error {
		attempts++
		if attempts == 1 {
			return wantErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("newSidecarFileLoggerGate() error = %v", err)
	}
	if err := gate.Ensure(); !errors.Is(err, wantErr) {
		t.Fatalf("first Ensure() error = %v, want %v", err, wantErr)
	}
	if err := gate.Ensure(); err != nil {
		t.Fatalf("retry Ensure() error = %v", err)
	}
	if err := gate.Ensure(); err != nil {
		t.Fatalf("memoized Ensure() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("initializer attempts = %d, want 2", attempts)
	}
}

// TestRunMainRequiresExistingLoggerRuntime 锁定 Fx runtime 必须复用 main 已初始化的私有文件 logger。
func TestRunMainRequiresExistingLoggerRuntime(t *testing.T) {
	if exitCode := runMain(nil, nil, nil); exitCode != 1 {
		t.Fatalf("runMain(nil, nil) exit code = %d, want 1", exitCode)
	}
}
