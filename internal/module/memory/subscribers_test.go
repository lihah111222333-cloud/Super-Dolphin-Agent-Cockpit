package memory

import (
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"
)

func TestNewMemorySubscribersSpec(t *testing.T) {
	t.Parallel()

	spec := NewMemorySubscribers(nil, nil, nil, memorySubscriberParams{}).Spec

	if spec.EventType != "memory.lifecycle" {
		t.Fatalf("EventType = %q", spec.EventType)
	}
	if spec.HandlerSymbol != "memory.registerLifecycleSubscriptions" {
		t.Fatalf("HandlerSymbol = %q", spec.HandlerSymbol)
	}
	if spec.OwnerModule != "memory" {
		t.Fatalf("OwnerModule = %q", spec.OwnerModule)
	}
	if spec.CancelOwner != "bus.SubscriberGroup" {
		t.Fatalf("CancelOwner = %q", spec.CancelOwner)
	}
	if spec.ShutdownClass != "bus-subscriber" {
		t.Fatalf("ShutdownClass = %q", spec.ShutdownClass)
	}
	if spec.TestFixtureID != "memory-lifecycle-subscribers" {
		t.Fatalf("TestFixtureID = %q", spec.TestFixtureID)
	}
	if spec.Register == nil {
		t.Fatal("Register must be non-nil")
	}
}

func TestMemorySubscribersRegisterCancelAndEnqueueTeamSync(t *testing.T) {
	t.Parallel()

	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	repoRoot := t.TempDir()
	store := &stubThreadMetadataStore{meta: &contract.ThreadMetadata{ThreadID: "thread-1", Cwd: repoRoot}}
	syncer := &recordingTeamSync{}
	coordinator := newTeamSyncCoordinator(syncer, store, nil)
	spec := NewMemorySubscribers(nil, nil, coordinator, memorySubscriberParams{ThreadStore: store, TeamSync: syncer}).Spec

	cancel := spec.Register(dispatcher)
	if cancel == nil {
		t.Fatal("Register returned nil cancel")
	}

	event.Publish(dispatcher, threaddto.Started{ThreadID: "thread-1", CWD: repoRoot})
	event.Publish(dispatcher, threaddto.Stopped{ThreadID: "thread-1"})
	waitForTeamSyncEnqueued(t, coordinator, 2)

	cancel()
	cancel()
	event.Publish(dispatcher, threaddto.Started{ThreadID: "thread-after-cancel", CWD: repoRoot})
	time.Sleep(50 * time.Millisecond)
	if got := coordinator.EnqueuedTotal(); got != 2 {
		t.Fatalf("EnqueuedTotal after cancel = %d, want 2", got)
	}
}

func waitForTeamSyncEnqueued(t *testing.T, coordinator *teamSyncCoordinator, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if coordinator.EnqueuedTotal() == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("EnqueuedTotal = %d, want %d", coordinator.EnqueuedTotal(), want)
}
