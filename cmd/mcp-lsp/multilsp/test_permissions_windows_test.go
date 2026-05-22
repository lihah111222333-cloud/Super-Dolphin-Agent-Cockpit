//go:build windows

package multilsp

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func makeCacheDirUnwritableForTest(t *testing.T, dir string) {
	t.Helper()
	principal := os.Getenv("USERNAME")
	if domain := os.Getenv("USERDOMAIN"); domain != "" && principal != "" {
		principal = domain + `\` + principal
	}
	if strings.TrimSpace(principal) == "" {
		t.Skip("USERNAME is required to make the cache directory unwritable on Windows")
	}
	deny := principal + ":(W)"
	cmd := exec.Command("icacls", dir, "/deny", deny)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("icacls deny write unavailable: %v; output=%s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("icacls", dir, "/remove:d", principal).Run()
	})
}

func isSymlinkPrivilegeNotHeld(err error) bool {
	return errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD)
}
