//go:build windows

package localci

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSchedulerCurrentUIDExclusiveStorageFailsFastOnWindows(t *testing.T) {
	t.Parallel()

	identity := daemonIdentity{key: "windows-unsupported", ownerUID: 0}
	privateDir := t.TempDir()
	lockPath := filepath.Join(privateDir, "scheduler.lock")
	statePath := filepath.Join(privateDir, "state.db")

	if _, err := acquireSchedulerLock(lockPath, identity); !errors.Is(err, errSchedulerPlatformUnsupported) {
		t.Fatalf("lock error=%v want=%v", err, errSchedulerPlatformUnsupported)
	}
	if _, err := openSchedulerState(context.Background(), statePath, identity); !errors.Is(err, errSchedulerPlatformUnsupported) {
		t.Fatalf("state error=%v want=%v", err, errSchedulerPlatformUnsupported)
	}
	if _, err := OpenScheduler(context.Background(), SchedulerConfig{
		Endpoint: "unix:///var/run/docker.sock",
		DaemonID: "windows-unsupported",
		OwnerUID: 0,
	}); !errors.Is(err, errSchedulerPlatformUnsupported) {
		t.Fatalf("facade error=%v want=%v", err, errSchedulerPlatformUnsupported)
	}
	for _, path := range []string{lockPath, statePath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unsupported scheduler created %q: %v", path, err)
		}
	}
	entries, err := os.ReadDir(privateDir)
	if err != nil {
		t.Fatalf("read unsupported runtime root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("unsupported scheduler left runtime files: %v", entries)
	}
}
