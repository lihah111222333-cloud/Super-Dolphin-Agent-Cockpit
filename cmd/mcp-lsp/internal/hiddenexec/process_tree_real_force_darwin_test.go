//go:build darwin

package hiddenexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
)

func assertPreparedForceOutcome(t *testing.T, tree *ProcessTree, _ *exec.Cmd, owner *unixProcessTree, prepared ProcessTreeSnapshot, ctx context.Context) {
	t.Helper()
	owner.signalMembers = signalProcessMembers
	if err := tree.Force(ctx); !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("Darwin Force() enrolled descendant error = %v, want CleanupPending", err)
	}
	remaining, err := tree.Remaining()
	if err != nil {
		t.Fatalf("Remaining() after blocked Darwin Force = %v", err)
	}
	for _, member := range remaining {
		if member.PID == prepared.Root.PID {
			continue
		}
		process, findErr := os.FindProcess(member.PID)
		if findErr != nil {
			t.Fatalf("find test descendant %d for explicit cleanup: %v", member.PID, findErr)
		}
		if killErr := process.Kill(); killErr != nil {
			t.Fatalf("test cleanup kill descendant %d: %v", member.PID, killErr)
		}
	}
	if err := tree.Wait(ctx); err != nil {
		t.Fatalf("Wait() after explicit Darwin fixture cleanup: %v", err)
	}
}
