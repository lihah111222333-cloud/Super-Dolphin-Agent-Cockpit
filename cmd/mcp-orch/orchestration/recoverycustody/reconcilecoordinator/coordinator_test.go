package reconcilecoordinator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

type fakeAuthority struct {
	handles []recoverycustody.SubmissionHandle
	status  string
	calls   int
}

func (f *fakeAuthority) ListActiveResolutionHandles(context.Context) ([]recoverycustody.SubmissionHandle, error) {
	return append([]recoverycustody.SubmissionHandle(nil), f.handles...), nil
}

func (f *fakeAuthority) ReconcileResolution(_ context.Context, h recoverycustody.SubmissionHandle) (recoverycustody.Outcome, error) {
	f.calls++
	if f.status == "error" {
		return recoverycustody.Outcome{Handle: h, Status: recoverycustody.StatusIndeterminate}, errors.New("authority unavailable")
	}
	out := recoverycustody.Outcome{Handle: h, Status: f.status}
	if f.status == recoverycustody.StatusAvailable || f.status == recoverycustody.StatusAborted {
		out.Receipt = &recoverycustody.ExternalReceipt{OperationID: h.OperationID}
	}
	return out, nil
}

func testHandle(t *testing.T, id string) recoverycustody.SubmissionHandle {
	t.Helper()
	r := recoverycustody.SubmissionRequest{SchemaVersion: 1, OperationID: id, SecurityDomainID: "security", AuthorityProfile: "profile", CommandID: "command", CommandVersion: 1,
		CommandProfileDigest: digest('1'), AuthorizationDigest: digest('2'), IdentityFenceDigest: digest('3'), ContentSnapshotDigest: digest('4'), ResourceSnapshotDigest: digest('5'), BatchDigest: digest('6'), IdempotencyKeyDigest: digest('7')}
	var err error
	r.RequestDigest, err = recoverycustody.ComputeRequestDigest(r)
	if err != nil {
		t.Fatal(err)
	}
	h, err := recoverycustody.NewSubmissionHandle(r)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func digest(ch byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}

func testConfig(path string, now *time.Time) Config {
	return Config{Path: path, OwnerID: "coordinator-owner", OwnerEpoch: 1, OwnerToken: digest('a'), ClaimTTL: time.Minute, RetryBase: time.Second, RetryMax: time.Minute, MaxAttempts: 3, RunnerPeriod: time.Millisecond,
		Now: func() time.Time { return *now }}
}

func coordinatorTestPath(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "coordinator-state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir coordinator state: %v", err)
	}
	return filepath.Join(dir, name)
}

func openTestCoordinator(t *testing.T, cfg Config, auth Authority) *Coordinator {
	t.Helper()
	if err := Bootstrap(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	c, err := Open(t.Context(), cfg, auth)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func requireStartupScans(t *testing.T, c *Coordinator) {
	t.Helper()
	for range 2 {
		if err := c.StartupScan(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func requireRunOnce(t *testing.T, c *Coordinator, label string) {
	t.Helper()
	ok, err := c.RunOnce(t.Context())
	if err != nil || !ok {
		t.Fatalf("%s RunOnce=(%v,%v)", label, ok, err)
	}
}

func requireJob(t *testing.T, c *Coordinator, state JobState, attempts int) Job {
	t.Helper()
	jobs, err := c.ListJobs(t.Context())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	job := jobs[0]
	if job.State != state || (attempts >= 0 && job.Attempts != attempts) {
		t.Fatalf("unexpected job=%+v", job)
	}
	return job
}

func TestBootstrapOpenStartupScanAndTerminalRun(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	path := coordinatorTestPath(t, "coordinator.sqlite")
	cfg := testConfig(path, &now)
	auth := &fakeAuthority{handles: []recoverycustody.SubmissionHandle{testHandle(t, "op-1")}, status: recoverycustody.StatusIndeterminate}
	c := openTestCoordinator(t, cfg, auth)
	requireStartupScans(t, c)
	requireJob(t, c, StateQueued, -1)
	requireRunOnce(t, c, "initial")
	requireJob(t, c, StateRetryWait, 1)
	now = now.Add(time.Second)
	auth.status = recoverycustody.StatusAvailable
	requireRunOnce(t, c, "terminal")
	job := requireJob(t, c, StateSucceeded, -1)
	journals, err := c.ListJournal(t.Context(), job.JobID)
	if err != nil || len(journals) != 5 {
		t.Fatalf("journal=%+v err=%v", journals, err)
	}
	audit, err := c.ListAudit(t.Context(), job.JobID)
	if err != nil || len(audit) != 2 {
		t.Fatalf("audit=%+v err=%v", audit, err)
	}
}

func TestTakeoverFencesOldOwner(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	path := coordinatorTestPath(t, "coordinator.sqlite")
	cfg := testConfig(path, &now)
	if err := Bootstrap(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	auth := &fakeAuthority{handles: []recoverycustody.SubmissionHandle{testHandle(t, "op-2")}, status: recoverycustody.StatusIndeterminate}
	old, err := Open(t.Context(), cfg, auth)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := old.StartupScan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := old.Takeover(t.Context(), 2, digest('b')); err != nil {
		t.Fatal(err)
	}
	if _, err := old.RunOnce(t.Context()); !errors.Is(err, ErrOwnerFenced) {
		t.Fatalf("old owner RunOnce err=%v", err)
	}
	nextCfg := cfg
	nextCfg.OwnerEpoch = 2
	nextCfg.OwnerToken = digest('b')
	successor, err := Open(t.Context(), nextCfg, auth)
	if err != nil {
		t.Fatal(err)
	}
	defer successor.Close()
	if _, err := successor.RunOnce(t.Context()); err != nil {
		t.Fatalf("successor RunOnce err=%v", err)
	}
}

func TestBootstrapRejectsCoordinatorSymlink(t *testing.T) {
	now := time.Unix(300, 0).UTC()
	dir := filepath.Dir(coordinatorTestPath(t, "placeholder"))
	target := filepath.Join(dir, "target.sqlite")
	path := filepath.Join(dir, "coordinator.sqlite")
	if err := os.WriteFile(target, []byte("not a coordinator"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := Bootstrap(t.Context(), testConfig(path, &now))
	if !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("Bootstrap symlink err=%v, want ErrSchemaMismatch", err)
	}
}

func TestOpenRejectsSQLiteSidecarSymlink(t *testing.T) {
	now := time.Unix(350, 0).UTC()
	path := coordinatorTestPath(t, "coordinator.sqlite")
	dir := filepath.Dir(path)
	cfg := testConfig(path, &now)
	if err := Bootstrap(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "outside-wal")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path+"-wal"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Open(t.Context(), cfg, &fakeAuthority{}); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("Open sidecar symlink err=%v, want ErrSchemaMismatch", err)
	}
}

func TestOpenRejectsWorldReadableCoordinatorFilesystem(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not authoritative on Windows")
	}
	tests := []struct {
		name  string
		setup func(t *testing.T, cfg Config)
	}{
		{
			name: "database",
			setup: func(t *testing.T, cfg Config) {
				if err := os.Chmod(cfg.Path, 0o644); err != nil {
					t.Fatalf("chmod coordinator database: %v", err)
				}
			},
		},
		{
			name: "parent",
			setup: func(t *testing.T, cfg Config) {
				if err := os.Chmod(filepath.Dir(cfg.Path), 0o755); err != nil {
					t.Fatalf("chmod coordinator parent: %v", err)
				}
			},
		},
		{
			name: "wal_sidecar",
			setup: func(t *testing.T, cfg Config) {
				if err := os.WriteFile(cfg.Path+"-wal", nil, 0o644); err != nil {
					t.Fatalf("create WAL sidecar: %v", err)
				}
			},
		},
		{
			name: "shm_sidecar",
			setup: func(t *testing.T, cfg Config) {
				if err := os.WriteFile(cfg.Path+"-shm", nil, 0o644); err != nil {
					t.Fatalf("create SHM sidecar: %v", err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(375, 0).UTC()
			path := coordinatorTestPath(t, "coordinator.sqlite")
			cfg := testConfig(path, &now)
			if err := Bootstrap(t.Context(), cfg); err != nil {
				t.Fatalf("Bootstrap() error = %v", err)
			}
			tc.setup(t, cfg)
			if _, err := Open(t.Context(), cfg, &fakeAuthority{}); !errors.Is(err, ErrSchemaMismatch) {
				t.Fatalf("Open() error = %v, want ErrSchemaMismatch", err)
			}
		})
	}
}

func TestOpenRejectsCoordinatorDatabaseSymlink(t *testing.T) {
	now := time.Unix(380, 0).UTC()
	dir := filepath.Dir(coordinatorTestPath(t, "placeholder"))
	target := filepath.Join(dir, "target.sqlite")
	path := filepath.Join(dir, "coordinator.sqlite")
	targetCfg := testConfig(target, &now)
	if err := Bootstrap(t.Context(), targetCfg); err != nil {
		t.Fatalf("Bootstrap() target error = %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	cfg := testConfig(path, &now)
	if _, err := Open(t.Context(), cfg, &fakeAuthority{}); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("Open() database symlink error = %v, want ErrSchemaMismatch", err)
	}
}

func TestNormalizeResultDoesNotPersistAuthorityError(t *testing.T) {
	h := testHandle(t, "op-redaction")
	secret := "/private/token=super-secret"
	status, detail, err := normalizeResult(h, recoverycustody.Outcome{Handle: h, Status: recoverycustody.StatusIndeterminate}, errors.New(secret))
	if err == nil || status != recoverycustody.StatusIndeterminate {
		t.Fatalf("normalizeResult=(%q,%q,%v)", status, detail, err)
	}
	if detail != "authority reconciliation failed" || strings.Contains(detail, secret) {
		t.Fatalf("detail=%q leaked authority error", detail)
	}
	status, detail, err = normalizeResult(h, recoverycustody.Outcome{Handle: h, Status: "unexpected-status"}, nil)
	if err == nil || status != recoverycustody.StatusIndeterminate || detail != "authority returned unsupported status" {
		t.Fatalf("unsupported status normalization=(%q,%q,%v)", status, detail, err)
	}
}

func TestRunOncePersistsOnlySafeAuthorityErrorDetail(t *testing.T) {
	now := time.Unix(400, 0).UTC()
	path := coordinatorTestPath(t, "coordinator.sqlite")
	cfg := testConfig(path, &now)
	if err := Bootstrap(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	secret := "/private/token=super-secret"
	auth := &errorAuthority{handle: testHandle(t, "op-error"), err: errors.New(secret)}
	c, err := Open(t.Context(), cfg, auth)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Enqueue(t.Context(), auth.handle); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	jobs, err := c.ListJobs(t.Context())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	if jobs[0].LastError != "authority reconciliation failed" || strings.Contains(jobs[0].LastError, secret) {
		t.Fatalf("last_error=%q leaked authority error", jobs[0].LastError)
	}
}

type errorAuthority struct {
	handle recoverycustody.SubmissionHandle
	err    error
}

func (a *errorAuthority) ListActiveResolutionHandles(context.Context) ([]recoverycustody.SubmissionHandle, error) {
	return nil, nil
}

func (a *errorAuthority) ReconcileResolution(context.Context, recoverycustody.SubmissionHandle) (recoverycustody.Outcome, error) {
	return recoverycustody.Outcome{Handle: a.handle, Status: recoverycustody.StatusIndeterminate}, a.err
}
