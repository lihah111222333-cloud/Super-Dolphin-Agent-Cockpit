//go:build darwin || linux || freebsd

package appupdaterecovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRollbackRestartEndpointUsesShortSafeRootForLongTempDir(t *testing.T) {
	shortRoot := newShortSafeRollbackRestartRoot(t)
	longRoot := filepath.Join(t.TempDir(), strings.Repeat("r", 128))
	if err := os.Mkdir(longRoot, 0o700); err != nil {
		t.Fatalf("create long rollback restart root: %v", err)
	}
	basename := "x"
	limit := len(syscall.RawSockaddrUnix{}.Path)
	if len(filepath.Join(longRoot, basename)) < limit {
		t.Fatalf("long root fixture is not over sockaddr limit %d: %q", limit, longRoot)
	}
	if len(filepath.Join(shortRoot, basename)) >= limit {
		t.Fatalf("short root fixture is over sockaddr limit %d: %q", limit, shortRoot)
	}
	if !rollbackRestartEndpointRootIsSafe(longRoot) {
		t.Fatalf("long root fixture is not a safe candidate: %q", longRoot)
	}

	root, err := rollbackRestartEndpointRootFromCandidates(basename, []string{longRoot, shortRoot})
	if err != nil {
		t.Fatalf("select rollback restart endpoint root: %v", err)
	}
	if root != shortRoot {
		t.Fatalf("endpoint root = %q, want controlled short root %q", root, shortRoot)
	}
	endpoint := filepath.Join(root, basename)
	if len(endpoint) >= limit {
		t.Fatalf("endpoint length = %d, want less than Unix sockaddr limit %d: %q", len(endpoint), limit, endpoint)
	}
}

func TestRollbackRestartEndpointRejectsSafeRootAtSockaddrLimit(t *testing.T) {
	root := newShortSafeRollbackRestartRoot(t)
	limit := len(syscall.RawSockaddrUnix{}.Path)
	basenameBytes := limit - len(root) - 1
	if basenameBytes <= 0 {
		t.Fatalf("short root leaves no basename space below sockaddr limit %d: %q", limit, root)
	}
	basename := strings.Repeat("x", basenameBytes)
	if got := len(filepath.Join(root, basename)); got != limit {
		t.Fatalf("boundary endpoint length = %d, want exact sockaddr limit %d", got, limit)
	}

	_, err := rollbackRestartEndpointRootFromCandidates(basename, []string{root})
	want := fmt.Sprintf("rollback restart Unix socket path exceeds %d-byte sockaddr limit", limit)
	if err == nil || err.Error() != want {
		t.Fatalf("select boundary rollback restart endpoint root error = %v, want %q", err, want)
	}
}

func newShortSafeRollbackRestartRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp(rollbackRestartShortTempRoot, "sd-rr-root-")
	if err != nil {
		t.Fatalf("create short rollback restart root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(root); err != nil {
			t.Errorf("remove short rollback restart root: %v", err)
		}
	})
	if !rollbackRestartEndpointRootIsSafe(root) {
		t.Fatalf("short root fixture is not a safe candidate: %q", root)
	}
	return root
}

func TestRollbackRestartTerminationEndpointUsesConfiguredTempRoot(t *testing.T) {
	root := filepath.Clean(os.TempDir())
	endpoint, err := RollbackRestartTerminationEndpoint(
		TransactionID(strings.Repeat("a", transactionIDBytes*2)),
		strings.Repeat("b", rollbackLaunchTokenBytes*2),
	)
	if err != nil {
		t.Fatalf("derive rollback restart endpoint: %v", err)
	}
	if filepath.Dir(endpoint) != root {
		t.Fatalf("endpoint root = %q, want configured temp root %q", filepath.Dir(endpoint), root)
	}
	if limit := len(syscall.RawSockaddrUnix{}.Path); len(endpoint) >= limit {
		t.Fatalf("endpoint length = %d, want less than Unix sockaddr limit %d: %q", len(endpoint), limit, endpoint)
	}
}
