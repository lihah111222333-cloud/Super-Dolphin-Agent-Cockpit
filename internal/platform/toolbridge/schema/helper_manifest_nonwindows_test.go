//go:build !windows

package schema

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestHelperPackageNonWindowsRejectsSymlinkAndNonExecutable 验证非 Windows helper 的
// Unix 可执行位与符号链接策略；Windows 不编译本测试，禁止用 runtime.Skip 隐藏平台语义。
func TestHelperPackageNonWindowsRejectsSymlinkAndNonExecutable(t *testing.T) {
	identity := HelperIdentity{AppCommit: "commit-a", GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	dir := t.TempDir()
	helper, manifest := writeHelperPackageFixture(t, dir, "#!/bin/sh\nexit 0\n", identity)
	if err := os.Chmod(helper, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyHelperPackage(helper, manifest, identity); err == nil || !strings.Contains(err.Error(), "execute") {
		t.Fatalf("non-executable verification error = %v", err)
	}
	if err := os.Chmod(helper, 0o700); err != nil {
		t.Fatal(err)
	}
	realHelper := filepath.Join(dir, "real-helper")
	if err := os.Rename(helper, realHelper); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realHelper, helper); err != nil {
		t.Fatal(err)
	}
	if err := VerifyHelperPackage(helper, manifest, identity); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink verification error = %v", err)
	}
}
