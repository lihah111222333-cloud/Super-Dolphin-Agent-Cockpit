//go:build windows

package main

import (
	"errors"
	"os"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// TestRuntimeServerWindowsTypeScriptLeaseRoundTrip 验证 Windows 选主锁与 lease 使用 DACL 而非 POSIX mode bits。
func TestRuntimeServerWindowsTypeScriptLeaseRoundTrip(t *testing.T) {
	root := runtimeServerSecureCacheRoot(t)
	const cohortID = "repo-windows-typescript"
	role, leasePath, err := runtimeServerAcquireResourceLease(root, cohortID)
	if err != nil {
		t.Fatalf("runtimeServerAcquireResourceLease() error = %v", err)
	}
	if role != multilsp.ResourceCohortRolePrimary {
		t.Fatalf("lease role = %q, want primary", role)
	}
	info, err := os.Lstat(leasePath)
	if err != nil {
		t.Fatalf("lstat lease: %v", err)
	}
	if info.Mode().Perm()&0o077 == 0 {
		t.Fatalf("Windows lease mode = %#o, want non-POSIX mode bits for regression coverage", info.Mode().Perm())
	}
	if _, err := runtimeServerReadResourceLease(leasePath); err != nil {
		t.Fatalf("runtimeServerReadResourceLease() error = %v", err)
	}
	env := []string{
		multilsp.ResourceRepositoryCohortIDEnv + "=" + cohortID,
		multilsp.ResourceCohortLeaseEnv + "=" + leasePath,
	}
	if err := multilsp.ReleaseResourceCohortLease(env); err != nil {
		t.Fatalf("ReleaseResourceCohortLease() error = %v", err)
	}
	if _, err := os.Lstat(leasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released lease still exists: %v", err)
	}
}
