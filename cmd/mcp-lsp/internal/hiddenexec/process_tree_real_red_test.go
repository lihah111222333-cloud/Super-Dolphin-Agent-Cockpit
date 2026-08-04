//go:build darwin || linux

package hiddenexec

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

// TestDynamicOrphanProcessTreeUnknownMemberBlocksSignal 锁定根退出后未入册同组成员的 zero-signal 结论。
func TestDynamicOrphanProcessTreeUnknownMemberBlocksSignal(t *testing.T) {
	cmd := Command("/bin/sh", "-c", "sleep 2 & exit 0")
	tree, err := StartProcessTree(cmd)
	if err != nil {
		t.Fatalf("StartProcessTree() error = %v", err)
	}
	identity, err := tree.Identity()
	if err != nil {
		t.Fatalf("Identity() error = %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("root shell Wait() error = %v", err)
	}
	defer releaseExitedTree(tree)
	time.Sleep(100 * time.Millisecond)
	owner := tree.controller.(*unixProcessTree)
	signalCount := 0
	owner.signalMembers = func([]ProcessIdentity, int) error {
		signalCount++
		return nil
	}
	assertCleanupPending(t, "PrepareShutdown", tree.PrepareShutdown())
	snapshot, err := tree.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	t.Logf("dynamic orphan process tree: root=%+v members=%+v unknown=%+v", identity, snapshot.Members, snapshot.Unknown)
	assertUnknownMember(t, snapshot)
	assertCleanupPending(t, "Force", tree.Force(context.Background()))
	if signalCount != 0 {
		t.Fatalf("unknown member received signal attempts = %d, want zero", signalCount)
	}
}

// TestPrepareShutdownEnrollsDynamicDescendantBeforeRootExit 验证协议 shutdown 前入册可覆盖父进程重挂窗口。
func TestPrepareShutdownEnrollsDynamicDescendantBeforeRootExit(t *testing.T) {
	cmd := Command("/bin/sh", "-c", "sleep 3 & sleep 1")
	tree, err := StartProcessTree(cmd)
	if err != nil {
		t.Fatalf("StartProcessTree() error = %v", err)
	}
	defer forceAndWaitTree(tree, cmd)
	time.Sleep(100 * time.Millisecond)
	if err := tree.PrepareShutdown(); err != nil {
		t.Fatalf("PrepareShutdown() error = %v", err)
	}
	prepared, err := tree.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() after PrepareShutdown() = %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("root shell Wait() error = %v", err)
	}
	remaining, err := tree.Remaining()
	if err != nil {
		t.Fatalf("Remaining() after root exit = %v", err)
	}
	if len(remaining) == 0 {
		t.Fatal("expected enrolled descendant to remain after root exit")
	}
	ctx, cancel := platformconfig.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	owner := tree.controller.(*unixProcessTree)
	assertPreparedForceOutcome(t, tree, cmd, owner, prepared, ctx)
}

func releaseExitedTree(tree *ProcessTree) {
	ctx, cancel := platformconfig.WithTimeout(context.Background(), 4*time.Second)
	_ = tree.Wait(ctx)
	cancel()
	_ = tree.Release()
}

func forceAndWaitTree(tree *ProcessTree, cmd *exec.Cmd) {
	ctx, cancel := platformconfig.WithTimeout(context.Background(), 4*time.Second)
	_ = tree.Force(ctx)
	_ = tree.Wait(ctx)
	cancel()
	_ = cmd.Wait()
}

func assertCleanupPending(t *testing.T, operation string, err error) {
	t.Helper()
	if errors.Is(err, ErrProcessTreeIdentityMismatch) && errors.Is(err, ErrProcessTreeCleanupPending) {
		return
	}
	t.Fatalf("%s() error = %v, want fail-closed identity mismatch and CleanupPending", operation, err)
}

func assertUnknownMember(t *testing.T, snapshot ProcessTreeSnapshot) {
	t.Helper()
	if len(snapshot.Unknown) == 0 {
		t.Fatal("dynamic orphan process tree did not expose unknown member")
	}
}

func assertEnrolledMembersSignaled(t *testing.T, signaled []ProcessIdentity, prepared ProcessTreeSnapshot) {
	t.Helper()
	if len(signaled) == 0 {
		t.Fatal("Force() sent no signal to enrolled descendant")
	}
	for _, member := range signaled {
		if member.PID == prepared.Root.PID {
			t.Fatalf("Force() signaled exited root PID %d", prepared.Root.PID)
		}
		if !containsProcessIdentity(prepared.Members, member) {
			t.Fatalf("Force() signaled un-enrolled member: %+v, enrolled=%+v", member, prepared.Members)
		}
	}
}

func containsProcessIdentity(members []ProcessIdentity, target ProcessIdentity) bool {
	for _, member := range members {
		if member.Equal(target) {
			return true
		}
	}
	return false
}
