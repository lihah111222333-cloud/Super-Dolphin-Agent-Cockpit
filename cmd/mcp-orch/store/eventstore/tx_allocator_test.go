package eventstore

import (
	"context"
	"errors"
	"sync"
	"testing"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

func TestAppendAllocatedEnvelopeTxFirstSourceAndRollback(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "allocator-first")
	build := func(id string) func() (EventEnvelope, error) {
		return func() (EventEnvelope, error) {
			return eventFrame(reservation, nil, nil, id, 1).Envelope, nil
		}
	}
	tx := beginEventTx(t, db)
	result, err := store.AppendAllocatedEnvelopeTx(t.Context(), tx, "source-allocator", "epoch-allocator", LaneControl, "writer-1", build("allocator-first"))
	if err != nil {
		rollbackEventTx(t, tx)
		t.Fatalf("AppendAllocatedEnvelopeTx() error = %v", err)
	}
	if result.Receipt.SourceSequence != 1 || result.State.NextSequence != 2 || result.Event == nil {
		rollbackEventTx(t, tx)
		t.Fatalf("result = %#v, want first materialized sequence", result)
	}
	commitEventTx(t, tx)
	// Reusing the same event identity cannot silently obtain a new sequence.
	tx = beginEventTx(t, db)
	if _, err := store.AppendAllocatedEnvelopeTx(t.Context(), tx, "source-allocator", "epoch-allocator", LaneControl, "writer-1", build("allocator-first")); !errors.Is(err, ErrEventConflict) {
		rollbackEventTx(t, tx)
		t.Fatalf("duplicate allocated append error = %v, want ErrEventConflict", err)
	}
	rollbackEventTx(t, tx)

	reservation = seedEventReservation(t, db, "allocator-rollback")
	tx = beginEventTx(t, db)
	if _, err := store.AppendAllocatedEnvelopeTx(t.Context(), tx, "source-rollback", "epoch-rollback", LaneControl, "writer-1", build("allocator-rollback")); err != nil {
		rollbackEventTx(t, tx)
		t.Fatalf("rollback append error = %v", err)
	}
	rollbackEventTx(t, tx)
	if _, err := store.GetSourceState(t.Context(), "source-rollback", "epoch-rollback", LaneControl); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("rolled-back source state = %v, want ErrEventNotFound", err)
	}
}

func TestAppendAllocatedEnvelopeTxRecoversGapAtNextSequence(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "allocator-gap")
	if _, err := db.ExecContext(t.Context(), `INSERT INTO orchestration_event_source_states (source_id, source_epoch, lane, next_sequence, materialized_sequence, dedup_floor, revision, status, gap_reason, writer_epoch) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "source-gap", "epoch-gap", "output", 1, 0, 0, 1, "gap", "missing", "writer-1"); err != nil {
		t.Fatal(err)
	}
	tx := beginEventTx(t, db)
	result, err := store.AppendAllocatedEnvelopeTx(t.Context(), tx, "source-gap", "epoch-gap", LaneOutput, "writer-1", func() (EventEnvelope, error) {
		return eventFrame(reservation, nil, nil, "allocator-gap", 1).Envelope, nil
	})
	if err != nil {
		rollbackEventTx(t, tx)
		t.Fatalf("gap recovery append error = %v", err)
	}
	if result.Receipt.SourceSequence != 1 || result.State.Status != "active" {
		rollbackEventTx(t, tx)
		t.Fatalf("gap recovery result = %#v", result)
	}
	commitEventTx(t, tx)
}

func TestAppendAllocatedEnvelopeTxBuilderFailureDoesNotCreateSource(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	tx := beginEventTx(t, db)
	if _, err := store.AppendAllocatedEnvelopeTx(t.Context(), tx, "source-builder-fail", "epoch-builder-fail", LaneControl, "writer-1", func() (EventEnvelope, error) {
		return EventEnvelope{}, ErrInvalidEvent
	}); !errors.Is(err, ErrInvalidEvent) {
		rollbackEventTx(t, tx)
		t.Fatalf("builder error = %v", err)
	}
	commitEventTx(t, tx) // prove no row relies on callback rollback semantics.
	if _, err := store.GetSourceState(t.Context(), "source-builder-fail", "epoch-builder-fail", LaneControl); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("source state after builder failure = %v, want ErrEventNotFound", err)
	}
}

func TestAppendAllocatedEnvelopeTxConcurrentWritersRemainDense(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "allocator-concurrent")
	const writers = 4
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		writerIndex := i
		runtimesafe.SafeGo(t.Context(), nil, "eventstore.test.concurrent-allocated-append", func(context.Context) {
			defer wg.Done()
			id := "allocator-concurrent-event-" + string(rune('a'+writerIndex))
			err := store.WithTransaction(t.Context(), func(txdb storedb.DBTX) error {
				_, err := store.AppendAllocatedEnvelopeTx(t.Context(), txdb, "source-concurrent", "epoch-concurrent", LaneControl, "writer-1", func() (EventEnvelope, error) {
					return eventFrame(reservation, nil, nil, id, 1).Envelope, nil
				})
				return err
			})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent allocated append error = %v", err)
		}
	}
	state, err := store.GetSourceState(t.Context(), "source-concurrent", "epoch-concurrent", LaneControl)
	if err != nil || state.NextSequence != writers+1 || state.MaterializedSequence != writers {
		t.Fatalf("concurrent source state = %#v, err=%v, want next=%d materialized=%d", state, err, writers+1, writers)
	}
}

func TestValidateAllocatedSourceStateFailsClosed(t *testing.T) {
	base := SourceState{SourceID: "source", SourceEpoch: "epoch", Lane: LaneOutput, NextSequence: 2, MaterializedSequence: 1, Revision: 2, Status: "active", WriterEpoch: "writer"}
	for _, tc := range []struct {
		name  string
		state SourceState
		lane  Lane
		want  error
	}{
		{name: "gap recovery", state: func() SourceState { s := base; s.Status = "gap"; return s }(), lane: LaneOutput, want: nil},
		{name: "control gap requires reconciliation", state: func() SourceState { s := base; s.Status = "gap"; s.Lane = LaneControl; return s }(), lane: LaneControl, want: ErrEventQuarantined},
		{name: "quarantined", state: func() SourceState { s := base; s.Status = "quarantined"; return s }(), lane: LaneOutput, want: ErrEventQuarantined},
		{name: "stale writer", state: func() SourceState { s := base; s.WriterEpoch = "old"; return s }(), lane: LaneOutput, want: ErrEventFence},
		{name: "invalid watermark", state: func() SourceState { s := base; s.MaterializedSequence = 2; return s }(), lane: LaneOutput, want: ErrEventFence},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateAllocatedSourceState(tc.state, "source", "epoch", tc.lane, "writer"); !errors.Is(err, tc.want) {
				t.Fatalf("validateAllocatedSourceState() = %v, want %v", err, tc.want)
			}
		})
	}
}
