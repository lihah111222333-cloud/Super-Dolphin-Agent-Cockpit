package appupdaterecovery

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type leaseRaceResult struct {
	lease ProbationLease
	err   error
}

type rollbackRaceResult struct {
	transaction Transaction
	err         error
}

type probationArtifactSnapshot struct {
	journal string
	target  string
	backup  string
}

func TestProbationFailureRollsBackExactTransaction(t *testing.T) {
	store, identity, _ := createProbationTransaction(t)
	transaction := loadTransaction(t, store, identity)
	process := ProcessIdentity{
		PID: 42, StartToken: "candidate-start", ExecutableIdentity: "/test/candidate",
		ExecutableSHA256: digestText("candidate-executable"),
	}
	lease, err := store.AcquireProbationLease(context.Background(), transaction.Identity, ProbationLeaseRequest{
		OwnerID: "updater", Process: process, TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("AcquireProbationLease() error = %v", err)
	}
	restarts := 0
	supervisor, err := NewProbationSupervisor(ProbationSupervisorConfig{
		Store: store, Identity: transaction.Identity, Lease: lease,
		ProcessAlive:  func(ProcessIdentity) (bool, error) { return false, nil },
		StopCandidate: stopCandidateForTest,
		RestartOldRelease: func(context.Context, Transaction) error {
			restarts++
			return nil
		},
		ObservationPeriod: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewProbationSupervisor() error = %v", err)
	}
	if err := supervisor.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got, err := store.Load(context.Background(), transaction.Identity)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.State != StateRolledBack || restarts != 1 {
		t.Fatalf("rollback result = state %q restarts %d, want rolled_back/1", got.State, restarts)
	}
}

func TestSupervisorCommitsOnlyExactACK(t *testing.T) {
	store, transaction, process, lease := createLeasedProbation(t, time.Minute)
	wrong := BuildHealthyACK(transaction, process, time.Now())
	wrong.AttemptID = "wrong-attempt"
	if _, err := store.RecordHealthyACK(context.Background(), transaction.Identity, lease, wrong); err == nil {
		t.Fatal("RecordHealthyACK(wrong) error = nil")
	}
	if loadTransaction(t, store, transaction.Identity).Probation.ACKPresent {
		t.Fatal("wrong ACK changed journal")
	}
	ack := BuildHealthyACK(transaction, process, time.Now())
	if _, err := store.RecordHealthyACK(context.Background(), transaction.Identity, lease, ack); err != nil {
		t.Fatalf("RecordHealthyACK() error = %v", err)
	}
	supervisor, err := NewProbationSupervisor(ProbationSupervisorConfig{
		Store: store, Identity: transaction.Identity, Lease: lease,
		ProcessAlive:      func(ProcessIdentity) (bool, error) { return true, nil },
		RestartOldRelease: func(context.Context, Transaction) error { return nil },
		StopCandidate:     stopCandidateForTest,
		Now:               time.Now,
		ObservationPeriod: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewProbationSupervisor() error = %v", err)
	}
	if err := supervisor.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := loadTransaction(t, store, transaction.Identity).State; got != StateCommitted {
		t.Fatalf("state = %q, want committed", got)
	}
}

func TestProbationTimeoutRollsBackAndRestarts(t *testing.T) {
	store, transaction, _, lease := createLeasedProbation(t, time.Second)
	expiresAt, err := time.Parse(time.RFC3339Nano, lease.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expiry: %v", err)
	}
	restarts := 0
	supervisor, err := NewProbationSupervisor(ProbationSupervisorConfig{
		Store: store, Identity: transaction.Identity, Lease: lease,
		ProcessAlive:      func(ProcessIdentity) (bool, error) { return true, nil },
		StopCandidate:     stopCandidateForTest,
		RestartOldRelease: func(context.Context, Transaction) error { restarts++; return nil },
		Now:               func() time.Time { return expiresAt },
		ObservationPeriod: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewProbationSupervisor() error = %v", err)
	}
	if err := supervisor.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := loadTransaction(t, store, transaction.Identity).State; got != StateRolledBack || restarts != 1 {
		t.Fatalf("timeout result = state %q restarts %d", got, restarts)
	}
}

func TestSupervisorAndDetachedGuardConvergeOneRollbackRestart(t *testing.T) {
	store, transaction, _, lease := createLeasedProbation(t, time.Second)
	expiresAt, err := time.Parse(time.RFC3339Nano, lease.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	restartProcess := RollbackRestartProcess{
		PID: 303, StartToken: "unique-old-start", ExecutableIdentity: "/test/old-release",
		ExecutableSHA256: digestText("unique-old-release"),
	}
	var launchMu sync.Mutex
	launchCount := 0
	resolve := func(context.Context, string) (RollbackRestartControl, bool, error) {
		launchMu.Lock()
		defer launchMu.Unlock()
		if launchCount == 0 {
			return RollbackRestartControl{}, false, nil
		}
		return fixtureRollbackRestartControl(restartProcess), true, nil
	}
	launch := func(context.Context, string) (RollbackRestartControl, error) {
		launchMu.Lock()
		defer launchMu.Unlock()
		launchCount++
		return fixtureRollbackRestartControl(restartProcess), nil
	}
	supervisorReady := make(chan struct{})
	guardReady := make(chan struct{})
	start := make(chan struct{})
	guardResult := make(chan error, 1)
	supervisor, err := NewProbationSupervisor(ProbationSupervisorConfig{
		Store: store, Identity: transaction.Identity, Lease: lease,
		ProcessAlive: func(ProcessIdentity) (bool, error) { return true, nil }, StopCandidate: stopCandidateForTest,
		RestartOldRelease: func(ctx context.Context, rolledBack Transaction) error {
			close(supervisorReady)
			<-guardReady
			close(start)
			_, convergeErr := store.ConvergeRollbackRestart(ctx, rolledBack.Identity, resolve, launch)
			return convergeErr
		},
		Now: func() time.Time { return expiresAt }, ObservationPeriod: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	var guardWait sync.WaitGroup
	guardWait.Go(func() {
		<-supervisorReady
		close(guardReady)
		<-start
		_, convergeErr := store.ConvergeRollbackRestart(context.Background(), transaction.Identity, resolve, launch)
		guardResult <- convergeErr
	})
	if err := supervisor.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-guardResult; err != nil {
		t.Fatal(err)
	}
	guardWait.Wait()
	got := loadTransaction(t, store, transaction.Identity)
	launchMu.Lock()
	defer launchMu.Unlock()
	if launchCount != 1 || !got.RollbackRestart.ACKPresent || got.RollbackRestart.ACK.Process != restartProcess ||
		got.RollbackRestart.ACK.LaunchToken != got.RollbackRestart.LaunchToken {
		t.Fatalf("launches=%d restart=%+v", launchCount, got.RollbackRestart)
	}
}

func TestSupervisorInterruptionLeavesLeaseForGuard(t *testing.T) {
	store, transaction, _, lease := createLeasedProbation(t, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	supervisor, err := NewProbationSupervisor(ProbationSupervisorConfig{
		Store: store, Identity: transaction.Identity, Lease: lease,
		ProcessAlive:      func(ProcessIdentity) (bool, error) { return true, nil },
		RestartOldRelease: func(context.Context, Transaction) error { return nil },
		StopCandidate:     stopCandidateForTest,
		ObservationPeriod: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewProbationSupervisor() error = %v", err)
	}
	if err := supervisor.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context canceled", err)
	}
	got := loadTransaction(t, store, transaction.Identity)
	if got.State != StateProbation || got.Probation.Lease != lease {
		t.Fatalf("interrupted transaction = %#v", got)
	}
}

func TestProbationSupervisorRejectsZeroObservationPeriod(t *testing.T) {
	store, transaction, _, lease := createLeasedProbation(t, time.Minute)
	config := ProbationSupervisorConfig{
		Store: store, Identity: transaction.Identity, Lease: lease,
		ProcessAlive:      func(ProcessIdentity) (bool, error) { return true, nil },
		StopCandidate:     stopCandidateForTest,
		RestartOldRelease: func(context.Context, Transaction) error { return nil },
	}
	if _, err := NewProbationSupervisor(config); err == nil || !strings.Contains(err.Error(), "must be positive") {
		t.Fatalf("NewProbationSupervisor(zero observation) error = %v, want positive-period failure", err)
	}
	config.ObservationPeriod = time.Second
	supervisor, err := NewProbationSupervisor(config)
	if err != nil {
		t.Fatalf("NewProbationSupervisor(positive observation) error = %v", err)
	}
	if supervisor.config.ObservationPeriod != time.Second || supervisor.config.PollInterval != defaultSupervisorPollInterval {
		t.Fatalf("supervisor timing = observation %s poll %s", supervisor.config.ObservationPeriod, supervisor.config.PollInterval)
	}
}

func TestWrongLeaseHasNoSideEffects(t *testing.T) {
	store, transaction, _, lease := createLeasedProbation(t, time.Second)
	before, err := os.ReadFile(store.journalPath(transaction.Identity.TransactionID))
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	wrong := lease
	wrong.Generation++
	if _, err := store.RollbackClaimed(context.Background(), transaction.Identity, wrong); !errors.Is(err, ErrProbationLeaseMismatch) {
		t.Fatalf("RollbackClaimed(wrong) error = %v", err)
	}
	after, err := os.ReadFile(store.journalPath(transaction.Identity.TransactionID))
	if err != nil {
		t.Fatalf("read journal after wrong lease: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("wrong lease changed journal")
	}
}

func TestRollbackUnclaimedProbationRestoresExactTransaction(t *testing.T) {
	store, identity, paths := createProbationTransaction(t)
	current := loadTransaction(t, store, identity)
	before := readProbationArtifactSnapshot(t, store, current)
	if _, err := store.Rollback(context.Background(), identity); !errors.Is(err, ErrProbationRollbackRequiresUnclaimed) {
		t.Fatalf("Rollback(unclaimed probation) error = %v, want ErrProbationRollbackRequiresUnclaimed", err)
	}
	if after := readProbationArtifactSnapshot(t, store, current); after != before {
		t.Fatal("generic rollback changed unclaimed probation journal or releases")
	}
	transaction, err := store.RollbackUnclaimedProbation(context.Background(), identity)
	if err != nil {
		t.Fatalf("RollbackUnclaimedProbation() error = %v", err)
	}
	if transaction.State != StateRolledBack || transaction.Trust.State != TrustRolledBack {
		t.Fatalf("rollback transaction = %#v", transaction)
	}
	contents, err := os.ReadFile(paths.Target)
	if err != nil || string(contents) != "old release" {
		t.Fatalf("restored target contents=%q error=%v", contents, err)
	}
	assertPathMissing(t, paths.Backup)
	assertPathMissing(t, paths.Staging)
}

func TestGenericRollbackRejectsLeasedProbationWithZeroSideEffects(t *testing.T) {
	store, transaction, _, _ := createLeasedProbation(t, time.Minute)
	before := readProbationArtifactSnapshot(t, store, transaction)
	if _, err := store.Rollback(context.Background(), transaction.Identity); !errors.Is(err, ErrProbationLeaseMismatch) {
		t.Fatalf("Rollback(leased probation) error = %v, want ErrProbationLeaseMismatch", err)
	}
	if after := readProbationArtifactSnapshot(t, store, transaction); after != before {
		t.Fatal("generic rollback changed leased probation journal or releases")
	}
}

func TestRollbackUnclaimedRejectsLeaseAcquiredFirstWithZeroSideEffects(t *testing.T) {
	store, transaction, _, _ := createLeasedProbation(t, time.Minute)
	before := readProbationArtifactSnapshot(t, store, loadTransaction(t, store, transaction.Identity))
	if _, err := store.RollbackUnclaimedProbation(context.Background(), transaction.Identity); !errors.Is(err, ErrProbationLeaseMismatch) {
		t.Fatalf("RollbackUnclaimedProbation(leased) error = %v, want ErrProbationLeaseMismatch", err)
	}
	current := loadTransaction(t, store, transaction.Identity)
	if !current.Probation.LeasePresent || current.State != StateProbation {
		t.Fatalf("leased probation after rejected rollback = %#v", current)
	}
	if after := readProbationArtifactSnapshot(t, store, current); after != before {
		t.Fatal("rejected unclaimed rollback changed leased probation journal or releases")
	}
}

func TestCommitHealthyLegacyAPIRequiresClaimedLeaseWithZeroSideEffects(t *testing.T) {
	store, identity, _ := createProbationTransaction(t)
	ackProbationTransaction(t, store, identity)
	transaction := loadTransaction(t, store, identity)
	before := readProbationArtifactSnapshot(t, store, transaction)
	if _, err := store.CommitHealthy(context.Background(), identity); !errors.Is(err, ErrHealthyCommitRequiresClaimed) {
		t.Fatalf("CommitHealthy() error = %v, want ErrHealthyCommitRequiresClaimed", err)
	}
	if after := readProbationArtifactSnapshot(t, store, transaction); after != before {
		t.Fatal("legacy healthy commit changed claimed probation journal or releases")
	}
}

func readProbationArtifactSnapshot(t *testing.T, store *Store, transaction Transaction) probationArtifactSnapshot {
	t.Helper()
	journal, err := os.ReadFile(store.journalPath(transaction.Identity.TransactionID))
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.ReadFile(transaction.Paths.Target)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(transaction.Paths.Backup)
	if err != nil {
		t.Fatal(err)
	}
	return probationArtifactSnapshot{journal: string(journal), target: string(target), backup: string(backup)}
}

func TestAcquireLeaseAndUnclaimedRollbackHaveSingleWinner(t *testing.T) {
	store, identity, paths := createProbationTransaction(t)
	process := ProcessIdentity{
		PID: 42, StartToken: "candidate-start", ExecutableIdentity: "/test/candidate",
		ExecutableSHA256: identity.CandidateRelease.SHA256,
	}
	lease, rollback := raceLeaseAgainstRollback(store, identity, process)
	assertProbationRaceOutcome(t, store, identity, paths, lease, rollback)
}

func raceLeaseAgainstRollback(store *Store, identity Identity, process ProcessIdentity) (leaseRaceResult, rollbackRaceResult) {
	start := make(chan struct{})
	leaseDone := make(chan leaseRaceResult, 1)
	rollbackDone := make(chan rollbackRaceResult, 1)
	var workers sync.WaitGroup
	workers.Go(func() {
		<-start
		lease, err := store.AcquireProbationLease(context.Background(), identity, ProbationLeaseRequest{
			OwnerID: "racing-updater", Process: process, TTL: time.Minute,
		})
		leaseDone <- leaseRaceResult{lease: lease, err: err}
	})
	workers.Go(func() {
		<-start
		transaction, err := store.RollbackUnclaimedProbation(context.Background(), identity)
		rollbackDone <- rollbackRaceResult{transaction: transaction, err: err}
	})
	close(start)
	lease := <-leaseDone
	rollback := <-rollbackDone
	workers.Wait()
	return lease, rollback
}

func assertProbationRaceOutcome(
	t *testing.T,
	store *Store,
	identity Identity,
	paths Paths,
	lease leaseRaceResult,
	rollback rollbackRaceResult,
) {
	t.Helper()
	switch {
	case lease.err == nil:
		assertLeaseWonProbationRace(t, store, identity, paths, lease, rollback)
	case rollback.err == nil:
		assertRollbackWonProbationRace(t, paths, lease, rollback)
	default:
		t.Fatalf("race has no winner: lease error=%v rollback error=%v", lease.err, rollback.err)
	}
}

func assertLeaseWonProbationRace(
	t *testing.T,
	store *Store,
	identity Identity,
	paths Paths,
	lease leaseRaceResult,
	rollback rollbackRaceResult,
) {
	t.Helper()
	if !isProbationRaceLoser(rollback.err, ErrProbationLeaseMismatch) {
		t.Fatalf("rollback loser error = %v, want lease mismatch or transaction busy", rollback.err)
	}
	transaction := loadTransaction(t, store, identity)
	if transaction.State != StateProbation || !transaction.Probation.LeasePresent || transaction.Probation.Lease != lease.lease {
		t.Fatalf("lease winner transaction = %#v", transaction)
	}
	contents, err := os.ReadFile(paths.Target)
	if err != nil || string(contents) != "candidate release" {
		t.Fatalf("lease winner target contents=%q error=%v", contents, err)
	}
	assertPathExists(t, paths.Backup)
}

func assertRollbackWonProbationRace(t *testing.T, paths Paths, lease leaseRaceResult, rollback rollbackRaceResult) {
	t.Helper()
	if !isProbationRaceLoser(lease.err, ErrNoActiveProbation) {
		t.Fatalf("lease loser error = %v, want no active probation or transaction busy", lease.err)
	}
	if rollback.transaction.State != StateRolledBack {
		t.Fatalf("rollback winner transaction = %#v", rollback.transaction)
	}
	contents, err := os.ReadFile(paths.Target)
	if err != nil || string(contents) != "old release" {
		t.Fatalf("rollback winner target contents=%q error=%v", contents, err)
	}
	assertPathMissing(t, paths.Backup)
}

func isProbationRaceLoser(err error, stateError error) bool {
	return errors.Is(err, stateError) || errors.Is(err, ErrTransactionBusy)
}

func TestSecondTakeoverHasNoSideEffects(t *testing.T) {
	store, transaction, _, lease := createLeasedProbation(t, time.Second)
	expiresAt, err := time.Parse(time.RFC3339Nano, lease.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expiry: %v", err)
	}
	replacement, err := store.TakeOverProbationLease(context.Background(), transaction.Identity, lease, "guard-1", expiresAt, time.Minute)
	if err != nil {
		t.Fatalf("TakeOverProbationLease() error = %v", err)
	}
	afterFirst, err := os.ReadFile(store.journalPath(transaction.Identity.TransactionID))
	if err != nil {
		t.Fatalf("read journal after takeover: %v", err)
	}
	if _, err := store.TakeOverProbationLease(context.Background(), transaction.Identity, lease, "guard-2", expiresAt.Add(time.Minute), time.Minute); !errors.Is(err, ErrProbationLeaseMismatch) {
		t.Fatalf("second takeover error = %v", err)
	}
	afterSecond, err := os.ReadFile(store.journalPath(transaction.Identity.TransactionID))
	if err != nil {
		t.Fatalf("read journal after second takeover: %v", err)
	}
	if string(afterFirst) != string(afterSecond) || replacement.Generation != 2 {
		t.Fatal("second takeover produced unexpected journal side effects")
	}
}

func createLeasedProbation(t *testing.T, ttl time.Duration) (*Store, Transaction, ProcessIdentity, ProbationLease) {
	t.Helper()
	store, identity, _ := createProbationTransaction(t)
	transaction := loadTransaction(t, store, identity)
	process := ProcessIdentity{
		PID: 42, StartToken: "candidate-start", ExecutableIdentity: "/test/candidate",
		ExecutableSHA256: digestText("candidate-executable"),
	}
	lease, err := store.AcquireProbationLease(context.Background(), identity, ProbationLeaseRequest{
		OwnerID: "updater", Process: process, TTL: ttl,
	})
	if err != nil {
		t.Fatalf("AcquireProbationLease() error = %v", err)
	}
	return store, transaction, process, lease
}

func stopCandidateForTest(context.Context, ProcessIdentity) error { return nil }
