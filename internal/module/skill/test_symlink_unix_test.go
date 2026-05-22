//go:build !windows

package skill

import "testing"

func skipIfSymlinkPrivilegeNotHeld(_ *testing.T, _ error) {}
