//go:build linux

package hiddenexec

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxPidfdUnavailableLeavesProcessTreePendingWithoutSignal(t *testing.T) {
	cmd := Command("/bin/sleep", "30")
	tree, err := StartProcessTree(cmd)
	if err != nil {
		t.Fatalf("StartProcessTree() error = %v", err)
	}
	owner := tree.controller.(*unixProcessTree)
	owner.signalMembers = func(members []ProcessIdentity, signal int) error {
		return signalProcessMembersWithOps(members, signal, linuxSignalOps{
			openPidfd:       func(int, int) (int, error) { return -1, unix.ENOSYS },
			sendPidfdSignal: unix.PidfdSendSignal,
		})
	}

	err = tree.Force(context.Background())
	if !errors.Is(err, ErrProcessTreePidfdUnavailable) {
		t.Fatalf("Force() error = %v, want pidfd-unavailable", err)
	}
	if !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("Force() error = %v, want CleanupPending", err)
	}
	alive, probeErr := owner.alive()
	if probeErr != nil {
		t.Fatalf("alive() error after blocked force = %v", probeErr)
	}
	if !alive {
		t.Fatal("process tree exited even though pidfd authorization was unavailable")
	}

	owner.signalMembers = signalProcessMembers
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tree.Force(ctx); err != nil {
		t.Fatalf("authorized cleanup Force() error = %v", err)
	}
	requireLinuxSIGKILLExit(t, cmd)
}

// requireLinuxSIGKILLExit 断言 pidfd 授权后的强制清理确实以 SIGKILL 退出。
func requireLinuxSIGKILLExit(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	waitErr := cmd.Wait()
	var exitErr *exec.ExitError
	if waitErr == nil || !errors.As(waitErr, &exitErr) {
		t.Fatalf("Wait() error = %v, want SIGKILL exit", waitErr)
	}
	status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("Wait() status = %v, want SIGKILL exit", exitErr.ProcessState)
	}
}

// TestLinuxProcessTerminalStates 覆盖 Linux stat 的终止态与活动态判定。
func TestLinuxProcessTerminalStates(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  bool
	}{
		{name: "zombie", state: "Z", want: true},
		{name: "dead", state: "X", want: true},
		{name: "running", state: "R", want: false},
		{name: "sleeping", state: "S", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLinuxProcessTerminal(tc.state); got != tc.want {
				t.Fatalf("isLinuxProcessTerminal(%q) = %v, want %v", tc.state, got, tc.want)
			}
		})
	}
}

func TestLinuxActionTimeIdentityMismatchBlocksSignal(t *testing.T) {
	cmd := Command("/bin/sleep", "30")
	tree, err := StartProcessTree(cmd)
	if err != nil {
		t.Fatalf("StartProcessTree() error = %v", err)
	}
	owner := tree.controller.(*unixProcessTree)
	original := owner.root
	owner.root.StartToken = original.StartToken + "/reused"
	err = tree.Force(context.Background())
	if !errors.Is(err, ErrProcessTreeIdentityMismatch) {
		t.Fatalf("Force() error = %v, want identity mismatch", err)
	}
	if !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("Force() error = %v, want CleanupPending", err)
	}
	owner.root = original
	if err := tree.Force(context.Background()); err != nil {
		t.Fatalf("cleanup Force() error = %v", err)
	}
	_ = cmd.Wait()
}

func requireProcessTreeShutdownPlatform(t *testing.T, tree *ProcessTree, ctx context.Context) bool {
	t.Helper()
	if err := tree.Graceful(ctx); err != nil {
		t.Fatalf("Graceful() error = %v", err)
	}
	return true
}

func cleanupProcessTreeFixture(tree *ProcessTree, cmd *exec.Cmd) {
	_ = tree.Force(context.Background())
	_ = cmd.Wait()
}
