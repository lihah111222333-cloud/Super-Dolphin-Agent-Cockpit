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

	identity, err := tree.Identity()
	if err != nil {
		t.Fatalf("Identity() error = %v", err)
	}
	if identity.PID <= 1 || identity.StartToken == "" {
		t.Fatalf("Identity() = %+v, want a stable process identity", identity)
	}

	snapshot, err := tree.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !snapshot.Root.Equal(identity) {
		t.Fatalf("Snapshot().Root = %+v, want %+v", snapshot.Root, identity)
	}

	alive, err := tree.Alive()
	if err != nil {
		t.Fatalf("Alive() error = %v", err)
	}
	if !alive {
		t.Fatal("Alive() = false before shutdown")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tree.Graceful(ctx); err != nil {
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
