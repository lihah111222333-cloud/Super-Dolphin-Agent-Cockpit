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
		func(context.Context, string) (RollbackRestartProcess, RollbackRestartCleanup, bool, error) {
			return RollbackRestartProcess{}, nil, false, nil
		},
		rollbackRestartLauncher(t, transaction.RollbackRestart.LaunchToken, process, &launches),
	)
	if err != nil {
		t.Fatal(err)
	}
	if launches != 1 || !converged.RollbackRestart.ACKPresent || converged.RollbackRestart.ACK.Process != process {
		t.Fatalf("converged restart = %+v launches=%d", converged.RollbackRestart, launches)
	}
	assertRollbackRestartAlreadyACKed(t, store, identity)
}

func rollbackRestartLauncher(t *testing.T, wantToken string, process RollbackRestartProcess, launches *int) RollbackRestartLauncher {
	t.Helper()
	return func(_ context.Context, token string) (RollbackRestartProcess, RollbackRestartCleanup, error) {
		*launches++
		if token != wantToken {
			t.Fatalf("launch token = %q, want durable intent token", token)
		}
		return process, func() error { return nil }, nil
	}
}

func assertRollbackRestartAlreadyACKed(t *testing.T, store *Store, identity Identity) {
	t.Helper()
	if _, err := store.ConvergeRollbackRestart(
		context.Background(), identity,
		func(context.Context, string) (RollbackRestartProcess, RollbackRestartCleanup, bool, error) {
			t.Fatal("resolver called after ACK")
			return RollbackRestartProcess{}, nil, false, nil
		},
		func(context.Context, string) (RollbackRestartProcess, RollbackRestartCleanup, error) {
			t.Fatal("launcher called after ACK")
			return RollbackRestartProcess{}, nil, nil
		},
	); err != nil {
		t.Fatal(err)
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
		func(_ context.Context, token string) (RollbackRestartProcess, RollbackRestartCleanup, bool, error) {
			resolved++
			if token != transaction.RollbackRestart.LaunchToken {
				t.Fatalf("resolve token = %q, want %q", token, transaction.RollbackRestart.LaunchToken)
			}
			return process, func() error { return nil }, true, nil
		},
		func(context.Context, string) (RollbackRestartProcess, RollbackRestartCleanup, error) {
			t.Fatal("launcher called although token-bound process was rediscovered")
			return RollbackRestartProcess{}, nil, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != 1 || !converged.RollbackRestart.ACKPresent || converged.RollbackRestart.ACK.LaunchToken != transaction.RollbackRestart.LaunchToken {
		t.Fatalf("converged restart = %+v resolved=%d", converged.RollbackRestart, resolved)
	}
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
		func(context.Context, string) (RollbackRestartProcess, RollbackRestartCleanup, bool, error) {
			return RollbackRestartProcess{}, func() error {
				cleanups++
				return cleanupEvidence
			}, true, nil
		},
		func(context.Context, string) (RollbackRestartProcess, RollbackRestartCleanup, error) {
			t.Fatal("launcher called after rediscovery")
			return RollbackRestartProcess{}, nil, nil
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
		func(context.Context, string) (RollbackRestartProcess, RollbackRestartCleanup, bool, error) {
			t.Fatal("resolver called while transaction lock is held")
			return RollbackRestartProcess{}, nil, false, nil
		},
		func(context.Context, string) (RollbackRestartProcess, RollbackRestartCleanup, error) {
			t.Fatal("launcher called while transaction lock is held")
			return RollbackRestartProcess{}, nil, nil
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
		func(callbackCtx context.Context, _ string) (RollbackRestartProcess, RollbackRestartCleanup, bool, error) {
			<-callbackCtx.Done()
			return RollbackRestartProcess{}, nil, false, context.Cause(callbackCtx)
		},
		func(context.Context, string) (RollbackRestartProcess, RollbackRestartCleanup, error) {
			t.Fatal("launcher called after resolver deadline")
			return RollbackRestartProcess{}, nil, nil
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
		func(context.Context, string) (RollbackRestartProcess, RollbackRestartCleanup, bool, error) {
			return RollbackRestartProcess{}, nil, false, nil
		},
		func(callbackCtx context.Context, _ string) (RollbackRestartProcess, RollbackRestartCleanup, error) {
			<-callbackCtx.Done()
			return RollbackRestartProcess{}, nil, context.Cause(callbackCtx)
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
		func(context.Context, string) (RollbackRestartProcess, RollbackRestartCleanup, bool, error) {
			return RollbackRestartProcess{}, nil, false, nil
		},
		func(context.Context, string) (RollbackRestartProcess, RollbackRestartCleanup, error) {
			cancel(context.DeadlineExceeded)
			return fixtureRollbackRestartProcess(), func() error {
				cleanups++
				return nil
			}, nil
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
		func(context.Context, string) (RollbackRestartProcess, RollbackRestartCleanup, bool, error) {
			cancel(context.DeadlineExceeded)
			return fixtureRollbackRestartProcess(), func() error {
				cleanups++
				return nil
			}, true, nil
		},
		func(context.Context, string) (RollbackRestartProcess, RollbackRestartCleanup, error) {
			t.Fatal("launcher called after resolver found exact process")
			return RollbackRestartProcess{}, nil, nil
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
