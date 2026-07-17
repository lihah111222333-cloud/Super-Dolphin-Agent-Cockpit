package appupdaterecovery

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestRollbackRestartIntentSurvivesRenameAndConvergesOnce(t *testing.T) {
	store, identity, paths := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if transaction.State != StateRolledBack || !transaction.RollbackRestart.IntentPresent || transaction.RollbackRestart.ACKPresent {
		t.Fatalf("rolled back restart record = %+v", transaction.RollbackRestart)
	}
	assertPathExists(t, paths.Target)
	assertPathMissing(t, paths.Backup)

	launches := 0
	process := fixtureRollbackRestartProcess()
	converged, err := store.ConvergeRollbackRestart(
		context.Background(), identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			return RollbackRestartControl{}, false, nil
		},
		rollbackRestartLauncher(t, transaction.RollbackRestart.LaunchToken, process, &launches),
	)
	if err != nil {
		t.Fatal(err)
	}
	if launches != 1 || !converged.RollbackRestart.ACKPresent || converged.RollbackRestart.ACK.Process != process {
		t.Fatalf("converged restart = %+v launches=%d", converged.RollbackRestart, launches)
	}
	assertRollbackRestartAlreadyACKed(t, store, identity, process)
}

func rollbackRestartLauncher(t *testing.T, wantToken string, process RollbackRestartProcess, launches *int) RollbackRestartLauncher {
	t.Helper()
	return func(_ context.Context, token string) (RollbackRestartControl, error) {
		*launches++
		if token != wantToken {
			t.Fatalf("launch token = %q, want durable intent token", token)
		}
		return fixtureRollbackRestartControl(process), nil
	}
}

func assertRollbackRestartAlreadyACKed(t *testing.T, store *Store, identity Identity, process RollbackRestartProcess) {
	t.Helper()
	commits := 0
	if _, err := store.ConvergeRollbackRestart(
		context.Background(), identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			control := fixtureRollbackRestartControl(process)
			control.Commit = func(context.Context) error { commits++; return nil }
			return control, true, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			t.Fatal("launcher called after ACK")
			return RollbackRestartControl{}, nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if commits != 1 {
		t.Fatalf("ACKed rollback COMMIT count = %d, want 1", commits)
	}
}

func TestRollbackRestartRecoversLaunchBeforeACKWindowByToken(t *testing.T) {
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	process := fixtureRollbackRestartProcess()
	resolved := 0
	converged, err := store.ConvergeRollbackRestart(
		context.Background(), identity,
		func(_ context.Context, token string) (RollbackRestartControl, bool, error) {
			resolved++
			if token != transaction.RollbackRestart.LaunchToken {
				t.Fatalf("resolve token = %q, want %q", token, transaction.RollbackRestart.LaunchToken)
			}
			return fixtureRollbackRestartControl(process), true, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			t.Fatal("launcher called although token-bound process was rediscovered")
			return RollbackRestartControl{}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != 1 || !converged.RollbackRestart.ACKPresent || converged.RollbackRestart.ACK.LaunchToken != transaction.RollbackRestart.LaunchToken {
		t.Fatalf("converged restart = %+v resolved=%d", converged.RollbackRestart, resolved)
	}
}

func TestRollbackRestartReplaysCommitAfterDurableACK(t *testing.T) {
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	process := fixtureRollbackRestartProcess()
	commitInterrupted := errors.New("COMMIT response lost")
	launches := 0
	commits := 0
	cleanups := 0
	control := func() RollbackRestartControl {
		return RollbackRestartControl{
			Process: process,
			Cleanup: func() error { cleanups++; return nil },
			Commit: func(context.Context) error {
				commits++
				if commits == 1 {
					return commitInterrupted
				}
				return nil
			},
		}
	}
	resolve := func(context.Context, string) (RollbackRestartControl, bool, error) {
		return control(), launches > 0, nil
	}
	launch := func(context.Context, string) (RollbackRestartControl, error) {
		launches++
		return control(), nil
	}
	if _, err := store.ConvergeRollbackRestart(t.Context(), transaction.Identity, resolve, launch); !errors.Is(err, commitInterrupted) {
		t.Fatalf("first convergence error = %v, want COMMIT interruption", err)
	}
	acked := loadTransaction(t, store, identity)
	if !acked.RollbackRestart.ACKPresent || acked.RollbackRestart.ACK.Process != process {
		t.Fatalf("durable ACK after COMMIT interruption = %+v", acked.RollbackRestart)
	}
	if _, err := store.ConvergeRollbackRestart(t.Context(), transaction.Identity, resolve, launch); err != nil {
		t.Fatalf("replayed convergence: %v", err)
	}
	if launches != 1 || commits != 2 || cleanups != 0 {
		t.Fatalf("launches=%d commits=%d cleanups=%d, want 1/2/0", launches, commits, cleanups)
	}
}

func TestRollbackRestartACKReplayIdentityChangeDoesNotCleanup(t *testing.T) {
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	process := fixtureRollbackRestartProcess()
	if _, err := store.ConvergeRollbackRestart(
		t.Context(), identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			return RollbackRestartControl{}, false, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			return fixtureRollbackRestartControl(process), nil
		},
	); err != nil {
		t.Fatal(err)
	}

	cleanups := 0
	changed := process
	changed.StartToken = "reused-generation"
	_, replayErr := store.ConvergeRollbackRestart(
		t.Context(), transaction.Identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			control := fixtureRollbackRestartControl(changed)
			control.Cleanup = func() error { cleanups++; return nil }
			return control, true, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			t.Fatal("launcher called after durable ACK")
			return RollbackRestartControl{}, nil
		},
	)
	if replayErr == nil || cleanups != 0 {
		t.Fatalf("ACK replay error=%v cleanups=%d, want identity error and no cleanup", replayErr, cleanups)
	}
}

func TestRollbackRestartLargeDigestDeadlineReleasesLockWithoutACK(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	store, identity, paths := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	transaction.Paths.Target, err = CanonicalExistingPath(paths.Target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(transaction.Paths.Target, 1<<30); err != nil {
		t.Fatal(err)
	}
	before := rollbackRestartJournalBytes(t, store, identity)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	resolve, launch := RollbackRestartCallbacks(transaction)
	started := time.Now()
	_, convergeErr := store.ConvergeRollbackRestart(ctx, identity, resolve, launch)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("large digest deadline elapsed %s", elapsed)
	}
	assertRollbackRestartDeadlineResult(t, store, identity, before, convergeErr)
}

func TestRollbackRestartRediscoveredValidationFailureCleansWithoutACK(t *testing.T) {
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	before := rollbackRestartJournalBytes(t, store, identity)
	cleanupEvidence := errors.New("rediscovered cleanup evidence")
	cleanups := 0
	_, convergeErr := store.ConvergeRollbackRestart(
		t.Context(), transaction.Identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			return RollbackRestartControl{Cleanup: func() error {
				cleanups++
				return cleanupEvidence
			}}, true, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			t.Fatal("launcher called after rediscovery")
			return RollbackRestartControl{}, nil
		},
	)
	if cleanups != 1 || !errors.Is(convergeErr, cleanupEvidence) {
		t.Fatalf("rediscovered cleanup count=%d error=%v", cleanups, convergeErr)
	}
	after := rollbackRestartJournalBytes(t, store, identity)
	if !bytes.Equal(before, after) {
		t.Fatal("journal changed after rediscovered process validation failure")
	}
}

func TestRollbackRestartBusyLockHonorsDeadlineWithoutJournalMutation(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.journalPath(identity.TransactionID))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := store.acquire(identity.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.releaseInto(&err)

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	_, convergeErr := store.ConvergeRollbackRestart(
		ctx, transaction.Identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			t.Fatal("resolver called while transaction lock is held")
			return RollbackRestartControl{}, false, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			t.Fatal("launcher called while transaction lock is held")
			return RollbackRestartControl{}, nil
		},
	)
	if !errors.Is(convergeErr, context.DeadlineExceeded) {
		t.Fatalf("ConvergeRollbackRestart() error = %v, want deadline exceeded", convergeErr)
	}
	after, readErr := os.ReadFile(store.journalPath(identity.TransactionID))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("transaction journal changed while convergence lock remained busy")
	}
}

func TestRollbackRestartBlockingResolverHonorsDeadline(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	before := rollbackRestartJournalBytes(t, store, identity)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()

	_, convergeErr := store.ConvergeRollbackRestart(
		ctx, transaction.Identity,
		func(callbackCtx context.Context, _ string) (RollbackRestartControl, bool, error) {
			<-callbackCtx.Done()
			return RollbackRestartControl{}, false, context.Cause(callbackCtx)
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			t.Fatal("launcher called after resolver deadline")
			return RollbackRestartControl{}, nil
		},
	)
	assertRollbackRestartDeadlineResult(t, store, identity, before, convergeErr)
}

func TestRollbackRestartBlockingLauncherHonorsDeadline(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	before := rollbackRestartJournalBytes(t, store, identity)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()

	_, convergeErr := store.ConvergeRollbackRestart(
		ctx, transaction.Identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			return RollbackRestartControl{}, false, nil
		},
		func(callbackCtx context.Context, _ string) (RollbackRestartControl, error) {
			<-callbackCtx.Done()
			return RollbackRestartControl{}, context.Cause(callbackCtx)
		},
	)
	assertRollbackRestartDeadlineResult(t, store, identity, before, convergeErr)
}

func TestRollbackRestartDeadlineAfterLaunchCleansWithoutACK(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	before := rollbackRestartJournalBytes(t, store, identity)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	cleanups := 0

	_, convergeErr := store.ConvergeRollbackRestart(
		ctx, transaction.Identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			return RollbackRestartControl{}, false, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			cancel(context.DeadlineExceeded)
			control := fixtureRollbackRestartControl(fixtureRollbackRestartProcess())
			control.Cleanup = func() error {
				cleanups++
				return nil
			}
			return control, nil
		},
	)
	if cleanups != 1 {
		t.Fatalf("post-launch cleanup count = %d, want 1", cleanups)
	}
	assertRollbackRestartDeadlineResult(t, store, identity, before, convergeErr)
}

func TestRollbackRestartDeadlineAfterResolverCleansWithoutACK(t *testing.T) {
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	before := rollbackRestartJournalBytes(t, store, identity)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	cleanups := 0

	_, convergeErr := store.ConvergeRollbackRestart(
		ctx, transaction.Identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			cancel(context.DeadlineExceeded)
			control := fixtureRollbackRestartControl(fixtureRollbackRestartProcess())
			control.Cleanup = func() error {
				cleanups++
				return nil
			}
			return control, true, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			t.Fatal("launcher called after resolver found exact process")
			return RollbackRestartControl{}, nil
		},
	)
	if cleanups != 1 {
		t.Fatalf("post-resolver cleanup count = %d, want 1", cleanups)
	}
	assertRollbackRestartDeadlineResult(t, store, identity, before, convergeErr)
}

func rollbackRestartJournalBytes(t *testing.T, store *Store, identity Identity) []byte {
	t.Helper()
	content, err := os.ReadFile(store.journalPath(identity.TransactionID))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func assertRollbackRestartDeadlineResult(t *testing.T, store *Store, identity Identity, before []byte, convergeErr error) {
	t.Helper()
	if !errors.Is(convergeErr, context.DeadlineExceeded) {
		t.Fatalf("ConvergeRollbackRestart() error = %v, want deadline exceeded", convergeErr)
	}
	after := rollbackRestartJournalBytes(t, store, identity)
	if !bytes.Equal(before, after) {
		t.Fatal("transaction journal changed after rollback restart deadline")
	}
	transaction, err := store.Load(context.Background(), identity)
	if err != nil {
		t.Fatalf("transaction lock remained held after deadline: %v", err)
	}
	if transaction.RollbackRestart.ACKPresent {
		t.Fatal("rollback restart ACK persisted after deadline")
	}
}

func fixtureRollbackRestartProcess() RollbackRestartProcess {
	return RollbackRestartProcess{
		PID: 303, StartToken: "kernel-start-token", ExecutableIdentity: "/Applications/Super Dolphin.app/Contents/MacOS/agent-terminal",
		ExecutableSHA256: digestText("old executable"),
	}
}

func fixtureRollbackRestartControl(process RollbackRestartProcess) RollbackRestartControl {
	return RollbackRestartControl{
		Process: process,
		Cleanup: func() error { return nil },
		Commit:  func(context.Context) error { return nil },
	}
}
