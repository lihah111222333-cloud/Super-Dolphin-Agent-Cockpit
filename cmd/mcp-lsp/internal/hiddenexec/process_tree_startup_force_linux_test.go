//go:build linux

package hiddenexec

import (
	"context"
	"os/exec"
	"testing"
)

func finishRetainedStartupOwner(t *testing.T, tree *ProcessTree, _ *exec.Cmd, ctx context.Context) {
	t.Helper()
	if err := tree.Force(ctx); err != nil {
		t.Fatalf("retained startup Force() error = %v", err)
	}
	if err := tree.Wait(ctx); err != nil {
		t.Fatalf("retained startup Wait() error = %v", err)
	}
	if err := tree.Release(); err != nil {
		t.Fatalf("retained startup Release() error = %v", err)
	}
}
