package eventstore

import (
	"errors"
	"testing"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// TestWithTransactionCommittedGapErrorStillCommits proves that a committed
// business error cannot be returned as the transaction callback error: doing
// so would roll back the staged receipt and durable gap state.
func TestWithTransactionCommittedGapErrorStillCommits(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "tx-committed-gap")
	frame := eventFrame(reservation, nil, nil, "tx-committed-gap", 2)
	frame.Lane = LaneOutput
	frame.CanonicalCursor = mustCanonicalCursor(t, frame.SourceID, frame.SourceEpoch, frame.Lane, frame.SourceSequence)

	err := store.WithTransaction(t.Context(), func(txdb storedb.DBTX) error {
		_, err := store.AppendTx(t.Context(), txdb, frame)
		return err
	})
	if !errors.Is(err, ErrEventGap) {
		t.Fatalf("WithTransaction(gap) error = %v, want ErrEventGap", err)
	}
	assertEventState(t, store, frame.SourceID, frame.SourceEpoch, frame.Lane, 1, 0, "gap")
	receipt, readErr := store.GetReceipt(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane, frame.SourceSequence)
	if readErr != nil || receipt.Status != "staged" {
		t.Fatalf("gap receipt = %#v err=%v, want committed staged receipt", receipt, readErr)
	}
}

// TestWithTransactionPreservesCommittedErrorWrapping keeps the caller's
// classification around the post-commit marker (for example ErrGap in the
// runtime writer) instead of reducing it to only ErrEventGap.
func TestWithTransactionPreservesCommittedErrorWrapping(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	wrapper := errors.New("caller classification")
	err := store.WithTransaction(t.Context(), func(storedb.DBTX) error {
		return errors.Join(wrapper, commitEventError(ErrEventGap))
	})
	if !errors.Is(err, wrapper) || !errors.Is(err, ErrEventGap) {
		t.Fatalf("WithTransaction(wrapped committed error) = %v, want wrapper and ErrEventGap", err)
	}
}

// TestWithTransactionAllocatedConflictCommitsQuarantine proves the allocator
// path preserves the quarantine transition when its callback is wrapped by
// the transaction owner. The fixture represents an output-lane gap with a
// staged sequence-one receipt whose hash conflicts with the newly built frame.
func TestWithTransactionAllocatedConflictCommitsQuarantine(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "tx-allocated-conflict")
	staged := eventFrame(reservation, nil, nil, "tx-allocated-staged", 1)
	staged.Lane = LaneOutput
	staged.CanonicalCursor = mustCanonicalCursor(t, staged.SourceID, staged.SourceEpoch, staged.Lane, staged.SourceSequence)
	if _, err := storedb.New(db).InsertOrchestrationEventIngressReceipt(t.Context(), ingressReceiptParams(staged, "staged", "")); err != nil {
		t.Fatalf("insert staged receipt: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO orchestration_event_source_states
		(source_id, source_epoch, lane, next_sequence, materialized_sequence, dedup_floor, revision, status, gap_reason, writer_epoch)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, staged.SourceID, staged.SourceEpoch, staged.Lane, 1, 0, 0, 1, "gap", "missing source sequence", "writer-1"); err != nil {
		t.Fatalf("insert gap source state: %v", err)
	}

	conflicting := staged.Envelope
	conflicting.EventID = "event-tx-allocated-conflict"
	err := store.WithTransaction(t.Context(), func(txdb storedb.DBTX) error {
		_, err := store.AppendAllocatedEnvelopeTx(t.Context(), txdb, staged.SourceID, staged.SourceEpoch, LaneOutput, "writer-1", func() (EventEnvelope, error) {
			return conflicting, nil
		})
		return err
	})
	if !errors.Is(err, ErrEventConflict) {
		t.Fatalf("WithTransaction(allocated conflict) error = %v, want ErrEventConflict", err)
	}
	assertEventState(t, store, staged.SourceID, staged.SourceEpoch, LaneOutput, 1, 0, "quarantined")
	receipt, readErr := store.GetReceipt(t.Context(), staged.SourceID, staged.SourceEpoch, LaneOutput, 1)
	if readErr != nil || receipt.Status != "quarantined" {
		t.Fatalf("conflict receipt = %#v err=%v, want committed quarantine", receipt, readErr)
	}
}

// TestAppendRejectsControlGapRecovery records the fail-closed boundary for a
// control source already known to have a hole. Recovery must be adjudicated by
// an explicit owner operation, not by an ordinary append.
func TestAppendRejectsControlGapRecovery(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "control-gap-recovery")
	frame := eventFrame(reservation, nil, nil, "control-gap-recovery", 1)
	if _, err := db.ExecContext(t.Context(), `INSERT INTO orchestration_event_source_states
		(source_id, source_epoch, lane, next_sequence, materialized_sequence, dedup_floor, revision, status, gap_reason, writer_epoch)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, frame.SourceID, frame.SourceEpoch, frame.Lane, 1, 0, 0, 1, "gap", "missing source sequence", "writer-1"); err != nil {
		t.Fatalf("insert control gap source state: %v", err)
	}
	if _, err := store.Append(t.Context(), frame); !errors.Is(err, ErrEventQuarantined) {
		t.Fatalf("Append(control gap recovery) error = %v, want ErrEventQuarantined", err)
	}
}

func mustCanonicalCursor(t *testing.T, sourceID, sourceEpoch string, lane Lane, sequence int64) string {
	t.Helper()
	cursor, err := CanonicalCursor(sourceID, sourceEpoch, lane, sequence)
	if err != nil {
		t.Fatalf("CanonicalCursor() error = %v", err)
	}
	return cursor
}
