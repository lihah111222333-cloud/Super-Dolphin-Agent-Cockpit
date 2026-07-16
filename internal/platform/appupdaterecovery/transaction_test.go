package appupdaterecovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

var errSimulatedCrash = errors.New("simulated crash")

func TestUpdateTransactionRetainsBackupUntilHealthy(t *testing.T) {
	store, identity, paths := createProbationTransaction(t)
	assertPathExists(t, paths.Backup)
	if got := loadTransaction(t, store, identity).Trust.State; got != TrustPending {
		t.Fatalf("trust state = %q, want %q", got, TrustPending)
	}
	transaction, err := store.CommitHealthy(context.Background(), identity)
	if err != nil {
		t.Fatalf("CommitHealthy() error = %v", err)
	}
	if transaction.State != StateCommitted || transaction.Trust.State != TrustCommitted {
		t.Fatalf("committed transaction = %#v", transaction)
	}
	assertPathMissing(t, paths.Backup)
}

func TestTrustGenerationCommitsOnlyAfterHealthy(t *testing.T) {
	store, identity, _ := createProbationTransaction(t)
	if _, err := store.advance(context.Background(), identity, TriggerCommitCompleted); err == nil {
		t.Fatal("advance(commit_completed) error = nil, want illegal transition")
	}
	if got := loadTransaction(t, store, identity).Trust.State; got != TrustPending {
		t.Fatalf("trust state after illegal commit = %q, want %q", got, TrustPending)
	}
	transaction, err := store.CommitHealthy(context.Background(), identity)
	if err != nil {
		t.Fatalf("CommitHealthy() error = %v", err)
	}
	if transaction.Trust.State != TrustCommitted {
		t.Fatalf("trust state after healthy = %q, want %q", transaction.Trust.State, TrustCommitted)
	}
}

func TestCrashReplayCompletesPersistedIntent(t *testing.T) {
	store, identity, paths := createPreparedTransaction(t)
	store.afterEffect = func(state State) error {
		if state == StateBackupPending {
			return errSimulatedCrash
		}
		return nil
	}
	if _, err := store.RetainBackup(context.Background(), identity); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("RetainBackup() error = %v, want simulated crash", err)
	}
	reopened, err := NewStore(store.root)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	transaction, err := reopened.Replay(context.Background(), identity)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if transaction.State != StateBackupRetained {
		t.Fatalf("replayed state = %q, want %q", transaction.State, StateBackupRetained)
	}
	assertPathMissing(t, paths.Target)
	assertPathExists(t, paths.Backup)
}

func TestCrashReplayCompletesInstallIntent(t *testing.T) {
	store, identity, paths := createPreparedTransaction(t)
	if _, err := store.RetainBackup(context.Background(), identity); err != nil {
		t.Fatalf("RetainBackup() error = %v", err)
	}
	crashAfterEffect(store, StateInstallPending)
	if _, err := store.InstallCandidate(context.Background(), identity); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("InstallCandidate() error = %v, want simulated crash", err)
	}
	transaction := replayTransaction(t, store, identity)
	if transaction.State != StateProbation || transaction.Trust.State != TrustPending {
		t.Fatalf("replayed install transaction = %#v", transaction)
	}
	assertPathExists(t, paths.Target)
	assertPathExists(t, paths.Backup)
	assertPathMissing(t, paths.Staging)
}

func TestCrashReplayCompletesCommitIntent(t *testing.T) {
	store, identity, paths := createProbationTransaction(t)
	crashAfterEffect(store, StateCommitPending)
	if _, err := store.CommitHealthy(context.Background(), identity); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("CommitHealthy() error = %v, want simulated crash", err)
	}
	transaction := replayTransaction(t, store, identity)
	if transaction.State != StateCommitted || transaction.Trust.State != TrustCommitted {
		t.Fatalf("replayed commit transaction = %#v", transaction)
	}
	assertPathExists(t, paths.Target)
	assertPathMissing(t, paths.Backup)
}

func TestCrashReplayCompletesRollbackIntent(t *testing.T) {
	store, identity, paths := createProbationTransaction(t)
	crashAfterEffect(store, StateRollbackPending)
	if _, err := store.Rollback(context.Background(), identity); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("Rollback() error = %v, want simulated crash", err)
	}
	transaction := replayTransaction(t, store, identity)
	if transaction.State != StateRolledBack || transaction.Trust.State != TrustRolledBack {
		t.Fatalf("replayed rollback transaction = %#v", transaction)
	}
	assertPathExists(t, paths.Target)
	assertPathMissing(t, paths.Backup)
	assertPathMissing(t, paths.Staging)
}

func TestInstallFailureAfterBackupRollsBackExactTransaction(t *testing.T) {
	store, identity, paths := createPreparedTransaction(t)
	if _, err := store.RetainBackup(context.Background(), identity); err != nil {
		t.Fatalf("RetainBackup() error = %v", err)
	}
	writeFixture(t, paths.Staging, "corrupt candidate")
	if _, err := store.InstallCandidate(context.Background(), identity); err == nil {
		t.Fatal("InstallCandidate() error = nil, want candidate identity mismatch")
	}
	if transaction := loadTransaction(t, store, identity); transaction.State != StateInstallPending {
		t.Fatalf("state after failed install = %q, want %q", transaction.State, StateInstallPending)
	}
	transaction, err := store.Rollback(context.Background(), identity)
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if transaction.State != StateRolledBack || transaction.Trust.State != TrustRolledBack {
		t.Fatalf("rollback state=%q trust=%q", transaction.State, transaction.Trust.State)
	}
	contents, err := os.ReadFile(paths.Target)
	if err != nil {
		t.Fatalf("read restored target: %v", err)
	}
	if string(contents) != "old release" {
		t.Fatalf("restored target = %q, want old release", contents)
	}
	assertPathMissing(t, paths.Backup)
	assertPathMissing(t, paths.Staging)
}

func TestTransactionStateMatrixRejectsIllegalTransitions(t *testing.T) {
	allowed := map[State]map[Trigger]State{
		StatePrepared:        {TriggerRetainBackup: StateBackupPending},
		StateBackupPending:   {TriggerBackupRetained: StateBackupRetained},
		StateBackupRetained:  {TriggerInstallCandidate: StateInstallPending, TriggerRollbackRequested: StateRollbackPending},
		StateInstallPending:  {TriggerCandidateInstalled: StateProbation, TriggerRollbackRequested: StateRollbackPending},
		StateProbation:       {TriggerHealthy: StateCommitPending, TriggerRollbackRequested: StateRollbackPending},
		StateCommitPending:   {TriggerCommitCompleted: StateCommitted},
		StateRollbackPending: {TriggerRollbackCompleted: StateRolledBack},
		StateCommitted:       {},
		StateRolledBack:      {},
	}
	for _, from := range allStates() {
		for _, trigger := range allTriggers() {
			canFire, err := newStateMachine(from).CanFire(trigger)
			if err != nil {
				t.Fatalf("CanFire(from=%s, trigger=%s): %v", from, trigger, err)
			}
			_, want := allowed[from][trigger]
			if canFire != want {
				t.Fatalf("from=%s trigger=%s canFire=%v want=%v", from, trigger, canFire, want)
			}
		}
	}
}

func TestWrongTransactionCannotMutateJournalOrFilesystem(t *testing.T) {
	store, identity, paths := createPreparedTransaction(t)
	before, err := os.ReadFile(store.journalPath(identity.TransactionID))
	if err != nil {
		t.Fatalf("read journal before wrong transaction: %v", err)
	}
	wrong := identity
	wrong.CandidateRelease.SHA256 = digestText("wrong candidate")
	if _, err := store.RetainBackup(context.Background(), wrong); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("RetainBackup(wrong identity) error = %v, want ErrIdentityMismatch", err)
	}
	after, err := os.ReadFile(store.journalPath(identity.TransactionID))
	if err != nil {
		t.Fatalf("read journal after wrong transaction: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("wrong transaction changed durable journal")
	}
	assertPathExists(t, paths.Target)
	assertPathMissing(t, paths.Backup)
}

func createProbationTransaction(t *testing.T) (*Store, Identity, Paths) {
	t.Helper()
	store, identity, paths := createPreparedTransaction(t)
	if _, err := store.RetainBackup(context.Background(), identity); err != nil {
		t.Fatalf("RetainBackup() error = %v", err)
	}
	if _, err := store.InstallCandidate(context.Background(), identity); err != nil {
		t.Fatalf("InstallCandidate() error = %v", err)
	}
	return store, identity, paths
}

func createPreparedTransaction(t *testing.T) (*Store, Identity, Paths) {
	t.Helper()
	parent := t.TempDir()
	store, err := NewStore(filepath.Join(parent, ".update-transactions"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	id, err := NewTransactionID()
	if err != nil {
		t.Fatalf("NewTransactionID() error = %v", err)
	}
	paths, err := PathsFor(filepath.Join(parent, "Super Dolphin.app"), id)
	if err != nil {
		t.Fatalf("PathsFor() error = %v", err)
	}
	writeFixture(t, paths.Target, "old release")
	writeFixture(t, paths.Staging, "candidate release")
	identity := Identity{
		TransactionID: id,
		AttemptID:     "attempt-1",
		OldRelease:    ReleaseIdentity{SHA256: digestText("old release"), SignerIdentity: "TEAM-OLD"},
		CandidateRelease: ReleaseIdentity{
			SHA256: digestText("candidate release"), SignerIdentity: "TEAM-NEW",
		},
	}
	_, err = store.Create(context.Background(), CreateRequest{
		Identity: identity,
		Paths:    paths,
		Trust: TrustGeneration{
			Generation: "trust-generation-2", PackageSigner: "TEAM-NEW", State: TrustPending,
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return store, identity, paths
}

func loadTransaction(t *testing.T, store *Store, identity Identity) Transaction {
	t.Helper()
	transaction, err := store.Load(context.Background(), identity)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return transaction
}

func crashAfterEffect(store *Store, expected State) {
	store.afterEffect = func(state State) error {
		if state == expected {
			return errSimulatedCrash
		}
		return nil
	}
}

func replayTransaction(t *testing.T, store *Store, identity Identity) Transaction {
	t.Helper()
	reopened, err := NewStore(store.root)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	transaction, err := reopened.Replay(context.Background(), identity)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	return transaction
}

func writeFixture(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("path %s should exist: %v", path, err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path %s should be missing, stat error = %v", path, err)
	}
}
