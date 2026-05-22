//go:build !windows

package team

import "testing"

func skipIfSymlinkPrivilegeNotHeld(_ *testing.T, _ error) {}
