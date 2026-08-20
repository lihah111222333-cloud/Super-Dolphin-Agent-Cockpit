//go:build windows

package search

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

var windowsTokenSIDPattern = regexp.MustCompile(`S-[0-9]+(-[0-9]+)+`)

// makeUnreadableForTest 使用当前 Windows token 的 SID 构造不可读 fixture，避免环境变量
// 用户名与实际执行 token 不一致；测试结束后恢复 fixture ACL。
func makeUnreadableForTest(t *testing.T, path string) {
	t.Helper()
	output, err := exec.Command("whoami.exe", "/user", "/fo", "csv", "/nh").CombinedOutput()
	if err != nil {
		t.Fatalf("resolve current Windows token SID: %v; output=%s", err, output)
	}
	sid := windowsTokenSIDPattern.FindString(string(output))
	if sid == "" {
		t.Fatalf("resolve current Windows token SID: output=%s", output)
	}
	deny := "*" + sid + ":(R)"
	if output, err := exec.Command("icacls.exe", path, "/deny", deny).CombinedOutput(); err != nil {
		t.Fatalf("icacls deny read for token SID %s: %v; output=%s", sid, err, output)
	}
	t.Cleanup(func() {
		if output, err := exec.Command("icacls.exe", path, "/remove:d", "*"+sid).CombinedOutput(); err != nil {
			t.Errorf("restore ACL for token SID %s: %v; output=%s", sid, err, output)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Errorf("restore fixture mode: %v", err)
		}
	})
}
