package eventstore

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

type eventPreIncarnationCase struct {
	name      string
	prepare   func(*testing.T, *sql.DB, identity.Reservation)
	wantErr   error
	wantEvent bool
}

func eventPreIncarnationCases() []eventPreIncarnationCase {
	return []eventPreIncarnationCase{
		{name: "rejected", prepare: func(t *testing.T, db *sql.DB, reservation identity.Reservation) {
			setReservationStateForEventTest(t, db, reservation.ReservationID, "rejected")
		}, wantEvent: true},
		{name: "failed", prepare: func(t *testing.T, db *sql.DB, reservation identity.Reservation) {
			if _, err := db.ExecContext(t.Context(), `UPDATE orchestration_identity_reservations SET state = 'claimed' WHERE reservation_id = ?`, reservation.ReservationID); err != nil {
				t.Fatalf("advance reservation to claimed: %v", err)
			}
			setReservationStateForEventTest(t, db, reservation.ReservationID, "failed")
		}, wantEvent: true},
		{name: "expired", prepare: func(t *testing.T, db *sql.DB, reservation identity.Reservation) {
			setReservationStateForEventTest(t, db, reservation.ReservationID, "expired")
		}, wantEvent: true},
		{name: "activated", prepare: func(*testing.T, *sql.DB, identity.Reservation) {}, wantErr: ErrEventFence},
	}
}

func runEventPreIncarnationCase(t *testing.T, tc eventPreIncarnationCase) {
	t.Helper()
	db := openEventTestDB(t)
	store := mustEventStore(t, db)
	suffix := "pre-incarnation-state-" + tc.name
	var reservation identity.Reservation
	if tc.name == "activated" {
		reservation, _, _ = seedBoundEventIdentity(t, db, suffix)
	} else {
		reservation = seedEventReservation(t, db, suffix)
	}
	tc.prepare(t, db, reservation)
	frame := eventFrame(reservation, nil, nil, suffix, 1)
	result, err := store.Append(t.Context(), frame)
	if tc.wantErr != nil {
		if !errors.Is(err, tc.wantErr) || result != (AppendResult{}) {
			t.Fatalf("Append(%s) = %#v, error=%v, want fenced zero result", tc.name, result, err)
		}
		if _, stateErr := store.GetSourceState(t.Context(), frame.SourceID, frame.SourceEpoch, frame.Lane); !errors.Is(stateErr, ErrEventNotFound) {
			t.Fatalf("activated source state = %v, want absent", stateErr)
		}
		return
	}
	if err != nil || !tc.wantEvent || result.Event == nil || result.Receipt.Status != "materialized" {
		t.Fatalf("Append(%s) result = %#v, error=%v, want materialized event", tc.name, result, err)
	}
}

func openEventTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return openEventTestDBAtPath(t, filepath.Join(t.TempDir(), "event.sqlite"))
}

func openEventTestDBAtPath(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: path})
	if err != nil {
		t.Fatalf("sqliteruntime.Open(%q) error = %v", path, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close event database: %v", err)
		}
	})
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatalf("RunEmbeddedMigrations() error = %v", err)
	}
	return db
}

func mustEventStore(t *testing.T, db *sql.DB) *Store {
	t.Helper()
	identityStore, err := identity.NewStore(db)
	if err != nil {
		t.Fatalf("NewStore(identity verifier) error = %v", err)
	}
	verifier, err := NewIdentityFenceVerifier(identityStore)
	if err != nil {
		t.Fatalf("NewIdentityFenceVerifier() error = %v", err)
	}
	store, err := NewStore(db, verifier)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

func seedEventReservation(t *testing.T, db *sql.DB, suffix string) identity.Reservation {
	t.Helper()
	return seedEventReservationWithGeneration(t, db, suffix, 1)
}

func seedEventReservationWithGeneration(t *testing.T, db *sql.DB, suffix string, generation int64) identity.Reservation {
	t.Helper()
	commandStore, err := orchestrationcommand.NewStore(db)
	if err != nil {
		t.Fatalf("NewStore(command) error = %v", err)
	}
	commandID := "event-command-" + suffix
	if _, err := commandStore.Reserve(t.Context(), orchestrationcommand.Intent{CommandID: commandID, CommandKind: "launch_agent", CallerPrincipalRef: "principal/" + suffix, ScopeRef: "scope/" + suffix, IdempotencyNamespace: "event-command/" + suffix, IdempotencyKeyDigest: eventDigest("command-key-" + suffix), RequestDigest: eventDigest("command-request-" + suffix), DigestKeyID: "event-test-key", RequestContentRef: "content/" + suffix, SourceFenceRef: "source/" + suffix, TargetFenceRef: "target/" + suffix}); err != nil {
		t.Fatalf("seed command %q error = %v", commandID, err)
	}
	identityStore, err := identity.NewStore(db)
	if err != nil {
		t.Fatalf("NewStore(identity) error = %v", err)
	}
	request := identity.ReservationRequest{ReservationID: "event-reservation-" + suffix, CommandID: commandID, AgentID: "event-agent-" + suffix, IdentityKind: "native_subagent", SourceKind: "first_launch", CallerPrincipalRef: "principal/" + suffix, ScopeRef: "scope/" + suffix, IdempotencyNamespace: "event-reservation/" + suffix, IdempotencyKeyDigest: eventDigest("reservation-key-" + suffix), RequestDigest: eventDigest("reservation-request-" + suffix), SourceFenceRef: "source/" + suffix, TargetFenceRef: "target/" + suffix, ReservationGeneration: generation}
	reservation, err := identityStore.Reserve(t.Context(), request)
	if err != nil {
		t.Fatalf("seed reservation %q error = %v", suffix, err)
	}
	return reservation
}

func seedBoundEventIdentity(t *testing.T, db *sql.DB, suffix string) (identity.Reservation, identity.Incarnation, identity.CurrentBinding) {
	t.Helper()
	return seedBoundEventIdentityWithGenerations(t, db, suffix, 1, 1)
}

func seedBoundEventIdentityWithGenerations(t *testing.T, db *sql.DB, suffix string, reservationGeneration, incarnationGeneration int64) (identity.Reservation, identity.Incarnation, identity.CurrentBinding) {
	t.Helper()
	reservation := seedEventReservationWithGeneration(t, db, suffix, reservationGeneration)
	identityStore, err := identity.NewStore(db)
	if err != nil {
		t.Fatalf("NewStore(identity bound) error = %v", err)
	}
	incarnationRequest := identity.IncarnationRequest{IncarnationID: "event-incarnation-" + suffix, ReservationID: reservation.ReservationID, AgentID: reservation.AgentID, IdentityKind: reservation.IdentityKind, ExternalIdentityRef: "external/" + suffix, ExternalEpoch: "external-epoch-" + suffix, SourceFenceRef: reservation.SourceFenceRef, IdentityDigest: eventDigest("identity-" + suffix), IncarnationGeneration: incarnationGeneration}
	incarnation, err := identityStore.CreateIncarnation(t.Context(), incarnationRequest)
	if err != nil {
		t.Fatalf("CreateIncarnation() error = %v", err)
	}
	preparation, err := identityStore.PrepareBinding(t.Context(), identity.BindingPreparationRequest{PreparationID: "event-preparation-" + suffix, ReservationID: reservation.ReservationID, IncarnationID: incarnation.IncarnationID, AgentID: reservation.AgentID, IdentityKind: reservation.IdentityKind, SourceFenceRef: reservation.SourceFenceRef, BindingDigest: eventDigest("binding-" + suffix), PreparationGeneration: 1, ExpectedBindingVersion: 0, ExpectedIncarnationGeneration: incarnation.IncarnationGeneration})
	if err != nil {
		t.Fatalf("PrepareBinding() error = %v", err)
	}
	binding, err := identityStore.FinalizeBinding(t.Context(), identity.FinalizeBindingRequest{PreparationID: preparation.PreparationID, AgentID: reservation.AgentID, ExpectedPreparationGeneration: preparation.PreparationGeneration, ExpectedBindingVersion: 0, IncarnationID: incarnation.IncarnationID, ExternalIdentityRef: incarnation.ExternalIdentityRef, IdentityKind: reservation.IdentityKind, SourceFenceRef: reservation.SourceFenceRef, ActiveProcessReceiptRef: "process-receipt/" + suffix, BindingDigest: preparation.BindingDigest})
	if err != nil {
		t.Fatalf("FinalizeBinding() error = %v", err)
	}
	return reservation, incarnation, binding
}

func eventFrame(reservation identity.Reservation, incarnation *identity.Incarnation, binding *identity.CurrentBinding, suffix string, sequence int64) IngressFrame {
	sourceID, epoch := "source-"+suffix, "epoch-"+suffix
	envelope := EventEnvelope{EventID: fmt.Sprintf("event-%s-%d", suffix, sequence), EventType: "orchestration.test", SchemaVersion: 1, RootAgentID: reservation.AgentID, SubjectAgentID: reservation.AgentID, ReservationID: reservation.ReservationID, SourceFenceRef: reservation.SourceFenceRef, Provenance: "test-fixture", TrustClass: "trusted", ContentRole: "none", ObservedAt: "2026-09-01T00:00:00.000000000Z", WriterEpoch: "writer-1"}
	if incarnation != nil && binding != nil {
		id := incarnation.IncarnationID
		sourceID, epoch = "task/"+envelope.RootAgentID, id
		envelope.IncarnationID = &id
		envelope.ExternalEpoch, envelope.IncarnationGeneration, envelope.IdentityDigest = incarnation.ExternalEpoch, incarnation.IncarnationGeneration, incarnation.IdentityDigest
		envelope.BindingVersion, envelope.BindingDigest = binding.BindingVersion, binding.BindingDigest
		envelope.ContentRole, envelope.ContentRef, envelope.ContentSize, envelope.MediaType = "event", "content/"+suffix, int64(len(suffix)), "application/json"
		provider := "test-provider"
		envelope.ProviderAt = &provider
	}
	hash, _ := CanonicalEnvelopeHash(envelope)
	return IngressFrame{SourceID: sourceID, SourceEpoch: epoch, Lane: LaneControl, SourceSequence: sequence, EnvelopeHash: hash, CanonicalCursor: canonicalCursor(sourceID, epoch, LaneControl, sequence), Envelope: envelope}
}

func tombstoneRequestFor(frame IngressFrame, expectedFloor int64, status string) TombstoneRequest {
	return TombstoneRequest{SourceID: frame.SourceID, SourceEpoch: frame.SourceEpoch, Lane: frame.Lane, SourceSequence: frame.SourceSequence, EnvelopeHash: frame.EnvelopeHash, EventID: frame.Envelope.EventID, CanonicalCursor: frame.CanonicalCursor, ExpectedFloor: expectedFloor, Status: status}
}

func assertEventState(t *testing.T, store *Store, sourceID, epoch string, lane Lane, next, materialized int64, status string) {
	t.Helper()
	state, err := store.GetSourceState(t.Context(), sourceID, epoch, lane)
	if err != nil {
		t.Fatalf("GetSourceState() error = %v", err)
	}
	if state.NextSequence != next || state.MaterializedSequence != materialized || state.Status != status {
		t.Fatalf("source state = %#v, want next=%d materialized=%d status=%s", state, next, materialized, status)
	}
}

func eventDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func countEventRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	return count
}

func legacyEventRowCounts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	tables := []string{"orchestration_direct_peer_events", "orchestration_direct_peer_event_outbox", "orchestration_direct_peer_grants", "orchestration_direct_peer_messages", "terminal_outcome_current_heads", "public_terminal_outcome_history", "terminal_outcome_private_dag_payloads", "terminal_outcome_outbox_v2", "task_dags", "task_dag_runs", "task_dag_nodes", "task_dag_wakeups", "task_dag_worker_leases"}
	counts := make(map[string]int, len(tables))
	for _, table := range tables {
		counts[table] = countEventRows(t, db, table)
	}
	return counts
}
