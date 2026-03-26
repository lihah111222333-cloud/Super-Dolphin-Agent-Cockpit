package turn

import (
	"testing"
)

type trackerHandleStub struct {
	localID    string
	providerID string
	done       chan struct{}
	err        error
}

func newTrackerHandleStub(localID, providerID string) *trackerHandleStub {
	return &trackerHandleStub{
		localID:    localID,
		providerID: providerID,
		done:       make(chan struct{}),
	}
}

func (h *trackerHandleStub) LocalID() string       { return h.localID }
func (h *trackerHandleStub) ProviderID() string    { return h.providerID }
func (h *trackerHandleStub) Done() <-chan struct{} { return h.done }
func (h *trackerHandleStub) Err() error            { return h.err }

func requireTurnStatus(t *testing.T, tracker *turnTracker, localID string) TurnStatus {
	t.Helper()

	status, ok := tracker.Get(localID)
	if !ok {
		t.Fatalf("Get(%q) found = false", localID)
	}
	return status
}

func TestTurnTrackerStartAttachUpdateAndGet(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()
	handle := newTrackerHandleStub("local-1", "provider-from-handle")

	tracker.Start(" local-1 ", "", " thread-1 ")
	tracker.AttachHandle(" local-1 ", handle)
	tracker.BindProviderID("local-1", "provider-bound")
	tracker.Update("local-1", "running")

	status := requireTurnStatus(t, tracker, "local-1")
	if status.LocalID != "local-1" || status.ProviderID != "provider-bound" || status.State != "running" {
		t.Fatalf("Get() = %+v", status)
	}
	active, ok := tracker.ActiveByThread("thread-1")
	if !ok || active.localID != "local-1" || active.handle != handle {
		t.Fatalf("ActiveByThread() = %+v, %v", active, ok)
	}
}

func TestTurnTrackerCompleteSuccessClearsActiveHandle(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()
	handle := newTrackerHandleStub("local-1", "provider-1")

	tracker.Start("local-1", "provider-1", "thread-1")
	tracker.AttachHandle("local-1", handle)
	tracker.Update("local-1", "running")
	tracker.Complete("local-1", true, "")

	status := requireTurnStatus(t, tracker, "local-1")
	if status.State != "completed" || status.Error != "" {
		t.Fatalf("Get() = %+v", status)
	}
	if tracker.turns["local-1"].handle != nil {
		t.Fatal("handle was not cleared on Complete()")
	}
	if _, ok := tracker.ActiveByThread("thread-1"); ok {
		t.Fatal("ActiveByThread() found completed turn")
	}
}

func TestTurnTrackerCompleteMarksInterruptedAfterInterruptRequest(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()
	handle := newTrackerHandleStub("local-1", "provider-1")

	tracker.Start("local-1", "provider-1", "thread-1")
	tracker.AttachHandle("local-1", handle)
	if !tracker.MarkInterruptRequested("local-1") {
		t.Fatal("MarkInterruptRequested() = false")
	}
	tracker.Complete("local-1", false, " canceled ")

	status := requireTurnStatus(t, tracker, "local-1")
	if status.State != "interrupted" || status.Error != "canceled" {
		t.Fatalf("Get() = %+v", status)
	}
	if tracker.turns["local-1"].handle != nil {
		t.Fatal("handle was not cleared on interrupted Complete()")
	}
}

func TestTurnTrackerAbortThreadSkipsTerminalTurns(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()

	tracker.Start("completed-turn", "provider-1", "thread-1")
	tracker.Complete("completed-turn", true, "")

	tracker.Start("running-turn", "provider-2", "thread-1")
	tracker.AttachHandle("running-turn", newTrackerHandleStub("running-turn", "provider-2"))
	tracker.Update("running-turn", "running")

	if !tracker.AbortThread(" thread-1 ", " stop requested ") {
		t.Fatal("AbortThread() = false")
	}

	completed := requireTurnStatus(t, tracker, "completed-turn")
	if completed.State != "completed" || completed.Error != "" {
		t.Fatalf("completed turn changed: %+v", completed)
	}
	running := requireTurnStatus(t, tracker, "running-turn")
	if running.State != "interrupted" || running.Error != "stop requested" {
		t.Fatalf("running turn after AbortThread() = %+v", running)
	}
	if !tracker.turns["running-turn"].interruptRequested {
		t.Fatal("interruptRequested was not set by AbortThread()")
	}
}

func TestTurnTrackerIgnoresInvalidInputs(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()

	tracker.Start("", "provider-1", "thread-1")
	if len(tracker.turns) != 0 {
		t.Fatalf("Start() created turns = %d", len(tracker.turns))
	}

	tracker.Start("local-1", "provider-1", "thread-1")
	tracker.AttachHandle("missing", newTrackerHandleStub("missing", "provider-2"))
	tracker.Update("local-1", "")
	tracker.Complete("missing", false, "boom")

	status := requireTurnStatus(t, tracker, "local-1")
	if status.State != "preparing" {
		t.Fatalf("Get() state = %q, want preparing", status.State)
	}
	if tracker.MarkInterruptRequested("missing") {
		t.Fatal("MarkInterruptRequested() = true for missing turn")
	}
	if tracker.AbortThread("", "boom") {
		t.Fatal("AbortThread() = true for blank thread")
	}
	if _, ok := tracker.Get("missing"); ok {
		t.Fatal("Get() found missing turn")
	}
}
