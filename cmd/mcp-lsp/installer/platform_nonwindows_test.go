//go:build !windows

package installer

import (
	"os"
	"path/filepath"
	"testing"
)

// assertExecutable 在非 Windows 构建中验证 POSIX execute bit。
func assertExecutable(t *testing.T, filename string) {
	t.Helper()
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatalf("stat executable %s: %v", filename, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("%s mode = %o, want executable", filename, info.Mode().Perm())
	}
}

// processFailureTestCommand 构造非 Windows /bin/sh 失败进程。
func processFailureTestCommand(secret string) (string, []string) {
	return "/bin/sh", []string{"-c", "printf '%s\\n' '" + secret + "'; exit 23"}
}

// writeFailingProcess 写入 POSIX shell fixture；!windows build tag 防止脚本方言泄漏。
func writeFailingProcess(t *testing.T, secret string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "failing-installer")
	contents := "#!/bin/sh\nprintf '%s\\n' '" + secret + "'\nexit 23\n"
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write failing process: %v", err)
	}
	return path
}

// setDotnetGlobalToolHomeForTest sets the non-Windows home variable used by os.UserHomeDir.
func setDotnetGlobalToolHomeForTest(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
}
