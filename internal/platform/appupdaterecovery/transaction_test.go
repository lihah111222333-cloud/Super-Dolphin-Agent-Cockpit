package appupdaterecovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var errSimulatedCrash = errors.New("simulated crash")

func TestUpdateTransactionRetainsBackupUntilHealthy(t *testing.T) {
	store, identity, paths := createProbationTransaction(t)
	assertPathExists(t, paths.Backup)
	if got := loadTransaction(t, store, identity).Trust.State; got != TrustPending {
		t.Fatalf("trust state = %q, want %q", got, TrustPending)
	}
	lease := ackProbationTransaction(t, store, identity)
	transaction, err := store.commitHealthyClaimed(context.Background(), identity, lease)
	if err != nil {
		t.Fatalf("commitHealthyClaimed() error = %v", err)
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
	lease := ackProbationTransaction(t, store, identity)
	transaction, err := store.commitHealthyClaimed(context.Background(), identity, lease)
	if err != nil {
		t.Fatalf("commitHealthyClaimed() error = %v", err)
	}
	if transaction.Trust.State != TrustCommitted {
		t.Fatalf("trust state after healthy = %q, want %q", transaction.Trust.State, TrustCommitted)
	}
}

func TestTerminalCommitCleansRecoveryCapsule(t *testing.T) {
	store, identity, paths := createProbationTransaction(t)
	if err := os.MkdirAll(paths.RecoveryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(paths.RecoveryDir, "super-dolphin-guard"), "old Guard")
	lease := ackProbationTransaction(t, store, identity)
	if _, err := store.commitHealthyClaimed(context.Background(), identity, lease); err != nil {
		t.Fatal(err)
	}
	assertPathMissing(t, paths.RecoveryDir)
}

func TestCapsuleBeforeJournalCrashConvergesWithoutUnknownDirectory(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "Super Dolphin.app")
	id := TransactionID("11223344556677889900aabbccddeeff")
	paths, err := PathsFor(target, id)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(TransactionRootForTarget(target))
	if err != nil {
		t.Fatal(err)
	}
	pending := paths.RecoveryDir + ".pending"
	if err := os.MkdirAll(pending, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(pending, "super-dolphin-guard"), "partial Guard")
	writeFixture(t, paths.Staging, "orphan candidate")
	if _, found, err := store.SelectActive(context.Background()); err != nil || found {
		t.Fatalf("SelectActive() = (_, %t, %v), want clean empty scanner", found, err)
	}
	assertPathMissing(t, filepath.Join(store.root, string(id)))
	assertPathMissing(t, paths.Staging)
}

func TestJournalBeforeCapsuleCrashRemainsRecoverable(t *testing.T) {
	store, identity, paths := createPreparedTransaction(t)
	assertPathMissing(t, paths.RecoveryDir)
	selected, found, err := store.SelectForTarget(context.Background(), paths.Target)
	if err != nil || !found || selected.Identity != identity {
		t.Fatalf("SelectForTarget() = (%+v, %t, %v), want prepared exact transaction", selected.Identity, found, err)
	}
	rolledBack, err := store.Rollback(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.State != StateRolledBack {
		t.Fatalf("Rollback() state = %q, want %q", rolledBack.State, StateRolledBack)
	}
	assertPathExists(t, paths.Target)
	assertPathMissing(t, paths.Staging)
}

func TestScannerRejectsUnknownIncompleteTransactionContent(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "Super Dolphin.app")
	id := TransactionID("ffeeddccbbaa00998877665544332211")
	store, err := NewStore(TransactionRootForTarget(target))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(store.root, string(id)), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(store.root, string(id), "unknown"), "unowned")
	if _, _, err := store.SelectActive(context.Background()); err == nil || !strings.Contains(err.Error(), "unexpected incomplete transaction entry") {
		t.Fatalf("SelectActive() error = %v, want unknown-content rejection", err)
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
	lease := ackProbationTransaction(t, store, identity)
	crashAfterEffect(store, StateCommitPending)
	if _, err := store.commitHealthyClaimed(context.Background(), identity, lease); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("commitHealthyClaimed() error = %v, want simulated crash", err)
	}
	pending := loadTransaction(t, store, identity)
	if pending.State != StateCommitPending || pending.Trust.State != TrustPending {
		t.Fatalf("commit crash transaction = %#v", pending)
	}
	assertPathMissing(t, paths.Backup)
	assertPathExists(t, expectedBackupDiscardPath(paths))
	transaction := replayTransaction(t, store, identity)
	if transaction.State != StateCommitted || transaction.Trust.State != TrustCommitted {
		t.Fatalf("replayed commit transaction = %#v", transaction)
	}
	assertPathExists(t, paths.Target)
	assertPathMissing(t, paths.Backup)
	assertPathMissing(t, expectedBackupDiscardPath(paths))
}

func TestCommitReplayCompletesAfterDiscardIdentityPersistedBeforeRename(t *testing.T) {
	store, identity, paths := createProbationTransaction(t)
	ackProbationTransaction(t, store, identity)
	beginCommitPending(t, store, identity)
	discard := expectedBackupDiscardPath(paths)
	root, err := captureDiscardRootIdentity(paths.Backup)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := store.loadExactLocked(identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.persistDiscardIdentityLocked(journal, root); err != nil {
		t.Fatal(err)
	}
	assertPathExists(t, paths.Backup)
	assertPathMissing(t, discard)
	transaction := replayTransaction(t, store, identity)
	if transaction.State != StateCommitted || transaction.Trust.State != TrustCommitted {
		t.Fatalf("replayed commit transaction = %#v", transaction)
	}
	assertPathExists(t, paths.Target)
	assertPathMissing(t, paths.Backup)
	assertPathMissing(t, discard)
	assertPathExists(t, store.discardIdentityPath(identity.TransactionID))
}

func TestCommitPendingReplayRejectsMatchingReplacementWithoutDiscardIdentity(t *testing.T) {
	requireDesktopDiscardIdentitySemantics(t)
	store, identity, paths := createDirectoryProbationTransaction(t)
	ackProbationTransaction(t, store, identity)
	beginCommitPending(t, store, identity)
	discard := replaceRenamedDiscardWithMatchingRoot(t, identity, paths)
	assertPathMissing(t, store.discardIdentityPath(identity.TransactionID))

	reopened, err := NewStore(store.root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reopened.Replay(t.Context(), identity)
	if err == nil {
		t.Fatal("Replay() error = nil, want missing persisted discard identity failure")
	}
	if !strings.Contains(err.Error(), "missing its persisted root identity") {
		t.Fatalf("Replay() error = %v, want missing persisted root identity", err)
	}
	assertPathExists(t, filepath.Join(discard, "Contents", "old-a"))
	assertPathExists(t, filepath.Join(discard, "Contents", "old-b"))
}

func replaceRenamedDiscardWithMatchingRoot(t *testing.T, identity Identity, paths Paths) string {
	t.Helper()
	discard := expectedBackupDiscardPath(paths)
	original, err := captureDiscardRootIdentity(paths.Backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(paths.Backup, discard); err != nil {
		t.Fatal(err)
	}
	if err := syncDirectory(filepath.Dir(paths.Target)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(discard); err != nil {
		t.Fatal(err)
	}
	writeDirectoryFixture(t, discard, "old-a", "old a")
	if err := os.WriteFile(filepath.Join(discard, "Contents", "old-b"), []byte("old b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyRelease(t.Context(), discard, identity.OldRelease); err != nil {
		t.Fatalf("replacement must match old release digest: %v", err)
	}
	replacement, err := captureDiscardRootIdentity(discard)
	if err != nil {
		t.Fatal(err)
	}
	if replacement == original {
		t.Fatalf("replacement unexpectedly reused original root identity: %+v", replacement)
	}
	return discard
}

func TestCommittedReplayRemovesPartiallyDeletedDiscardTree(t *testing.T) {
	store, identity, paths := createDirectoryProbationTransaction(t)
	lease := ackProbationTransaction(t, store, identity)
	crashAfterEffect(store, StateCommitted)
	if _, err := store.commitHealthyClaimed(t.Context(), identity, lease); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("commitHealthyClaimed() error = %v, want committed crash", err)
	}
	assertCommittedDiscardCrash(t, store, identity)
	discard := expectedBackupDiscardPath(paths)
	if err := os.Remove(filepath.Join(discard, "Contents", "old-a")); err != nil {
		t.Fatal(err)
	}
	transaction := replayTransaction(t, store, identity)
	if transaction.State != StateCommitted || transaction.Trust.State != TrustCommitted {
		t.Fatalf("replayed terminal commit transaction = %#v", transaction)
	}
	if err := verifyRelease(t.Context(), paths.Target, identity.CandidateRelease); err != nil {
		t.Fatalf("candidate release lost trust after discard replay: %v", err)
	}
	assertPathMissing(t, discard)
}

func TestCommittedReplayRejectsReplacedDiscardRoot(t *testing.T) {
	requireDesktopDiscardIdentitySemantics(t)
	store, identity, paths := createDirectoryProbationTransaction(t)
	lease := ackProbationTransaction(t, store, identity)
	crashAfterEffect(store, StateCommitted)
	if _, err := store.commitHealthyClaimed(t.Context(), identity, lease); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("commitHealthyClaimed() error = %v, want committed crash", err)
	}
	assertCommittedDiscardCrash(t, store, identity)
	discard := expectedBackupDiscardPath(paths)
	if err := os.RemoveAll(discard); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(discard, "Contents", "replacement-marker")
	writeDirectoryFixture(t, discard, "replacement-marker", "unrelated")
	reopened, err := NewStore(store.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Replay(t.Context(), identity); err == nil {
		t.Fatal("Replay() error = nil, want replaced discard root rejection")
	} else if !strings.Contains(err.Error(), "backup discard root identity changed") {
		t.Fatalf("Replay() error = %v, want root identity mismatch", err)
	}
	assertPathExists(t, replacement)
}

func requireDesktopDiscardIdentitySemantics(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "linux" {
		t.Skip("discard root identity is enforced by the supported desktop filesystem implementations")
	}
}

func assertCommittedDiscardCrash(t *testing.T, store *Store, identity Identity) {
	t.Helper()
	transaction := loadTransaction(t, store, identity)
	if transaction.State != StateCommitted || transaction.Trust.State != TrustCommitted {
		t.Fatalf("committed cleanup crash transaction = %#v", transaction)
	}
	assertPathExists(t, store.discardIdentityPath(identity.TransactionID))
}

func TestCommittedReplaySupportsLegacyJournalWithoutDiscardIdentity(t *testing.T) {
	store, identity, paths := createProbationTransaction(t)
	lease := ackProbationTransaction(t, store, identity)
	transaction, err := store.commitHealthyClaimed(t.Context(), identity, lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.discardIdentityPath(identity.TransactionID)); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(store.root)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := reopened.Replay(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.State != StateCommitted || transaction.State != StateCommitted {
		t.Fatalf("legacy committed replay = %#v, initial = %#v", replayed, transaction)
	}
	assertPathMissing(t, paths.Backup)
	assertPathMissing(t, expectedBackupDiscardPath(paths))
}

func TestCommitRejectsDiscardCollision(t *testing.T) {
	store, identity, paths := createProbationTransaction(t)
	lease := ackProbationTransaction(t, store, identity)
	discard := expectedBackupDiscardPath(paths)
	writeFixture(t, discard, "replacement")
	assertCommitRejectsDiscard(t, store, identity, paths, lease)
}

func TestCommitRejectsDiscardSymlink(t *testing.T) {
	store, identity, paths := createProbationTransaction(t)
	ackProbationTransaction(t, store, identity)
	beginCommitPending(t, store, identity)
	discard := expectedBackupDiscardPath(paths)
	external := filepath.Join(t.TempDir(), "external-backup")
	if err := os.Rename(paths.Backup, external); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, discard); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(store.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Replay(t.Context(), identity); err == nil {
		t.Fatal("Replay() error = nil, want discard symlink rejection")
	}
	pending := loadTransaction(t, reopened, identity)
	if pending.State != StateCommitPending || pending.Trust.State != TrustPending {
		t.Fatalf("discard symlink activated candidate trust: %#v", pending)
	}
	assertPathMissing(t, paths.Backup)
	assertPathExists(t, discard)
	assertPathExists(t, external)
}

func assertCommitRejectsDiscard(t *testing.T, store *Store, identity Identity, paths Paths, lease ProbationLease) {
	t.Helper()
	if _, err := store.commitHealthyClaimed(t.Context(), identity, lease); err == nil {
		t.Fatal("commitHealthyClaimed() error = nil, want discard collision rejection")
	}
	pending := loadTransaction(t, store, identity)
	if pending.State != StateCommitPending || pending.Trust.State != TrustPending {
		t.Fatalf("failed commit transaction = %#v", pending)
	}
	assertPathExists(t, paths.Backup)
	assertPathExists(t, expectedBackupDiscardPath(paths))
}

func TestCommitPendingReplayRejectsPartialDiscardAsTrustedBackup(t *testing.T) {
	store, identity, paths := createDirectoryProbationTransaction(t)
	ackProbationTransaction(t, store, identity)
	beginCommitPending(t, store, identity)
	discard := expectedBackupDiscardPath(paths)
	if err := os.Rename(paths.Backup, discard); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(discard, "Contents", "old-a")); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(store.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Replay(t.Context(), identity); err == nil {
		t.Fatal("Replay() error = nil, want partial discard rejection")
	}
	pending := loadTransaction(t, reopened, identity)
	if pending.State != StateCommitPending || pending.Trust.State != TrustPending {
		t.Fatalf("partial discard activated candidate trust: %#v", pending)
	}
	assertPathExists(t, paths.Target)
	assertPathMissing(t, paths.Backup)
	assertPathExists(t, discard)
}

func TestCrashReplayCompletesRollbackIntent(t *testing.T) {
	store, identity, paths := createProbationTransaction(t)
	crashAfterEffect(store, StateRollbackPending)
	if _, err := store.RollbackUnclaimedProbation(context.Background(), identity); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("RollbackUnclaimedProbation() error = %v, want simulated crash", err)
	}
	pending := loadTransaction(t, store, identity)
	if pending.State != StateRollbackPending || !pending.RollbackRestart.IntentPresent || pending.RollbackRestart.ACKPresent {
		t.Fatalf("rollback crash record = state %q restart %+v", pending.State, pending.RollbackRestart)
	}
	if pending.RollbackRestart.LaunchToken == "" {
		t.Fatal("rollback crash lost durable launch token")
	}
	transaction := replayTransaction(t, store, identity)
	if transaction.State != StateRolledBack || transaction.Trust.State != TrustRolledBack {
		t.Fatalf("replayed rollback transaction = %#v", transaction)
	}
	assertPathExists(t, paths.Target)
	assertPathMissing(t, paths.Backup)
	assertPathMissing(t, paths.Staging)
}

func TestPreparedRollbackTerminatesTransactionWithoutReplacingOldTarget(t *testing.T) {
	store, identity, paths := createPreparedTransaction(t)
	before, err := os.ReadFile(paths.Target)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := store.Rollback(context.Background(), identity)
	if err != nil {
		t.Fatalf("Rollback(prepared) error = %v", err)
	}
	if transaction.State != StateRolledBack || transaction.Trust.State != TrustRolledBack {
		t.Fatalf("prepared rollback transaction = %#v", transaction)
	}
	after, err := os.ReadFile(paths.Target)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("prepared rollback changed old target: before=%q after=%q", before, after)
	}
	assertPathMissing(t, paths.Backup)
	assertPathMissing(t, paths.Staging)
}

func TestPreparedRollbackCrashReplaysPersistedTerminationIntent(t *testing.T) {
	store, identity, paths := createPreparedTransaction(t)
	crashAfterEffect(store, StateRollbackPending)
	if _, err := store.Rollback(context.Background(), identity); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("Rollback(prepared crash) error = %v", err)
	}
	if got := loadTransaction(t, store, identity).State; got != StateRollbackPending {
		t.Fatalf("prepared crash state = %q, want rollback_pending", got)
	}
	assertPathExists(t, paths.Target)
	assertPathMissing(t, paths.Staging)
	transaction := replayTransaction(t, store, identity)
	if transaction.State != StateRolledBack {
		t.Fatalf("replayed prepared rollback state = %q", transaction.State)
	}
}

func TestPreparedRollbackValidationFailureHasZeroSideEffects(t *testing.T) {
	store, identity, paths := createPreparedTransaction(t)
	beforeJournal, err := os.ReadFile(store.journalPath(identity.TransactionID))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Target, []byte("corrupt old release"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Rollback(context.Background(), identity); err == nil {
		t.Fatal("Rollback(corrupt prepared) error = nil")
	}
	afterJournal, err := os.ReadFile(store.journalPath(identity.TransactionID))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterJournal) != string(beforeJournal) {
		t.Fatal("failed prepared rollback changed journal")
	}
	assertPathExists(t, paths.Target)
	assertPathExists(t, paths.Staging)
	assertPathMissing(t, paths.Backup)
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
		StatePrepared:        {TriggerRetainBackup: StateBackupPending, TriggerRollbackRequested: StateRollbackPending},
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

func createDirectoryProbationTransaction(t *testing.T) (*Store, Identity, Paths) {
	t.Helper()
	parent := t.TempDir()
	store, err := NewStore(filepath.Join(parent, ".update-transactions"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewTransactionID()
	if err != nil {
		t.Fatal(err)
	}
	paths, err := PathsFor(filepath.Join(parent, "Super Dolphin.app"), id)
	if err != nil {
		t.Fatal(err)
	}
	writeDirectoryFixture(t, paths.Target, "old-a", "old a")
	writeDirectoryFixture(t, paths.Target, "old-b", "old b")
	writeDirectoryFixture(t, paths.Staging, "new-a", "new a")
	writeDirectoryFixture(t, paths.Staging, "new-b", "new b")
	oldDigest, err := ComputeReleaseDigest(paths.Target)
	if err != nil {
		t.Fatal(err)
	}
	candidateDigest, err := ComputeReleaseDigest(paths.Staging)
	if err != nil {
		t.Fatal(err)
	}
	identity := Identity{
		TransactionID: id, AttemptID: "attempt-directory",
		OldRelease:       ReleaseIdentity{SHA256: oldDigest, SignerIdentity: "TEAM-OLD"},
		CandidateRelease: ReleaseIdentity{SHA256: candidateDigest, SignerIdentity: "TEAM-NEW"},
		OldHelpers:       fixtureHelperIdentity("old"), CandidateHelpers: fixtureHelperIdentity("candidate"),
		UpdaterProcess: fixtureUpdaterProcess(),
	}
	if _, err := store.Create(t.Context(), CreateRequest{
		Identity: identity, Paths: paths,
		Trust: TrustGeneration{PreviousGeneration: "trust-generation-1", Generation: "trust-generation-2", PackageSigner: "TEAM-NEW", State: TrustPending},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetainBackup(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InstallCandidate(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	return store, identity, paths
}

func writeDirectoryFixture(t *testing.T, root, name, contents string) {
	t.Helper()
	full := filepath.Join(root, "Contents", name)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, full, contents)
}

func expectedBackupDiscardPath(paths Paths) string {
	return paths.Backup + ".discard"
}

func beginCommitPending(t *testing.T, store *Store, identity Identity) {
	t.Helper()
	if _, err := store.withExact(t.Context(), identity, func(journal *journalPayload) error {
		return store.beginHealthyCommitLocked(journal)
	}); err != nil {
		t.Fatalf("beginHealthyCommitLocked() error = %v", err)
	}
}

func ackProbationTransaction(t *testing.T, store *Store, identity Identity) ProbationLease {
	t.Helper()
	process := ProcessIdentity{
		PID: 42, StartToken: "test-candidate", ExecutableIdentity: "/test/candidate",
		ExecutableSHA256: identity.CandidateRelease.SHA256,
	}
	lease, err := store.AcquireProbationLease(context.Background(), identity, ProbationLeaseRequest{
		OwnerID: "test-supervisor", Process: process, TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("AcquireProbationLease() error = %v", err)
	}
	transaction := loadTransaction(t, store, identity)
	ack := BuildHealthyACK(transaction, process, time.Now())
	if _, err := store.RecordHealthyACK(context.Background(), identity, lease, ack); err != nil {
		t.Fatalf("RecordHealthyACK() error = %v", err)
	}
	return lease
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
		OldHelpers:       fixtureHelperIdentity("old"),
		CandidateHelpers: fixtureHelperIdentity("candidate"),
		UpdaterProcess:   fixtureUpdaterProcess(),
	}
	_, err = store.Create(context.Background(), CreateRequest{
		Identity: identity,
		Paths:    paths,
		Trust: TrustGeneration{
			PreviousGeneration: "trust-generation-1",
			Generation:         "trust-generation-2", PackageSigner: "TEAM-NEW", State: TrustPending,
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return store, identity, paths
}

func fixtureHelperIdentity(label string) HelperIdentity {
	return HelperIdentity{UpdaterSHA256: digestText(label + " updater"), GuardSHA256: digestText(label + " guard")}
}

func fixtureUpdaterProcess() ProcessIdentity {
	return ProcessIdentity{
		PID: 77, StartToken: "updater-start", ExecutableIdentity: "/test/updater",
		ExecutableSHA256: digestText("updater executable"),
	}
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
