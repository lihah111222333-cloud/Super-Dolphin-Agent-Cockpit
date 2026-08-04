//go:build darwin || linux

package hiddenexec

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

func TestStartProcessTreeBindFailureAbortsOwnedStartupAndReapsRoot(t *testing.T) {
	// Keep a descendant in the same startup process group so the assertion
	// covers the complete constrained-abort scope, not just cmd.Process.
	cmd := Command("/bin/sh", "-c", "sleep 30")
	tree, err := startProcessTreeWithHooks(cmd, startupAbortHooks{
		captureIdentity: func(int) (ProcessIdentity, error) {
			return ProcessIdentity{}, errors.New("injected startup bind failure")
		},
		groupOwned:  startupProcessGroupOwned,
		startWait:   startStartupProcessWait,
		waitTimeout: 3 * time.Second,
	})
	if tree != nil {
		t.Fatal("StartProcessTree() returned an owner after an injected bind failure")
	}
	if err == nil {
		t.Fatal("StartProcessTree() error = nil after an injected bind failure")
	}
	if errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("StartProcessTree() reported CleanupPending despite owned startup cleanup: %v", err)
	}
	if cmd.Process == nil {
		t.Fatal("StartProcessTree() did not retain the started process for startup cleanup evidence")
	}

	rootPID := cmd.Process.Pid
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		table, tableErr := processTable()
		if tableErr == nil && !processGroupPresent(table, rootPID) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	table, tableErr := processTable()
	if tableErr != nil {
		t.Fatalf("inspect startup-aborted process group: %v", tableErr)
	}
	if processGroupPresent(table, rootPID) {
		t.Fatalf("startup-aborted process group %d still has members", rootPID)
	}
}

func processGroupPresent(table map[int]ProcessIdentity, processGroupID int) bool {
	for _, identity := range table {
		if identity.ProcessGroupID == processGroupID {
			return true
		}
	}
	return false
}

func TestStartProcessTreeOwnershipProbeFailureRetainsExactRootOwner(t *testing.T) {
	cmd := Command("/bin/sleep", "30")
	tree, err := startProcessTreeWithHooks(cmd, startupAbortHooks{
		captureIdentity: func(pid int) (ProcessIdentity, error) {
			return ProcessIdentity{PID: pid}, nil
		},
		groupOwned: func(int) (bool, error) {
			return false, errors.New("injected ownership probe failure")
		},
		startWait:   startStartupProcessWait,
		waitTimeout: 3 * time.Second,
	})
	if tree == nil {
		t.Fatal("StartProcessTree() lost the exact-root cleanup owner")
	}
	if !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("StartProcessTree() error = %v, want CleanupPending", err)
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
