//go:build darwin

package hiddenexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
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

func TestDarwinProcessTreeRSSUsesPsClosure(t *testing.T) {
	cmd := Command("/bin/sleep", "30")
	tree := startDarwinRSSProcessTree(t, cmd)
	t.Cleanup(func() {
		cleanupDarwinRSSProcessTree(t, tree, cmd)
	})
	identity := requireDarwinRSSProcessIdentity(t, tree)
	requireDarwinProcessTreeRSS(t, identity.PID)
}

func startDarwinRSSProcessTree(t *testing.T, cmd *exec.Cmd) *ProcessTree {
	t.Helper()
	tree, err := StartProcessTree(cmd)
	if err != nil {
		cleanupDarwinRSSCommand(t, cmd, "startup failure fixture")
		t.Fatalf("StartProcessTree() error = %v", err)
	}
	return tree
}

func cleanupDarwinRSSProcessTree(t *testing.T, tree *ProcessTree, cmd *exec.Cmd) {
	t.Helper()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tree.Force(cleanupCtx); err != nil && !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Errorf("cleanup Force() error = %v", err)
	}
	cleanupDarwinRSSCommand(t, cmd, "cleanup fixture")
	if err := tree.Wait(cleanupCtx); err != nil {
		reportDarwinRSSCleanupWait(t, err)
	}
}

func cleanupDarwinRSSCommand(t *testing.T, cmd *exec.Cmd, label string) {
	t.Helper()
	// Darwin production Force is intentionally zero-signal; this kills only the exact fixture root.
	if cmd.Process != nil {
		if killErr := cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			t.Errorf("%s kill: %v", label, killErr)
		}
	}
	waitDarwinRSSCommand(t, cmd, label)
}

func waitDarwinRSSCommand(t *testing.T, cmd *exec.Cmd, label string) {
	t.Helper()
	waitDone := make(chan error, 1)
	var waitGroup sync.WaitGroup
	waitGroup.Go(func() {
		waitDone <- cmd.Wait()
	})
	select {
	case waitErr := <-waitDone:
		waitGroup.Wait()
		var exitErr *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exitErr) {
			t.Errorf("%s reap error: %v", label, waitErr)
		}
	case <-time.After(3 * time.Second):
		t.Errorf("%s reap timed out", label)
	}
}

func reportDarwinRSSCleanupWait(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, syscall.EPERM) {
		t.Logf("cleanup tree.Wait() could not re-probe Darwin process closure: %v", err)
		return
	}
	t.Errorf("cleanup tree.Wait() error = %v", err)
}

func requireDarwinRSSProcessIdentity(t *testing.T, tree *ProcessTree) ProcessIdentity {
	t.Helper()
	identity, err := tree.Identity()
	if err != nil {
		t.Fatalf("Identity() error = %v", err)
	}
	if identity.PID <= 1 || identity.SessionID != identity.PID || identity.ProcessGroupID != identity.PID {
		t.Fatalf("Identity() = %+v, want an independently owned Darwin process tree root", identity)
	}
	current, err := captureProcessIdentity(identity.PID)
	if err != nil {
		t.Fatalf("captureProcessIdentity() error = %v", err)
	}
	if !current.Equal(identity) {
		t.Fatalf("current process identity = %+v, want startup identity %+v", current, identity)
	}
	return identity
}

func requireDarwinProcessTreeRSS(t *testing.T, pid int) {
	t.Helper()
	rssBytes, err := ProcessTreeRSSBytes(pid)
	if err != nil {
		t.Fatalf("ProcessTreeRSSBytes(%d) error = %v", pid, err)
	}
	if rssBytes == 0 {
		t.Fatalf("ProcessTreeRSSBytes(%d) = 0, want positive RSS", pid)
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
