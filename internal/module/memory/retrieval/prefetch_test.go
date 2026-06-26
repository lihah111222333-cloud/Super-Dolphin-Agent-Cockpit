package retrieval

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPrefetchManagerConsumeIfReady(t *testing.T) {
	manager := NewPrefetchManager("/tmp/memory-root")
	manager.buildManifest = func(string) ([]MemoryEntry, error) {
		return []MemoryEntry{{FilePath: "alpha.md"}}, nil
	}
	manager.findRelevant = func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error) {
		time.Sleep(20 * time.Millisecond)
		return []MemoryEntry{{FilePath: "alpha.md", Content: "alpha"}}, nil
	}

	handle := manager.StartRelevantMemoryPrefetch(context.Background(), "review")
	if got, ok := manager.ConsumeIfReady(handle); ok || got != nil {
		t.Fatalf("ConsumeIfReady() before ready = (%v, %v), want (nil, false)", got, ok)
	}
	waitForHandle(t, handle)

	got, ok := manager.ConsumeIfReady(handle)
	if !ok {
		t.Fatalf("ConsumeIfReady() ready = false, want true")
	}
	if len(got) != 1 || got[0].Content != "alpha" {
		t.Fatalf("ConsumeIfReady() entries = %#v, want hydrated alpha entry", got)
	}
	if _, ok := manager.ConsumeIfReady(handle); ok {
		t.Fatalf("ConsumeIfReady() should be idempotent")
	}
}

func TestPrefetchManagerCancelsTurnScopedContext(t *testing.T) {
	manager := NewPrefetchManager("/tmp/memory-root")
	exited := make(chan struct{})
	manager.buildManifest = func(string) ([]MemoryEntry, error) {
		return []MemoryEntry{{FilePath: "alpha.md"}}, nil
	}
	manager.findRelevant = func(ctx context.Context, _ string, _ []MemoryEntry) ([]MemoryEntry, error) {
		defer close(exited)
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	handle := manager.StartRelevantMemoryPrefetch(ctx, "review")
	cancel()
	waitForHandle(t, handle)
	waitForSignal(t, exited)

	if _, ok := manager.ConsumeIfReady(handle); ok {
		t.Fatalf("ConsumeIfReady() canceled handle = true, want false")
	}
	if state := handle.state.Load(); state != PrefetchStateDiscarded {
		t.Fatalf("prefetch state = %d, want discarded", state)
	}
	if !errors.Is(handle.err, context.Canceled) {
		t.Fatalf("prefetch err = %v, want %v", handle.err, context.Canceled)
	}
}

func TestPrefetchManagerConsumeIfReadyDoesNotHideLookupErrors(t *testing.T) {
	tests := []struct {
		name          string
		buildManifest func(string) ([]MemoryEntry, error)
		findRelevant  func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error)
	}{
		{
			name: "build manifest error",
			buildManifest: func(string) ([]MemoryEntry, error) {
				return nil, errors.New("manifest unavailable")
			},
			findRelevant: func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error) {
				return []MemoryEntry{{FilePath: "should-not-run.md"}}, nil
			},
		},
		{
			name: "find relevant error",
			buildManifest: func(string) ([]MemoryEntry, error) {
				return []MemoryEntry{{FilePath: "alpha.md"}}, nil
			},
			findRelevant: func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error) {
				return nil, errors.New("finder unavailable")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewPrefetchManager("/tmp/memory-root")
			manager.buildManifest = tt.buildManifest
			manager.findRelevant = tt.findRelevant

			handle := manager.StartRelevantMemoryPrefetch(context.Background(), "review")
			waitForHandle(t, handle)
			if _, ok := manager.ConsumeIfReady(handle); ok {
				t.Fatalf("ConsumeIfReady() ok = true, want false when Err() = %v", handle.Err())
			}
			if handle.Err() == nil {
				t.Fatal("PrefetchHandle.Err() = nil, want lookup error")
			}
		})
	}
}

func TestPrefetchManagerDiscardsStaleGeneration(t *testing.T) {
	manager := NewPrefetchManager("/tmp/memory-root")
	releaseFirst := make(chan struct{})
	manager.buildManifest = func(string) ([]MemoryEntry, error) {
		return []MemoryEntry{{FilePath: "alpha.md"}}, nil
	}
	manager.findRelevant = func(_ context.Context, query string, _ []MemoryEntry) ([]MemoryEntry, error) {
		if query == "first" {
			<-releaseFirst
			return []MemoryEntry{{FilePath: "first.md", Content: "first"}}, nil
		}
		return []MemoryEntry{{FilePath: "second.md", Content: "second"}}, nil
	}

	first := manager.StartRelevantMemoryPrefetch(context.Background(), "first")
	second := manager.StartRelevantMemoryPrefetch(context.Background(), "second")
	close(releaseFirst)
	waitForHandle(t, first)
	waitForHandle(t, second)

	if _, ok := manager.ConsumeIfReady(first); ok {
		t.Fatalf("ConsumeIfReady() stale handle = true, want false")
	}
	got, ok := manager.ConsumeIfReady(second)
	if !ok {
		t.Fatalf("ConsumeIfReady() latest handle = false, want true")
	}
	if len(got) != 1 || got[0].FilePath != "second.md" {
		t.Fatalf("ConsumeIfReady() entries = %#v, want second generation result", got)
	}
	if state := first.state.Load(); state != PrefetchStateDiscarded {
		t.Fatalf("first prefetch state = %d, want discarded", state)
	}
}

func TestPrefetchManagerResetClearsSurfacedAndDiscardsCurrentHandle(t *testing.T) {
	manager := NewPrefetchManager("/tmp/memory-root")
	entry := MemoryEntry{FilePath: "alpha.md", Content: "alpha"}
	manager.buildManifest = func(string) ([]MemoryEntry, error) {
		return []MemoryEntry{{FilePath: "alpha.md"}}, nil
	}
	manager.findRelevant = func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error) {
		return []MemoryEntry{entry}, nil
	}
	manager.MarkSurfaced([]MemoryEntry{entry})

	handle := manager.StartRelevantMemoryPrefetch(context.Background(), "review")
	waitForHandle(t, handle)
	manager.Reset("compact")

	if got := manager.FilterAlreadySurfaced([]MemoryEntry{entry}); len(got) != 1 {
		t.Fatalf("FilterAlreadySurfaced() after reset = %#v, want original entry", got)
	}
	if _, ok := manager.ConsumeIfReady(handle); ok {
		t.Fatalf("ConsumeIfReady() reset handle = true, want false")
	}
	if state := handle.state.Load(); state != PrefetchStateDiscarded {
		t.Fatalf("prefetch state after reset = %d, want discarded", state)
	}
	if manager.current != nil {
		t.Fatalf("manager.current = %#v, want nil after reset", manager.current)
	}
}

func waitForHandle(t *testing.T, handle *PrefetchHandle) {
	t.Helper()
	select {
	case <-handle.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prefetch handle")
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
