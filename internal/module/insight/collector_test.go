package insight

import (
	"testing"
)

// The collector is a tight synchronous path: enqueue succeeds while the
// channel has room, drops the signal + bumps the Dropped counter when
// the channel is full, and rejects empty turn ids outright.

func TestCollectorEnqueueDropsOnFullQueue(t *testing.T) {
	t.Parallel()
	c := newCollector(nil, 1)
	c.enqueueTerminal("turn-1", "thread-1", "agent-1")
	if c.Dropped() != 0 {
		t.Fatalf("first enqueue should not drop; dropped=%d", c.Dropped())
	}
	c.enqueueTerminal("turn-2", "thread-1", "agent-1")
	if c.Dropped() != 1 {
		t.Fatalf("second enqueue on full queue should drop; dropped=%d", c.Dropped())
	}
	// Drain one and confirm the next enqueue goes through again.
	<-c.queue
	c.enqueueTerminal("turn-3", "thread-1", "agent-1")
	if c.Dropped() != 1 {
		t.Fatalf("enqueue after drain should not drop; dropped=%d", c.Dropped())
	}
}

func TestCollectorRejectsEmptyTurnID(t *testing.T) {
	t.Parallel()
	c := newCollector(nil, 4)
	c.enqueueTerminal("   ", "thread-1", "agent-1")
	c.enqueueTerminal("", "thread-1", "agent-1")
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
	c.enqueueTerminal("  turn-1  ", " thread-1 ", " agent-1 ")
	sig := <-c.queue
	if sig.LocalTurnID != "turn-1" || sig.ThreadID != "thread-1" || sig.AgentID != "agent-1" {
		t.Fatalf("identity not trimmed: %+v", sig)
	}
}
