//go:build linux

package multilsp

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

func killTransportFixtureForTest(t *testing.T, tr *transport, _ int) bool {
	t.Helper()
	if err := tr.killProcess(); err != nil {
		t.Fatalf("killProcess() error = %v", err)
	}
	return false
}

func assertHandedOffOwnerState(t *testing.T, cmd *exec.Cmd, _ *hiddenexec.ProcessTree, _ error) {
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

func assertTransportContextTerminationPlatform(t *testing.T, tr *transport) {
	t.Helper()
	select {
	case <-tr.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("request() returned on context termination but LSP process stayed alive; want request context termination path to terminate the process before cleanup")
	}
}
