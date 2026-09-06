package sqliteauthority

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// TestKernelAuthorityLeaseRejectsDatabaseReplacementBeforeOpen verifies that
// a private regular replacement at the same path cannot be opened under an
// existing kernel lease.  Directory+sentinel locking alone would accept this
// replacement because the parent inode is unchanged.
func TestKernelAuthorityLeaseRejectsDatabaseReplacementBeforeOpen(t *testing.T) {
	config := bootstrapExternalAuthorityForIdentityTest(t, "identity-open")
	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if err != nil {
		t.Fatalf("AcquireKernelAuthorityLease() error = %v", err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	replaceAuthorityEntry(t, config.Path, "database")

	authority, err := OpenAuthority(t.Context(), config, lease, inertFinalizer{}, acceptingReceiptVerifier{})
	if authority != nil {
		_ = authority.Close()
		t.Fatal("OpenAuthority() accepted a replacement database inode")
	}
	if !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("OpenAuthority() error = %v, want ErrLeaseRejected", err)
	}
}

// TestKernelAuthorityLeaseRejectsDatabaseReplacementBeforeTakeover keeps the
// takeover path under the same lock-to-DB identity check as normal Open.
func TestKernelAuthorityLeaseRejectsDatabaseReplacementBeforeTakeover(t *testing.T) {
	config := bootstrapExternalAuthorityForIdentityTest(t, "identity-takeover")
	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if err != nil {
		t.Fatalf("AcquireKernelAuthorityLease() error = %v", err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	replaceAuthorityEntry(t, config.Path, "database")

	next := WriterFence{SchemaVersion: WriterFenceSchemaVersion, WriterEpoch: config.WriterEpoch + 1, DeploymentFence: "deployment-successor"}
	err = TakeoverAuthority(t.Context(), config, lease, next)
	if !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("TakeoverAuthority() error = %v, want ErrLeaseRejected", err)
	}
}

// TestKernelAuthorityLeaseRejectsDatabaseAppearanceBeforeBootstrap closes the
// fresh-DB TOCTOU window: a file appearing after lease acquisition must not be
// treated as the database that Bootstrap is authorized to create.
func TestKernelAuthorityLeaseRejectsDatabaseAppearanceBeforeBootstrap(t *testing.T) {
	config := externalAuthorityIdentityTestConfig(t, "identity-bootstrap")
	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if err != nil {
		t.Fatalf("AcquireKernelAuthorityLease() error = %v", err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	if err := os.WriteFile(config.Path, nil, 0o600); err != nil {
		t.Fatalf("create replacement database: %v", err)
	}

	err = Bootstrap(t.Context(), config, lease)
	if !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("Bootstrap() error = %v, want ErrLeaseRejected", err)
	}
	if info, statErr := os.Stat(config.Path); statErr != nil || info.Size() != 0 {
		t.Fatalf("replacement database changed after rejected Bootstrap: info=%v err=%v", info, statErr)
	}
}

// TestKernelAuthorityLeaseRejectsExistingSQLiteSidecarReplacement verifies
// WAL and SHM objects are pinned whenever they already exist at lease time.
func TestKernelAuthorityLeaseRejectsExistingSQLiteSidecarReplacement(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm"} {
		t.Run(suffix[1:], func(t *testing.T) {
			config := bootstrapExternalAuthorityForIdentityTest(t, "identity"+suffix[1:])
			path := config.Path + suffix
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("remove pre-existing sidecar: %v", err)
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatalf("create sidecar fixture: %v", err)
			}
			lease, err := AcquireKernelAuthorityLease(t.Context(), config)
			if err != nil {
				t.Fatalf("AcquireKernelAuthorityLease() error = %v", err)
			}
			t.Cleanup(func() { _ = lease.Close() })
			replaceAuthorityEntry(t, path, "sidecar"+suffix)

			authority, err := OpenAuthority(t.Context(), config, lease, inertFinalizer{}, acceptingReceiptVerifier{})
			if authority != nil {
				_ = authority.Close()
				t.Fatal("OpenAuthority() accepted a replacement SQLite sidecar inode")
			}
			if !errors.Is(err, ErrLeaseRejected) {
				t.Fatalf("OpenAuthority() error = %v, want ErrLeaseRejected", err)
			}
		})
	}
}

// TestKernelAuthorityLeaseRejectsAuthorityPathUsingLockSentinelName verifies
// that a fresh database cannot steal the fixed lock-sentinel pathname.
func TestKernelAuthorityLeaseRejectsAuthorityPathUsingLockSentinelName(t *testing.T) {
	config := externalAuthorityIdentityTestConfig(t, "identity-lock-sentinel-name")
	config.Path = filepath.Join(filepath.Dir(config.Path), kernelAuthorityLeaseSentinel)

	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if lease != nil || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("AcquireKernelAuthorityLease() = (%v, %v), want nil ErrInvalidConfig", lease, err)
	}
	if _, statErr := os.Lstat(config.Path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("lock sentinel was created after pathname collision rejection: %v", statErr)
	}
}

// TestKernelAuthorityLeaseRejectsAuthorityPathHardlinkToLockSentinel verifies
// that a differently named authority path cannot alias the lock sentinel inode.
func TestKernelAuthorityLeaseRejectsAuthorityPathHardlinkToLockSentinel(t *testing.T) {
	config := externalAuthorityIdentityTestConfig(t, "identity-lock-sentinel-hardlink")
	sentinelPath := filepath.Join(filepath.Dir(config.Path), kernelAuthorityLeaseSentinel)
	if err := os.WriteFile(sentinelPath, nil, 0o600); err != nil {
		t.Fatalf("write lock sentinel fixture: %v", err)
	}
	if err := os.Link(sentinelPath, config.Path); err != nil {
		t.Fatalf("link authority path to lock sentinel: %v", err)
	}

	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if lease != nil || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("AcquireKernelAuthorityLease() = (%v, %v), want nil ErrInvalidConfig", lease, err)
	}
	if err := os.Remove(config.Path); err != nil {
		t.Fatalf("remove authority hardlink fixture: %v", err)
	}
	lock, err := securefs.AcquirePinnedDirectoryLock(filepath.Dir(config.Path), kernelAuthorityLeaseSentinel)
	if err != nil {
		t.Fatalf("AcquirePinnedDirectoryLock() after rejected alias: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close() after rejected alias: %v", err)
	}
}

// TestAuthorityMutationRejectsDatabaseReplacementBeforeBusinessCallback
// verifies the authority-level guard also protects custom/test leases: a
// replacement path cannot reach BEGIN IMMEDIATE or the business callback.
func TestAuthorityMutationRejectsDatabaseReplacementBeforeBusinessCallback(t *testing.T) {
	config := bootstrapExternalAuthorityForIdentityTest(t, "identity-mutation")
	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	t.Cleanup(func() { _ = authority.Close() })
	replaceAuthorityEntry(t, config.Path, "mutation")

	_, err = authority.Submit(t.Context(), schemaTestRequest(t))
	if !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("Submit() error = %v, want ErrLeaseRejected", err)
	}
	if !authority.queryOnly.Load() {
		t.Fatal("authority did not enter query-only mode after DB identity drift")
	}
}

// TestKernelAuthorityLeaseBindsFirstSidecarAppearance verifies the permitted
// SQLite lifecycle: a sidecar absent at lease acquisition is bound after the
// first mutation, and a later same-path inode replacement is rejected.
func TestKernelAuthorityLeaseBindsFirstSidecarAppearance(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm"} {
		t.Run(suffix[1:], func(t *testing.T) { testFirstSidecarAppearance(t, suffix) })
	}
}

// TestAuthorityQueryRejectsSQLiteSidecarReplacement keeps the read path under
// the same sidecar identity fence as mutations.
func TestAuthorityQueryRejectsSQLiteSidecarReplacement(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm"} {
		t.Run(suffix[1:], func(t *testing.T) { testQuerySidecarReplacement(t, suffix) })
	}
}

func testQuerySidecarReplacement(t *testing.T, suffix string) {
	t.Helper()
	config := bootstrapExternalAuthorityForIdentityTest(t, "query-sidecar-replacement"+suffix[1:])
	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if err != nil {
		t.Fatalf("AcquireKernelAuthorityLease() error = %v", err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	authority, err := OpenAuthority(t.Context(), config, lease, inertFinalizer{}, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	t.Cleanup(func() { _ = authority.Close() })
	pending, err := authority.Submit(t.Context(), schemaTestRequest(t))
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if _, err := os.Stat(config.Path + suffix); err != nil {
		t.Fatalf("no SQLite sidecar available for replacement: %v", err)
	}
	replaceAuthorityEntry(t, config.Path+suffix, "query-sidecar")
	if _, err := authority.Query(t.Context(), pending.Handle); !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("Query(after sidecar replacement) error = %v, want ErrLeaseRejected", err)
	}
	if !authority.queryOnly.Load() {
		t.Fatal("Query(after sidecar replacement) did not fuse authority to query-only mode")
	}
}

// TestAuthorityQueryAllowsSQLiteSidecarDisappearanceAndReappearance preserves
// SQLite's lazy sidecar lifecycle while still rebinding a newly-created file.
func TestAuthorityQueryAllowsSQLiteSidecarDisappearanceAndReappearance(t *testing.T) {
	config := bootstrapExternalAuthorityForIdentityTest(t, "query-sidecar-lifecycle")
	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	t.Cleanup(func() { _ = authority.Close() })
	pending, err := authority.Submit(t.Context(), schemaTestRequest(t))
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	path := requireAuthoritySidecar(t, config.Path)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove SQLite sidecar: %v", err)
	}
	if _, err := authority.Query(t.Context(), pending.Handle); err != nil {
		t.Fatalf("Query(after sidecar disappearance) error = %v", err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("recreate SQLite sidecar: %v", err)
	}
	if _, err := authority.Query(t.Context(), pending.Handle); err != nil {
		t.Fatalf("Query(after sidecar reappearance) error = %v", err)
	}
}

// TestAuthorityQueryAllowsKernelSQLiteSidecarDisappearanceAndReappearance
// covers the same lifecycle while the real directory lease owns the snapshot.
func TestAuthorityQueryAllowsKernelSQLiteSidecarDisappearanceAndReappearance(t *testing.T) {
	config := bootstrapExternalAuthorityForIdentityTest(t, "query-kernel-sidecar-lifecycle")
	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if err != nil {
		t.Fatalf("AcquireKernelAuthorityLease() error = %v", err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	authority, err := OpenAuthority(t.Context(), config, lease, inertFinalizer{}, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	t.Cleanup(func() { _ = authority.Close() })
	pending, err := authority.Submit(t.Context(), schemaTestRequest(t))
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	path := requireAuthoritySidecar(t, config.Path)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove SQLite sidecar: %v", err)
	}
	if _, err := authority.Query(t.Context(), pending.Handle); err != nil {
		t.Fatalf("Query(after sidecar disappearance) error = %v", err)
	}
	recreateMissingAuthoritySidecar(t, path)
	if _, err := authority.Query(t.Context(), pending.Handle); err != nil {
		t.Fatalf("Query(after sidecar reappearance) error = %v", err)
	}
}

// requireAuthoritySidecar 返回当前可用的 SQLite WAL 或 SHM sidecar。
func requireAuthoritySidecar(t *testing.T, basePath string) string {
	t.Helper()
	for _, suffix := range []string{"-wal", "-shm"} {
		path := basePath + suffix
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	t.Fatalf("no SQLite sidecar available for lifecycle")
	return ""
}

// recreateMissingAuthoritySidecar 仅在查询未自行重建 sidecar 时创建测试文件。
func recreateMissingAuthoritySidecar(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("recreate SQLite sidecar: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("inspect SQLite sidecar after disappearance query: %v", err)
	}
}

// testFirstSidecarAppearance executes one SQLite sidecar's absent-to-bound
// lifecycle and proves that a later inode replacement is rejected.
func testFirstSidecarAppearance(t *testing.T, suffix string) {
	t.Helper()
	config := bootstrapExternalAuthorityForIdentityTest(t, "first-sidecar"+suffix[1:])
	removeAuthoritySidecars(t, config.Path)
	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if err != nil {
		t.Fatalf("AcquireKernelAuthorityLease() error = %v", err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	authority, err := OpenAuthority(t.Context(), config, lease, inertFinalizer{}, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	t.Cleanup(func() { _ = authority.Close() })
	if _, err := authority.Submit(t.Context(), schemaTestRequest(t)); err != nil {
		t.Fatalf("first Submit() error = %v", err)
	}
	if _, err := os.Stat(config.Path + suffix); err != nil {
		t.Fatalf("first mutation did not create SQLite sidecar %s: %v", suffix, err)
	}
	replaceAuthorityEntry(t, config.Path+suffix, "first-sidecar"+suffix)
	assertSidecarReplacementRejected(t, authority)
}

func removeAuthoritySidecars(t *testing.T, path string) {
	t.Helper()
	for _, sidecar := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + sidecar); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("remove pre-existing sidecar %s: %v", sidecar, err)
		}
	}
}

func assertSidecarReplacementRejected(t *testing.T, authority *Authority) {
	t.Helper()
	second := schemaTestRequest(t)
	second.OperationID = "operation-sidecar-replacement"
	var err error
	second, err = recoverycustody.FinalizeRequestDigest(second)
	if err != nil {
		t.Fatalf("FinalizeRequestDigest(second) error = %v", err)
	}
	if _, err := authority.Submit(t.Context(), second); !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("Submit(after sidecar replacement) error = %v, want ErrLeaseRejected", err)
	}
}

func bootstrapExternalAuthorityForIdentityTest(t *testing.T, name string) Config {
	t.Helper()
	config := externalAuthorityIdentityTestConfig(t, name)
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	return config
}

func externalAuthorityIdentityTestConfig(t *testing.T, name string) Config {
	t.Helper()
	config := authorityTestConfig(t, name)
	config.ExternalService = true
	config.ProductSQLitePath = ""
	config.WorkspaceRoot = ""
	return config
}

func replaceAuthorityEntry(t *testing.T, path, label string) {
	t.Helper()
	moved := filepath.Join(filepath.Dir(path), ".moved-"+label)
	if err := os.Rename(path, moved); err != nil {
		t.Fatalf("rename authority %s: %v", label, err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write replacement authority %s: %v", label, err)
	}
}

var _ recoverycustody.Client = (*Authority)(nil)
