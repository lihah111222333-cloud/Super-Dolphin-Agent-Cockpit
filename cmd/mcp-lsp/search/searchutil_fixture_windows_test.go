//go:build windows

package search

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeFakeSG(t *testing.T, exitCode int, stdout, stderr string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, fakeSGName())
	psPath := filepath.Join(dir, "sg-output.ps1")
	psScript := "[Console]::Out.Write(" + powershellSingleQuote(stdout) + ")\n" +
		"[Console]::Error.Write(" + powershellSingleQuote(stderr) + ")\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(psPath, []byte(psScript), 0o600); err != nil {
		t.Fatalf("write fake sg output powershell: %v", err)
	}
	script := "@echo off\r\n" +
		"powershell -NoProfile -ExecutionPolicy Bypass -File \"%~dp0sg-output.ps1\"\r\n" +
		"exit /b %ERRORLEVEL%\r\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sg: %v", err)
	}
	return path
}

func writeFakeSGArgs(t *testing.T, argsPath string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, fakeSGName())
	psPath := filepath.Join(dir, "sg.ps1")
	psScript := "Set-Content -LiteralPath " + powershellSingleQuote(argsPath) + " -Value $args\nexit 1\n"
	if err := os.WriteFile(psPath, []byte(psScript), 0o755); err != nil {
		t.Fatalf("write fake sg args powershell: %v", err)
	}
	script := "@echo off\r\n" +
		"powershell -NoProfile -ExecutionPolicy Bypass -File \"%~dp0sg.ps1\" %*\r\n" +
		"exit /b %ERRORLEVEL%\r\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sg args: %v", err)
	}
	return path
}

func setFakeSGPath(t *testing.T, sg string) {
	t.Helper()
	t.Setenv("PATH", filepath.Dir(sg)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func fakeSGName() string { return "sg.cmd" }

func powershellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// makeUnreadableForTest 使用 Windows ACL 构造不可读 fixture；权限不足时测试明确
// 记录宿主限制，不修改生产 ACL。
func makeUnreadableForTest(t *testing.T, path string) {
	t.Helper()
	principal := os.Getenv("USERNAME")
	if domain := os.Getenv("USERDOMAIN"); domain != "" && principal != "" {
		principal = domain + `\` + principal
	}
	if strings.TrimSpace(principal) == "" {
		t.Skip("USERNAME is required to make the test file unreadable on Windows")
	}
	deny := principal + ":(R)"
	cmd := exec.Command("icacls", path, "/deny", deny)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("icacls deny read unavailable: %v; output=%s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("icacls", path, "/remove:d", principal).Run()
		_ = os.Chmod(path, 0o600)
	})
}
