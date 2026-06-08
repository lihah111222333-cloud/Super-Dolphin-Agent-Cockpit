//go:build !windows

package memory

import "testing"

func skipIfSymlinkPrivilegeNotHeld(t *testing.T, err error) {
	_ = t
	_ = err
}
