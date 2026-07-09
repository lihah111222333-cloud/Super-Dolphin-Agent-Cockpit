package toolbridge

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/kelindar/event"
)

func TestNewToolbridgeDiffFallbackSubscribersSpec(t *testing.T) {
	t.Parallel()

	tracker := newDiffFallbackTracker(nil, nil, nil)
	spec := NewToolbridgeDiffFallbackSubscribers(tracker).Spec

	if spec.EventType != "toolbridge.diff.fallback" {
		t.Fatalf("EventType = %q", spec.EventType)
	}
	if spec.HandlerSymbol != "toolbridge.tracker.handleToolCallEnd" {
		t.Fatalf("HandlerSymbol = %q", spec.HandlerSymbol)
	}
	if spec.OwnerModule != "toolbridge" {
		t.Fatalf("OwnerModule = %q", spec.OwnerModule)
	}
	if spec.CancelOwner != "bus.SubscriberGroup" {
		t.Fatalf("CancelOwner = %q", spec.CancelOwner)
	}
	if spec.ShutdownClass != "bus-subscriber" {
		t.Fatalf("ShutdownClass = %q", spec.ShutdownClass)
	}
	if spec.TestFixtureID != "toolbridge-diff-fallback-subscribers" {
		t.Fatalf("TestFixtureID = %q", spec.TestFixtureID)
	}
	if spec.Register == nil {
		t.Fatal("Register must be non-nil")
	}
}

func TestToolbridgeDiffFallbackSubscribersRegisterCancelAndDeliver(t *testing.T) {
	t.Parallel()

	repo := initGitRepo(t, map[string]string{"tracked.txt": "before\n"})
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "after\n")

	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })

	var (
		mu      sync.Mutex
		emitted []difftracker.DiffResult
	)
	tracker := newDiffFallbackTracker(func(_ context.Context, diff difftracker.DiffResult) error {
		mu.Lock()
		defer mu.Unlock()
		emitted = append(emitted, diff)
		return nil
	}, resolverFunc(func(context.Context, string) (string, error) { return repo, nil }), nil)
	tracker.readCurrentDiff = func(context.Context, string) (string, []string, bool) {
		return "diff --git a/tracked.txt b/tracked.txt\n", []string{"tracked.txt"}, true
	}
	spec := NewToolbridgeDiffFallbackSubscribers(tracker).Spec

	cancel := spec.Register(dispatcher)
	if cancel == nil {
		t.Fatal("Register returned nil cancel")
	}

	event.Publish(dispatcher, diffFallbackEvent("agent-1", "thread-1", "call-1", "patch_edit"))
	waitForToolbridgeFallbackEmits(t, &mu, &emitted, 1)

	cancel()
	cancel()

	event.Publish(dispatcher, diffFallbackEvent("agent-1", "thread-1", "call-after-cancel", "patch_edit"))
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	got := len(emitted)
	mu.Unlock()
	if got != 1 {
		t.Fatalf("emitted count after cancel = %d, want 1", got)
	}
}

func waitForToolbridgeFallbackEmits(t *testing.T, mu *sync.Mutex, emitted *[]difftracker.DiffResult, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(*emitted)
		mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	mu.Lock()
	got := len(*emitted)
	mu.Unlock()
	t.Fatalf("emitted count = %d, want %d", got, want)
}

var _ = tooldto.ToolCallEnd{}
