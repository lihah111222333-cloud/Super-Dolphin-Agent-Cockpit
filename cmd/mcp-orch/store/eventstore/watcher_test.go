package eventstore

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
)

// TestEventWatcherContract fixes the local candidate's bounded/lifecycle
// behavior without implying durable or cross-process watcher semantics.
func TestEventWatcherContract(t *testing.T) {
	t.Run("filter_and_nonblocking_capacity", testWatcherFilterAndCapacity)
	t.Run("context_and_close_cleanup", testWatcherContextAndClose)
	t.Run("overflow_fails_closed", testWatcherOverflowFailsClosed)
	t.Run("invalid_event_does_not_wake", testWatcherRejectsUnmaterializedEvent)
	t.Run("append_notifies_after_commit_only", testAppendNotifiesAfterCommit)
	t.Run("append_notifies_healthy_consumers_after_overflow", testAppendNotifiesHealthyConsumersAfterOverflow)
	t.Run("rollback_does_not_wake", testAppendRollbackDoesNotWake)
	t.Run("transaction_bound_append_never_notifies", testAppendTxNeverNotifies)
	t.Run("append_gap_or_unknown_source_does_not_wake", testAppendGapAndUnknownSourceDoNotWake)
}

func testWatcherFilterAndCapacity(t *testing.T) {
	watcher, err := NewWatcher(WatcherConfig{BufferSize: 1, MaxRegistrations: 2})
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}
	filter := WatchFilter{SourceID: "source-watch", SourceEpoch: "epoch-watch", Lane: LaneControl}
	first, err := watcher.Register(t.Context(), filter)
	if err != nil {
		t.Fatalf("Register(first) error = %v", err)
	}
	second, err := watcher.Register(t.Context(), WatchFilter{SourceID: "source-other", SourceEpoch: "epoch-other", Lane: LaneOutput})
	if err != nil {
		t.Fatalf("Register(second) error = %v", err)
	}
	assertWatcherCapacity(t, watcher, filter)
	event := watcherTestEvent("source-watch", "epoch-watch", LaneControl, 1, "watch-event-1")
	if err := watcher.Notify(event); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	assertMatchingWatcherEvent(t, first, event)
	select {
	case got, ok := <-second.Events():
		t.Fatalf("non-matching watcher received event=%#v open=%v", got, ok)
	default:
	}
	assertWatcherCloseCounts(t, watcher, first, second)
	watcher.Close()
}

func assertWatcherCapacity(t *testing.T, watcher *Watcher, filter WatchFilter) {
	t.Helper()
	if _, err := watcher.Register(t.Context(), filter); !errors.Is(err, ErrWatcherCapacity) {
		t.Fatalf("Register(over capacity) = %v, want ErrWatcherCapacity", err)
	}
}

func assertMatchingWatcherEvent(t *testing.T, sub *WatchSubscription, event AgentEvent) {
	t.Helper()
	select {
	case got, ok := <-sub.Events():
		if !ok || got.Envelope.EventID != event.Envelope.EventID {
			t.Fatalf("first event = %#v, open=%v", got, ok)
		}
	default:
		t.Fatal("matching watcher did not receive event")
	}
}

func assertWatcherCloseCounts(t *testing.T, watcher *Watcher, first, second *WatchSubscription) {
	t.Helper()
	first.Close()
	if watcher.RegistrationCount() != 1 {
		t.Fatalf("RegistrationCount(after first close) = %d, want 1", watcher.RegistrationCount())
	}
	second.Close()
	if watcher.RegistrationCount() != 0 {
		t.Fatalf("RegistrationCount(after second close) = %d, want 0", watcher.RegistrationCount())
	}
}

func testWatcherOverflowFailsClosed(t *testing.T) {
	watcher, err := NewWatcher(WatcherConfig{BufferSize: 1, MaxRegistrations: 1})
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}
	sub, err := watcher.Register(t.Context(), WatchFilter{SourceID: "source-overflow", SourceEpoch: "epoch-overflow", Lane: LaneOutput})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	first := watcherTestEvent("source-overflow", "epoch-overflow", LaneOutput, 1, "overflow-1")
	second := watcherTestEvent("source-overflow", "epoch-overflow", LaneOutput, 2, "overflow-2")
	if err := watcher.Notify(first); err != nil {
		t.Fatalf("Notify(first) error = %v", err)
	}
	if err := watcher.Notify(second); !errors.Is(err, ErrWatcherOverflow) {
		t.Fatalf("Notify(full channel) = %v, want ErrWatcherOverflow", err)
	}
	if watcher.RegistrationCount() != 0 {
		t.Fatalf("RegistrationCount(after overflow) = %d, want 0", watcher.RegistrationCount())
	}
	assertWatcherClosed(t, sub)
	watcher.Close()
}

func testWatcherContextAndClose(t *testing.T) {
	watcher, err := NewWatcher(WatcherConfig{BufferSize: 1, MaxRegistrations: 2})
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	sub, err := watcher.Register(ctx, WatchFilter{SourceID: "source-context", SourceEpoch: "epoch-context", Lane: LaneContent})
	if err != nil {
		t.Fatalf("Register(context) error = %v", err)
	}
	cancel()
	assertSubscriptionClosed(t, sub, "context cancellation")
	if watcher.RegistrationCount() != 0 {
		t.Fatalf("RegistrationCount(after context cancel) = %d, want 0", watcher.RegistrationCount())
	}
	other, err := watcher.Register(t.Context(), WatchFilter{SourceID: "source-close", SourceEpoch: "epoch-close", Lane: LaneControl})
	if err != nil {
		t.Fatalf("Register(close) error = %v", err)
	}
	watcher.Close()
	assertSubscriptionClosed(t, other, "owner Close")
	if _, err := watcher.Register(t.Context(), WatchFilter{SourceID: "source-late", SourceEpoch: "epoch-late", Lane: LaneControl}); !errors.Is(err, ErrWatcherClosed) {
		t.Fatalf("Register(after Close) = %v, want ErrWatcherClosed", err)
	}
}

func assertSubscriptionClosed(t *testing.T, sub *WatchSubscription, source string) {
	t.Helper()
	select {
	case _, ok := <-sub.Events():
		if ok {
			t.Fatalf("%s left subscription channel open", source)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s did not close subscription", source)
	}
}

func testWatcherRejectsUnmaterializedEvent(t *testing.T) {
	watcher, err := NewWatcher(WatcherConfig{BufferSize: 1, MaxRegistrations: 1})
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}
	sub, err := watcher.Register(t.Context(), WatchFilter{SourceID: "source-invalid", SourceEpoch: "epoch-invalid", Lane: LaneControl})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	invalid := watcherTestEvent("source-invalid", "epoch-invalid", LaneControl, 1, "watch-invalid")
	invalid.Status = "staged"
	if err := watcher.Notify(invalid); !errors.Is(err, ErrInvalidWatchEvent) {
		t.Fatalf("Notify(staged) = %v, want ErrInvalidWatchEvent", err)
	}
	select {
	case got, ok := <-sub.Events():
		t.Fatalf("invalid event woke watcher=%#v open=%v", got, ok)
	default:
	}
	sub.Close()
}

func testAppendNotifiesAfterCommit(t *testing.T) {
	db := openEventTestDB(t)
	reservation := seedEventReservation(t, db, "watch-commit")
	frame := eventFrame(reservation, nil, nil, "watch-commit", 1)
	probe := &commitProbeNotifier{db: db, eventID: frame.Envelope.EventID, observed: make(chan error, 1)}
	verifier := permissiveWatchVerifier{}
	store, err := NewStoreWithWatcher(db, verifier, probe)
	if err != nil {
		t.Fatalf("NewStoreWithWatcher() error = %v", err)
	}
	result, err := store.Append(t.Context(), frame)
	if err != nil || result.Event == nil {
		t.Fatalf("Append() = %#v err=%v, want committed event", result, err)
	}
	select {
	case probeErr := <-probe.observed:
		if probeErr != nil {
			t.Fatalf("notifier observed uncommitted event: %v", probeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("post-commit notifier was not called")
	}
}

func testAppendRollbackDoesNotWake(t *testing.T) {
	db := openEventTestDB(t)
	reservation := seedEventReservation(t, db, "watch-rollback")
	frame := eventFrame(reservation, nil, nil, "watch-rollback", 1)
	probe := &commitProbeNotifier{db: db, eventID: frame.Envelope.EventID, observed: make(chan error, 1)}
	store, err := NewStoreWithWatcher(db, permissiveWatchVerifier{}, probe)
	if err != nil {
		t.Fatalf("NewStoreWithWatcher() error = %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER watcher_rollback_source
BEFORE UPDATE ON orchestration_event_source_states
WHEN NEW.source_id = 'source-watch-rollback'
BEGIN SELECT RAISE(ABORT, 'watcher rollback'); END`); err != nil {
		t.Fatalf("install rollback trigger: %v", err)
	}
	if _, err := store.Append(t.Context(), frame); err == nil {
		t.Fatal("Append(trigger rollback) unexpectedly succeeded")
	}
	select {
	case notifyErr := <-probe.observed:
		t.Fatalf("rollback append notified event: %v", notifyErr)
	default:
	}
	var rows int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM orchestration_agent_events WHERE event_id = ?", frame.Envelope.EventID).Scan(&rows); err != nil {
		t.Fatalf("count rolled-back events: %v", err)
	}
	if rows != 0 {
		t.Fatalf("rolled-back canonical rows = %d, want 0", rows)
	}
}

func testAppendNotifiesHealthyConsumersAfterOverflow(t *testing.T) {
	db := openEventTestDB(t)
	reservation := seedEventReservation(t, db, "watch-batch-overflow")
	second := eventFrame(reservation, nil, nil, "watch-batch-overflow", 2)
	first := eventFrame(reservation, nil, nil, "watch-batch-overflow", 1)
	first.Lane, second.Lane = LaneOutput, LaneOutput
	first.CanonicalCursor = mustCanonicalCursor(t, first.SourceID, first.SourceEpoch, first.Lane, first.SourceSequence)
	second.CanonicalCursor = mustCanonicalCursor(t, second.SourceID, second.SourceEpoch, second.Lane, second.SourceSequence)
	notifier := &batchNotifier{failFirst: true}
	store, err := NewStoreWithWatcher(db, permissiveWatchVerifier{}, notifier)
	if err != nil {
		t.Fatalf("NewStoreWithWatcher() error = %v", err)
	}
	if _, err := store.Append(t.Context(), second); !errors.Is(err, ErrEventGap) {
		t.Fatalf("Append(second gap) = %v, want ErrEventGap", err)
	}
	result, err := store.Append(t.Context(), first)
	if !errors.Is(err, ErrWatcherOverflow) || result.Event == nil {
		t.Fatalf("Append(first drain) = %#v err=%v, want committed event and ErrWatcherOverflow", result, err)
	}
	if got := notifier.events(); len(got) != 2 || got[0].SourceSequence != 1 || got[1].SourceSequence != 2 {
		t.Fatalf("post-commit notification batch = %#v, want sequences [1 2]", got)
	}
}

func testAppendTxNeverNotifies(t *testing.T) {
	db := openEventTestDB(t)
	reservation, incarnation, binding := seedBoundEventIdentity(t, db, "watch-append-tx")
	frame := eventFrame(reservation, &incarnation, &binding, "watch-append-tx", 1)
	identityStore, err := identity.NewStore(db)
	if err != nil {
		t.Fatalf("identity store: %v", err)
	}
	verifier, err := NewIdentityFenceVerifier(identityStore)
	if err != nil {
		t.Fatalf("identity verifier: %v", err)
	}
	notifier := &countNotifier{}
	store, err := NewStoreWithWatcher(db, verifier, notifier)
	if err != nil {
		t.Fatalf("NewStoreWithWatcher() error = %v", err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	result, err := store.AppendTx(t.Context(), tx, frame)
	if err != nil || result.Event == nil {
		_ = tx.Rollback()
		t.Fatalf("AppendTx() = %#v err=%v, want event in caller transaction", result, err)
	}
	if notifier.count() != 0 {
		_ = tx.Rollback()
		t.Fatal("AppendTx notified before caller commit")
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if notifier.count() != 0 {
		t.Fatal("AppendTx notified after commit; caller-bound seam must not publish")
	}
}

func testAppendGapAndUnknownSourceDoNotWake(t *testing.T) {
	db := openEventTestDB(t)
	reservation := seedEventReservation(t, db, "watch-fence")
	watcher, err := NewWatcher(WatcherConfig{BufferSize: 8, MaxRegistrations: 2})
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}
	store, err := NewStoreWithWatcher(db, permissiveWatchVerifier{}, watcher)
	if err != nil {
		t.Fatalf("NewStoreWithWatcher() error = %v", err)
	}
	first := eventFrame(reservation, nil, nil, "watch-fence", 1)
	first.Lane = LaneOutput
	first.CanonicalCursor = mustCanonicalCursor(t, first.SourceID, first.SourceEpoch, first.Lane, first.SourceSequence)
	second := eventFrame(reservation, nil, nil, "watch-fence", 2)
	second.Lane = LaneOutput
	second.CanonicalCursor = mustCanonicalCursor(t, second.SourceID, second.SourceEpoch, second.Lane, second.SourceSequence)
	sub, err := watcher.Register(t.Context(), WatchFilter{SourceID: first.SourceID, SourceEpoch: first.SourceEpoch, Lane: first.Lane})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := store.Append(t.Context(), second); !errors.Is(err, ErrEventGap) {
		t.Fatalf("Append(gap) = %v, want ErrEventGap", err)
	}
	assertWatcherEmpty(t, sub)
	if _, err := store.Append(t.Context(), first); err != nil {
		t.Fatalf("Append(drain) error = %v", err)
	}
	assertDrainedWatcherEvents(t, sub)
	if _, err := store.Append(t.Context(), first); err != nil {
		t.Fatalf("Append(replay) error = %v", err)
	}
	assertWatcherEmpty(t, sub)
	assertUnknownEpochRejected(t, store, sub, first)
	assertWatcherEmpty(t, sub)
	sub.Close()
	watcher.Close()
}

func assertDrainedWatcherEvents(t *testing.T, sub *WatchSubscription) {
	t.Helper()
	for want := int64(1); want <= 2; want++ {
		select {
		case event, ok := <-sub.Events():
			if !ok || event.SourceSequence != want {
				t.Fatalf("drained wake = %#v open=%v, want sequence %d", event, ok, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("missing drained wake for sequence %d", want)
		}
	}
}

func assertUnknownEpochRejected(t *testing.T, store *Store, sub *WatchSubscription, first IngressFrame) {
	t.Helper()
	unknown := first
	unknown.SourceEpoch = "watch-fence-unknown-epoch"
	unknown.Envelope.EventID = "event-watch-fence-unknown"
	unknown.EnvelopeHash, _ = CanonicalEnvelopeHash(unknown.Envelope)
	unknown.CanonicalCursor = mustCanonicalCursor(t, unknown.SourceID, unknown.SourceEpoch, unknown.Lane, unknown.SourceSequence)
	if _, err := store.Append(t.Context(), unknown); !errors.Is(err, ErrEventFence) {
		t.Fatalf("Append(unknown epoch) = %v, want ErrEventFence", err)
	}
	assertWatcherEmpty(t, sub)
}

func assertWatcherEmpty(t *testing.T, sub *WatchSubscription) {
	t.Helper()
	select {
	case got, ok := <-sub.Events():
		t.Fatalf("unexpected watcher wake=%#v open=%v", got, ok)
	default:
	}
}

func assertWatcherClosed(t *testing.T, sub *WatchSubscription) {
	t.Helper()
	for range 2 {
		select {
		case _, ok := <-sub.Events():
			if !ok {
				return
			}
		case <-time.After(time.Second):
			t.Fatal("watcher subscription did not close")
		}
	}
	t.Fatal("watcher subscription remained open after buffered event")
}

func watcherTestEvent(sourceID, sourceEpoch string, lane Lane, sequence int64, eventID string) AgentEvent {
	frame := IngressFrame{
		SourceID: sourceID, SourceEpoch: sourceEpoch, Lane: lane, SourceSequence: sequence,
		EnvelopeHash: eventDigest(eventID + "-hash"), CanonicalCursor: canonicalCursor(sourceID, sourceEpoch, lane, sequence),
		Envelope: EventEnvelope{
			EventID: eventID, EventType: "watch.test", SchemaVersion: 1,
			RootAgentID: "watch-agent", SubjectAgentID: "watch-agent", ReservationID: "watch-reservation",
			SourceFenceRef: "watch-fence", Provenance: "watch-test", TrustClass: "trusted", ContentRole: "none",
			ObservedAt: "2026-09-01T00:00:00.000000000Z", WriterEpoch: "watch-writer",
		},
	}
	frame.EnvelopeHash, _ = CanonicalEnvelopeHash(frame.Envelope)
	return AgentEvent{IngressFrame: frame, Status: "materialized"}
}

type permissiveWatchVerifier struct{}

func (permissiveWatchVerifier) VerifyEventFence(context.Context, EventEnvelope) error { return nil }

type commitProbeNotifier struct {
	db       *sql.DB
	eventID  string
	observed chan error
	once     sync.Once
}

func (p *commitProbeNotifier) Notify(event AgentEvent) error {
	var count int
	err := p.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM orchestration_agent_events WHERE event_id = ?", event.Envelope.EventID).Scan(&count)
	if err == nil && count != 1 {
		err = errors.New("canonical event row is not visible at notification")
	}
	p.once.Do(func() { p.observed <- err })
	return err
}

type countNotifier struct {
	mu    sync.Mutex
	calls int
}

type batchNotifier struct {
	mu        sync.Mutex
	failFirst bool
	seen      []AgentEvent
}

func (n *batchNotifier) Notify(event AgentEvent) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.seen = append(n.seen, event)
	if n.failFirst {
		n.failFirst = false
		return ErrWatcherOverflow
	}
	return nil
}

func (n *batchNotifier) events() []AgentEvent {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]AgentEvent(nil), n.seen...)
}

func (n *countNotifier) Notify(AgentEvent) error {
	n.mu.Lock()
	n.calls++
	n.mu.Unlock()
	return nil
}

func (n *countNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}
