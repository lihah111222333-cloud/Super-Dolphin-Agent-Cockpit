//go:build darwin || linux

package hiddenexec

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestConfigureCommandCreatesIndependentSessionAndProcessGroup(t *testing.T) {
	cmd := exec.Command("/bin/sh")
	configureCommand(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("language-server command does not create an independent session")
	}
}

func TestCommandContextCancellationTerminatesExactRootOnly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := CommandContext(
		ctx,
		"/bin/sleep",
		"30",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start CommandContext helper: %v", err)
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		alive, probeErr := ProcessAlive(cmd.Process.Pid)
		if probeErr == nil && !alive {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if alive, probeErr := ProcessAlive(cmd.Process.Pid); probeErr != nil {
		t.Fatalf("probe CommandContext parent after cancellation: %v", probeErr)
	} else if alive {
		t.Fatal("CommandContext cancellation did not terminate the exact root")
	}
	if waitErr := cmd.Wait(); waitErr == nil {
		t.Fatal("CommandContext cancellation unexpectedly reported a successful exit")
	}
	parentAlive, err := ProcessAlive(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("probe CommandContext parent after Wait: %v", err)
	}
	if parentAlive {
		t.Fatal("CommandContext parent is still alive after Wait")
	}
}
