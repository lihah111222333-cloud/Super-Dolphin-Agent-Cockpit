package eventstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// TestEventStoreContract 聚合 canonical event owner 的关键持久化契约。
func TestEventStoreContract(t *testing.T) {
	checks := []struct {
		name string
		run  func(*testing.T)
	}{
		{"append_gap_replay_conflict", TestEventStoreAppendGapReplayConflict},
		{"constructor", TestEventStoreConstructorRequiresFenceVerifier},
		{"writer_and_status_fences", TestEventStoreWriterEpochAndStatusFences},
		{"source_epoch_fence", TestEventStoreRejectsUnregisteredSourceEpoch},
		{"bound_fence_verifier", TestEventStoreBoundFenceVerifierRejectsMismatch},
		{"independent_identity_generations", TestEventStoreAcceptsIndependentIdentityGenerations},
		{"bound_source_binding", TestEventStoreBoundSourceBinding},
		{"pre_incarnation_reservation_states", TestEventStorePreIncarnationReservationStateMatrix},
		{"identity_and_content_shapes", TestEventStoreIdentityAndContentShapes},
		{"event_id_source_conflict", TestEventStoreEventIDConflictAcrossSources},
		{"close_reopen", TestEventStoreCloseReopenPreservesCanonicalRows},
		{"concurrent_append", TestEventStoreConcurrentAppendConverges},
		{"cursor_cas", TestEventStoreCursorCASAndWatermark},
		{"tombstone_floor", TestEventStoreTombstoneFloorAndExpiredReplay},
		{"legacy_projection_guard", TestEventStoreNeverWritesLegacyProjections},
		{"read_mapper_guard", TestEventStoreReadMappersRejectMalformedRows},
		{"field_guard", TestEventStoreFieldGuard},
		{"canonical_cursor_wire", TestCanonicalCursorPreservesWireFormat},
		{"canonical_cursor_validation", TestCanonicalCursorRejectsInvalidTuple},
		{"transaction_bound_reads", TestEventStoreTransactionBoundReads},
	}
	for _, check := range checks {
		t.Run(check.name, check.run)
	}
}

func TestEventStoreTransactionBoundReads(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "tx-read")
	frame := eventFrame(reservation, nil, nil, "tx-read", 1)
	appended, err := store.Append(t.Context(), frame)
	if err != nil || appended.Event == nil {
		t.Fatalf("Append = %#v err=%v", appended, err)
	}
	if err := store.WithTransaction(t.Context(), func(txdb storedb.DBTX) error {
		items, err := store.ListEventsTx(t.Context(), txdb, frame.SourceID, frame.SourceEpoch, frame.Lane, 0, 1)
		if err != nil || len(items) != 1 || items[0].Envelope.EventID != frame.Envelope.EventID {
			return fmt.Errorf("ListEventsTx = %#v err=%v", items, err)
		}
		got, err := store.GetEventTx(t.Context(), txdb, frame.Envelope.EventID)
		if err != nil || !canonicalEventTupleMatchesForTest(got, items[0]) {
			return fmt.Errorf("GetEventTx = %#v err=%v", got, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTransaction reads: %v", err)
	}
	if _, err := store.ListEventsTx(t.Context(), db, frame.SourceID, frame.SourceEpoch, frame.Lane, 0, 1); err == nil {
		t.Fatal("ListEventsTx with bare DB unexpectedly succeeded")
	}
}

func canonicalEventTupleMatchesForTest(left, right AgentEvent) bool {
	return left.SourceID == right.SourceID && left.SourceEpoch == right.SourceEpoch && left.Lane == right.Lane && left.SourceSequence == right.SourceSequence && left.EnvelopeHash == right.EnvelopeHash && left.Envelope.EventID == right.Envelope.EventID && left.CanonicalCursor == right.CanonicalCursor
}

func TestEventStoreConstructorRequiresFenceVerifier(t *testing.T) {
	if store, err := NewStore(nil, nil); err == nil || store != nil {
		t.Fatalf("NewStore(nil,nil) = store=%#v err=%v, want fail-fast", store, err)
	}
	db := openEventTestDB(t)
	if store, err := NewStore(db, nil); err == nil || store != nil {
		t.Fatalf("NewStore(db,nil) = store=%#v err=%v, want fail-fast", store, err)
	}
}

func TestEventStoreAppendGapReplayConflict(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "gap")
	frameOne := eventFrame(reservation, nil, nil, "gap", 1)
	frameTwo := eventFrame(reservation, nil, nil, "gap", 2)
	frameOne.Lane = LaneOutput
	frameTwo.Lane = LaneOutput
	frameOne.CanonicalCursor = mustCanonicalCursor(t, frameOne.SourceID, frameOne.SourceEpoch, frameOne.Lane, frameOne.SourceSequence)
	frameTwo.CanonicalCursor = mustCanonicalCursor(t, frameTwo.SourceID, frameTwo.SourceEpoch, frameTwo.Lane, frameTwo.SourceSequence)
	assertGapAccepted(t, store, frameTwo)
	assertGapDrained(t, store, frameOne)
	assertEventReplayConflict(t, store, db, frameOne)
}

func assertGapAccepted(t *testing.T, store *Store, frameTwo IngressFrame) {
	t.Helper()
	result, err := store.Append(t.Context(), frameTwo)
	if !errors.Is(err, ErrEventGap) {
		t.Fatalf("Append(seq2 first) error = %v, want ErrEventGap", err)
	}
	if result.Receipt.Status != "staged" || result.Event != nil {
		t.Fatalf("seq2 result = %#v, want staged receipt without event", result)
	}
	assertEventState(t, store, "source-gap", "epoch-gap", LaneOutput, 1, 0, "gap")
}

func assertGapDrained(t *testing.T, store *Store, frameOne IngressFrame) {
	t.Helper()
	result, err := store.Append(t.Context(), frameOne)
	if err != nil {
		t.Fatalf("Append(seq1) error = %v", err)
	}
	if result.Receipt.Status != "materialized" || result.Event == nil {
		t.Fatalf("seq1 result = %#v, want materialized event", result)
	}
	assertGapStateAndRows(t, store)
	assertGapEvents(t, store)
}

func assertGapStateAndRows(t *testing.T, store *Store) {
	t.Helper()
	assertEventState(t, store, "source-gap", "epoch-gap", LaneOutput, 3, 2, "active")
	for sequence := int64(1); sequence <= 2; sequence++ {
		receipt, readErr := store.GetReceipt(t.Context(), "source-gap", "epoch-gap", LaneOutput, sequence)
		if readErr != nil || receipt.Status != "materialized" {
			t.Fatalf("receipt %d = %#v err=%v, want materialized", sequence, receipt, readErr)
		}
	}
}

func assertGapEvents(t *testing.T, store *Store) {
	t.Helper()
	events, err := store.ListEvents(t.Context(), "source-gap", "epoch-gap", LaneOutput, 0, 10)
	if err != nil || len(events) != 2 || events[0].SourceSequence != 1 || events[1].SourceSequence != 2 {
		t.Fatalf("ListEvents = %#v err=%v, want ordered sequences 1,2", events, err)
	}
}

func assertEventReplayConflict(t *testing.T, store *Store, db *sql.DB, frameOne IngressFrame) {
	t.Helper()
	before := countEventRows(t, db, "orchestration_agent_events")
	replay, err := store.Append(t.Context(), frameOne)
	if err != nil || replay.Event == nil || countEventRows(t, db, "orchestration_agent_events") != before {
		t.Fatalf("same seq/hash replay result=%#v err=%v, want no new event", replay, err)
	}
	conflict := frameOne
	conflict.Envelope.EventID = "event-gap-conflict"
	conflict.EnvelopeHash, _ = CanonicalEnvelopeHash(conflict.Envelope)
	result, err := store.Append(t.Context(), conflict)
	if !errors.Is(err, ErrEventConflict) {
		t.Fatalf("same seq/different hash error = %v, want ErrEventConflict", err)
	}
	if result.Receipt.Status != "quarantined" || result.State.Status != "quarantined" {
		t.Fatalf("conflict result = %#v, want quarantined receipt and source", result)
	}
	if got, readErr := store.GetEvent(t.Context(), "event-gap-1"); readErr != nil || got.EnvelopeHash != frameOne.EnvelopeHash {
		t.Fatalf("canonical event after conflict = %#v err=%v, want original hash", got, readErr)
	}
}

func TestEventStoreWriterEpochAndStatusFences(t *testing.T) {
	cases := []struct {
		name       string
		status     string
		want       error
		changeFunc func(*testing.T, *sql.DB, IngressFrame)
	}{
		{name: "writer_epoch", want: ErrEventFence, changeFunc: changeWriterEpoch},
		{name: "quarantined", status: "quarantined", want: ErrEventQuarantined, changeFunc: changeSourceStatus},
		{name: "closed", status: "closed", want: ErrEventQuarantined, changeFunc: changeSourceStatus},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			db := openEventTestDB(t)
			store := mustEventStore(t, db)
			reservation := seedEventReservation(t, db, "fence-"+testCase.name)
			first := eventFrame(reservation, nil, nil, "fence-"+testCase.name, 1)
			if _, err := store.Append(t.Context(), first); err != nil {
				t.Fatalf("Append(first) error = %v", err)
			}
			next := eventFrame(reservation, nil, nil, "fence-"+testCase.name, 2)
			testCase.changeFunc(t, db, next)
			if _, err := store.Append(t.Context(), next); !errors.Is(err, testCase.want) {
				t.Fatalf("Append(fenced) error = %v, want %v", err, testCase.want)
			}
			if _, err := store.GetReceipt(t.Context(), next.SourceID, next.SourceEpoch, next.Lane, next.SourceSequence); !errors.Is(err, ErrEventNotFound) {
				t.Fatalf("fenced receipt read error = %v, want ErrEventNotFound", err)
			}
		})
	}
}

func TestEventStoreRejectsUnregisteredSourceEpoch(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "epoch-fence")
	first := eventFrame(reservation, nil, nil, "epoch-fence", 1)
	if _, err := store.Append(t.Context(), first); err != nil {
		t.Fatalf("Append(first epoch) error = %v", err)
	}
	second := first
	second.SourceEpoch = "epoch-fence-other"
	second.Envelope.EventID = "event-epoch-fence-other"
	second.EnvelopeHash, _ = CanonicalEnvelopeHash(second.Envelope)
	second.CanonicalCursor = canonicalCursor(second.SourceID, second.SourceEpoch, second.Lane, second.SourceSequence)
	if _, err := store.Append(t.Context(), second); !errors.Is(err, ErrEventFence) {
		t.Fatalf("Append(unregistered epoch) error = %v, want ErrEventFence", err)
	}
	if _, err := store.GetSourceState(t.Context(), second.SourceID, second.SourceEpoch, second.Lane); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("unregistered epoch state = %v, want absent", err)
	}
}

func TestEventStoreBoundFenceVerifierRejectsMismatch(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation, incarnation, binding := seedBoundEventIdentity(t, db, "bound-mismatch")
	frame := eventFrame(reservation, &incarnation, &binding, "bound-mismatch", 1)
	frame.Envelope.BindingDigest = eventDigest("wrong-binding")
	frame.EnvelopeHash, _ = CanonicalEnvelopeHash(frame.Envelope)
	if _, err := store.Append(t.Context(), frame); !errors.Is(err, ErrEventFence) {
		t.Fatalf("Append(wrong binding fence) error = %v, want ErrEventFence", err)
	}
	if _, err := store.GetSourceState(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("mismatched bound source state = %v, want absent", err)
	}
}

// TestEventStoreBoundSourceBinding proves the public Store.Append boundary does
// not trust taskeventsource. A caller holding the Appender can still construct a
// frame directly, so source tuple and envelope identity must agree here too.
func TestEventStoreBoundSourceBinding(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*IngressFrame)
	}{
		{name: "source id", mutate: func(frame *IngressFrame) {
			frame.SourceID = "task/other-agent"
			frame.CanonicalCursor = canonicalCursor(frame.SourceID, frame.SourceEpoch, frame.Lane, frame.SourceSequence)
		}},
		{name: "source epoch", mutate: func(frame *IngressFrame) {
			frame.SourceEpoch = "other-incarnation"
			frame.CanonicalCursor = canonicalCursor(frame.SourceID, frame.SourceEpoch, frame.Lane, frame.SourceSequence)
		}},
		{name: "root agent", mutate: func(frame *IngressFrame) { frame.Envelope.RootAgentID = "other-agent" }},
		{name: "subject agent", mutate: func(frame *IngressFrame) { frame.Envelope.SubjectAgentID = "other-agent" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openEventTestDB(t)
			store := mustEventStore(t, db)
			reservation, incarnation, binding := seedBoundEventIdentity(t, db, "source-binding-"+strings.ReplaceAll(tc.name, " ", "-"))
			frame := eventFrame(reservation, &incarnation, &binding, "source-binding-"+strings.ReplaceAll(tc.name, " ", "-"), 1)
			tc.mutate(&frame)
			if _, err := store.Append(t.Context(), frame); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("Append(%s) error = %v, want ErrInvalidEvent", tc.name, err)
			}
		})
	}
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "pre-task-source")
	frame := eventFrame(reservation, nil, nil, "pre-task-source", 1)
	frame.SourceID = "task/" + reservation.AgentID
	frame.CanonicalCursor = canonicalCursor(frame.SourceID, frame.SourceEpoch, frame.Lane, frame.SourceSequence)
	if _, err := store.Append(t.Context(), frame); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("Append(pre-incarnation task source) error = %v, want ErrInvalidEvent", err)
	}
}

// TestEventStorePreIncarnationReservationStateMatrix keeps reservation
// lifecycle events usable while preventing an activated reservation from
// reopening an unbound source stream.  Terminal rejected/failed/expired rows
// have no incarnation by design, so their durable lifecycle facts must still
// materialize through the reservation discriminator.
func TestEventStorePreIncarnationReservationStateMatrix(t *testing.T) {
	for _, tc := range eventPreIncarnationCases() {
		t.Run(tc.name, func(t *testing.T) { runEventPreIncarnationCase(t, tc) })
	}
}

func setReservationStateForEventTest(t *testing.T, db *sql.DB, reservationID, state string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `UPDATE orchestration_identity_reservations SET state = ? WHERE reservation_id = ?`, state, reservationID); err != nil {
		t.Fatalf("set reservation state %q: %v", state, err)
	}
}

func TestEventStoreAcceptsIndependentIdentityGenerations(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation, incarnation, binding := seedBoundEventIdentityWithGenerations(t, db, "bound-rollover", 3, 4)
	frame := eventFrame(reservation, &incarnation, &binding, "bound-rollover", 1)
	if _, err := store.Append(t.Context(), frame); err != nil {
		t.Fatalf("Append(rollover) error = %v, want success", err)
	}
}

func changeWriterEpoch(t *testing.T, db *sql.DB, frame IngressFrame) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `UPDATE orchestration_event_source_states SET writer_epoch = ?, revision = revision + 1 WHERE source_id = ? AND source_epoch = ? AND lane = ?`, "writer-other", frame.SourceID, frame.SourceEpoch, frame.Lane); err != nil {
		t.Fatalf("change writer epoch: %v", err)
	}
}

func changeSourceStatus(t *testing.T, db *sql.DB, frame IngressFrame) {
	t.Helper()
	status := "quarantined"
	if strings.Contains(t.Name(), "/closed") {
		status = "closed"
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE orchestration_event_source_states SET status = ?, gap_reason = '', revision = revision + 1 WHERE source_id = ? AND source_epoch = ? AND lane = ?`, status, frame.SourceID, frame.SourceEpoch, frame.Lane); err != nil {
		t.Fatalf("change source status: %v", err)
	}
}

func TestEventStoreIdentityAndContentShapes(t *testing.T) {
	assertPreIncarnationEvent(t)
	assertForgedFenceEvents(t)
	assertBoundIdentityEvent(t)
	assertContentShapeEvents(t)
	assertCursorTupleRejects(t)
}

func assertPreIncarnationEvent(t *testing.T) {
	t.Helper()
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "pre")
	result, err := store.Append(t.Context(), eventFrame(reservation, nil, nil, "pre", 1))
	if err != nil || result.Receipt.Envelope.IncarnationID != nil || result.Receipt.Envelope.IncarnationGeneration != 0 || result.Receipt.Envelope.BindingVersion != 0 {
		t.Fatalf("pre-incarnation result=%#v err=%v, want NULL fence fields", result, err)
	}
}

func assertForgedFenceEvents(t *testing.T) {
	t.Helper()
	for _, field := range []string{"external_epoch", "incarnation_generation", "identity_digest", "binding_version", "binding_digest"} {
		t.Run("forged_"+field, func(t *testing.T) {
			db := openEventTestDB(t)
			store := mustEventStore(t, db)
			reservation := seedEventReservation(t, db, "forged-"+field)
			frame := eventFrame(reservation, nil, nil, "forged-"+field, 1)
			setForgedFenceField(&frame.Envelope, field)
			if _, err := store.Append(t.Context(), frame); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("forged %s error = %v, want ErrInvalidEvent", field, err)
			}
		})
	}
}

func assertBoundIdentityEvent(t *testing.T) {
	t.Helper()
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation, incarnation, binding := seedBoundEventIdentity(t, db, "bound")
	frame := eventFrame(reservation, &incarnation, &binding, "bound", 1)
	result, err := store.Append(t.Context(), frame)
	if err != nil {
		t.Fatalf("Append(bound) error = %v", err)
	}
	got, err := store.GetReceipt(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane, frame.SourceSequence)
	if err != nil {
		t.Fatalf("GetReceipt(bound) error = %v", err)
	}
	assertBoundEnvelope(t, got.Envelope, incarnation, binding)
	if result.Event == nil {
		t.Fatal("bound append did not return canonical event")
	}
}

func assertContentShapeEvents(t *testing.T) {
	t.Helper()
	for _, testCase := range contentShapeCases() {
		t.Run("content_"+testCase.name, func(t *testing.T) {
			db := openEventTestDB(t)
			store := mustEventStore(t, db)
			reservation := seedEventReservation(t, db, "content-"+testCase.name)
			frame := eventFrame(reservation, nil, nil, "content-"+testCase.name, 1)
			testCase.mutate(&frame.Envelope)
			if _, err := store.Append(t.Context(), frame); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("content shape error = %v, want ErrInvalidEvent", err)
			}
		})
	}
}

func assertCursorTupleRejects(t *testing.T) {
	t.Helper()
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "cursor-tuple")
	frame := eventFrame(reservation, nil, nil, "cursor-tuple", 1)
	frame.CanonicalCursor = "wrong/source/tuple"
	if _, err := store.Append(t.Context(), frame); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("mismatched canonical cursor error = %v, want ErrInvalidEvent", err)
	}
}

func setForgedFenceField(envelope *EventEnvelope, field string) {
	switch field {
	case "external_epoch":
		envelope.ExternalEpoch = "forged-epoch"
	case "incarnation_generation":
		envelope.IncarnationGeneration = 1
	case "identity_digest":
		envelope.IdentityDigest = eventDigest("forged-identity")
	case "binding_version":
		envelope.BindingVersion = 1
	case "binding_digest":
		envelope.BindingDigest = eventDigest("forged-binding")
	}
}

type contentShapeCase struct {
	name   string
	mutate func(*EventEnvelope)
}

func contentShapeCases() []contentShapeCase {
	return []contentShapeCase{
		{name: "none_ref", mutate: func(e *EventEnvelope) { e.ContentRef = "content/ref" }},
		{name: "none_size", mutate: func(e *EventEnvelope) { e.ContentSize = 1 }},
		{name: "none_media", mutate: func(e *EventEnvelope) { e.MediaType = "text/plain" }},
		{name: "unsupported_role", mutate: func(e *EventEnvelope) { e.ContentRole = "unknown" }},
		{name: "prompt_missing_ref", mutate: func(e *EventEnvelope) { e.ContentRole, e.MediaType = "prompt", "text/plain" }},
		{name: "prompt_missing_media", mutate: func(e *EventEnvelope) { e.ContentRole, e.ContentRef = "prompt", "content/ref" }},
		{name: "negative_size", mutate: func(e *EventEnvelope) {
			e.ContentRole, e.ContentRef, e.MediaType, e.ContentSize = "prompt", "content/ref", "text/plain", -1
		}},
	}
}

func assertBoundEnvelope(t *testing.T, envelope EventEnvelope, incarnation identity.Incarnation, binding identity.CurrentBinding) {
	t.Helper()
	if envelope.IncarnationID == nil || *envelope.IncarnationID != incarnation.IncarnationID || envelope.ExternalEpoch != incarnation.ExternalEpoch || envelope.IncarnationGeneration != incarnation.IncarnationGeneration || envelope.IdentityDigest != incarnation.IdentityDigest {
		t.Fatalf("bound incarnation fields = %#v, want %#v", envelope, incarnation)
	}
	if envelope.BindingVersion != binding.BindingVersion || envelope.BindingDigest != binding.BindingDigest || envelope.SourceFenceRef != binding.SourceFenceRef {
		t.Fatalf("bound binding fields = %#v, want %#v", envelope, binding)
	}
}

func TestEventStoreEventIDConflictAcrossSources(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "event-id")
	first := eventFrame(reservation, nil, nil, "event-id-one", 1)
	if _, err := store.Append(t.Context(), first); err != nil {
		t.Fatalf("Append(first) error = %v", err)
	}
	second := eventFrame(reservation, nil, nil, "event-id-two", 1)
	second.Envelope.EventID = first.Envelope.EventID
	second.EnvelopeHash, _ = CanonicalEnvelopeHash(second.Envelope)
	if _, err := store.Append(t.Context(), second); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("duplicate event_id across source error = %v, want ErrEventConflict", err)
	}
	if _, err := store.GetSourceState(t.Context(), second.SourceID, second.SourceEpoch, second.Lane); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("conflicting source state error = %v, want rollback/ErrEventNotFound", err)
	}
	got, err := store.GetEvent(t.Context(), first.Envelope.EventID)
	if err != nil || got.SourceID != first.SourceID || got.EnvelopeHash != first.EnvelopeHash {
		t.Fatalf("original event after duplicate = %#v err=%v, want unchanged", got, err)
	}
}

func TestEventStoreCloseReopenPreservesCanonicalRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event-reopen.sqlite")
	db := openEventTestDBAtPath(t, path)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "reopen")
	frame := eventFrame(reservation, nil, nil, "reopen", 1)
	if _, err := store.Append(t.Context(), frame); err != nil {
		t.Fatalf("Append(before close) error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close event database: %v", err)
	}
	reopenedDB := openEventTestDBAtPath(t, path)
	reopened := mustEventStore(t, reopenedDB)
	if _, err := reopened.GetEvent(t.Context(), frame.Envelope.EventID); err != nil {
		t.Fatalf("GetEvent(after reopen) error = %v", err)
	}
	if _, err := reopened.GetReceipt(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane, 1); err != nil {
		t.Fatalf("GetReceipt(after reopen) error = %v", err)
	}
	assertEventState(t, reopened, frame.SourceID, frame.SourceEpoch, frame.Lane, 2, 1, "active")
}

func TestEventStoreConcurrentAppendConverges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event-concurrent.sqlite")
	dbOne := openEventTestDBAtPath(t, path)
	dbTwo := openEventTestDBAtPath(t, path)
	storeOne := mustEventStore(t, dbOne)
	storeTwo := mustEventStore(t, dbTwo)
	reservation := seedEventReservation(t, dbOne, "concurrent")
	frame := eventFrame(reservation, nil, nil, "concurrent", 1)
	start := make(chan struct{})
	results := make(chan eventAppendResult, 2)
	var workers sync.WaitGroup
	workers.Go(func() { appendConcurrently(start, results, storeOne, frame) })
	workers.Go(func() { appendConcurrently(start, results, storeTwo, frame) })
	close(start)
	workers.Wait()
	close(results)
	for result := range results {
		if result.err != nil || result.result.Receipt.Status != "materialized" || result.result.Event == nil {
			t.Fatalf("concurrent append result = %#v err=%v, want materialized", result.result, result.err)
		}
	}
	if got := countEventRows(t, dbOne, "orchestration_event_ingress_receipts"); got != 1 {
		t.Fatalf("concurrent receipt rows = %d, want 1", got)
	}
	if got := countEventRows(t, dbOne, "orchestration_agent_events"); got != 1 {
		t.Fatalf("concurrent event rows = %d, want 1", got)
	}
	assertEventState(t, storeOne, frame.SourceID, frame.SourceEpoch, frame.Lane, 2, 1, "active")
}

type eventAppendResult struct {
	result AppendResult
	err    error
}

func appendConcurrently(start <-chan struct{}, results chan<- eventAppendResult, store *Store, frame IngressFrame) {
	<-start
	result, err := store.Append(context.Background(), frame)
	results <- eventAppendResult{result: result, err: err}
}

func TestEventStoreCursorCASAndWatermark(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "cursor")
	frame := eventFrame(reservation, nil, nil, "cursor", 1)
	key := CursorKey{ProjectionID: "projection-cursor", SourceID: frame.SourceID, SourceEpoch: frame.SourceEpoch, Lane: frame.Lane}
	appendCursorEvent(t, store, frame)
	assertCursorLifecycle(t, store, key, frame)
	assertCursorFences(t, store, key)
}

func appendCursorEvent(t *testing.T, store *Store, frame IngressFrame) {
	t.Helper()
	if _, err := store.Append(t.Context(), frame); err != nil {
		t.Fatalf("Append(cursor event) error = %v", err)
	}
}

func assertCursorLifecycle(t *testing.T, store *Store, key CursorKey, frame IngressFrame) {
	t.Helper()
	assertCursorEnsure(t, store, key)
	assertCursorAdvance(t, store, key, frame)
}

func assertCursorEnsure(t *testing.T, store *Store, key CursorKey) {
	t.Helper()
	cursor, err := store.EnsureCursor(t.Context(), key)
	if err != nil || cursor.MaterializedSequence != 0 || cursor.Revision != 1 || cursor.Status != "active" {
		t.Fatalf("EnsureCursor = %#v err=%v, want initial revision 1", cursor, err)
	}
	repeat, err := store.EnsureCursor(t.Context(), key)
	if err != nil || repeat != cursor {
		t.Fatalf("EnsureCursor(repeat) = %#v err=%v, want stable cursor", repeat, err)
	}
	if _, err := store.GetCursor(t.Context(), key); err != nil {
		t.Fatalf("GetCursor() error = %v", err)
	}
}

func assertCursorAdvance(t *testing.T, store *Store, key CursorKey, frame IngressFrame) {
	t.Helper()
	advanced, err := store.AdvanceCursor(t.Context(), key, 1, 1, frame.EnvelopeHash, "active")
	if err != nil || advanced.MaterializedSequence != 1 || advanced.Revision != 2 {
		t.Fatalf("AdvanceCursor() = %#v err=%v, want sequence 1 revision 2", advanced, err)
	}
}

func assertCursorFences(t *testing.T, store *Store, key CursorKey) {
	t.Helper()
	current, err := store.GetCursor(t.Context(), key)
	if err != nil {
		t.Fatalf("GetCursor(after advance) error = %v", err)
	}
	replay, err := store.AdvanceCursor(t.Context(), key, current.Revision, current.MaterializedSequence, current.MaterializedHash, current.Status)
	if err != nil || replay.Revision != current.Revision {
		t.Fatalf("same cursor advance = %#v err=%v, want idempotent revision", replay, err)
	}
	if _, err := store.AdvanceCursor(t.Context(), key, current.Revision-1, current.MaterializedSequence, current.MaterializedHash, "active"); !errors.Is(err, ErrEventFence) {
		t.Fatalf("stale cursor CAS error = %v, want ErrEventFence", err)
	}
	if _, err := store.AdvanceCursor(t.Context(), key, current.Revision, current.MaterializedSequence+1, eventDigest("too-far"), "active"); !errors.Is(err, ErrEventFence) {
		t.Fatalf("cursor beyond source error = %v, want ErrEventFence", err)
	}
	if _, err := store.EnsureCursor(t.Context(), CursorKey{ProjectionID: "missing", SourceID: "missing", SourceEpoch: "epoch", Lane: LaneControl}); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("EnsureCursor(missing source) error = %v, want ErrEventNotFound", err)
	}
}

func TestEventStoreTombstoneFloorAndExpiredReplay(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "tombstone")
	first := eventFrame(reservation, nil, nil, "tombstone", 1)
	second := eventFrame(reservation, nil, nil, "tombstone", 2)
	appendTombstoneEvents(t, store, first, second)
	assertTombstoneFloor(t, store, first, second)
	assertExpiredReplay(t, store, db, first)
}

func appendTombstoneEvents(t *testing.T, store *Store, first, second IngressFrame) {
	t.Helper()
	for _, frame := range []IngressFrame{first, second} {
		if _, err := store.Append(t.Context(), frame); err != nil {
			t.Fatalf("Append(%s) error = %v", frame.Envelope.EventID, err)
		}
	}
}

func assertTombstoneFloor(t *testing.T, store *Store, first, second IngressFrame) {
	t.Helper()
	assertFirstTombstone(t, store, first)
	assertSecondTombstone(t, store, first, second)
}

func assertFirstTombstone(t *testing.T, store *Store, first IngressFrame) {
	t.Helper()
	request := tombstoneRequestFor(first, 0, "expired")
	firstTombstone, err := store.RecordTombstone(t.Context(), request)
	if err != nil {
		t.Fatalf("RecordTombstone(seq1) error = %v", err)
	}
	if replay, err := store.RecordTombstone(t.Context(), request); err != nil || replay != firstTombstone {
		t.Fatalf("RecordTombstone(seq1 replay) = %#v err=%v, want original", replay, err)
	}
	state, err := store.GetSourceState(t.Context(), first.SourceID, first.SourceEpoch, first.Lane)
	if err != nil || state.DedupFloor != 1 {
		t.Fatalf("state after seq1 tombstone = %#v err=%v, want floor 1", state, err)
	}
}

func assertSecondTombstone(t *testing.T, store *Store, first, second IngressFrame) {
	t.Helper()
	secondRequest := tombstoneRequestFor(second, 1, "expired")
	if _, err := store.RecordTombstone(t.Context(), secondRequest); err != nil {
		t.Fatalf("RecordTombstone(seq2) error = %v", err)
	}
	if _, err := store.RecordTombstone(t.Context(), secondRequest); err != nil {
		t.Fatalf("RecordTombstone(seq2 replay) error = %v", err)
	}
	state, err := store.GetSourceState(t.Context(), first.SourceID, first.SourceEpoch, first.Lane)
	if err != nil || state.DedupFloor != 2 {
		t.Fatalf("state after seq2 tombstone = %#v err=%v, want floor 2", state, err)
	}
	if replay, err := store.RecordTombstone(t.Context(), tombstoneRequestFor(first, 0, "expired")); err != nil || replay.DedupFloor != 1 {
		t.Fatalf("non-contiguous floor replay = %#v err=%v, want original tombstone", replay, err)
	}
}

func assertExpiredReplay(t *testing.T, store *Store, db *sql.DB, first IngressFrame) {
	t.Helper()
	before := countEventRows(t, db, "orchestration_agent_events")
	if _, err := store.Append(t.Context(), first); !errors.Is(err, ErrEventReplayExpired) {
		t.Fatalf("low-floor same hash append = %v, want ErrEventReplayExpired", err)
	}
	if countEventRows(t, db, "orchestration_agent_events") != before {
		t.Fatal("expired replay changed canonical event count")
	}
	conflict := first
	conflict.Envelope.EventID = "event-expired-conflict"
	conflict.EnvelopeHash, _ = CanonicalEnvelopeHash(conflict.Envelope)
	if _, err := store.Append(t.Context(), conflict); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("low-floor different hash append = %v, want ErrEventConflict", err)
	}
}

func TestEventStoreNeverWritesLegacyProjections(t *testing.T) {
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	reservation := seedEventReservation(t, db, "legacy")
	before := legacyEventRowCounts(t, db)
	if _, err := store.Append(t.Context(), eventFrame(reservation, nil, nil, "legacy", 1)); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	after := legacyEventRowCounts(t, db)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("legacy projection rows changed: before=%v after=%v", before, after)
	}
}

func TestEventStoreReadMappersRejectMalformedRows(t *testing.T) {
	validCursor := storedb.OrchestrationEventMaterializationCursor{ProjectionID: "projection", SourceID: "source", SourceEpoch: "epoch", Lane: "control", MaterializedSequence: 0, Revision: 1, Status: "active", UpdatedAt: "2026-09-01T00:00:00.000000000Z"}
	for _, testCase := range []struct {
		name string
		edit func(*storedb.OrchestrationEventMaterializationCursor)
	}{
		{name: "status", edit: func(row *storedb.OrchestrationEventMaterializationCursor) { row.Status = "broken" }},
		{name: "lane", edit: func(row *storedb.OrchestrationEventMaterializationCursor) { row.Lane = "broken" }},
		{name: "timestamp", edit: func(row *storedb.OrchestrationEventMaterializationCursor) { row.UpdatedAt = "not-a-timestamp" }},
		{name: "hash", edit: func(row *storedb.OrchestrationEventMaterializationCursor) {
			row.MaterializedSequence, row.MaterializedHash = 1, "bad"
		}},
	} {
		t.Run("cursor_"+testCase.name, func(t *testing.T) {
			row := validCursor
			testCase.edit(&row)
			if _, err := materializationCursorFromRow(row); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("cursor mapper error = %v, want ErrInvalidEvent", err)
			}
		})
	}
	state := storedb.OrchestrationEventSourceState{SourceID: "source", SourceEpoch: "epoch", Lane: "control", NextSequence: 1, Revision: 1, Status: "active", WriterEpoch: "writer", CreatedAt: "2026-09-01T00:00:00.000000000Z", UpdatedAt: "2026-09-01T00:00:00.000000000Z"}
	state.MaterializedSequence = 1
	if _, err := sourceStateFromRow(state); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("source state mapper error = %v, want ErrInvalidEvent", err)
	}
	state.NextSequence, state.MaterializedSequence = 10, 1
	if _, err := sourceStateFromRow(state); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("non-dense source state mapper error = %v, want ErrInvalidEvent", err)
	}
}
