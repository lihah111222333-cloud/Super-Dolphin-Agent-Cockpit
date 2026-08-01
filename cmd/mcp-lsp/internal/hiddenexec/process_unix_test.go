//go:build darwin || linux

package hiddenexec

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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

func TestConfigureCommandCreatesIndependentProcessGroup(t *testing.T) {
	cmd := exec.Command("/bin/sh")
	configureCommand(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("language-server command does not create an independent process group")
	}
}

func TestCommandContextCancellationTerminatesParentAndChild(t *testing.T) {
	childPIDPath := t.TempDir() + "/child.pid"
	ctx, cancel := context.WithCancel(context.Background())
	cmd := CommandContext(
		ctx,
		"/bin/sh",
		"-c",
		`sleep 30 & child=$!; printf '%s' "$child" > "$HIDDENEXEC_TEST_CHILD_PID"; wait`,
	)
	cmd.Env = append(os.Environ(), "HIDDENEXEC_TEST_CHILD_PID="+childPIDPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start CommandContext helper: %v", err)
	}
	childPID := waitForCommandContextChildPID(t, childPIDPath)
	cancel()
	waitErr := make(chan error, 1)
	safego.Go(context.Background(), nil, "mcp-lsp.hiddenexec.command-context-wait", func(context.Context) {
		waitErr <- cmd.Wait()
	})
	select {
	case err := <-waitErr:
		if err == nil {
			t.Fatal("CommandContext Wait() error = nil after cancellation")
		}
	case <-time.After(3 * time.Second):
		killErr := KillProcessTree(cmd)
		t.Fatalf("CommandContext parent did not exit after cancellation; forced kill error = %v", killErr)
	}
	parentAlive, err := ProcessAlive(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("probe CommandContext parent after Wait: %v", err)
	}
	if parentAlive {
		t.Fatal("CommandContext parent is still alive after Wait")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(childPID, syscall.SIGKILL)
	t.Fatalf("CommandContext child pid %d survived context cancellation", childPID)
}

func waitForCommandContextChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		payload, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(payload)))
			if parseErr != nil || pid <= 1 {
				t.Fatalf("invalid CommandContext child PID %q: %v", payload, parseErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read CommandContext child PID: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for CommandContext child PID")
	return 0
}
