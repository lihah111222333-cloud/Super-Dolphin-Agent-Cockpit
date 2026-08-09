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
	wantDir := filepath.Join(homeDir, ".multi-agent", "log", binaryName)
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

// TestRunMainRequiresExistingLoggerRuntime 锁定 Fx runtime 必须复用 main 已初始化的私有文件 logger。
func TestRunMainRequiresExistingLoggerRuntime(t *testing.T) {
	if exitCode := runMain(nil, nil); exitCode != 1 {
		t.Fatalf("runMain(nil, nil) exit code = %d, want 1", exitCode)
	}
}

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
