//go:build !windows

package skill

import "testing"

func skipIfSymlinkPrivilegeNotHeld(t *testing.T, err error) {
	_ = t
	_ = err
}
