//go:build unix

package localci

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSchedulerStateRejectsSchemaVersionDrift(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(canonicalPrivateTempDir(t), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open drift fixture: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE scheduler_schema (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			version INTEGER NOT NULL,
			daemon_key TEXT NOT NULL
		);
		INSERT INTO scheduler_schema (id, version, daemon_key) VALUES (1, 999, 'wrong');
	`); err != nil {
		t.Fatalf("create drift fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close drift fixture: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("set drift fixture mode: %v", err)
	}

	identity := mustDaemonIdentity(t, "daemon-state")
	if _, err := openSchedulerState(ctx, path, identity); err == nil {
		t.Fatal("expected schema version drift to fail")
	}
}

func TestSchedulerStateRejectsDaemonIdentityMismatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(canonicalPrivateTempDir(t), "state.db")
	first, err := openSchedulerState(ctx, path, mustDaemonIdentity(t, "daemon-first"))
	if err != nil {
		t.Fatalf("open first state: %v", err)
	}
	if err := first.close(); err != nil {
		t.Fatalf("close first state: %v", err)
	}
	if _, err := openSchedulerState(ctx, path, mustDaemonIdentity(t, "daemon-second")); err == nil {
		t.Fatal("expected daemon identity mismatch to fail")
	}
}

func TestSchedulerStateRestoresQueueAndLeases(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(canonicalPrivateTempDir(t), "state.db")
	identity := mustDaemonIdentity(t, "daemon-restore")
	state := mustOpenSchedulerState(t, ctx, path, identity)
	kernel := mustNewSchedulerKernel(t, identity)
	mustEnqueue(t, kernel, workloadSpec{id: "build", invocationID: "inv-1", enqueueSeq: 1, kind: workloadBuild})
	mustEnqueue(t, kernel, workloadSpec{
		id:           "job",
		invocationID: "inv-1",
		enqueueSeq:   1,
		kind:         workloadJob,
		dependencies: []workloadID{"build"},
	})
	if _, err := kernel.reserveRunnable(); err != nil {
		t.Fatalf("reserve build: %v", err)
	}
	if err := state.saveKernel(ctx, kernel); err != nil {
		t.Fatalf("save kernel: %v", err)
	}
	mustCloseSchedulerState(t, state)

	reopened := mustOpenSchedulerState(t, ctx, path, identity)
	cleanupSchedulerState(t, reopened)
	restored, err := reopened.loadKernel(ctx, identity)
	if err != nil {
		t.Fatalf("load kernel: %v", err)
	}
	if got := restored.state("build"); got != stateStarted {
		t.Fatalf("build state=%s want=%s", got, stateStarted)
	}
	if got := restored.state("job"); got != stateQueued {
		t.Fatalf("job state=%s want=%s", got, stateQueued)
	}
	if len(restored.leases) != 1 {
		t.Fatalf("restored leases=%d want=1", len(restored.leases))
	}
}

func TestSchedulerRecoveryFailsOnUnknownOrDuplicateLease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state, kernel := newTestSchedulerState(t, "reconcile")
	mustEnqueue(t, kernel, workloadSpec{id: "job", invocationID: "inv-1", enqueueSeq: 1, kind: workloadJob})
	reservations, err := kernel.reserveRunnable()
	if err != nil {
		t.Fatalf("reserve job: %v", err)
	}
	if err := state.saveKernel(ctx, kernel); err != nil {
		t.Fatalf("save kernel: %v", err)
	}
	lease := reservations[0].leases[0]

	if err := state.reconcileLeases(ctx, []observedLease{
		{id: lease.id, workloadID: lease.workloadID},
		{id: "unknown", workloadID: "unknown"},
	}); err == nil {
		t.Fatal("expected unknown observed lease to fail")
	}
	if err := state.reconcileLeases(ctx, []observedLease{
		{id: lease.id, workloadID: lease.workloadID},
		{id: lease.id, workloadID: lease.workloadID},
	}); err == nil {
		t.Fatal("expected duplicate observed lease to fail")
	}
}

func TestSchedulerRecoveryMarksUnprovedRunningWorkloadInfraFailed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state, kernel := newTestSchedulerState(t, "missing-lease")
	mustEnqueue(t, kernel, workloadSpec{id: "job", invocationID: "inv-1", enqueueSeq: 1, kind: workloadJob})
	if _, err := kernel.reserveRunnable(); err != nil {
		t.Fatalf("reserve job: %v", err)
	}
	if err := state.saveKernel(ctx, kernel); err != nil {
		t.Fatalf("save kernel: %v", err)
	}
	if err := state.reconcileLeases(ctx, nil); !errors.Is(err, errRecoveryUnproved) {
		t.Fatalf("reconcile error=%v want=%v", err, errRecoveryUnproved)
	}
	restored, err := state.loadKernel(ctx, kernel.identity)
	if err != nil {
		t.Fatalf("load reconciled kernel: %v", err)
	}
	if got := restored.state("job"); got != stateInfraFailed {
		t.Fatalf("job state=%s want=%s", got, stateInfraFailed)
	}
	if len(restored.leases) != 0 {
		t.Fatalf("leases after failed recovery=%d want=0", len(restored.leases))
	}
}

func TestSchedulerOutboxTransitionReplayAndAckAreDurable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(canonicalPrivateTempDir(t), "state.db")
	identity := mustDaemonIdentity(t, "daemon-outbox")
	state := mustOpenSchedulerState(t, ctx, path, identity)
	kernel := mustNewSchedulerKernel(t, identity)
	mustEnqueue(t, kernel, workloadSpec{id: "job", invocationID: "inv-1", enqueueSeq: 1, kind: workloadJob})
	if err := state.saveKernel(ctx, kernel); err != nil {
		t.Fatalf("save kernel: %v", err)
	}
	statuses := []workloadState{stateQueued, stateStarted, stateFailed}
	for index, status := range statuses {
		event := outboxEvent{
			subscriberID: "subscriber-1",
			invocationID: "inv-1",
			status:       status,
			payload:      []byte(`{"status":"` + string(status) + `"}`),
		}
		stored, err := state.transitionWithEvent(ctx, "job", event)
		if err != nil {
			t.Fatalf("transition %s with event: %v", status, err)
		}
		if stored.sequence != uint64(index+1) {
			t.Fatalf("event sequence=%d want=%d", stored.sequence, index+1)
		}
	}
	mustCloseSchedulerState(t, state)

	reopened := mustOpenSchedulerState(t, ctx, path, identity)
	cleanupSchedulerState(t, reopened)
	events, err := reopened.replayOutbox(ctx, "subscriber-1", "inv-1")
	if err != nil {
		t.Fatalf("replay outbox: %v", err)
	}
	assertOutboxSequences(t, events, 1, 2, 3)
	if err := reopened.ackOutbox(ctx, "subscriber-1", "inv-1", 3); err != nil {
		t.Fatalf("ack outbox: %v", err)
	}
	events, err = reopened.replayOutbox(ctx, "subscriber-1", "inv-1")
	if err != nil {
		t.Fatalf("replay after ack: %v", err)
	}
	assertOutboxSequences(t, events)
	if err := reopened.ackOutbox(ctx, "subscriber-1", "inv-1", 4); err == nil {
		t.Fatal("expected ack beyond emitted sequence to fail")
	}
}

func TestSchedulerOutboxTransitionRejectsOwnerAndIllegalStateWithoutChanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state, kernel := newTestSchedulerState(t, "outbox-cas")
	mustEnqueue(t, kernel, workloadSpec{id: "job", invocationID: "inv-1", enqueueSeq: 1, kind: workloadJob})
	if err := state.saveKernel(ctx, kernel); err != nil {
		t.Fatalf("save kernel: %v", err)
	}

	cases := []struct {
		name  string
		id    workloadID
		event outboxEvent
	}{
		{
			name: "invocation-owner",
			id:   "job",
			event: outboxEvent{subscriberID: "subscriber-1", invocationID: "inv-2", status: stateQueued,
				payload: []byte(`{"status":"queued"}`)},
		},
		{
			name: "queued-to-terminal",
			id:   "job",
			event: outboxEvent{subscriberID: "subscriber-1", invocationID: "inv-1", status: stateFailed,
				payload: []byte(`{"status":"failed"}`)},
		},
		{
			name: "unknown-workload",
			id:   "missing",
			event: outboxEvent{subscriberID: "subscriber-1", invocationID: "inv-1", status: stateQueued,
				payload: []byte(`{"status":"queued"}`)},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := state.transitionWithEvent(ctx, test.id, test.event); err == nil {
				t.Fatal("expected transition to fail")
			}
			assertPersistedWorkloadAndOutbox(t, state, "job", stateQueued, 0)
		})
	}
}

func TestSchedulerOutboxTransitionRollsBackStateWhenInsertFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state, kernel := newTestSchedulerState(t, "outbox-rollback")
	mustEnqueue(t, kernel, workloadSpec{id: "job", invocationID: "inv-1", enqueueSeq: 1, kind: workloadJob})
	if err := state.saveKernel(ctx, kernel); err != nil {
		t.Fatalf("save kernel: %v", err)
	}
	if _, err := state.db.ExecContext(ctx, `
		CREATE TRIGGER reject_scheduler_outbox
		BEFORE INSERT ON scheduler_outbox
		BEGIN SELECT RAISE(ABORT, 'reject outbox fixture'); END;
	`); err != nil {
		t.Fatalf("create outbox rejection trigger: %v", err)
	}
	event := outboxEvent{
		subscriberID: "subscriber-1",
		invocationID: "inv-1",
		status:       stateStarted,
		payload:      []byte(`{"status":"running"}`),
	}
	if _, err := state.transitionWithEvent(ctx, "job", event); err == nil {
		t.Fatal("expected outbox insert failure")
	}
	assertPersistedWorkloadAndOutbox(t, state, "job", stateQueued, 0)
}

func TestSchedulerStateRejectsRelativePath(t *testing.T) {
	t.Parallel()

	if _, err := validateCurrentUIDPrivatePath("state.db", os.Geteuid()); err == nil {
		t.Fatal("expected relative state path to fail")
	}
}

func TestSchedulerStateRejectsSymlink(t *testing.T) {
	t.Parallel()

	privateDir := canonicalPrivateTempDir(t)
	target := filepath.Join(privateDir, "state-target.db")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatalf("write state target: %v", err)
	}
	link := filepath.Join(privateDir, "state-link.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create state symlink: %v", err)
	}
	expectSchedulerStateRejected(t, link, mustDaemonIdentity(t, "daemon-state-symlink"))
}

func TestSchedulerStateRejectsSharedParent(t *testing.T) {
	t.Parallel()

	parent := filepath.Join(canonicalPrivateTempDir(t), "shared-state")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatalf("create shared state parent: %v", err)
	}
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatalf("set shared state parent mode: %v", err)
	}
	expectSchedulerStateRejected(t, filepath.Join(parent, "state.db"), mustDaemonIdentity(t, "daemon-state-shared-parent"))
}

func TestSchedulerStateRejectsSharedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(canonicalPrivateTempDir(t), "shared-state.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write shared state: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("set shared state mode: %v", err)
	}
	expectSchedulerStateRejected(t, path, mustDaemonIdentity(t, "daemon-state-shared-file"))
}

func TestSchedulerStateRejectsPeerOwner(t *testing.T) {
	t.Parallel()

	peer := mustDaemonIdentity(t, "daemon-state-peer-owner")
	peer.ownerUID = os.Geteuid() + 1
	expectSchedulerStateRejected(t, filepath.Join(canonicalPrivateTempDir(t), "peer-state.db"), peer)
}

func TestSchedulerStateCreatesPrivateFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(canonicalPrivateTempDir(t), "created-state.db")
	state := mustOpenSchedulerState(t, context.Background(), path, mustDaemonIdentity(t, "daemon-state-private-mode"))
	mustCloseSchedulerState(t, state)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat created state: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("created state mode=%04o want=0600", info.Mode().Perm())
	}
}

func expectSchedulerStateRejected(t *testing.T, path string, identity daemonIdentity) {
	t.Helper()

	if _, err := openSchedulerState(context.Background(), path, identity); err == nil {
		t.Fatal("expected scheduler state open to fail")
	}
}

func TestSchedulerConstructorsLeavePackageDirectoryClean(t *testing.T) {
	t.Parallel()

	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("read package working directory: %v", err)
	}
	residue := []string{
		filepath.Join(workingDir, "scheduler.lock"),
		filepath.Join(workingDir, "state.db"),
	}
	assertPathsAbsent(t, residue)

	privateDir := canonicalPrivateTempDir(t)
	identity := mustDaemonIdentity(t, "daemon-no-residue")
	lock, err := acquireSchedulerLock(filepath.Join(privateDir, "scheduler.lock"), identity)
	if err != nil {
		t.Fatalf("acquire temp scheduler lock: %v", err)
	}
	if err := lock.close(); err != nil {
		t.Fatalf("close temp scheduler lock: %v", err)
	}
	state := mustOpenSchedulerState(t, context.Background(), filepath.Join(privateDir, "state.db"), identity)
	mustCloseSchedulerState(t, state)

	assertPathsAbsent(t, residue)
}

func TestSchedulerOpaqueHandshakeIsBoundExpiringAndSingleUse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state, _ := newTestSchedulerState(t, "token")
	now := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now }
	binding := opaqueHandshakeBinding{jobID: "job-1", invocationID: "inv-1", containerID: "container-1"}
	token, err := state.issueOpaqueHandshake(ctx, binding, time.Minute)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	if err := state.consumeOpaqueHandshake(ctx, token, opaqueHandshakeBinding{
		jobID: "job-1", invocationID: "inv-1", containerID: "wrong",
	}); err == nil {
		t.Fatal("expected cross-container token use to fail")
	}
	if err := state.consumeOpaqueHandshake(ctx, token, binding); err != nil {
		t.Fatalf("consume token: %v", err)
	}
	if err := state.consumeOpaqueHandshake(ctx, token, binding); !errors.Is(err, errHandshakeConsumed) {
		t.Fatalf("second consume error=%v want=%v", err, errHandshakeConsumed)
	}

	expired, err := state.issueOpaqueHandshake(ctx, binding, time.Minute)
	if err != nil {
		t.Fatalf("issue expiring token: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if err := state.consumeOpaqueHandshake(ctx, expired, binding); !errors.Is(err, errHandshakeExpired) {
		t.Fatalf("expired consume error=%v want=%v", err, errHandshakeExpired)
	}
}

func newTestSchedulerState(t *testing.T, suffix string) (*schedulerState, *schedulerKernel) {
	t.Helper()

	ctx := context.Background()
	identity := mustDaemonIdentity(t, "daemon-"+suffix)
	state := mustOpenSchedulerState(t, ctx, filepath.Join(canonicalPrivateTempDir(t), "state.db"), identity)
	t.Cleanup(func() {
		if err := state.close(); err != nil {
			t.Errorf("close scheduler state: %v", err)
		}
	})
	kernel := mustNewSchedulerKernel(t, identity)
	return state, kernel
}

func mustOpenSchedulerState(
	t *testing.T,
	ctx context.Context,
	path string,
	identity daemonIdentity,
) *schedulerState {
	t.Helper()

	state, err := openSchedulerState(ctx, path, identity)
	if err != nil {
		t.Fatalf("open scheduler state: %v", err)
	}
	return state
}

func mustCloseSchedulerState(t *testing.T, state *schedulerState) {
	t.Helper()

	if err := state.close(); err != nil {
		t.Fatalf("close scheduler state: %v", err)
	}
}

func cleanupSchedulerState(t *testing.T, state *schedulerState) {
	t.Helper()

	t.Cleanup(func() {
		if err := state.close(); err != nil {
			t.Errorf("close scheduler state: %v", err)
		}
	})
}

func assertOutboxSequences(t *testing.T, events []outboxEvent, want ...uint64) {
	t.Helper()

	if len(events) != len(want) {
		t.Fatalf("outbox event count=%d want=%d", len(events), len(want))
	}
	for i := range want {
		if events[i].sequence != want[i] {
			t.Fatalf("outbox sequence[%d]=%d want=%d", i, events[i].sequence, want[i])
		}
	}
}

func mustNewSchedulerKernel(t *testing.T, identity daemonIdentity) *schedulerKernel {
	t.Helper()

	kernel, err := newSchedulerKernel(identity)
	if err != nil {
		t.Fatalf("new scheduler kernel: %v", err)
	}
	return kernel
}

func mustDaemonIdentity(t *testing.T, daemonID string) daemonIdentity {
	t.Helper()

	identity, err := newDaemonIdentity("unix:///var/run/docker.sock", "", daemonID, os.Geteuid())
	if err != nil {
		t.Fatalf("new daemon identity: %v", err)
	}
	return identity
}

func canonicalPrivateTempDir(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize private temp directory: %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("set private temp directory mode: %v", err)
	}
	return dir
}

func assertPersistedWorkloadAndOutbox(
	t *testing.T,
	state *schedulerState,
	id workloadID,
	wantState workloadState,
	wantEvents int,
) {
	t.Helper()

	var gotState workloadState
	if err := state.db.QueryRow("SELECT status FROM scheduler_workloads WHERE id = ?", id).Scan(&gotState); err != nil {
		t.Fatalf("read workload state: %v", err)
	}
	if gotState != wantState {
		t.Fatalf("workload state=%s want=%s", gotState, wantState)
	}
	var gotEvents int
	if err := state.db.QueryRow("SELECT COUNT(*) FROM scheduler_outbox").Scan(&gotEvents); err != nil {
		t.Fatalf("count outbox events: %v", err)
	}
	if gotEvents != wantEvents {
		t.Fatalf("outbox events=%d want=%d", gotEvents, wantEvents)
	}
}

func assertPathsAbsent(t *testing.T, paths []string) {
	t.Helper()

	for _, path := range paths {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("runtime residue %q exists or cannot be checked: %v", path, err)
		}
	}
}
