//go:build darwin || linux || freebsd

package appupdaterecovery

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRollbackRestartEndpointUsesShortSafeRootForLongTempDir(t *testing.T) {
	shortRoot := t.TempDir()
	longRoot := filepath.Join(t.TempDir(), strings.Repeat("r", 128))
	basename := "x"
	limit := len(syscall.RawSockaddrUnix{}.Path)
	if len(filepath.Join(longRoot, basename)) < limit {
		t.Fatalf("long root fixture is not over sockaddr limit %d: %q", limit, longRoot)
	}
	if len(filepath.Join(shortRoot, basename)) >= limit {
		t.Fatalf("short root fixture is over sockaddr limit %d: %q", limit, shortRoot)
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
