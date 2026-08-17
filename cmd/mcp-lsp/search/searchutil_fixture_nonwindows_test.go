//go:build !windows

package search

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeFakeSG(t *testing.T, exitCode int, stdout, stderr string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), fakeSGName())
	script := "#!/bin/sh\n" +
		"printf '%s' " + shellQuote(stdout) + "\n" +
		"printf '%s' " + shellQuote(stderr) + " >&2\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sg: %v", err)
	}
	return path
}

func writeFakeSGArgs(t *testing.T, argsPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), fakeSGName())
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + shellQuote(argsPath) + "\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sg args: %v", err)
	}
	return path
}

func setFakeSGPath(t *testing.T, sg string) {
	t.Helper()
	t.Setenv("PATH", filepath.Dir(sg))
}

func fakeSGName() string { return "sg" }

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// makeUnreadableForTest 在非 Windows 构建中只登记 mode 恢复；测试本体负责设置权限。
func makeUnreadableForTest(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
}
