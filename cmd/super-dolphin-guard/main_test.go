package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

type guardFixture struct {
	store       *recovery.Store
	transaction recovery.Transaction
	expiredAt   time.Time
	restarts    int
}

func newGuardFixture(t *testing.T) *guardFixture {
	t.Helper()
	parent := t.TempDir()
	target := filepath.Join(parent, "Super Dolphin.app")
	id := recovery.TransactionID("11111111111111111111111111111111")
	paths, err := recovery.PathsFor(target, id)
	if err != nil {
		t.Fatalf("PathsFor() error = %v", err)
	}
	writeFixtureRelease(t, paths.Target, "old")
	writeFixtureRelease(t, paths.Staging, "candidate")
	oldRelease := fixtureRelease(t, paths.Target, "old-signer")
	candidate := fixtureRelease(t, paths.Staging, "candidate-signer")
	store, err := recovery.NewStore(filepath.Join(parent, ".super-dolphin-update-transactions"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	identity := recovery.Identity{
		TransactionID: id, AttemptID: "attempt-guard",
		OldRelease: oldRelease, CandidateRelease: candidate,
		OldHelpers: fixtureGuardHelpers("old"), CandidateHelpers: fixtureGuardHelpers("candidate"),
		UpdaterProcess: fixtureGuardUpdaterProcess(),
	}
	_, err = store.Create(context.Background(), recovery.CreateRequest{
		Identity: identity, Paths: paths,
		Trust: recovery.TrustGeneration{PreviousGeneration: "generation-old", Generation: "generation-guard", PackageSigner: candidate.SignerIdentity, State: recovery.TrustPending},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err = store.RetainBackup(context.Background(), identity); err != nil {
		t.Fatalf("RetainBackup() error = %v", err)
	}
	transaction, err := store.InstallCandidate(context.Background(), identity)
	if err != nil {
		t.Fatalf("InstallCandidate() error = %v", err)
	}
	process := recovery.ProcessIdentity{
		PID: 101, StartToken: "candidate-token", ExecutableIdentity: "/test/candidate",
		ExecutableSHA256: candidate.SHA256,
	}
	lease, err := store.AcquireProbationLease(context.Background(), identity, recovery.ProbationLeaseRequest{
		OwnerID: "updater-owner", Process: process, TTL: time.Second,
	})
	if err != nil {
		t.Fatalf("AcquireProbationLease() error = %v", err)
	}
	transaction, err = store.Load(context.Background(), identity)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, lease.ExpiresAt)
	if err != nil {
		t.Fatalf("parse lease expiry: %v", err)
	}
	return &guardFixture{store: store, transaction: transaction, expiredAt: expiresAt.Add(time.Nanosecond)}
}

func fixtureGuardHelpers(label string) recovery.HelperIdentity {
	return recovery.HelperIdentity{UpdaterSHA256: fixtureDigest(label + " updater"), GuardSHA256: fixtureDigest(label + " guard")}
}

func fixtureGuardUpdaterProcess() recovery.ProcessIdentity {
	return recovery.ProcessIdentity{PID: 88, StartToken: "guard-updater", ExecutableIdentity: "/test/updater", ExecutableSHA256: fixtureDigest("updater")}
}

func fixtureDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func writeFixtureRelease(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func fixtureRelease(t *testing.T, path string, signer string) recovery.ReleaseIdentity {
	t.Helper()
	digest, err := recovery.ComputeReleaseDigest(path)
	if err != nil {
		t.Fatalf("ComputeReleaseDigest(%s) error = %v", path, err)
	}
	return recovery.ReleaseIdentity{SHA256: digest, SignerIdentity: signer}
}

func (fixture *guardFixture) resolve(context.Context, string) (recovery.RollbackRestartControl, bool, error) {
	if fixture.restarts == 0 {
		return recovery.RollbackRestartControl{}, false, nil
	}
	return fixtureRestartControl(fixtureRestartProcess()), true, nil
}

func (fixture *guardFixture) restart(context.Context, string) (recovery.RollbackRestartControl, error) {
	fixture.restarts++
	return fixtureRestartControl(fixtureRestartProcess()), nil
}

func fixtureRestartCleanup() error { return nil }

func fixtureRestartControl(process recovery.RollbackRestartProcess) recovery.RollbackRestartControl {
	return recovery.RollbackRestartControl{
		Process: process, Cleanup: fixtureRestartCleanup,
		Commit: func(context.Context) error { return nil },
	}
}

func fixtureRestartProcess() recovery.RollbackRestartProcess {
	return recovery.RollbackRestartProcess{
		PID: 202, StartToken: "old-release-start", ExecutableIdentity: "/test/old-release",
		ExecutableSHA256: fixtureDigest("old executable"),
	}
}

func TestGuardRollbackRestartUsesFifteenSecondDeadline(t *testing.T) {
	fixture := newGuardFixture(t)
	rolledBack, err := fixture.store.RollbackClaimed(
		t.Context(), fixture.transaction.Identity, fixture.transaction.Probation.Lease,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolverEvidence := errors.New("resolver observed deadline")
	guard := newGuard(guardConfig{
		Store: fixture.store,
		ResolveOldRelease: func(ctx context.Context, _ string) (recovery.RollbackRestartControl, bool, error) {
			deadline, ok := ctx.Deadline()
			remaining := time.Until(deadline)
			if !ok || remaining < 14*time.Second || remaining > 15*time.Second {
				t.Fatalf("rollback restart deadline ok=%t remaining=%s", ok, remaining)
			}
			return recovery.RollbackRestartControl{}, false, resolverEvidence
		},
		RestartOldRelease: func(context.Context, string) (recovery.RollbackRestartControl, error) {
			t.Fatal("launcher called after resolver failure")
			return recovery.RollbackRestartControl{}, nil
		},
	})
	if err := guard.restartRolledBackRelease(context.Background(), rolledBack); !errors.Is(err, resolverEvidence) {
		t.Fatalf("restartRolledBackRelease() error = %v", err)
	}
}

func TestGuardTakesOverStaleProbationOnce(t *testing.T) {
	fixture := newGuardFixture(t)
	first := newGuard(guardConfig{
		Store: fixture.store, Identity: fixture.transaction.Identity,
		OwnerID: "guard-1", Now: func() time.Time { return fixture.expiredAt },
		UpdaterAlive:      func(recovery.ProcessIdentity) (bool, error) { return true, nil },
		StopCandidate:     func(context.Context, recovery.ProcessIdentity) error { return nil },
		ResolveOldRelease: fixture.resolve,
		RestartOldRelease: fixture.restart,
	})
	if err := first.Run(context.Background()); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	second := newGuard(guardConfig{
		Store: fixture.store, Identity: fixture.transaction.Identity,
		OwnerID: "guard-2", Now: func() time.Time { return fixture.expiredAt.Add(time.Minute) },
		UpdaterAlive:      func(recovery.ProcessIdentity) (bool, error) { return true, nil },
		StopCandidate:     func(context.Context, recovery.ProcessIdentity) error { return nil },
		ResolveOldRelease: fixture.resolve,
		RestartOldRelease: fixture.restart,
	})
	if err := second.Run(context.Background()); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if fixture.restarts != 1 {
		t.Fatalf("restart count = %d, want 1", fixture.restarts)
	}
	got, err := fixture.store.Load(context.Background(), fixture.transaction.Identity)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.State != recovery.StateRolledBack {
		t.Fatalf("state = %q, want rolled_back", got.State)
	}
}

func TestGuardReloadsRollbackPendingAfterProbationWait(t *testing.T) {
	fixture := newGuardFixture(t)
	guard := runGuardAcrossProbationWaitTransition(t, fixture, func() {
		forceGuardPendingState(t, fixture.transaction, recovery.TriggerRollbackRequested, recovery.StateRollbackPending, true)
	})
	assertGuardRestartConvergedOnce(t, fixture, guard)
}

func TestGuardReloadsRolledBackWithoutRestartACKAfterProbationWait(t *testing.T) {
	fixture := newGuardFixture(t)
	guard := runGuardAcrossProbationWaitTransition(t, fixture, func() {
		forceGuardPendingState(t, fixture.transaction, recovery.TriggerRollbackRequested, recovery.StateRollbackPending, true)
		if _, err := fixture.store.Replay(context.Background(), fixture.transaction.Identity); err != nil {
			t.Fatalf("Replay() error = %v", err)
		}
	})
	assertGuardRestartConvergedOnce(t, fixture, guard)
}

func runGuardAcrossProbationWaitTransition(t *testing.T, fixture *guardFixture, transition func()) *probationGuard {
	t.Helper()
	waitEntered := make(chan struct{})
	journalAdvanced := make(chan struct{})
	nowCalls := 0
	guard := newGuard(guardConfig{
		Store: fixture.store, Identity: fixture.transaction.Identity,
		OwnerID: "guard-wait-reload",
		Now: func() time.Time {
			nowCalls++
			if nowCalls == 1 {
				close(waitEntered)
				<-journalAdvanced
			}
			return fixture.expiredAt
		},
		UpdaterAlive:      func(recovery.ProcessIdentity) (bool, error) { return true, nil },
		StopCandidate:     func(context.Context, recovery.ProcessIdentity) error { return nil },
		ResolveOldRelease: fixture.resolve,
		RestartOldRelease: fixture.restart,
	})
	runDone := make(chan error, 1)
	var runGroup sync.WaitGroup
	runGroup.Go(func() {
		runDone <- guard.Run(context.Background())
	})
	<-waitEntered
	transition()
	close(journalAdvanced)
	if err := <-runDone; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	runGroup.Wait()
	return guard
}

func assertGuardRestartConvergedOnce(t *testing.T, fixture *guardFixture, guard *probationGuard) {
	t.Helper()
	transaction, err := fixture.store.Load(context.Background(), fixture.transaction.Identity)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if transaction.State != recovery.StateRolledBack || !transaction.RollbackRestart.ACKPresent {
		t.Fatalf("rollback restart did not converge: state=%q record=%+v", transaction.State, transaction.RollbackRestart)
	}
	if fixture.restarts != 1 {
		t.Fatalf("restart count = %d, want 1", fixture.restarts)
	}
	if err := guard.Run(context.Background()); err != nil {
		t.Fatalf("idempotent Run() error = %v", err)
	}
	if fixture.restarts != 1 {
		t.Fatalf("restart count after replay = %d, want 1", fixture.restarts)
	}
}

func TestGuardReplaysCommitPendingToCommitted(t *testing.T) {
	fixture := newGuardFixture(t)
	transaction, err := fixture.store.Load(context.Background(), fixture.transaction.Identity)
	if err != nil {
		t.Fatal(err)
	}
	ack := recovery.BuildHealthyACK(transaction, transaction.Probation.Lease.Process, time.Now())
	if _, err := fixture.store.RecordHealthyACK(context.Background(), transaction.Identity, transaction.Probation.Lease, ack); err != nil {
		t.Fatal(err)
	}
	forceGuardPendingState(t, transaction, recovery.TriggerHealthy, recovery.StateCommitPending, false)
	guard := newPendingReplayGuard(fixture, func(context.Context, string) (recovery.RollbackRestartControl, error) {
		t.Fatal("committed replay attempted old release restart")
		return recovery.RollbackRestartControl{}, nil
	})
	if err := guard.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGuardTransactionState(t, fixture, recovery.StateCommitted)
}

func TestGuardReplaysRollbackPendingAndConvergesRestart(t *testing.T) {
	fixture := newGuardFixture(t)
	forceGuardPendingState(t, fixture.transaction, recovery.TriggerRollbackRequested, recovery.StateRollbackPending, true)
	guard := newPendingReplayGuard(fixture, fixture.restart)
	if err := guard.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fixture.restarts != 1 {
		t.Fatalf("restart count = %d, want 1", fixture.restarts)
	}
	assertGuardTransactionState(t, fixture, recovery.StateRolledBack)
}

func newPendingReplayGuard(fixture *guardFixture, restart recovery.RollbackRestartLauncher) *probationGuard {
	return newGuard(guardConfig{
		Store: fixture.store, Identity: fixture.transaction.Identity, OwnerID: "pending-replay", Now: time.Now,
		UpdaterAlive:      func(recovery.ProcessIdentity) (bool, error) { return false, nil },
		StopCandidate:     func(context.Context, recovery.ProcessIdentity) error { return nil },
		ResolveOldRelease: fixture.resolve, RestartOldRelease: restart,
	})
}

func forceGuardPendingState(t *testing.T, transaction recovery.Transaction, trigger recovery.Trigger, state recovery.State, restartIntent bool) {
	t.Helper()
	journalPath := filepath.Join(
		recovery.TransactionRootForTarget(transaction.Paths.Target),
		string(transaction.Identity.TransactionID),
		"journal.json",
	)
	raw, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Payload  []byte `json:"payload"`
		Checksum string `json:"checksum"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	entries := payload["entries"].([]any)
	sequence := uint64(entries[len(entries)-1].(map[string]any)["sequence"].(float64)) + 1
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	payload["entries"] = append(entries, map[string]any{
		"sequence": sequence, "trigger": trigger, "state": state, "at": timestamp,
	})
	payload["updated_at"] = timestamp
	if restartIntent {
		record := payload["rollback_restart"].(map[string]any)
		record["intent_present"] = true
		record["launch_token"] = strings.Repeat("a", 64)
		record["intent_at"] = timestamp
	}
	envelope.Payload, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(envelope.Payload)
	envelope.Checksum = hex.EncodeToString(sum[:])
	raw, err = json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGuardStopsAliveExactCandidateBeforeRollback(t *testing.T) {
	fixture := newGuardFixture(t)
	stopCalls := 0
	guard := newGuard(guardConfig{
		Store: fixture.store, Identity: fixture.transaction.Identity,
		OwnerID: "guard-exact-stop", Now: func() time.Time { return fixture.expiredAt },
		UpdaterAlive: func(recovery.ProcessIdentity) (bool, error) { return true, nil },
		StopCandidate: func(_ context.Context, got recovery.ProcessIdentity) error {
			stopCalls++
			if got != fixture.transaction.Probation.Lease.Process {
				t.Fatalf("stop process = %#v, want exact lease process %#v", got, fixture.transaction.Probation.Lease.Process)
			}
			return nil
		},
		ResolveOldRelease: fixture.resolve,
		RestartOldRelease: fixture.restart,
	})
	if err := guard.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stopCalls != 1 || fixture.restarts != 1 {
		t.Fatalf("stop/restart calls = %d/%d, want 1/1", stopCalls, fixture.restarts)
	}
	assertGuardTransactionState(t, fixture, recovery.StateRolledBack)
}

func TestGuardTreatsReusedUpdaterPIDAsDeadAndRollsBack(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "Super Dolphin.app")
	id := recovery.TransactionID("22222222222222222222222222222222")
	paths, err := recovery.PathsFor(target, id)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureRelease(t, paths.Target, "old")
	writeFixtureRelease(t, paths.Staging, "candidate")
	identity := recovery.Identity{
		TransactionID: id, AttemptID: "updater-pid-reuse",
		OldRelease: fixtureRelease(t, paths.Target, "old-signer"), CandidateRelease: fixtureRelease(t, paths.Staging, "candidate-signer"),
		OldHelpers: fixtureGuardHelpers("old"), CandidateHelpers: fixtureGuardHelpers("candidate"),
		UpdaterProcess: recovery.ProcessIdentity{
			PID: os.Getpid(), StartToken: "reused-start-token", ExecutableIdentity: "/reused/updater",
			ExecutableSHA256: fixtureDigest("updater"),
		},
	}
	store, err := recovery.NewStore(filepath.Join(parent, recovery.TransactionRootDirName))
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := store.Create(context.Background(), recovery.CreateRequest{
		Identity: identity, Paths: paths,
		Trust: recovery.TrustGeneration{
			PreviousGeneration: "generation-old", Generation: "generation-new",
			PackageSigner: "candidate-signer", State: recovery.TrustPending,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stopCalls := 0
	restartProcess := fixtureRestartProcess()
	restartLive := false
	guard := newGuard(guardConfig{
		Store: store, Identity: transaction.Identity, OwnerID: "guard-updater-reuse", Now: time.Now,
		UpdaterAlive: updaterAliveForTransaction(transaction),
		StopCandidate: func(context.Context, recovery.ProcessIdentity) error {
			stopCalls++
			return nil
		},
		ResolveOldRelease: func(context.Context, string) (recovery.RollbackRestartControl, bool, error) {
			return fixtureRestartControl(restartProcess), restartLive, nil
		},
		RestartOldRelease: func(context.Context, string) (recovery.RollbackRestartControl, error) {
			restartLive = true
			return fixtureRestartControl(restartProcess), nil
		},
	})
	if err := guard.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stopCalls != 0 {
		t.Fatalf("stop calls against reused PID = %d, want 0", stopCalls)
	}
	got, err := store.Load(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != recovery.StateRolledBack {
		t.Fatalf("state = %q, want %q", got.State, recovery.StateRolledBack)
	}
}

func TestGuardPIDReuseIdentityMismatchLeavesRecoveryActive(t *testing.T) {
	fixture := newGuardFixture(t)
	restarts := 0
	guard := newGuard(guardConfig{
		Store: fixture.store, Identity: fixture.transaction.Identity,
		OwnerID: "guard-pid-reuse", Now: func() time.Time { return fixture.expiredAt },
		UpdaterAlive: func(recovery.ProcessIdentity) (bool, error) { return true, nil },
		StopCandidate: func(context.Context, recovery.ProcessIdentity) error {
			return ErrCandidateProcessIdentityMismatch
		},
		ResolveOldRelease: fixture.resolve,
		RestartOldRelease: func(context.Context, string) (recovery.RollbackRestartControl, error) {
			restarts++
			return fixtureRestartControl(fixtureRestartProcess()), nil
		},
	})
	if err := guard.Run(context.Background()); !errors.Is(err, ErrCandidateProcessIdentityMismatch) {
		t.Fatalf("Run() error = %v, want identity mismatch", err)
	}
	if restarts != 0 {
		t.Fatalf("restart calls = %d, want 0", restarts)
	}
	assertGuardTransactionState(t, fixture, recovery.StateProbation)
}

func TestGuardTerminationFailureHasNoRollbackOrRestart(t *testing.T) {
	fixture := newGuardFixture(t)
	terminationErr := errors.New("candidate refused termination")
	restarts := 0
	guard := newGuard(guardConfig{
		Store: fixture.store, Identity: fixture.transaction.Identity,
		OwnerID: "guard-stop-failure", Now: func() time.Time { return fixture.expiredAt },
		UpdaterAlive: func(recovery.ProcessIdentity) (bool, error) { return true, nil },
		StopCandidate: func(context.Context, recovery.ProcessIdentity) error {
			return terminationErr
		},
		ResolveOldRelease: fixture.resolve,
		RestartOldRelease: func(context.Context, string) (recovery.RollbackRestartControl, error) {
			restarts++
			return fixtureRestartControl(fixtureRestartProcess()), nil
		},
	})
	if err := guard.Run(context.Background()); !errors.Is(err, terminationErr) {
		t.Fatalf("Run() error = %v, want termination failure", err)
	}
	if restarts != 0 {
		t.Fatalf("restart calls = %d, want 0", restarts)
	}
	assertGuardTransactionState(t, fixture, recovery.StateProbation)
}

func TestDetachedGuardsConvergeOneRollbackRestart(t *testing.T) {
	fixture := newGuardFixture(t)
	forceGuardPendingState(t, fixture.transaction, recovery.TriggerRollbackRequested, recovery.StateRollbackPending, true)
	rolledBack, err := fixture.store.Replay(context.Background(), fixture.transaction.Identity)
	if err != nil {
		t.Fatal(err)
	}
	restartProcess := fixtureRestartProcess()
	var launchMu sync.Mutex
	launchCount := 0
	resolve := func(context.Context, string) (recovery.RollbackRestartControl, bool, error) {
		launchMu.Lock()
		defer launchMu.Unlock()
		return fixtureRestartControl(restartProcess), launchCount > 0, nil
	}
	launch := func(context.Context, string) (recovery.RollbackRestartControl, error) {
		launchMu.Lock()
		defer launchMu.Unlock()
		launchCount++
		return fixtureRestartControl(restartProcess), nil
	}
	guards := []*probationGuard{
		newGuard(guardConfig{Store: fixture.store, Identity: rolledBack.Identity, ResolveOldRelease: resolve, RestartOldRelease: launch}),
		newGuard(guardConfig{Store: fixture.store, Identity: rolledBack.Identity, ResolveOldRelease: resolve, RestartOldRelease: launch}),
	}
	start := make(chan struct{})
	results := make(chan error, len(guards))
	var ready sync.WaitGroup
	var runners sync.WaitGroup
	ready.Add(len(guards))
	for _, guard := range guards {
		runners.Go(func() {
			ready.Done()
			<-start
			results <- guard.restartRolledBackRelease(context.Background(), rolledBack)
		})
	}
	ready.Wait()
	close(start)
	for range guards {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	runners.Wait()
	got, err := fixture.store.Load(context.Background(), rolledBack.Identity)
	if err != nil {
		t.Fatal(err)
	}
	launchMu.Lock()
	defer launchMu.Unlock()
	if launchCount != 1 || !got.RollbackRestart.ACKPresent || got.RollbackRestart.ACK.Process != restartProcess ||
		got.RollbackRestart.ACK.LaunchToken != got.RollbackRestart.LaunchToken {
		t.Fatalf("launches=%d restart=%+v", launchCount, got.RollbackRestart)
	}
}

func assertGuardTransactionState(t *testing.T, fixture *guardFixture, want recovery.State) {
	t.Helper()
	got, err := fixture.store.Load(context.Background(), fixture.transaction.Identity)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.State != want {
		t.Fatalf("state = %q, want %q", got.State, want)
	}
}
