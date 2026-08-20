//go:build windows

package multilsp

import (
	"errors"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func makeCacheDirUnwritableForTest(t *testing.T, dir string) {
	t.Helper()
	tokenUser, err := windows.GetCurrentThreadEffectiveToken().GetTokenUser()
	if err != nil {
		t.Fatalf("get current Windows token user: %v", err)
	}
	if tokenUser == nil || tokenUser.User.Sid == nil {
		t.Fatalf("current Windows token has no user SID")
	}
	// USERNAME/USERDOMAIN 可能描述宿主账户，而不是实际执行文件操作的沙箱令牌；
	// 使用 effective token SID，确保 fixture 确实命中持久化写失败。
	principal := "*" + tokenUser.User.Sid.String()
	deny := principal + ":(W)"
	cmd := exec.Command("icacls", dir, "/deny", deny)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("icacls deny write failed: %v; output=%s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("icacls", dir, "/remove:d", principal).Run()
	})
}

func isSymlinkPrivilegeNotHeld(err error) bool {
	return errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD)
}
