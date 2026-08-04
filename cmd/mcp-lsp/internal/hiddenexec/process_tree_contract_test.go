//go:build darwin || linux || windows

package hiddenexec

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestProcessTreeProvidesIdentitySnapshotAndBoundedLifecycle(t *testing.T) {
	name, args := treeTestCommand()
	cmd := Command(name, args...)
	tree, err := StartProcessTree(cmd)
	if err != nil {
		t.Fatalf("StartProcessTree() error = %v", err)
	}
	defer func() {
		_ = tree.Force(context.Background())
		_ = cmd.Wait()
	}()
	identity := requireProcessTreeIdentity(t, tree)
	requireProcessTreeSnapshot(t, tree, identity)
	requireProcessTreeAlive(t, tree)
	requireProcessTreeShutdown(t, tree)
}

func requireProcessTreeIdentity(t *testing.T, tree *ProcessTree) ProcessIdentity {
	t.Helper()
	identity, err := tree.Identity()
	if err != nil {
		t.Fatalf("Identity() error = %v", err)
	}
	if identity.PID <= 1 || identity.StartToken == "" {
		t.Fatalf("Identity() = %+v, want a stable process identity", identity)
	}
	return identity
}

func requireProcessTreeSnapshot(t *testing.T, tree *ProcessTree, identity ProcessIdentity) {
	t.Helper()
	snapshot, err := tree.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !snapshot.Root.Equal(identity) {
		t.Fatalf("Snapshot().Root = %+v, want %+v", snapshot.Root, identity)
	}
}

func requireProcessTreeAlive(t *testing.T, tree *ProcessTree) {
	t.Helper()
	alive, err := tree.Alive()
	if err != nil {
		t.Fatalf("Alive() error = %v", err)
	}
	if !alive {
		t.Fatal("Alive() = false before shutdown")
	}
}

func requireProcessTreeShutdown(t *testing.T, tree *ProcessTree) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if isWindows() {
		if err := tree.Graceful(ctx); err == nil {
			t.Fatal("Graceful() error = nil on Windows, want unsupported TERM phase")
		}
		if err := tree.Force(ctx); err != nil {
			t.Fatalf("Force() error = %v", err)
		}
	} else if err := tree.Graceful(ctx); err != nil {
		t.Fatalf("Graceful() error = %v", err)
	}
	if err := tree.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	remaining, err := tree.Remaining()
	if err != nil {
		t.Fatalf("Remaining() error = %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("Remaining() = %+v, want empty after bounded wait", remaining)
	}
}

func treeTestCommand() (string, []string) {
	if isWindows() {
		return "cmd.exe", []string{"/c", "ping", "-n", "30", "127.0.0.1", ">", "NUL"}
	}
	return "/bin/sleep", []string{"30"}
}

func isWindows() bool {
	return runtime.GOOS == "windows"
}
