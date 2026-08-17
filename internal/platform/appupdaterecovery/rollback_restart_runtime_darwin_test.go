//go:build darwin

package appupdaterecovery

import (
	"errors"
	"net"
	"os"
	"testing"
)

func newRefusedRollbackRestartEndpoint(t *testing.T) string {
	t.Helper()
	file, err := os.CreateTemp("/tmp", "sd-rollback-ready-")
	if err != nil {
		t.Fatalf("create refused rollback endpoint path: %v", err)
	}
	endpoint := file.Name() + ".sock"
	if err := file.Close(); err != nil {
		t.Fatalf("close refused rollback endpoint path: %v", err)
	}
	if err := os.Remove(file.Name()); err != nil {
		t.Fatalf("remove refused rollback endpoint path: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(endpoint); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove refused rollback endpoint: %v", err)
		}
	})
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: endpoint, Net: "unix"})
	if err != nil {
		t.Fatalf("listen refused rollback endpoint: %v", err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(endpoint, 0o600); err != nil {
		_ = listener.Close()
		t.Fatalf("chmod refused rollback endpoint: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close refused rollback listener: %v", err)
	}
	return endpoint
}
