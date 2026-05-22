//go:build windows

package team

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func skipIfSymlinkPrivilegeNotHeld(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
		t.Skipf("symlink privilege unavailable: %v", err)
	}
}
