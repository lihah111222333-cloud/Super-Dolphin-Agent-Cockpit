//go:build !windows

package installer

import (
	"os"
	"path/filepath"
	"testing"
)

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
