//go:build darwin

package hiddenexec

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func finishRetainedStartupOwner(t *testing.T, tree *ProcessTree, cmd *exec.Cmd, ctx context.Context) {
	t.Helper()
	if err := tree.Force(ctx); !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("retained startup Force() error = %v, want CleanupPending", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("test fixture cleanup kill: %v", err)
	}
	if err := tree.Wait(ctx); err != nil {
		t.Fatalf("retained startup Wait() after test cleanup: %v", err)
	}
	if err := tree.Release(); err != nil {
		t.Fatalf("retained startup Release() after test cleanup: %v", err)
	}
}
