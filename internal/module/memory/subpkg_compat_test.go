package memory

import (
	"testing"
	"time"

	teampkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/team"
)

func withTeamMemoryRuntimeReady(t *testing.T, ready bool) {
	t.Helper()
	restore := teampkg.SwapRuntimeReadyFuncForTest(func() bool { return ready })
	t.Cleanup(restore)
}

func waitForHandle(t *testing.T, handle *PrefetchHandle) {
	t.Helper()
	select {
	case <-handle.Done():
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for prefetch handle")
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for signal")
	}
}
