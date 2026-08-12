package appupdaterecovery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestRollbackRestartIntentSurvivesRenameAndConvergesOnce(t *testing.T) {
	t.Parallel()
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
	live := false
	process := fixtureRollbackRestartProcess()
	converged, err := store.ConvergeRollbackRestart(
		context.Background(), identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			return fixtureRollbackRestartControl(process), live, nil
		},
		rollbackRestartLauncher(t, transaction.RollbackRestart.LaunchToken, process, &launches, &live),
	)
	if err != nil {
		t.Fatal(err)
	}
	if launches != 1 || !converged.RollbackRestart.ACKPresent || converged.RollbackRestart.ACK.Process != process {
		t.Fatalf("converged restart = %+v launches=%d", converged.RollbackRestart, launches)
	}
	assertRollbackRestartAlreadyACKed(t, store, identity, process)
}

func rollbackRestartLauncher(
	t *testing.T,
	wantToken string,
	process RollbackRestartProcess,
	launches *int,
	live *bool,
) RollbackRestartLauncher {
	t.Helper()
	return func(_ context.Context, token string) (RollbackRestartControl, error) {
		*launches++
		if token != wantToken {
			t.Fatalf("launch token = %q, want durable intent token", token)
		}
		*live = true
		return fixtureRollbackRestartControl(process), nil
	}
}

func assertRollbackRestartAlreadyACKed(t *testing.T, store *Store, identity Identity, process RollbackRestartProcess) {
	t.Helper()
	resolves := 0
	activations := 0
	if _, err := store.ConvergeRollbackRestart(
		context.Background(), identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			resolves++
			control := fixtureRollbackRestartControl(process)
			control.Activate = func(context.Context) error { activations++; return nil }
			return control, true, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			t.Fatal("launcher called after ACK")
			return RollbackRestartControl{}, nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if resolves != 1 || activations != 1 {
		t.Fatalf("ACKed rollback resolve/ACTIVATE count = %d/%d, want 1/1", resolves, activations)
	}
}

func TestRollbackRestartRecoversLaunchBeforeACKWindowByToken(t *testing.T) {
	t.Parallel()
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
	if resolved != 2 || !converged.RollbackRestart.ACKPresent || converged.RollbackRestart.ACK.LaunchToken != transaction.RollbackRestart.LaunchToken {
		t.Fatalf("converged restart = %+v resolved=%d", converged.RollbackRestart, resolved)
	}
}

func TestRollbackRestartReplaysActivationAfterDurableACK(t *testing.T) {
	t.Parallel()
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	activationInterrupted := errors.New("ACTIVATE response lost")
	fixture := &rollbackHelperLifecycle{t: t, activateErrorOnce: activationInterrupted}
	if _, err := store.ConvergeRollbackRestart(t.Context(), transaction.Identity, fixture.resolve, fixture.launch); !errors.Is(err, activationInterrupted) {
		t.Fatalf("first convergence error = %v, want ACTIVATE interruption", err)
	}
	acked := loadTransaction(t, store, identity)
	if !acked.RollbackRestart.ACKPresent || !fixture.live {
		t.Fatalf("ACTIVATE interruption lost durable ACK or helper: %+v live=%t", acked.RollbackRestart, fixture.live)
	}
	if _, err := store.ConvergeRollbackRestart(t.Context(), transaction.Identity, fixture.resolve, fixture.launch); err != nil {
		t.Fatalf("replayed convergence: %v", err)
	}
	if fixture.launches != 1 || fixture.activations != 2 || fixture.cleanups != 0 {
		t.Fatalf("lifecycle=%+v, want launches/activations/cleanups 1/2/0", fixture)
	}
}

func TestRollbackRestartCrashAfterACKBeforeActivateLaunchesReplacement(t *testing.T) {
	t.Parallel()
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &rollbackHelperLifecycle{t: t, exitWindow: "activate"}
	if _, err := store.ConvergeRollbackRestart(t.Context(), transaction.Identity, fixture.resolve, fixture.launch); err == nil {
		t.Fatal("helper crash before ACTIVATE unexpectedly converged")
	}
	oldProcess := loadTransaction(t, store, identity).RollbackRestart.ACK.Process

	converged, err := store.ConvergeRollbackRestart(t.Context(), transaction.Identity, fixture.resolve, fixture.launch)
	if err != nil {
		t.Fatalf("replace definitively missing ACKed helper: %v", err)
	}
	if fixture.launches != 2 || fixture.maxLive != 1 || !fixture.live {
		t.Fatalf("replacement lifecycle=%+v", fixture)
	}
	if !converged.RollbackRestart.ACKPresent || converged.RollbackRestart.ACK.Process == oldProcess ||
		converged.RollbackRestart.ACK.Process != fixture.process {
		t.Fatalf("replacement ACK=%+v old=%+v live=%+v", converged.RollbackRestart.ACK, oldProcess, fixture.process)
	}
}

func TestRollbackRestartActivationResponseLossThenDeathLaunchesReplacement(t *testing.T) {
	t.Parallel()
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	responseLost := errors.New("ACTIVATE response lost")
	fixture := &rollbackHelperLifecycle{t: t, activateErrorOnce: responseLost}
	if _, err := store.ConvergeRollbackRestart(t.Context(), identity, fixture.resolve, fixture.launch); !errors.Is(err, responseLost) {
		t.Fatalf("first convergence error = %v, want response loss", err)
	}
	oldProcess := loadTransaction(t, store, identity).RollbackRestart.ACK.Process
	fixture.live = false

	converged, err := store.ConvergeRollbackRestart(t.Context(), transaction.Identity, fixture.resolve, fixture.launch)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.launches != 2 || fixture.maxLive != 1 || converged.RollbackRestart.ACK.Process == oldProcess ||
		converged.RollbackRestart.ACK.Process != fixture.process {
		t.Fatalf("response-loss replacement lifecycle=%+v ACK=%+v", fixture, converged.RollbackRestart.ACK)
	}
}

func TestRollbackRestartDeadACKReplacementWriteFailureCleansNewHelper(t *testing.T) {
	t.Parallel()
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &rollbackHelperLifecycle{t: t}
	first, err := store.ConvergeRollbackRestart(t.Context(), identity, fixture.resolve, fixture.launch)
	if err != nil {
		t.Fatal(err)
	}
	fixture.live = false
	journalDir := filepath.Dir(store.journalPath(identity.TransactionID))
	replacement := first.RollbackRestart.ACK.Process
	replacement.StartToken = "replacement-write-failure"
	replacementLive := false
	cleanups := 0
	control := func() RollbackRestartControl {
		return RollbackRestartControl{
			Process: replacement,
			Cleanup: func() error {
				cleanups++
				replacementLive = false
				return os.Chmod(journalDir, 0o700)
			},
			Prepare:  func(context.Context) error { return os.Chmod(journalDir, 0o500) },
			Activate: func(context.Context) error { t.Fatal("replacement activated without durable ACK"); return nil },
		}
	}
	t.Cleanup(func() { _ = os.Chmod(journalDir, 0o700) })
	_, convergeErr := store.ConvergeRollbackRestart(
		t.Context(), transaction.Identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			return control(), replacementLive, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			replacementLive = true
			return control(), nil
		},
	)
	got := loadTransaction(t, store, identity)
	if convergeErr == nil || cleanups != 1 || replacementLive || got.RollbackRestart.ACK.Process != first.RollbackRestart.ACK.Process {
		t.Fatalf("replacement write failure error=%v cleanups=%d live=%t ACK=%+v", convergeErr, cleanups, replacementLive, got.RollbackRestart.ACK)
	}
}

func TestRollbackRestartDeadACKResolverErrorDoesNotLaunch(t *testing.T) {
	t.Parallel()
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &rollbackHelperLifecycle{t: t}
	if _, err := store.ConvergeRollbackRestart(t.Context(), identity, fixture.resolve, fixture.launch); err != nil {
		t.Fatal(err)
	}
	fixture.live = false
	resolveErr := errors.New("resolver identity uncertain")
	_, convergeErr := store.ConvergeRollbackRestart(
		t.Context(), transaction.Identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			return RollbackRestartControl{}, false, resolveErr
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			t.Fatal("launcher called after uncertain resolver failure")
			return RollbackRestartControl{}, nil
		},
	)
	if !errors.Is(convergeErr, resolveErr) || fixture.launches != 1 {
		t.Fatalf("resolver error=%v lifecycle=%+v", convergeErr, fixture)
	}
}

func TestRollbackRestartDeadACKReplacementValidationFailureCleansLaunch(t *testing.T) {
	t.Parallel()
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &rollbackHelperLifecycle{t: t}
	first, err := store.ConvergeRollbackRestart(t.Context(), identity, fixture.resolve, fixture.launch)
	if err != nil {
		t.Fatal(err)
	}
	fixture.live = false
	cleanups := 0
	_, convergeErr := store.ConvergeRollbackRestart(
		t.Context(), transaction.Identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			return RollbackRestartControl{}, false, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			return RollbackRestartControl{Cleanup: func() error { cleanups++; return nil }}, nil
		},
	)
	got := loadTransaction(t, store, identity)
	if convergeErr == nil || cleanups != 1 || got.RollbackRestart.ACK.Process != first.RollbackRestart.ACK.Process {
		t.Fatalf("replacement validation error=%v cleanups=%d ACK=%+v", convergeErr, cleanups, got.RollbackRestart.ACK)
	}
}

func TestRollbackRestartACKWriteFailureCleansParkedHelper(t *testing.T) {
	t.Parallel()
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	journalDir := filepath.Dir(store.journalPath(identity.TransactionID))
	permissionRestored := false
	cleanups := 0
	activations := 0
	process := fixtureRollbackRestartProcess()
	live := true
	control := func() RollbackRestartControl {
		return RollbackRestartControl{
			Process: process,
			Cleanup: func() error {
				cleanups++
				live = false
				permissionRestored = true
				return os.Chmod(journalDir, 0o700)
			},
			Prepare: func(context.Context) error { return os.Chmod(journalDir, 0o500) },
			Activate: func(context.Context) error {
				activations++
				return nil
			},
		}
	}
	t.Cleanup(func() { _ = os.Chmod(journalDir, 0o700) })
	_, convergeErr := store.ConvergeRollbackRestart(
		t.Context(), transaction.Identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			return control(), live, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			t.Fatal("launcher called despite live parked helper")
			return RollbackRestartControl{}, nil
		},
	)
	if convergeErr == nil || cleanups != 1 || activations != 0 || !permissionRestored || live {
		t.Fatalf("ACK write failure error=%v cleanups=%d activations=%d restored=%t live=%t", convergeErr, cleanups, activations, permissionRestored, live)
	}
	if got := loadTransaction(t, store, identity); got.RollbackRestart.ACKPresent {
		t.Fatalf("ACK write failure persisted dead ACK: %+v", got.RollbackRestart.ACK)
	}
}

func TestRollbackRestartCancellationAfterConfirmCleansBeforeACK(t *testing.T) {
	t.Parallel()
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	cancelErr := errors.New("cancel after confirm")
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(context.Canceled)
	fixture := &rollbackHelperLifecycle{t: t}
	resolves := 0
	resolve := func(resolveCtx context.Context, token string) (RollbackRestartControl, bool, error) {
		control, found, resolveErr := fixture.resolve(resolveCtx, token)
		resolves++
		if resolves == 2 {
			cancel(cancelErr)
		}
		return control, found, resolveErr
	}
	_, convergeErr := store.ConvergeRollbackRestart(ctx, transaction.Identity, resolve, fixture.launch)
	if !errors.Is(convergeErr, cancelErr) || fixture.cleanups != 1 || fixture.activations != 0 || fixture.live {
		t.Fatalf("confirm cancellation error=%v lifecycle=%+v", convergeErr, fixture)
	}
	if got := loadTransaction(t, store, identity); got.RollbackRestart.ACKPresent {
		t.Fatalf("confirm cancellation persisted ACK: %+v", got.RollbackRestart.ACK)
	}
}

func TestRollbackRestartExitedHelperWindowsConvergeWithoutDeadACK(t *testing.T) {
	t.Parallel()
	for _, window := range []string{"ready", "prepare"} {
		t.Run(window, func(t *testing.T) {
			store, identity, _ := createProbationTransaction(t)
			transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
			if err != nil {
				t.Fatal(err)
			}
			fixture := &rollbackHelperLifecycle{t: t, exitWindow: window}
			if _, err := store.ConvergeRollbackRestart(t.Context(), identity, fixture.resolve, fixture.launch); err == nil {
				t.Fatalf("%s exit unexpectedly converged", window)
			}
			if got := loadTransaction(t, store, identity); got.RollbackRestart.ACKPresent {
				t.Fatalf("%s exit persisted dead helper ACK: %+v", window, got.RollbackRestart.ACK)
			}
			converged, err := store.ConvergeRollbackRestart(t.Context(), transaction.Identity, fixture.resolve, fixture.launch)
			if err != nil {
				t.Fatal(err)
			}
			if fixture.launches != 2 || fixture.maxLive != 1 || !converged.RollbackRestart.ACKPresent ||
				converged.RollbackRestart.ACK.Process != fixture.process {
				t.Fatalf("%s lifecycle=%+v ACK=%+v", window, fixture, converged.RollbackRestart.ACK)
			}
		})
	}
}

type rollbackHelperLifecycle struct {
	t                 *testing.T
	exitWindow        string
	activateErrorOnce error
	process           RollbackRestartProcess
	live              bool
	launches          int
	prepares          int
	activations       int
	cleanups          int
	resolves          int
	maxLive           int
}

func (fixture *rollbackHelperLifecycle) resolve(context.Context, string) (RollbackRestartControl, bool, error) {
	fixture.resolves++
	if !fixture.live {
		return RollbackRestartControl{}, false, nil
	}
	return fixture.control(), true, nil
}

func (fixture *rollbackHelperLifecycle) launch(context.Context, string) (RollbackRestartControl, error) {
	if fixture.live {
		fixture.t.Fatal("launcher created a second live rollback helper")
	}
	fixture.launches++
	fixture.process = fixtureRollbackRestartProcess()
	fixture.process.StartToken = fmt.Sprintf("helper-generation-%d", fixture.launches)
	fixture.live = true
	fixture.maxLive = 1
	return fixture.control(), nil
}

func (fixture *rollbackHelperLifecycle) control() RollbackRestartControl {
	process := fixture.process
	return RollbackRestartControl{
		Process: process,
		Cleanup: func() error {
			fixture.cleanups++
			if fixture.process == process {
				fixture.live = false
			}
			return nil
		},
		Prepare:  func(context.Context) error { return fixture.prepare(process) },
		Activate: func(context.Context) error { return fixture.activate(process) },
	}
}

func (fixture *rollbackHelperLifecycle) prepare(process RollbackRestartProcess) error {
	fixture.prepares++
	if !fixture.live || fixture.process != process {
		return errors.New("rollback helper exited before PREPARE")
	}
	window := fixture.exitWindow
	if window == "ready" || window == "prepare" {
		fixture.live = false
		fixture.exitWindow = ""
	}
	if window == "ready" {
		return errors.New("rollback helper exited after READY")
	}
	return nil
}

func (fixture *rollbackHelperLifecycle) activate(process RollbackRestartProcess) error {
	fixture.activations++
	if !fixture.live || fixture.process != process {
		return errors.New("rollback helper exited before ACTIVATE")
	}
	if fixture.exitWindow == "activate" {
		fixture.live = false
		fixture.exitWindow = ""
		return errors.New("rollback helper exited after durable ACK before ACTIVATE")
	}
	if fixture.activateErrorOnce != nil {
		err := fixture.activateErrorOnce
		fixture.activateErrorOnce = nil
		return err
	}
	return nil
}

func TestRollbackRestartACKRejectsReusedHelper(t *testing.T) {
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &rollbackHelperLifecycle{t: t}
	if _, err := store.ConvergeRollbackRestart(t.Context(), identity, fixture.resolve, fixture.launch); err != nil {
		t.Fatal(err)
	}

	resolves := 0
	cleanups := 0
	changed := fixture.process
	changed.StartToken = "reused-generation"
	_, replayErr := store.ConvergeRollbackRestart(
		t.Context(), transaction.Identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			resolves++
			control := fixtureRollbackRestartControl(changed)
			control.Cleanup = func() error { cleanups++; return nil }
			return control, true, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			t.Fatal("launcher called after durable ACK")
			return RollbackRestartControl{}, nil
		},
	)
	if replayErr == nil || resolves != 1 || cleanups != 0 {
		t.Fatalf("ACK replay error=%v resolves=%d cleanups=%d, want fail-closed identity mismatch", replayErr, resolves, cleanups)
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

func TestRollbackRestartBlockedDigestDeadlineReleasesLockWithoutACK(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	digestFile, err := os.CreateTemp(t.TempDir(), "blocked-digest-")
	if err != nil {
		t.Fatal(err)
	}
	if err := digestFile.Close(); err != nil {
		t.Fatal(err)
	}
	ops, release := newBlockedReleaseDigestOps(t)
	defer release()
	before := rollbackRestartJournalBytes(t, store, identity)
	resolve := func(ctx context.Context, _ string) (RollbackRestartControl, bool, error) {
		_, digestErr := computeReleaseDigestContextWithOps(ctx, digestFile.Name(), ops)
		return RollbackRestartControl{}, false, digestErr
	}
	launch := func(context.Context, string) (RollbackRestartControl, error) {
		return RollbackRestartControl{}, errors.New("launch must not run after blocked digest deadline")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, convergeErr := store.ConvergeRollbackRestart(ctx, transaction.Identity, resolve, launch)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("blocked digest deadline elapsed %s", elapsed)
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
		Process:  process,
		Cleanup:  func() error { return nil },
		Prepare:  func(context.Context) error { return nil },
		Activate: func(context.Context) error { return nil },
	}
}
