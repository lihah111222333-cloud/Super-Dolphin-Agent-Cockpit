//go:build darwin || linux

package multilsp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

type cleanupOwnerStub struct {
	terminateErr error
	waitErr      error
	releaseErr   error
}

func (s *cleanupOwnerStub) Terminate() error           { return s.terminateErr }
func (s *cleanupOwnerStub) Wait(context.Context) error { return s.waitErr }
func (s *cleanupOwnerStub) Release() error             { return s.releaseErr }

func TestFailedTransportCleanupErrorRetainsOwner(t *testing.T) {
	owner := &cleanupOwnerStub{terminateErr: errors.New("terminate failed")}
	err := cleanupProcessTreeOwner(owner)
	if err == nil {
		t.Fatal("cleanupProcessTreeOwner() error = nil")
	}
	var retained interface {
		ProcessTreeOwner() processTreeCleanupTarget
	}
	if !errors.As(err, &retained) {
		t.Fatalf("cleanup error %T does not retain owner", err)
	}
	if retained.ProcessTreeOwner() != owner {
		t.Fatal("cleanup error retained a different owner")
	}
}

func TestTransportKillProcessTerminatesLanguageServerProcessTree(t *testing.T) {
	childPIDPath := t.TempDir() + "/child.pid"
	cmd, processTree, stdin, stdout, _, err := startTransport(transportOptions{
		Binary: "/bin/sh",
		Args: []string{
			"-c",
			`sleep 30 & child=$!; printf '%s' "$child" > "$MCP_LSP_TEST_CHILD_PID"; wait`,
		},
		Env: []string{"MCP_LSP_TEST_CHILD_PID=" + childPIDPath},
	})
	if err != nil {
		t.Fatalf("startTransport() error = %v", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = stdout.Close()
	}()

	childPID := waitForTestChildPID(t, childPIDPath)
	childExited := false
	defer func() {
		if !childExited {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
		}
	}()

	tr := &transport{cmd: cmd, processTree: processTree}
	if err := tr.killProcess(); err != nil {
		t.Fatalf("killProcess() error = %v", err)
	}
	waitDone := make(chan struct{})
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		_ = cmd.Wait()
		close(waitDone)
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !testProcessExists(childPID) {
			childExited = true
			select {
			case <-waitDone:
			case <-time.After(3 * time.Second):
				t.Fatal("language-server parent did not exit")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("language-server descendant pid %d survived parent shutdown", childPID)
}

func TestTransportWithoutProcessTreeOwnerRefusesDestructiveFallback(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "0.2")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start unowned process: %v", err)
	}
	tr := &transport{cmd: cmd}
	if err := tr.killProcess(); !errors.Is(err, hiddenexec.ErrProcessTreeOwnerMissing) {
		t.Fatalf("killProcess() error = %v, want owner-missing", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("unowned process Wait() error = %v, want natural exit", err)
	}
}

func TestNewTransportTransfersFailedStartOwnerToCleanup(t *testing.T) {
	cmd := hiddenexec.Command("/bin/sleep", "30")
	tree, err := hiddenexec.StartProcessTree(cmd)
	if err != nil {
		t.Fatalf("StartProcessTree() error = %v", err)
	}
	starter := func(*exec.Cmd) (*hiddenexec.ProcessTree, error) {
		return tree, errors.New("injected process-tree startup failure")
	}

	if _, err := newTransportWithStarter(transportOptions{Binary: "/bin/sleep", Args: []string{"30"}}, starter); err == nil {
		t.Fatal("newTransport() error = nil after injected process-tree startup failure")
	} else {
		t.Logf("injected startup failure cleanup: %v", err)
	}
	assertHandedOffOwnerDead(t, cmd)
}

func assertHandedOffOwnerDead(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		alive, probeErr := hiddenexec.ProcessAlive(cmd.Process.Pid)
		if probeErr != nil {
			t.Fatalf("probe handed-off startup owner: %v", probeErr)
		}
		if !alive {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	alive, probeErr := hiddenexec.ProcessAlive(cmd.Process.Pid)
	if probeErr != nil {
		t.Fatalf("probe handed-off startup owner after retry: %v", probeErr)
	}
	if alive {
		t.Fatalf("handed-off startup owner process %d is still alive", cmd.Process.Pid)
	}
	if waitErr := cmd.Wait(); waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.Is(waitErr, os.ErrProcessDone) && !errors.As(waitErr, &exitErr) {
			t.Fatalf("reap handed-off startup owner: %v", waitErr)
		}
	}
}

func waitForTestChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		payload, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(payload)))
			if parseErr != nil || pid <= 1 {
				t.Fatalf("invalid child pid %q: %v", payload, parseErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read child pid: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for language-server child pid")
	return 0
}

func testProcessExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
