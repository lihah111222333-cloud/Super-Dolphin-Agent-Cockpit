//go:build darwin || linux

package hiddenexec

import (
	"context"
	"errors"
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
	defer func() {
		ctx, cancel := platformconfig.WithTimeout(context.Background(), 4*time.Second)
		_ = tree.Wait(ctx)
		cancel()
		_ = tree.Release()
	}()
	time.Sleep(100 * time.Millisecond)
	owner := tree.controller.(*unixProcessTree)
	signalCount := 0
	owner.signalMembers = func([]ProcessIdentity, int) error {
		signalCount++
		return nil
	}
	prepareErr := tree.PrepareShutdown()
	if !errors.Is(prepareErr, ErrProcessTreeIdentityMismatch) || !errors.Is(prepareErr, ErrProcessTreeCleanupPending) {
		t.Fatalf("PrepareShutdown() error = %v, want fail-closed identity mismatch and CleanupPending", prepareErr)
	}
	snapshot, err := tree.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	t.Logf("dynamic orphan process tree: root=%+v members=%+v unknown=%+v", identity, snapshot.Members, snapshot.Unknown)
	if len(snapshot.Unknown) == 0 {
		t.Fatal("dynamic orphan process tree did not expose unknown member")
	}
	if err := tree.Force(context.Background()); !errors.Is(err, ErrProcessTreeIdentityMismatch) || !errors.Is(err, ErrProcessTreeCleanupPending) {
		t.Fatalf("Force() error = %v, want fail-closed identity mismatch and CleanupPending", err)
	}
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
	defer func() {
		ctx, cancel := platformconfig.WithTimeout(context.Background(), 4*time.Second)
		_ = tree.Force(ctx)
		_ = tree.Wait(ctx)
		cancel()
		_ = cmd.Wait()
	}()
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
	var signaled []ProcessIdentity
	owner := tree.controller.(*unixProcessTree)
	owner.signalMembers = func(members []ProcessIdentity, signal int) error {
		signaled = append(signaled, members...)
		return signalProcessMembers(members, signal)
	}
	if err := tree.Force(ctx); err != nil {
		t.Fatalf("Force() enrolled descendant: %v", err)
	}
	for _, member := range signaled {
		if member.PID == prepared.Root.PID {
			t.Fatalf("Force() signaled exited root PID %d", member.PID)
		}
		found := false
		for _, enrolled := range prepared.Members {
			if member.Equal(enrolled) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Force() signaled un-enrolled member: %+v, enrolled=%+v", member, prepared.Members)
		}
	}
	if len(signaled) == 0 {
		t.Fatal("Force() sent no signal to enrolled descendant")
	}
	if err := tree.Wait(ctx); err != nil {
		t.Fatalf("Wait() enrolled descendant: %v", err)
	}
}
