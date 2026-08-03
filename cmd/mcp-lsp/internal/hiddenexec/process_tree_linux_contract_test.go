//go:build linux

package hiddenexec

import (
	"context"
	"errors"
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
	oldOpen := openPidfd
	openPidfd = func(int, int) (int, error) { return -1, unix.ENOSYS }
	defer func() { openPidfd = oldOpen }()

	err = tree.Force(context.Background())
	if !errors.Is(err, ErrProcessTreePidfdUnavailable) {
		t.Fatalf("Force() error = %v, want pidfd-unavailable", err)
	}
	alive, probeErr := owner.alive()
	if probeErr != nil {
		t.Fatalf("alive() error after blocked force = %v", probeErr)
	}
	if !alive {
		t.Fatal("process tree exited even though pidfd authorization was unavailable")
	}

	openPidfd = oldOpen
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tree.Force(ctx); err != nil {
		t.Fatalf("authorized cleanup Force() error = %v", err)
	}
	if err := cmd.Wait(); err != nil && !errors.Is(err, syscall.EINTR) {
		t.Fatalf("Wait() error = %v", err)
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
	owner.root = original
	if err := tree.Force(context.Background()); err != nil {
		t.Fatalf("cleanup Force() error = %v", err)
	}
	_ = cmd.Wait()
}
