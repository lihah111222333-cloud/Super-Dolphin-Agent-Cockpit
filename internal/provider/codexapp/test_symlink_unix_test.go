//go:build !windows

package codexapp

import "testing"

func skipIfSymlinkPrivilegeNotHeld(t *testing.T, err error) {
	t.Helper()
}
