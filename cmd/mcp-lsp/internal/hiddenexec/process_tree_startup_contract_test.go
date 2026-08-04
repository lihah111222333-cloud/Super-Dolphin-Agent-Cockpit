//go:build darwin || linux

package hiddenexec

// 合同测试验证启动身份权限，禁止触碰无关进程。
import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

func TestStartProcessTreeBindFailureRetainsUnknownStartupOwnerWithoutSignal(t *testing.T) {
	// When the root identity cannot be captured, cleanup must fail closed and
	// avoid signaling a PID that cannot be proven to be ours.
	groupSignals := 0
	exactSignals := 0
	cmd := Command("/bin/sleep", "30")
	tree, err := startProcessTreeWithHooks(cmd, startupAbortHooks{
		captureIdentity: func(int) (ProcessIdentity, error) {
			return ProcessIdentity{}, errors.New("injected startup bind failure")
		},
		groupOwned: func(int) (bool, error) {
			return true, nil
		},
		startWait:   startStartupProcessWait,
		waitTimeout: time.Millisecond,
		killGroup: func(int) error {
			groupSignals++
			return nil
		},
		killProcess: func(*exec.Cmd) error {
			exactSignals++
			return nil
		},
	})
	assertRetainedStartupOwner(t, tree, err)
	assertNoStartupSignals(t, "startup identity failure", groupSignals, exactSignals)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	assertCleanupPending(t, "retained unknown owner Force", tree.Force(ctx))
	assertNoStartupSignals(t, "retained unknown owner", groupSignals, exactSignals)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("test cleanup kill: %v", err)
	}
	if err := tree.Wait(ctx); err != nil {
		t.Fatalf("reap fail-closed startup owner: %v", err)
	}
}

// assertRetainedStartupOwner 固定启动身份不可用时必须返回 retained owner 与 CleanupPending。
func assertRetainedStartupOwner(t *testing.T, tree *ProcessTree, err error) {
	t.Helper()
	if tree == nil {
		t.Fatal("StartProcessTree() lost the fail-closed startup owner")
	}
	if err == nil {
		t.Fatal("StartProcessTree() error = nil after an injected bind failure")
	}
	if !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("StartProcessTree() error = %v, want CleanupPending", err)
	}
}

// assertNoStartupSignals 固定注入的 group/exact signal hooks 均未被调用。
func assertNoStartupSignals(t *testing.T, operation string, groupSignals, exactSignals int) {
	t.Helper()
	if groupSignals != 0 || exactSignals != 0 {
		t.Fatalf("%s sent signals: group=%d exact=%d", operation, groupSignals, exactSignals)
	}
}

func TestStartProcessTreeOwnershipProbeFailureRetainsExactRootOwner(t *testing.T) {
	groupSignals := 0
	exactSignals := 0
	cmd := Command("/bin/sleep", "30")
	tree, err := startProcessTreeWithHooks(cmd, startupAbortHooks{
		captureIdentity: func(pid int) (ProcessIdentity, error) {
			return ProcessIdentity{PID: pid}, nil
		},
		groupOwned: func(int) (bool, error) {
			return false, errors.New("injected ownership probe failure")
		},
		startWait:   startStartupProcessWait,
		waitTimeout: time.Millisecond,
		killGroup: func(int) error {
			groupSignals++
			return nil
		},
		killProcess: func(*exec.Cmd) error {
			exactSignals++
			return nil
		},
	})
	if tree == nil {
		t.Fatal("StartProcessTree() lost the exact-root cleanup owner")
	}
	if !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("StartProcessTree() error = %v, want CleanupPending", err)
	}
	if groupSignals != 0 || exactSignals != 0 {
		t.Fatalf("ownership probe failure sent startup signals: group=%d exact=%d", groupSignals, exactSignals)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tree.Force(ctx); err != nil {
		t.Fatalf("retained exact-root Force() error = %v", err)
	}
	if err := tree.Wait(ctx); err != nil {
		t.Fatalf("retained exact-root Wait() error = %v", err)
	}
	if err := tree.Release(); err != nil {
		t.Fatalf("retained exact-root Release() error = %v", err)
	}
	alive, probeErr := ProcessAlive(cmd.Process.Pid)
	if probeErr != nil {
		t.Fatalf("probe retained exact-root process: %v", probeErr)
	}
	if alive {
		t.Fatalf("retained exact-root process %d is still alive", cmd.Process.Pid)
	}
}

func TestStartProcessTreeWaitTimeoutRetainsOwnerForRetry(t *testing.T) {
	cmd := Command("/bin/sleep", "30")
	tree, err := startProcessTreeWithHooks(cmd, startupAbortHooks{
		captureIdentity: func(pid int) (ProcessIdentity, error) {
			return ProcessIdentity{PID: pid}, nil
		},
		groupOwned: startupProcessGroupOwned,
		startWait: func(cmd *exec.Cmd) chan error {
			done := make(chan error, 1)
			safego.Go(context.Background(), nil, "test.hiddenexec.startup-wait-delay", func(context.Context) {
				time.Sleep(100 * time.Millisecond)
				done <- cmd.Wait()
			})
			return done
		},
		waitTimeout: time.Millisecond,
	})
	if tree == nil {
		t.Fatal("StartProcessTree() lost the owner after a bounded wait timeout")
	}
	if !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("StartProcessTree() error = %v, want CleanupPending", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tree.Wait(ctx); err != nil {
		t.Fatalf("retained owner Wait() error = %v", err)
	}
	if err := tree.Release(); err != nil {
		t.Fatalf("retained owner Release() error = %v", err)
	}
	alive, probeErr := ProcessAlive(cmd.Process.Pid)
	if probeErr != nil {
		t.Fatalf("probe timed-out startup process: %v", probeErr)
	}
	if alive {
		t.Fatalf("timed-out startup process %d is still alive", cmd.Process.Pid)
	}
}

func TestStartupAbortActionPIDReuseDoesNotSignal(t *testing.T) {
	expected := &ProcessIdentity{PID: 4242, StartToken: "boot/start-a"}
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
	if !pending {
		t.Fatal("startupAbortAction() pending = false, want fail-closed")
	}
	if !errors.Is(err, ErrProcessTreeIdentityMismatch) {
		t.Fatalf("startupAbortAction() error = %v, want identity mismatch", err)
	}
	if signals != 0 {
		t.Fatalf("PID reuse received signal attempts = %d, want zero", signals)
	}
}

func TestStartupAbortActionIdentityProbePermissionDoesNotSignal(t *testing.T) {
	expected := &ProcessIdentity{PID: 4343, StartToken: "boot/start-a"}
	signals := 0
	err, pending := startupAbortAction(&exec.Cmd{}, expected.PID, expected, true, nil, startupAbortHooks{
		captureIdentity: func(int) (ProcessIdentity, error) {
			return ProcessIdentity{}, os.ErrPermission
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
	if !pending {
		t.Fatal("startupAbortAction() pending = false, want fail-closed")
	}
	if !errors.Is(err, ErrProcessTreeIdentityMismatch) {
		t.Fatalf("startupAbortAction() error = %v, want identity mismatch", err)
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("startupAbortAction() error = %v, want permission error", err)
	}
	if signals != 0 {
		t.Fatalf("permission failure received signal attempts = %d, want zero", signals)
	}
}

func TestStartupAbortActionSignalPermissionDoesNotFallbackToRoot(t *testing.T) {
	expected := &ProcessIdentity{PID: 4545, StartToken: "boot/start-a"}
	exactSignals := 0
	err, pending := startupAbortAction(&exec.Cmd{}, expected.PID, expected, true, nil, startupAbortHooks{
		captureIdentity: func(pid int) (ProcessIdentity, error) {
			return *expected, nil
		},
		killGroup: func(int) error {
			return os.ErrPermission
		},
		killProcess: func(*exec.Cmd) error {
			exactSignals++
			return nil
		},
	})
	if !pending || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("startupAbortAction() result = (%v, pending=%v), want fail-closed permission error", err, pending)
	}
	if exactSignals != 0 {
		t.Fatalf("permission failure fell back to exact signal attempts = %d, want zero", exactSignals)
	}
}

func TestStartupOwnerRetryPIDReuseDoesNotSignal(t *testing.T) {
	expected := ProcessIdentity{PID: 4444, StartToken: "boot/start-a"}
	done := make(chan error, 1)
	done <- nil
	signals := 0
	state := &startupProcessTreeState{
		cmd:              &exec.Cmd{Process: &os.Process{}},
		waitDone:         done,
		waitStarted:      true,
		startupIdentity:  expected,
		identityKnown:    true,
		identityRequired: true,
		captureIdentity: func(pid int) (ProcessIdentity, error) {
			return ProcessIdentity{PID: pid, StartToken: "boot/start-b"}, nil
		},
	}
	err := state.terminateExact()
	if !errors.Is(err, ErrProcessTreeIdentityMismatch) {
		t.Fatalf("terminateExact() error = %v, want identity mismatch", err)
	}
	if !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("terminateExact() error = %v, want CleanupPending", err)
	}
	if signals != 0 {
		t.Fatalf("PID reuse retry received signal attempts = %d, want zero", signals)
	}
}
