package insight

import (
	"testing"
	"time"

	"github.com/kelindar/event"
	sharedto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
)

func TestNewInsightSubscribersSpec(t *testing.T) {
	t.Parallel()

	c := newCollector(nil, 4)
	result := NewInsightSubscribers(c, nil)
	spec := result.Spec

	if spec.EventType != "turn.terminal" {
		t.Fatalf("EventType = %q", spec.EventType)
	}
	if spec.HandlerSymbol != "insight.collector.enqueueTerminal" {
		t.Fatalf("HandlerSymbol = %q", spec.HandlerSymbol)
	}
	if spec.OwnerModule != "insight" {
		t.Fatalf("OwnerModule = %q", spec.OwnerModule)
	}
	if spec.CancelOwner != "bus.SubscriberGroup" {
		t.Fatalf("CancelOwner = %q", spec.CancelOwner)
	}
	if spec.ShutdownClass != "bus-subscriber" {
		t.Fatalf("ShutdownClass = %q", spec.ShutdownClass)
	}
	if spec.TestFixtureID != "insight-subscribers" {
		t.Fatalf("TestFixtureID = %q", spec.TestFixtureID)
	}
	if spec.Register == nil {
		t.Fatal("Register must be non-nil")
	}
}

func TestInsightSubscribersRegisterCancelAndDeliver(t *testing.T) {
	t.Parallel()

	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	c := newCollector(nil, 4)
	spec := NewInsightSubscribers(c, nil).Spec

	cancel := spec.Register(dispatcher)
	if cancel == nil {
		t.Fatal("Register returned nil cancel")
	}

	event.Publish(dispatcher, turndto.TurnCompleted{
		TurnHeader: insightTestTurnHeader("thread-1", "turn-1", "agent-1"),
		Success:    true,
	})
	waitForInsightQueueDepth(t, c, 1)

	cancel()
	cancel()

	event.Publish(dispatcher, turndto.TurnInterrupted{
		TurnHeader: insightTestTurnHeader("thread-1", "turn-after-cancel", "agent-1"),
		Reason:     "test",
	})
	time.Sleep(50 * time.Millisecond)
	if got := len(c.queue); got != 1 {
		t.Fatalf("queue depth after cancel = %d, want 1", got)
	}
}

func insightTestTurnHeader(threadID, turnID, agentID string) sharedto.TurnHeader {
	return sharedto.TurnHeader{
		AgentHeader: sharedto.AgentHeader{
			ThreadHeader: sharedto.ThreadHeader{ThreadID: threadID},
			AgentID:      agentID,
		},
		TurnIDHeader: sharedto.TurnIDHeader{TurnID: turnID},
	}
}

func waitForInsightQueueDepth(t *testing.T, c *collector, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(c.queue) == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("queue depth = %d, want %d", len(c.queue), want)
}
