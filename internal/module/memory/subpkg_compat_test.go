package memory

import (
	"testing"
	"time"
)

func withTeamMemoryRuntimeReady(t *testing.T, manager *TeamMemoryManager, ready bool) {
	t.Helper()
	if manager == nil {
		t.Fatal("team memory manager is required")
	}
	manager.SetRuntimeReady(ready)
	t.Cleanup(func() {
		manager.SetRuntimeReady(false)
	})
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
