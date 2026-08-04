//go:build darwin

package hiddenexec

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestDarwinIdentityUsesHighResolutionNativeStartToken(t *testing.T) {
	cmd := Command("/bin/sleep", "30")
	tree, err := StartProcessTree(cmd)
	if err != nil {
		t.Fatalf("StartProcessTree() error = %v", err)
	}
	identity, err := tree.Identity()
	if err != nil {
		t.Fatalf("Identity() error = %v", err)
	}
	if !strings.Contains(identity.StartToken, ".") {
		t.Fatalf("StartToken = %q, want native second.microsecond token", identity.StartToken)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := tree.Force(ctx); !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("Force() error = %v, want CleanupPending without a stable process handle", err)
	}
	alive, err := ProcessAlive(identity.PID)
	if err != nil {
		t.Fatalf("ProcessAlive() after blocked Force = %v", err)
	}
	if !alive {
		t.Fatal("blocked Darwin Force unexpectedly terminated the fixture")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("test fixture cleanup kill: %v", err)
	}
	if err := tree.Wait(ctx); err != nil {
		t.Fatalf("Wait() after test fixture cleanup: %v", err)
	}
}

func TestDarwinStartupPIDReuseDoesNotSignal(t *testing.T) {
	expected := &ProcessIdentity{PID: 5454, StartToken: "boot/start-a"}
	signals := 0
	err, pending := startupAbortAction(&exec.Cmd{}, expected.PID, expected, true, nil, startupAbortHooks{
		captureIdentity: func(pid int) (ProcessIdentity, error) {
			return ProcessIdentity{PID: pid, StartToken: "boot/start-b"}, nil
		},
		killGroup: func(int) error {
			signals++
			return nil
		},
		killProcess: func(*exec.Cmd) error {
			signals++
			return nil
		},
	})
	if !pending || !errors.Is(err, ErrProcessTreeIdentityMismatch) {
		t.Fatalf("Darwin startup PID reuse result = (%v, pending=%v), want fail-closed identity mismatch", err, pending)
	}
	if signals != 0 {
		t.Fatalf("Darwin startup PID reuse signal attempts = %d, want zero", signals)
	}
}

func TestDarwinStartupCaptureActionRaceRemainsZeroSignal(t *testing.T) {
	expected := &ProcessIdentity{PID: 5656, StartToken: "boot/start-a"}
	current := *expected
	captures := 0
	err, pending := startupAbortAction(&exec.Cmd{}, expected.PID, expected, true, nil, startupAbortHooks{
		captureIdentity: func(pid int) (ProcessIdentity, error) {
			captures++
			return ProcessIdentity{PID: pid, StartToken: current.StartToken}, nil
		},
	})
	if !pending || !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("Darwin startup action result = (%v, pending=%v), want CleanupPending", err, pending)
	}
	current.StartToken = "boot/start-reused"
	err, pending = startupAbortAction(&exec.Cmd{}, expected.PID, expected, true, nil, startupAbortHooks{
		captureIdentity: func(pid int) (ProcessIdentity, error) {
			captures++
			return ProcessIdentity{PID: pid, StartToken: current.StartToken}, nil
		},
	})
	if !pending || !errors.Is(err, ErrProcessTreeIdentityMismatch) {
		t.Fatalf("Darwin capture/action race result = (%v, pending=%v), want identity mismatch", err, pending)
	}
	if captures != 2 {
		t.Fatalf("Darwin capture/action race captures = %d, want two action-time probes", captures)
	}
}

func requireProcessTreeShutdownPlatform(t *testing.T, tree *ProcessTree, ctx context.Context) bool {
	t.Helper()
	if err := tree.Graceful(ctx); !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("Darwin Graceful() error = %v, want CleanupPending", err)
	}
	if err := tree.Force(ctx); !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("Darwin Force() error = %v, want CleanupPending", err)
	}
	return false
}

func cleanupProcessTreeFixture(tree *ProcessTree, cmd *exec.Cmd) {
	_ = tree.Force(context.Background())
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}
