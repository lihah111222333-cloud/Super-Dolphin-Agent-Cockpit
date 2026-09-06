package eventstore

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func TestReplayRejectsCanonicalEnvelopeDrift(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "replay-drift")
	frame := eventFrame(reservation, nil, nil, "replay-drift", 1)
	frame.Lane = LaneOutput
	frame.CanonicalCursor = mustCanonicalCursor(t, frame.SourceID, frame.SourceEpoch, frame.Lane, frame.SourceSequence)
	if _, err := storedb.New(db).InsertOrchestrationEventIngressReceipt(t.Context(), ingressReceiptParams(frame, "staged", "")); err != nil {
		t.Fatalf("insert staged receipt: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO orchestration_event_source_states
		(source_id, source_epoch, lane, next_sequence, materialized_sequence, dedup_floor, revision, status, gap_reason, writer_epoch)
		VALUES (?, ?, ?, 1, 0, 0, 1, 'active', '', ?)`, frame.SourceID, frame.SourceEpoch, frame.Lane, frame.Envelope.WriterEpoch); err != nil {
		t.Fatalf("insert source state: %v", err)
	}
	params := agentEventParams(frame)
	params.Provenance = "drifted-canonical-fact"
	if _, err := storedb.New(db).InsertOrchestrationAgentEvent(t.Context(), params); err != nil {
		t.Fatalf("insert drifted canonical event: %v", err)
	}
	if _, err := store.Append(t.Context(), frame); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("replay with envelope drift = %v, want ErrEventConflict", err)
	}
	receipt, err := store.GetReceipt(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane, frame.SourceSequence)
	if err != nil {
		t.Fatalf("read receipt after rejected replay: %v", err)
	}
	if receipt.Status != "staged" {
		t.Fatalf("receipt after rejected replay = %#v, want staged", receipt)
	}
}

func TestDrainLimitConsecutiveTailDoesNotBecomeGap(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "drain-limit")
	const total = maxEventDrain + 1
	var first, tail IngressFrame
	first, tail = seedDrainLimitFrames(t, db, storedb.New(db), reservation, total)
	if _, err := store.Append(t.Context(), first); err != nil {
		t.Fatalf("append first: %v", err)
	}
	state, err := store.GetSourceState(t.Context(), "source-drain-limit", "epoch-drain-limit", LaneOutput)
	if err != nil {
		t.Fatal(err)
	}
	assertDrainState(t, state, total, total-1)
	if _, err := store.Append(t.Context(), tail); err != nil {
		t.Fatalf("append bounded-drain tail: %v", err)
	}
	state, err = store.GetSourceState(t.Context(), "source-drain-limit", "epoch-drain-limit", LaneOutput)
	if err != nil {
		t.Fatal(err)
	}
	assertDrainState(t, state, total+1, total)
}

func assertDrainState(t *testing.T, state SourceState, next, materialized int64) {
	t.Helper()
	if state.NextSequence != next || state.MaterializedSequence != materialized || state.Status != "active" {
		t.Fatalf("state = %#v", state)
	}
}

func seedDrainLimitFrames(t *testing.T, db *sql.DB, queries *storedb.Queries, reservation identity.Reservation, total int64) (IngressFrame, IngressFrame) {
	t.Helper()
	var first, tail IngressFrame
	for sequence := int64(1); sequence <= total; sequence++ {
		frame := eventFrame(reservation, nil, nil, fmt.Sprintf("drain-limit-%d", sequence), sequence)
		frame.SourceID, frame.SourceEpoch, frame.Lane = "source-drain-limit", "epoch-drain-limit", LaneOutput
		frame.CanonicalCursor = mustCanonicalCursor(t, frame.SourceID, frame.SourceEpoch, frame.Lane, sequence)
		if sequence == 1 {
			first = frame
			continue
		}
		if sequence == total {
			tail = frame
		}
		if _, err := queries.InsertOrchestrationEventIngressReceipt(t.Context(), ingressReceiptParams(frame, "staged", "")); err != nil {
			t.Fatalf("seed receipt %d: %v", sequence, err)
		}
	}
	return first, tail
}
