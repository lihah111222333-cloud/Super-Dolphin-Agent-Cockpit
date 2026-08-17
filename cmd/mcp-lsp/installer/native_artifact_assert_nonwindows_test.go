//go:build !windows

package installer

import (
	"os"
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
