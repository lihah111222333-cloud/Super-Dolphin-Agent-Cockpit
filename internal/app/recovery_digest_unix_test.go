//go:build unix

package app

import (
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

const releaseFilesystemHelperExecutableEnv = "SUPER_DOLPHIN_RELEASE_FS_HELPER_EXECUTABLE"

type startupBlockingFilesystemHelperFixture struct {
	started  string
	finished string
}

func TestSelectStartupBlockedReleaseDigestEntersRecoveryAtDeadline(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	store, transaction, process := createStartupProbation(t)
	fixture := installStartupBlockingFilesystemHelper(t, transaction.Paths.Target)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var selection StartupSelection
	var group errgroup.Group
	group.Go(func() error {
		var err error
		selection, err = SelectStartup(ctx, StartupSelectorInput{
			Store: store, Process: process, ExpectedTransactionID: transaction.Identity.TransactionID,
			LeaseWait: time.Second,
		})
		return err
	})
	waitForStartupFilesystemHelper(t, fixture.started)
	started := time.Now()
	err := group.Wait()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SelectStartup() error = %v, want deadline exceeded", err)
	}
	if selection.Mode != StartupModeRecovery {
		t.Fatalf("SelectStartup() mode = %q, want Recovery", selection.Mode)
	}
	if elapsed := time.Since(started); elapsed > 6*time.Second {
		t.Fatalf("blocked startup selector deadline elapsed %s", elapsed)
	}
	assertStartupFilesystemHelperReaped(t, fixture)
}

func TestRecoveryCheckBlockedReleaseDigestReturnsAtDeadline(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	store, transaction, _ := createStartupProbation(t)
	runtime, err := NewRecoveryRuntime(StartupSelection{
		Mode: StartupModeRecovery, Store: store, Transaction: transaction,
		Projection: projectRecoveryTransaction(transaction, "test recovery check"),
	})
	if err != nil {
		t.Fatalf("NewRecoveryRuntime() error = %v", err)
	}
	fixture := installStartupBlockingFilesystemHelper(t, transaction.Paths.Target)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var group errgroup.Group
	group.Go(func() error { return runtime.Check.Check(ctx) })
	waitForStartupFilesystemHelper(t, fixture.started)
	started := time.Now()
	err = group.Wait()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RecoveryCheckService.Check() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 6*time.Second {
		t.Fatalf("blocked RecoveryCheck deadline elapsed %s", elapsed)
	}
	assertStartupFilesystemHelperReaped(t, fixture)
}

func installStartupBlockingFilesystemHelper(t *testing.T, blockedPath string) *startupBlockingFilesystemHelperFixture {
	t.Helper()
	if blockedPath == "" {
		t.Fatal("blocking filesystem helper path is required")
	}
	dir := t.TempDir()
	fifo := filepath.Join(dir, "blocked-open.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("create blocking FIFO: %v", err)
	}
	fixture := &startupBlockingFilesystemHelperFixture{
		started: filepath.Join(dir, "started"), finished: filepath.Join(dir, "finished"),
	}
	script := filepath.Join(dir, "helper")
	content := "#!/bin/sh\n" +
		"payload=$(cat)\n" +
		"case \"$payload\" in\n" +
		"  *\"$SUPER_DOLPHIN_TEST_BLOCK_DIGEST_PATH\"*)\n" +
		"    printf '%s' \"$$\" > \"$SUPER_DOLPHIN_TEST_BLOCK_DIGEST_STARTED\"\n" +
		"    exec 3<\"$SUPER_DOLPHIN_TEST_BLOCK_DIGEST_FIFO\"\n" +
		"    printf done > \"$SUPER_DOLPHIN_TEST_BLOCK_DIGEST_FINISHED\"\n" +
		"    ;;\n" +
		"  *)\n" +
		"    printf '%s\\n' \"$payload\" | exec \"$SUPER_DOLPHIN_TEST_ORIGINAL_FS_HELPER\"\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatalf("write blocking helper: %v", err)
	}
	original, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve original filesystem helper: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_TEST_BLOCK_DIGEST_PATH", blockedPath)
	t.Setenv("SUPER_DOLPHIN_TEST_BLOCK_DIGEST_FIFO", fifo)
	t.Setenv("SUPER_DOLPHIN_TEST_BLOCK_DIGEST_STARTED", fixture.started)
	t.Setenv("SUPER_DOLPHIN_TEST_BLOCK_DIGEST_FINISHED", fixture.finished)
	t.Setenv("SUPER_DOLPHIN_TEST_ORIGINAL_FS_HELPER", original)
	t.Setenv(releaseFilesystemHelperExecutableEnv, script)
	return fixture
}

func waitForStartupFilesystemHelper(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
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

func assertStartupFilesystemHelperReaped(t *testing.T, fixture *startupBlockingFilesystemHelperFixture) {
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
