//go:build unix

package appupdaterecovery

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/sync/errgroup"
)

type blockingReleaseFilesystemHelperFixture struct {
	started  string
	finished string
}

func TestComputeReleaseDigestContextKillsAndReapsBlockedSyscall(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	fixture := installBlockingReleaseFilesystemHelper(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	target := t.TempDir()
	var group errgroup.Group
	group.Go(func() error {
		_, err := ComputeReleaseDigestContext(ctx, target)
		return err
	})
	waitForReleaseFilesystemHelperStart(t, fixture.started)
	started := time.Now()
	err := group.Wait()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ComputeReleaseDigestContext() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("blocked filesystem helper cancellation elapsed %s", elapsed)
	}
	assertBlockingReleaseFilesystemHelperReaped(t, fixture)
}

func TestRollbackRestartBlockedFilesystemHelperReleasesLockWithoutLateWrite(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	before := rollbackRestartJournalBytes(t, store, identity)
	fixture := installBlockingReleaseFilesystemHelper(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var group errgroup.Group
	group.Go(func() error {
		_, convergeErr := store.ConvergeRollbackRestart(
			ctx,
			transaction.Identity,
			func(callbackCtx context.Context, _ string) (RollbackRestartControl, bool, error) {
				_, digestErr := ComputeReleaseDigestContext(callbackCtx, transaction.Paths.Target)
				return RollbackRestartControl{}, false, digestErr
			},
			func(context.Context, string) (RollbackRestartControl, error) {
				return RollbackRestartControl{}, errors.New("launcher reached after blocked resolver")
			},
		)
		return convergeErr
	})
	waitForReleaseFilesystemHelperStart(t, fixture.started)
	started := time.Now()
	convergeErr := group.Wait()
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("blocked rollback convergence deadline elapsed %s", elapsed)
	}
	assertRollbackRestartDeadlineResult(t, store, identity, before, convergeErr)
	assertBlockingReleaseFilesystemHelperReaped(t, fixture)
	time.Sleep(100 * time.Millisecond)
	if after := rollbackRestartJournalBytes(t, store, identity); !bytes.Equal(before, after) {
		t.Fatal("transaction journal changed after helper was killed and convergence returned")
	}
}

// TestRetainBackupBlockedFilesystemHelperReleasesLockWithoutLateWrite proves a
// cancelled reconcile does not retain the transaction lock or write later.
func TestRetainBackupBlockedFilesystemHelperReleasesLockWithoutLateWrite(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	store, identity, _ := createPreparedTransaction(t)
	fixture := installBlockingReleaseFilesystemHelper(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var group errgroup.Group
	group.Go(func() error {
		_, err := store.RetainBackup(ctx, identity)
		return err
	})
	waitForReleaseFilesystemHelperStart(t, fixture.started)
	before := rollbackRestartJournalBytes(t, store, identity)
	started := time.Now()
	err := group.Wait()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RetainBackup() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("blocked backup reconciliation deadline elapsed %s", elapsed)
	}
	assertTransactionLockReleased(t, store, identity)
	assertBlockingReleaseFilesystemHelperReaped(t, fixture)
	assertTransactionJournalStable(t, store, identity, before)
}

// TestRollbackBlockedFilesystemHelperReleasesLockWithoutLateWrite proves a
// cancelled rollback effect does not retain the transaction lock or write later.
func TestRollbackBlockedFilesystemHelperReleasesLockWithoutLateWrite(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	store, identity, _ := createProbationTransaction(t)
	fixture := installBlockingReleaseFilesystemHelper(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var group errgroup.Group
	group.Go(func() error {
		_, err := store.RollbackUnclaimedProbation(ctx, identity)
		return err
	})
	waitForReleaseFilesystemHelperStart(t, fixture.started)
	before := rollbackRestartJournalBytes(t, store, identity)
	started := time.Now()
	err := group.Wait()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RollbackUnclaimedProbation() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("blocked rollback effect deadline elapsed %s", elapsed)
	}
	assertTransactionLockReleased(t, store, identity)
	assertBlockingReleaseFilesystemHelperReaped(t, fixture)
	assertTransactionJournalStable(t, store, identity, before)
}

func installBlockingReleaseFilesystemHelper(t *testing.T) *blockingReleaseFilesystemHelperFixture {
	t.Helper()
	dir := t.TempDir()
	fifo := filepath.Join(dir, "blocked-open.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("create blocking FIFO: %v", err)
	}
	fixture := &blockingReleaseFilesystemHelperFixture{
		started:  filepath.Join(dir, "started"),
		finished: filepath.Join(dir, "finished"),
	}
	t.Setenv(blockingReleaseFilesystemHelperEnv, "1")
	t.Setenv(blockingReleaseFilesystemHelperFIFOEnv, fifo)
	t.Setenv(blockingReleaseFilesystemHelperStartEnv, fixture.started)
	t.Setenv(blockingReleaseFilesystemHelperDoneEnv, fixture.finished)
	return fixture
}

func waitForReleaseFilesystemHelperStart(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		content, err := os.ReadFile(path)
		if err == nil && len(content) > 0 {
			return
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspect blocking helper start marker: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("blocking release filesystem helper did not enter syscall")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertBlockingReleaseFilesystemHelperReaped(t *testing.T, fixture *blockingReleaseFilesystemHelperFixture) {
	t.Helper()
	rawPID, err := os.ReadFile(fixture.started)
	if err != nil {
		t.Fatalf("read release filesystem helper PID: %v", err)
	}
	pid, err := strconv.Atoi(string(rawPID))
	if err != nil || pid <= 0 {
		t.Fatalf("parse release filesystem helper PID %q: %v", rawPID, err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find release filesystem helper process: %v", err)
	}
	if err := process.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("signal reaped release filesystem helper: %v", err)
	}
	if _, err := os.Stat(fixture.finished); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blocked release filesystem helper continued after cancellation: %v", err)
	}
}

func assertTransactionLockReleased(t *testing.T, store *Store, identity Identity) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := store.Load(ctx, identity); err != nil {
		t.Fatalf("Load() after cancelled effect error = %v", err)
	}
}

func assertTransactionJournalStable(t *testing.T, store *Store, identity Identity, before []byte) {
	t.Helper()
	time.Sleep(100 * time.Millisecond)
	if after := rollbackRestartJournalBytes(t, store, identity); !bytes.Equal(before, after) {
		t.Fatal("transaction journal changed after helper was killed and effect returned")
	}
}
