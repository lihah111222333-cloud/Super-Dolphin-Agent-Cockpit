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
		func(string) (RollbackRestartProcess, bool, error) { return RollbackRestartProcess{}, false, nil },
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
	return func(token string) (RollbackRestartProcess, error) {
		*launches++
		if token != wantToken {
			t.Fatalf("launch token = %q, want durable intent token", token)
		}
		return process, nil
	}
}

func assertRollbackRestartAlreadyACKed(t *testing.T, store *Store, identity Identity) {
	t.Helper()
	if _, err := store.ConvergeRollbackRestart(
		context.Background(), identity,
		func(string) (RollbackRestartProcess, bool, error) {
			t.Fatal("resolver called after ACK")
			return RollbackRestartProcess{}, false, nil
		},
		func(string) (RollbackRestartProcess, error) {
			t.Fatal("launcher called after ACK")
			return RollbackRestartProcess{}, nil
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
		func(token string) (RollbackRestartProcess, bool, error) {
			resolved++
			if token != transaction.RollbackRestart.LaunchToken {
				t.Fatalf("resolve token = %q, want %q", token, transaction.RollbackRestart.LaunchToken)
			}
			return process, true, nil
		},
		func(string) (RollbackRestartProcess, error) {
			t.Fatal("launcher called although token-bound process was rediscovered")
			return RollbackRestartProcess{}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != 1 || !converged.RollbackRestart.ACKPresent || converged.RollbackRestart.ACK.LaunchToken != transaction.RollbackRestart.LaunchToken {
		t.Fatalf("converged restart = %+v resolved=%d", converged.RollbackRestart, resolved)
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
		func(string) (RollbackRestartProcess, bool, error) {
			t.Fatal("resolver called while transaction lock is held")
			return RollbackRestartProcess{}, false, nil
		},
		func(string) (RollbackRestartProcess, error) {
			t.Fatal("launcher called while transaction lock is held")
			return RollbackRestartProcess{}, nil
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

func fixtureRollbackRestartProcess() RollbackRestartProcess {
	return RollbackRestartProcess{
		PID: 303, StartToken: "kernel-start-token", ExecutableIdentity: "/Applications/Super Dolphin.app/Contents/MacOS/agent-terminal",
		ExecutableSHA256: digestText("old executable"),
	}
}
