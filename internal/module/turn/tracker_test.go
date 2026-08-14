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

// viewHandle returns the internal TurnHandle for a tracked turn (test-only).
func viewHandle(tracker *turnTracker, localID string) any {
	var h any
	tracker.store.View(localID, func(turn *trackedTurn) {
		h = turn.handle
	})
	return h
}

// viewInterruptRequested returns the interruptRequested flag (test-only).
func viewInterruptRequested(tracker *turnTracker, localID string) bool {
	var v bool
	tracker.store.View(localID, func(turn *trackedTurn) {
		v = turn.interruptRequested
	})
	return v
}

// countEntries returns the number of tracked turns (test-only).
func countEntries(tracker *turnTracker) int {
	count := 0
	tracker.store.RangeView(func(_ string, _ *trackedTurn) bool {
		count++
		return true
	})
	return count
}

func TestTurnTrackerStartAttachUpdateAndGet(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()
	handle := newTrackerHandleStub("local-1", "provider-from-handle")

	tracker.Start(" local-1 ", "", " thread-1 ")
	tracker.AttachHandle(" local-1 ", handle)
	tracker.BindProviderID("local-1", "provider-bound")
	tracker.Update("local-1", StateRunning)

	status := requireTurnStatus(t, tracker, "local-1")
	if status.LocalID != "local-1" || status.ProviderID != "provider-bound" || status.State != "running" {
		t.Fatalf("Get() = %+v", status)
	}
	active, ok := tracker.ActiveByThread("thread-1")
	if !ok || active.localID != "local-1" || active.handle != handle {
		t.Fatalf("ActiveByThread() = %+v, %v", active, ok)
	}
}

func TestBindProviderIDClaimsPendingInterruptBeforeConfirmation(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()
	if !tracker.Start("local-claim-bind", "", "thread-claim-bind") {
		t.Fatal("Start() = false")
	}
	claim := tracker.ClaimInterruptTarget("thread-claim-bind", "local-claim-bind", "request-claim-bind")
	if !claim.claimed {
		t.Fatalf("ClaimInterruptTarget() = %#v, want claimed", claim)
	}
	if requestID := tracker.BindProviderID("local-claim-bind", "provider-claim-bind"); requestID != "request-claim-bind" {
		t.Fatalf("BindProviderID() = %q, want pending request id", requestID)
	}
	if !confirmInterruptClaim(tracker, "local-claim-bind", "request-claim-bind") {
		t.Fatal("confirmInterruptClaim() = false after bind took delivery ownership")
	}
	status := requireTurnStatus(t, tracker, "local-claim-bind")
	if status.State != "interrupting" || status.ProviderID != "provider-claim-bind" {
		t.Fatalf("status after claim-bind-confirm = %#v, want interrupting bound turn", status)
	}
}

func TestTurnTrackerCompleteSuccessClearsActiveHandle(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()
	handle := newTrackerHandleStub("local-1", "provider-1")

	tracker.Start("local-1", "provider-1", "thread-1")
	tracker.AttachHandle("local-1", handle)
	tracker.Update("local-1", StateRunning)
	tracker.Complete("local-1", true, "")

	status := requireTurnStatus(t, tracker, "local-1")
	if status.State != "completed" || status.Error != "" {
		t.Fatalf("Get() = %+v", status)
	}
	if viewHandle(tracker, "local-1") != nil {
		t.Fatal("handle was not cleared on Complete()")
	}
	if _, ok := tracker.ActiveByThread("thread-1"); ok {
		t.Fatal("ActiveByThread() found completed turn")
	}
}

func TestTurnTrackerCompleteMarksInterruptedAfterDeliveredInterruptRequest(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()
	handle := newTrackerHandleStub("local-1", "provider-1")

	tracker.Start("local-1", "provider-1", "thread-1")
	tracker.AttachHandle("local-1", handle)
	if !tracker.MarkInterruptRequested("local-1") {
		t.Fatal("MarkInterruptRequested() = false")
	}
	tracker.store.Mutate("local-1", func(turn *trackedTurn) {
		turn.interruptDeliverySent = true
	})
	tracker.Complete("local-1", false, " canceled ")

	status := requireTurnStatus(t, tracker, "local-1")
	if status.State != "interrupted" || status.Error != "canceled" {
		t.Fatalf("Get() = %+v", status)
	}
	if viewHandle(tracker, "local-1") != nil {
		t.Fatal("handle was not cleared on interrupted Complete()")
	}
}

func TestTurnTrackerStallClearsInterruptLifecycle(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()
	tracker.Start("local-stall", "provider-stall", "thread-stall")
	tracker.store.Mutate("local-stall", func(turn *trackedTurn) {
		turn.interruptRequested = true
		turn.interruptDeliveryClaimed = true
		turn.interruptDeliverySent = true
		turn.interruptRetryable = true
		turn.interruptRetryableCode = "REGISTERED_INTERRUPT_DELIVERY_RETRYABLE"
	})
	tracker.Stall("local-stall", "watch timed out")

	status := requireTurnStatus(t, tracker, "local-stall")
	if status.State != string(StateStalled) || status.InterruptRetryable || status.InterruptRetryableCode != "" {
		t.Fatalf("stall status = %+v, want terminal cleanup", status)
	}
	tracker.store.View("local-stall", func(turn *trackedTurn) {
		if turn.interruptRequested || turn.interruptDeliveryClaimed || turn.interruptDeliverySent {
			t.Fatalf("stalled turn retains live interrupt state: %+v", turn)
		}
		if !turn.terminalInterruptSent {
			t.Fatal("stalled turn lost delivered interrupt replay fact")
		}
	})
}

func TestTurnTrackerAbortThreadSkipsTerminalTurns(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()

	tracker.Start("completed-turn", "provider-1", "thread-1")
	tracker.Complete("completed-turn", true, "")

	tracker.Start("running-turn", "provider-2", "thread-1")
	tracker.AttachHandle("running-turn", newTrackerHandleStub("running-turn", "provider-2"))
	tracker.Update("running-turn", StateRunning)

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
	if !viewInterruptRequested(tracker, "running-turn") {
		t.Fatal("interruptRequested was not set by AbortThread()")
	}
}

func TestTurnTrackerIgnoresInvalidInputs(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()

	tracker.Start("", "provider-1", "thread-1")
	if countEntries(tracker) != 0 {
		t.Fatalf("Start() created turns = %d", countEntries(tracker))
	}

	tracker.Start("local-1", "provider-1", "thread-1")
	tracker.AttachHandle("missing", newTrackerHandleStub("missing", "provider-2"))
	tracker.Update("local-1", TurnState(""))
	tracker.Update("local-1", TurnState("not-a-state"))
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

func TestTurnTrackerRegisterAndGetByDedupeKey(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()
	tracker.Start("local-1", "", "thread-1")
	tracker.RegisterDedupeKey("local-1", " key-a ")
	tracker.Update("local-1", StateRunning)

	status, ok := tracker.GetByDedupeKey("key-a")
	if !ok {
		t.Fatal("GetByDedupeKey(key-a) found = false, want true")
	}
	if status.LocalID != "local-1" || status.State != "running" {
		t.Fatalf("GetByDedupeKey returned %+v", status)
	}
}

func TestTurnTrackerGetByDedupeKeyEmptyKey(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()
	tracker.Start("local-1", "", "thread-1")
	tracker.RegisterDedupeKey("local-1", "key-a")

	if _, ok := tracker.GetByDedupeKey("  "); ok {
		t.Fatal("empty key should miss")
	}
}

func TestTurnTrackerGetByDedupeKeySkipsTerminal(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()
	tracker.Start("local-1", "", "thread-1")
	tracker.RegisterDedupeKey("local-1", "key-a")
	tracker.Complete("local-1", true, "") // terminal -> state=completed

	// A second live turn with the same key (simulating a restart scenario)
	tracker.Start("local-2", "", "thread-1")
	tracker.RegisterDedupeKey("local-2", "key-a")
	tracker.Update("local-2", StateRunning)

	status, ok := tracker.GetByDedupeKey("key-a")
	if !ok || status.LocalID != "local-2" {
		t.Fatalf("want live turn local-2, got %+v (ok=%v)", status, ok)
	}
}

func TestTurnTrackerRegisterDedupeKeyIgnoresUnknownLocalID(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()
	tracker.RegisterDedupeKey("never-started", "key-a")

	if _, ok := tracker.GetByDedupeKey("key-a"); ok {
		t.Fatal("unknown localID registration should be a no-op")
	}
}

func TestTurnTrackerGetByDedupeKeyReturnsLatestOnConflict(t *testing.T) {
	t.Parallel()

	tracker := newTurnTracker()
	tracker.Start("local-old", "", "thread-1")
	tracker.RegisterDedupeKey("local-old", "key-shared")
	tracker.Update("local-old", StateRunning)

	// Second registration with the same key — last-wins semantics.
	tracker.Start("local-new", "", "thread-1")
	tracker.RegisterDedupeKey("local-new", "key-shared")
	tracker.Update("local-new", StateRunning)

	status, ok := tracker.GetByDedupeKey("key-shared")
	if !ok {
		t.Fatal("GetByDedupeKey found = false")
	}
	// Both live; GetByDedupeKey returns the most recently updated.
	if status.LocalID != "local-new" {
		t.Fatalf("want local-new (latest updated), got %+v", status)
	}
}
