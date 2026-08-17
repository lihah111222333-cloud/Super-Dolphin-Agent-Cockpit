//go:build !windows

package discovery

import (
	"os"
	"testing"
)

func assertDiscoveryFileOwnerOnly(t *testing.T, info os.FileInfo) {
	t.Helper()
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}
