//go:build !windows

package multilsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeGoOutput 是非 Windows 的 POSIX fixture；
// 使用 shell 脚本模拟 go 输出，保持公共 resolver 测试的 PATH 语义。
func writeFakeGoOutput(t *testing.T, root, name, output string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	body := "#!/bin/sh\nprintf '%s' '" + strings.ReplaceAll(output, "'", "'\\''") + "'\n"
	path := filepath.Join(dir, "go")
	writeFile(t, path, body)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod fake go: %v", err)
	}
	return dir
}

// writeCWDDependentFakeGoVersion 是非 Windows 的 cwd/toolchain fixture；
// 通过 shell 直观复现目录匹配与 GOTOOLCHAIN=auto。
func writeCWDDependentFakeGoVersion(t *testing.T, root, name, requiredDir, matchingOutput, fallbackOutput string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	body := "#!/bin/sh\n" +
		"if [ \"$PWD\" = '" + requiredDir + "' ] && [ \"$GOTOOLCHAIN\" = 'auto' ]; then\n" +
		"  /bin/echo '" + matchingOutput + "'\n" +
		"else\n" +
		"  /bin/echo '" + fallbackOutput + "'\n" +
		"fi\n"
	path := filepath.Join(dir, "go")
	writeFile(t, path, body)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod cwd-sensitive fake go: %v", err)
	}
	return dir
}
