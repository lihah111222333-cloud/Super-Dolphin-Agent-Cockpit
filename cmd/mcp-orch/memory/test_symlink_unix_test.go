//go:build !windows

package memory

import "testing"

func skipIfSymlinkPrivilegeNotHeld(_ *testing.T, _ error) {}
