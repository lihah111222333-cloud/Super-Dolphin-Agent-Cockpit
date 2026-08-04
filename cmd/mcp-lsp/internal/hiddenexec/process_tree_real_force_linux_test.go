//go:build linux

package hiddenexec

import (
	"context"
	"os/exec"
	"testing"
)

func assertPreparedForceOutcome(t *testing.T, tree *ProcessTree, _ *exec.Cmd, owner *unixProcessTree, prepared ProcessTreeSnapshot, ctx context.Context) {
	t.Helper()
	var signaled []ProcessIdentity
	owner.signalMembers = func(members []ProcessIdentity, signal int) error {
		signaled = append(signaled, members...)
		return signalProcessMembers(members, signal)
	}
	if err := tree.Force(ctx); err != nil {
		t.Fatalf("Force() enrolled descendant: %v", err)
	}
	assertEnrolledMembersSignaled(t, signaled, prepared)
	if err := tree.Wait(ctx); err != nil {
		t.Fatalf("Wait() enrolled descendant: %v", err)
	}
}
