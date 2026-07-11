package insight

import (
	"testing"
	"time"

	"github.com/kelindar/event"
	sharedto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// The collector is a tight synchronous path: enqueue succeeds while the
// channel has room, drops the signal + bumps the Dropped counter when
// the channel is full, and rejects empty turn ids outright.

func TestCollectorEnqueueDropsOnFullQueue(t *testing.T) {
	t.Parallel()
	c := newCollector(nil, 1)
	c.enqueueTerminal("turn-1", "thread-1", "agent-1", "", time.Time{})
	if c.Dropped() != 0 {
		t.Fatalf("first enqueue should not drop; dropped=%d", c.Dropped())
	}
	c.enqueueTerminal("turn-2", "thread-1", "agent-1", "", time.Time{})
	if c.Dropped() != 1 {
		t.Fatalf("second enqueue on full queue should drop; dropped=%d", c.Dropped())
	}
	// Drain one and confirm the next enqueue goes through again.
	<-c.queue
	c.enqueueTerminal("turn-3", "thread-1", "agent-1", "", time.Time{})
	if c.Dropped() != 1 {
		t.Fatalf("enqueue after drain should not drop; dropped=%d", c.Dropped())
	}
}

func TestCollectorRejectsEmptyTurnID(t *testing.T) {
	t.Parallel()
	c := newCollector(nil, 4)
	c.enqueueTerminal("   ", "thread-1", "agent-1", "", time.Time{})
	c.enqueueTerminal("", "thread-1", "agent-1", "", time.Time{})
	if len(c.queue) != 0 {
		t.Fatalf("empty turn_id must not be enqueued; queue=%d", len(c.queue))
	}
	if c.Dropped() != 0 {
		t.Fatalf("empty turn_id should not count as dropped; dropped=%d", c.Dropped())
	}
}

func TestCollectorTrimsIdentityFields(t *testing.T) {
	t.Parallel()
	c := newCollector(nil, 4)
	c.enqueueTerminal("  turn-1  ", " thread-1 ", " agent-1 ", " codex ", time.Unix(123, 0).UTC())
	sig := <-c.queue
	if sig.LocalTurnID != "turn-1" || sig.ThreadID != "thread-1" || sig.AgentID != "agent-1" || sig.Provider != "codex" {
		t.Fatalf("identity not trimmed: %+v", sig)
	}
	if !sig.Timestamp.Equal(time.Unix(123, 0).UTC()) {
		t.Fatalf("timestamp not preserved: %+v", sig)
	}
}

func TestCollectorSubscribeCarriesTimestamp(t *testing.T) {
	t.Parallel()
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()
	c := newCollector(nil, 4)
	cancel := c.subscribe(dispatcher, nil)
	defer cancel()

	stamp := time.Unix(456, 0).UTC()
	event.Publish(dispatcher, turndto.TurnCompleted{
		TurnHeader: sharedto.TurnHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{EventHeader: sharedto.EventHeader{Timestamp: stamp}, ThreadID: "thread-1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-1"},
		},
		Success: true,
	})

	select {
	case sig := <-c.queue:
		if !sig.Timestamp.Equal(stamp) {
			t.Fatalf("timestamp = %v, want %v", sig.Timestamp, stamp)
		}
		if sig.Provider != "" {
			t.Fatalf("current turn DTO has no provider field; got %q", sig.Provider)
		}
	case <-time.After(time.Second):
		t.Fatal("collector did not enqueue published terminal event")
	}
}

func TestEventProviderReadsOptionalProviderField(t *testing.T) {
	t.Parallel()
	got := eventProvider(struct{ Provider string }{Provider: "codex"})
	if got != "codex" {
		t.Fatalf("eventProvider = %q, want codex", got)
	}
}
