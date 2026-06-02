package main

import (
	"runtime"
	"strings"
	"testing"
)

func skipIfSymlinkPrivilegeNotHeld(t *testing.T, err error) {
	t.Helper()
	if runtime.GOOS == "windows" && strings.Contains(err.Error(), "A required privilege is not held") {
		t.Skipf("skipping symlink test without Windows symlink privilege: %v", err)
	}
}
