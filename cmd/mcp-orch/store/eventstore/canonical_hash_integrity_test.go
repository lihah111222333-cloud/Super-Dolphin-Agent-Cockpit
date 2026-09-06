package eventstore

import (
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
)

type canonicalHashProbeNotifier struct{ calls atomic.Int64 }

func (p *canonicalHashProbeNotifier) Notify(AgentEvent) error {
	p.calls.Add(1)
	return nil
}

func TestAppendRejectsNonCanonicalEnvelopeHashWithoutWritesOrNotification(t *testing.T) {
	db := openEventTestDB(t)
	reservation := seedEventReservation(t, db, "canonical-hash-append")
	probe := new(canonicalHashProbeNotifier)
	store := mustEventStoreWithWatcher(t, db, probe)
	frame := eventFrame(reservation, nil, nil, "canonical-hash-append", 1)
	frame.EnvelopeHash = eventDigest("valid-format-but-wrong")

	if _, err := store.Append(t.Context(), frame); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("Append(wrong envelope hash) error = %v, want ErrInvalidEvent", err)
	}
	assertCanonicalHashRows(t, db, frame, 0)
	if got := probe.calls.Load(); got != 0 {
		t.Fatalf("wrong-hash append notifications = %d, want 0", got)
	}
}

func TestAppendTxRejectsNonCanonicalEnvelopeHashWithoutWrites(t *testing.T) {
	db := openEventTestDB(t)
	reservation := seedEventReservation(t, db, "canonical-hash-tx")
	store := mustEventStore(t, db)
	frame := eventFrame(reservation, nil, nil, "canonical-hash-tx", 1)
	frame.EnvelopeHash = eventDigest("valid-format-but-wrong-tx")
	tx := beginEventTx(t, db)
	if _, err := store.AppendTx(t.Context(), tx, frame); !errors.Is(err, ErrInvalidEvent) {
		rollbackEventTx(t, tx)
		t.Fatalf("AppendTx(wrong envelope hash) error = %v, want ErrInvalidEvent", err)
	}
	assertCanonicalHashRowsInTx(t, tx, 0)
	rollbackEventTx(t, tx)
	assertCanonicalHashRows(t, db, frame, 0)
}

func TestAppendAcceptsCanonicalEnvelopeHashAndNotifiesAfterCommit(t *testing.T) {
	db := openEventTestDB(t)
	reservation := seedEventReservation(t, db, "canonical-hash-success")
	probe := new(canonicalHashProbeNotifier)
	store := mustEventStoreWithWatcher(t, db, probe)
	frame := eventFrame(reservation, nil, nil, "canonical-hash-success", 1)

	result, err := store.Append(t.Context(), frame)
	if err != nil || result.Event == nil || result.Receipt.EnvelopeHash != frame.EnvelopeHash {
		t.Fatalf("Append(canonical envelope hash) = %#v, err=%v, want materialized event", result, err)
	}
	assertCanonicalHashRows(t, db, frame, 1)
	if got := probe.calls.Load(); got != 1 {
		t.Fatalf("canonical append notifications = %d, want 1", got)
	}
}

func TestWatcherRejectsNonCanonicalEnvelopeHashWithoutSending(t *testing.T) {
	watcher, err := NewWatcher(WatcherConfig{BufferSize: 1, MaxRegistrations: 1})
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}
	sub, err := watcher.Register(t.Context(), WatchFilter{SourceID: "canonical-watch", SourceEpoch: "epoch", Lane: LaneControl})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	defer sub.Close()
	forged := watcherTestEvent("canonical-watch", "epoch", LaneControl, 1, "canonical-watch-event")
	forged.EnvelopeHash = eventDigest("valid-format-but-wrong-watch")
	if err := watcher.Notify(forged); !errors.Is(err, ErrInvalidWatchEvent) {
		t.Fatalf("Notify(wrong envelope hash) error = %v, want ErrInvalidWatchEvent", err)
	}
	select {
	case event := <-sub.Events():
		t.Fatalf("wrong-hash watcher event = %#v, want no send", event)
	default:
	}
}

func mustEventStoreWithWatcher(t *testing.T, db *sql.DB, notifier EventNotifier) *Store {
	t.Helper()
	store, err := NewStoreWithWatcher(db, permissiveWatchVerifier{}, notifier)
	if err != nil {
		t.Fatalf("NewStoreWithWatcher() error = %v", err)
	}
	return store
}

func assertCanonicalHashRows(t *testing.T, db *sql.DB, frame IngressFrame, want int) {
	t.Helper()
	for _, table := range []string{"orchestration_event_source_states", "orchestration_event_ingress_receipts", "orchestration_agent_events"} {
		var got int
		if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table+" WHERE source_id = ? AND source_epoch = ? AND lane = ?", frame.SourceID, frame.SourceEpoch, frame.Lane).Scan(&got); err != nil {
			t.Fatalf("count %s rows: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s rows = %d, want %d", table, got, want)
		}
	}
}

func assertCanonicalHashRowsInTx(t *testing.T, tx *sql.Tx, want int) {
	t.Helper()
	for _, table := range []string{"orchestration_event_source_states", "orchestration_event_ingress_receipts", "orchestration_agent_events"} {
		var got int
		if err := tx.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&got); err != nil {
			t.Fatalf("count %s rows in transaction: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s rows in transaction = %d, want %d", table, got, want)
		}
	}
}
