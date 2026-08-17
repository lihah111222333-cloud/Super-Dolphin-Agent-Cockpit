//go:build windows

package installer

import (
	"os"
	"path/filepath"
	"testing"
)

// processFailureTestCommand 构造 Windows cmd.exe 失败进程。
func processFailureTestCommand(secret string) (string, []string) {
	command := os.Getenv("ComSpec")
	if command == "" {
		command = "cmd.exe"
	}
	return command, []string{"/d", "/c", "echo " + secret + " & exit /b 23"}
}

// writeFailingProcess 写入 Windows .cmd fixture；windows build tag 保证脚本方言可见。
func writeFailingProcess(t *testing.T, secret string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "failing-installer.cmd")
	contents := "@echo off\r\necho " + secret + "\r\nexit /b 23\r\n"
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write failing process: %v", err)
	}
	return path
}
