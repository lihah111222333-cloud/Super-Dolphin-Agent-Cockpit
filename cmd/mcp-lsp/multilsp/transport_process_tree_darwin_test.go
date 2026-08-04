//go:build darwin

package multilsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

func killTransportFixtureForTest(t *testing.T, tr *transport, childPID int) bool {
	t.Helper()
	requireDarwinKillPending(t, tr)
	requireDarwinTransportProcess(t, tr)
	requireDarwinFixtureKill(t, tr.cmd.Process)
	requireDarwinChildKill(t, childPID)
	requireDarwinRootWait(t, tr.cmd)
	if err := tr.processTree.Release(); err != nil {
		t.Fatalf("Darwin test fixture process-tree release: %v", err)
	}
	return true
}

func assertHandedOffOwnerState(t *testing.T, cmd *exec.Cmd, tree *hiddenexec.ProcessTree, cleanupErr error) {
	t.Helper()
	if !errors.Is(cleanupErr, hiddenexec.ErrProcessTreeCleanupPending) {
		t.Fatalf("Darwin handed-off startup cleanup = %v, want CleanupPending", cleanupErr)
	}
	if cmd == nil || cmd.Process == nil {
		t.Fatal("Darwin handed-off startup owner process is unavailable")
	}
	if err := killAndWaitDarwinProcess(cmd); err != nil {
		t.Fatalf("Darwin handed-off startup fixture cleanup: %v", err)
	}
	if err := tree.Wait(context.Background()); err != nil {
		t.Fatalf("Darwin handed-off startup tree wait after test cleanup: %v", err)
	}
	if err := tree.Release(); err != nil {
		t.Fatalf("Darwin handed-off startup tree release after test cleanup: %v", err)
	}
}

func assertTransportContextTerminationPlatform(t *testing.T, tr *transport) {
	t.Helper()
	requireDarwinTransportProcess(t, tr)
	requireDarwinFixtureKill(t, tr.cmd.Process)
	requireDarwinTransportDone(t, tr)
	if err := retryDarwinTransportClose(tr); err != nil {
		t.Fatalf("Darwin context-cancelled transport retry cleanup: %v", err)
	}
	if !processTreeReleaseComplete(tr) {
		t.Fatal("Darwin context-cancelled transport retry Close() did not complete owner release")
	}
}

func requireDarwinKillPending(t *testing.T, tr *transport) {
	t.Helper()
	if err := tr.killProcess(); !errors.Is(err, hiddenexec.ErrProcessTreeCleanupPending) {
		t.Fatalf("Darwin killProcess() error = %v, want CleanupPending", err)
	}
}

func requireDarwinTransportProcess(t *testing.T, tr *transport) {
	t.Helper()
	if tr == nil || tr.cmd == nil || tr.cmd.Process == nil {
		t.Fatal("Darwin transport process is unavailable for test fixture cleanup")
	}
}

func requireDarwinFixtureKill(t *testing.T, process *os.Process) {
	t.Helper()
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("Darwin test fixture process kill: %v", err)
	}
}

func requireDarwinChildKill(t *testing.T, childPID int) {
	t.Helper()
	process, err := os.FindProcess(childPID)
	if err != nil {
		return
	}
	requireDarwinFixtureKill(t, process)
}

func requireDarwinRootWait(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Wait(); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ECHILD) && !isExitError(err) {
		t.Fatalf("Darwin test fixture root wait: %v", err)
	}
}

func killAndWaitDarwinProcess(cmd *exec.Cmd) error {
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if err := cmd.Wait(); err != nil && !errors.Is(err, os.ErrProcessDone) && !isExitError(err) {
		return err
	}
	return nil
}

func requireDarwinTransportDone(t *testing.T, tr *transport) {
	t.Helper()
	select {
	case <-tr.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Darwin context-cancelled transport did not finish after test fixture cleanup")
	}
}

func retryDarwinTransportClose(tr *transport) error {
	firstErr := tr.Close()
	if firstErr == nil {
		return nil
	}
	secondErr := tr.Close()
	if secondErr == nil {
		return nil
	}
	return fmt.Errorf("Darwin context-cancelled transport retry Close() errors = (%v, %v)", firstErr, secondErr)
}

func processTreeReleaseComplete(tr *transport) bool {
	tr.treeReleaseMu.Lock()
	defer tr.treeReleaseMu.Unlock()
	return tr.treeReleased
}

func isExitError(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}
