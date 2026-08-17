//go:build windows && e2e

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// TestRuntimeDurableGoplsRootCohortWindowsPersistenceE2E 锁定 Windows 文件锁、严格记录和原子发布闭环。
func TestRuntimeDurableGoplsRootCohortWindowsPersistenceE2E(t *testing.T) {
	cacheRoot := filepath.Join(t.TempDir(), "shared-cache")
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	config := multilsp.GoplsRootCohortConfig{
		CohortID: "windows-durable-e2e",
		RepositoryInstanceProof: multilsp.GoplsRepositoryInstanceProof{
			CanonicalRootDigest: "windows-canonical-root",
			FilesystemIdentity:  "windows-volume-file-id",
			GitMarkerDigest:     "windows-git-marker",
			InstanceNonce:       "windows-instance-nonce",
		},
		EffectiveConfigDigest: "windows-effective-config",
	}
	controller, err := runtimeServerNewDurableGoplsRootCohortController()
	if err != nil {
		t.Fatalf("new durable controller: %v", err)
	}
	t.Cleanup(func() {
		if err := controller.Close(); err != nil {
			t.Errorf("close durable controller: %v", err)
		}
	})

	lease, err := controller.AcquireLease(config)
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	dir := runtimeServerGoplsRootCohortDir(cacheRoot, config)
	statePath := filepath.Join(dir, "state.json")
	leasePath := runtimeServerGoplsRootCohortLeasePath(dir, lease.Fence())
	state := assertRuntimeWindowsDurableRecords(t, config, lease.Fence(), statePath, leasePath)
	assertRuntimeWindowsPrivatePaths(t, dir, filepath.Join(dir, ".primary.lock"), statePath, leasePath)
	assertRuntimeWindowsDurableRelease(t, &lease, state.JournalRevision, dir, statePath, leasePath)
}

// assertRuntimeWindowsDurableRecords 校验 state 与 lease 的严格持久化内容。
func assertRuntimeWindowsDurableRecords(
	t *testing.T,
	config multilsp.GoplsRootCohortConfig,
	fence multilsp.GoplsRootCohortFence,
	statePath, leasePath string,
) *runtimeServerDurableGoplsRootCohortState {
	t.Helper()
	state, err := runtimeServerReadGoplsRootCohortState(statePath)
	if err != nil {
		t.Fatalf("read strict state record: %v", err)
	}
	persistedLease, err := runtimeServerReadGoplsRootCohortLease(leasePath)
	if err != nil {
		t.Fatalf("read strict lease record: %v", err)
	}
	if stored, err := state.configValue(); err != nil || stored != config {
		t.Fatalf("persisted config = %+v, error = %v, want %+v", stored, err, config)
	}
	if persistedLease.ConfigDigest != state.ConfigDigest || persistedLease.Fence.toValue() != fence {
		t.Fatalf("persisted lease = %+v, want state digest and fence %+v", persistedLease, fence)
	}
	return state
}

// assertRuntimeWindowsPrivatePaths 复用生产校验验证目录和记录的受保护 DACL。
func assertRuntimeWindowsPrivatePaths(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("Lstat(%q): %v", path, err)
		}
		if err := securefs.CheckPrivateOwnerOnly(path, info); err != nil {
			t.Fatalf("Windows private DACL validation for %q: %v", path, err)
		}
	}
}

// assertRuntimeWindowsDurableRelease 校验 Release 后的原子 state 替换和 lease 清理。
func assertRuntimeWindowsDurableRelease(
	t *testing.T,
	lease *multilsp.GoplsRootCohortLease,
	previousRevision uint64,
	dir, statePath, leasePath string,
) {
	t.Helper()
	if err := lease.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	released, err := runtimeServerReadGoplsRootCohortState(statePath)
	if err != nil {
		t.Fatalf("read atomically replaced state record: %v", err)
	}
	if released.JournalRevision <= previousRevision || released.DrainStatus != runtimeGoplsRootCohortDrainCompleted {
		t.Fatalf("released state = %+v, want newer completed state", released)
	}
	assertRuntimeWindowsPrivatePaths(t, statePath)
	if _, err := os.Lstat(leasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released lease still exists or cannot be inspected: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read durable cohort directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".gopls-root-cohort-") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("atomic publish left temporary record %q", entry.Name())
		}
	}
}
