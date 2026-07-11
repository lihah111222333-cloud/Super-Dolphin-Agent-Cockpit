package observation

import (
	"testing"
	"time"

	"github.com/kelindar/event"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	sharedto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
)

func newSubscribedTest(t *testing.T) (*Memory, *event.Dispatcher) {
	t.Helper()
	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	mem := NewMemory()
	cancel := Subscribe(dispatcher, mem, nil)
	t.Cleanup(cancel)
	return mem, dispatcher
}

func turnHeader(threadID, turnID string) sharedto.TurnHeader {
	return sharedto.TurnHeader{
		AgentHeader:  sharedto.AgentHeader{ThreadHeader: sharedto.ThreadHeader{ThreadID: threadID}},
		TurnIDHeader: sharedto.TurnIDHeader{TurnID: turnID},
	}
}

// waitForTerminal polls observation Memory until a terminal for turnID exists
// with the expected kind, or fails the test after the deadline. Needed
// because kelindar/event dispatch is asynchronous per subscriber.
func waitForTerminal(t *testing.T, mem *Memory, turnID string, want TerminalKind) Terminal {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if tr, ok := mem.Terminal(turnID); ok && tr.Kind == want {
			return tr
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("terminal for %q did not reach kind=%s in time; got=%+v", turnID, want, mustTerminal(mem, turnID))
	return Terminal{}
}

func mustTerminal(mem *Memory, turnID string) Terminal {
	tr, _ := mem.Terminal(turnID)
	return tr
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("wait timed out: %s", msg)
}

func TestSubscribeRecordsTurnCompletedSuccess(t *testing.T) {
	t.Parallel()

	mem, d := newSubscribedTest(t)
	event.Publish(d, turndto.TurnCompleted{
		TurnHeader: turnHeader("thread-1", "turn-1"),
		Success:    true,
	})
	tr := waitForTerminal(t, mem, "turn-1", TerminalCompleted)
	if tr.Success == nil || !*tr.Success {
		t.Fatalf("Success must be true for completed turn, got=%+v", tr)
	}
}

func TestSubscribeTurnCompletedStatusOverridesSuccess(t *testing.T) {
	t.Parallel()

	mem, d := newSubscribedTest(t)
	// TurnCompleted with Status=interrupted must become a locked
	// interrupted terminal even though Success defaults to false. This
	// guards against the non-pointer-bool "default true" trap called out
	// by the P0b plan (here Success is false, but the test also ensures
	// that even with Success=true and Status=interrupted, interrupted
	// wins).
	event.Publish(d, turndto.TurnCompleted{
		TurnHeader: turnHeader("thread-1", "turn-2"),
		Success:    true,
		Status:     "interrupted",
		Reason:     "user",
	})
	tr := waitForTerminal(t, mem, "turn-2", TerminalInterrupted)
	if tr.Reason != "user" {
		t.Fatalf("reason = %q, want user", tr.Reason)
	}
}

func TestSubscribeTurnCompletedFailureWithoutStatus(t *testing.T) {
	t.Parallel()

	mem, d := newSubscribedTest(t)
	event.Publish(d, turndto.TurnCompleted{
		TurnHeader: turnHeader("t", "turn-fail"),
		Success:    false,
		Error:      "boom",
	})
	tr := waitForTerminal(t, mem, "turn-fail", TerminalFailed)
	if tr.Reason != "boom" {
		t.Fatalf("reason = %q, want boom", tr.Reason)
	}
}

func TestSubscribeTurnInterruptedIsSticky(t *testing.T) {
	t.Parallel()

	mem, d := newSubscribedTest(t)
	event.Publish(d, turndto.TurnInterrupted{
		TurnHeader: turnHeader("t", "turn-i"),
		Reason:     "user",
	})
	waitForTerminal(t, mem, "turn-i", TerminalInterrupted)

	// Late TurnCompleted must not displace the sticky interrupted state.
	event.Publish(d, turndto.TurnCompleted{
		TurnHeader: turnHeader("t", "turn-i"),
		Success:    true,
	})
	// Give dispatcher a window to misbehave.
	time.Sleep(50 * time.Millisecond)
	if tr, _ := mem.Terminal("turn-i"); tr.Kind != TerminalInterrupted || tr.Reason != "user" {
		t.Fatalf("late completed displaced interrupted: %+v", tr)
	}
}

func TestSubscribeTurnStalledRecorded(t *testing.T) {
	t.Parallel()

	mem, d := newSubscribedTest(t)
	event.Publish(d, turndto.TurnStalled{
		TurnHeader: turnHeader("t", "turn-s"),
		Reason:     "no progress",
	})
	tr := waitForTerminal(t, mem, "turn-s", TerminalStalled)
	if tr.Reason != "no progress" {
		t.Fatalf("reason = %q, want no progress", tr.Reason)
	}
}

func TestSubscribeToolCallAttribution(t *testing.T) {
	t.Parallel()

	mem, d := newSubscribedTest(t)
	callHeader := sharedto.ToolCallHeader{
		TurnHeader: turnHeader("t", "turn-tc"),
		CallID:     "call-1",
		ToolName:   "fs.read",
	}
	event.Publish(d, tooldto.ToolCallBegin{ToolCallHeader: callHeader})
	waitFor(t, func() bool {
		id, ok := mem.LookupCall("call-1")
		return ok && id == "turn-tc"
	}, "call-1 -> turn-tc binding")

	// ToolCallEnd is idempotent.
	event.Publish(d, tooldto.ToolCallEnd{ToolCallHeader: callHeader, Success: true})
	time.Sleep(20 * time.Millisecond)
	if id, ok := mem.LookupCall("call-1"); !ok || id != "turn-tc" {
		t.Fatalf("LookupCall after end = (%q,%v), want (turn-tc,true)", id, ok)
	}
}

func TestObservationDeduplicatesRawAndTypedEvents(t *testing.T) {
	t.Parallel()

	mem, d := newSubscribedTest(t)
	raw := providerdto.BusRawProviderEvent{Event: providerdto.RawProviderEvent{
		EventType: "tool.call.begin",
		Data:      map[string]any{"eventId": "raw-1", "callId": "call-raw"},
	}}
	event.Publish(d, raw)
	event.Publish(d, raw)
	time.Sleep(20 * time.Millisecond)
	if counts, ok := mem.Counts("turn-raw"); ok && counts.ToolCalls != 0 {
		t.Fatalf("raw event must not increment counts: %+v", counts)
	}

	callHeader := sharedto.ToolCallHeader{
		TurnHeader: turnHeader("thread-1", "turn-raw"),
		CallID:     "call-raw",
		ToolName:   "fs.read",
	}
	event.Publish(d, tooldto.ToolCallBegin{ToolCallHeader: callHeader})
	event.Publish(d, tooldto.ToolCallBegin{ToolCallHeader: callHeader})
	waitFor(t, func() bool {
		counts, ok := mem.Counts("turn-raw")
		return ok && counts.ToolCalls == 1
	}, "typed tool begin counted once")
}

func TestSubscribeUITokensMergesProjection(t *testing.T) {
	t.Parallel()

	mem, d := newSubscribedTest(t)
	// A real non-zero snapshot lands first.
	event.Publish(d, uidto.UITokensUpdated{
		UITurnHeader: sharedto.UITurnHeader{
			UIProjectionHeader: sharedto.UIProjectionHeader{
				ThreadHeader: sharedto.ThreadHeader{ThreadID: "t"},
				Projection:   "thread",
			},
			TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-tk"},
		},
		InputTokens: 10, OutputTokens: 20, TotalTokens: 30,
	})
	waitFor(t, func() bool {
		snap, ok := mem.Tokens("turn-tk")
		return ok && snap.Total == 30 && snap.Observed
	}, "first snapshot absorbed")

	// A follow-up zero / context-window-only event must not clobber the
	// recorded non-zero counts.
	event.Publish(d, uidto.UITokensUpdated{
		UITurnHeader: sharedto.UITurnHeader{
			UIProjectionHeader: sharedto.UIProjectionHeader{
				ThreadHeader: sharedto.ThreadHeader{ThreadID: "t"},
				Projection:   "thread",
			},
			TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-tk"},
		},
		ContextWindowTokens: 123456,
	})
	time.Sleep(50 * time.Millisecond)
	snap, _ := mem.Tokens("turn-tk")
	if snap.Input != 10 || snap.Output != 20 || snap.Total != 30 {
		t.Fatalf("zero-event clobbered token counts: %+v", snap)
	}
	if snap.ContextWindowTokens != 123456 {
		t.Fatalf("context window not merged: %+v", snap)
	}
	if !snap.Observed {
		t.Fatalf("Observed flag should remain true: %+v", snap)
	}
}

func TestSubscribeDropsUITokensWithoutTurnID(t *testing.T) {
	t.Parallel()

	mem, d := newSubscribedTest(t)
	// Claude path UITokensUpdated can arrive without a turn_id. The
	// subscriber must drop such events rather than misattribute them.
	event.Publish(d, uidto.UITokensUpdated{
		UITurnHeader: sharedto.UITurnHeader{
			UIProjectionHeader: sharedto.UIProjectionHeader{
				ThreadHeader: sharedto.ThreadHeader{ThreadID: "t"},
				Projection:   "thread",
			},
		},
		InputTokens: 10,
	})
	time.Sleep(50 * time.Millisecond)
	if snap, ok := mem.Tokens(""); ok && (snap.Input != 0 || snap.Observed) {
		t.Fatalf("empty-turn-id snapshot leaked into storage: %+v", snap)
	}
}

func TestSubscribeCancelStopsFurtherEvents(t *testing.T) {
	t.Parallel()

	dispatcher := platformbus.NewDispatcher()
	defer dispatcher.Close()
	mem := NewMemory()
	cancel := Subscribe(dispatcher, mem, nil)

	event.Publish(dispatcher, turndto.TurnCompleted{
		TurnHeader: turnHeader("t", "turn-before"),
		Success:    true,
	})
	waitForTerminal(t, mem, "turn-before", TerminalCompleted)

	cancel()
	event.Publish(dispatcher, turndto.TurnCompleted{
		TurnHeader: turnHeader("t", "turn-after"),
		Success:    true,
	})
	// Give time for (mis)subscriptions to fire.
	time.Sleep(50 * time.Millisecond)
	if _, ok := mem.Terminal("turn-after"); ok {
		t.Fatal("events after cancel must not reach observation")
	}
}
