//go:build !windows

package team

import "testing"

func skipIfSymlinkPrivilegeNotHeld(t *testing.T, err error) {
	_ = t
	_ = err
}
