package taskeventsource

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

// TestRuntimeEventTxCandidateUsesCanonicalStores 验证真实 identity/event owner 共用一笔 *sql.Tx。
func TestRuntimeEventTxCandidateUsesCanonicalStores(t *testing.T) {
	db := openRuntimeEventTxStoreDB(t)
	identityStore, eventStore, chain := seedRuntimeEventTxStores(t, db)
	owner := newRuntimeEventTxSQLOwner(db)
	candidate := mustRuntimeEventTxStoreCandidate(t, owner, identityStore, eventStore, eventStore)
	result, err := candidate.Append(t.Context(), runtimeStoreEvent(chain.reservation.AgentID, "runtime-tx-event"))
	if err != nil {
		t.Fatalf("candidate.Append() error = %v", err)
	}
	assertRuntimeStoreAppendResult(t, result)
	assertRuntimeStorePersistedEvent(t, eventStore, chain.reservation.AgentID, chain.incarnation.IncarnationID, "runtime-tx-event")
}

// TestRuntimeEventTxCandidateRollbackWithCanonicalAppender 验证真实 append 写入在 callback 错误时整体回滚。
func TestRuntimeEventTxCandidateRollbackWithCanonicalAppender(t *testing.T) {
	db := openRuntimeEventTxStoreDB(t)
	identityStore, eventStore, chain := seedRuntimeEventTxStores(t, db)
	owner := newRuntimeEventTxSQLOwner(db)
	first := mustRuntimeEventTxStoreCandidate(t, owner, identityStore, eventStore, eventStore)
	if _, err := first.Append(t.Context(), runtimeStoreEvent(chain.reservation.AgentID, "runtime-tx-first")); err != nil {
		t.Fatalf("first candidate.Append() error = %v", err)
	}
	rollbackAppender := &runtimeStoreRollbackAppender{store: eventStore, err: errors.New("rollback canonical append")}
	second := mustRuntimeEventTxStoreCandidate(t, owner, identityStore, eventStore, rollbackAppender)
	if _, err := second.Append(t.Context(), runtimeStoreEvent(chain.reservation.AgentID, "runtime-tx-rollback")); !errors.Is(err, rollbackAppender.err) {
		t.Fatalf("rollback candidate.Append() error = %v, want %v", err, rollbackAppender.err)
	}
	assertRuntimeStoreState(t, eventStore, chain.reservation.AgentID, chain.incarnation.IncarnationID, 2, 1)
	if _, err := eventStore.GetEvent(t.Context(), "runtime-tx-rollback"); !errors.Is(err, eventstore.ErrEventNotFound) {
		t.Fatalf("rolled-back event read = %v, want ErrEventNotFound", err)
	}
}

func openRuntimeEventTxStoreDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(t.TempDir(), "runtime-event-tx.sqlite")})
	if err != nil {
		t.Fatalf("open runtime event tx database: %v", err)
	}
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		_ = db.Close()
		t.Fatalf("run runtime event tx migrations: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close runtime event tx database: %v", err)
		}
	})
	return db
}

func seedRuntimeEventTxStores(t *testing.T, db *sql.DB) (*identity.Store, *eventstore.Store, runtimeStoreIdentity) {
	t.Helper()
	identityStore, err := identity.NewStore(db)
	if err != nil {
		t.Fatalf("create identity store: %v", err)
	}
	chain := seedRuntimeStoreIdentity(t, db, identityStore)
	if _, err := db.ExecContext(t.Context(), `INSERT INTO orchestration_event_source_states (source_id, source_epoch, lane, next_sequence, materialized_sequence, dedup_floor, revision, status, gap_reason, writer_epoch) VALUES (?, ?, ?, 1, 0, 0, 1, 'active', '', ?)`, "task/"+chain.reservation.AgentID, chain.incarnation.IncarnationID, "output", "writer-1"); err != nil {
		t.Fatalf("seed event source state: %v", err)
	}
	verifier, err := eventstore.NewIdentityFenceVerifier(identityStore)
	if err != nil {
		t.Fatalf("create event fence verifier: %v", err)
	}
	eventStore, err := eventstore.NewStore(db, verifier)
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	return identityStore, eventStore, chain
}

type runtimeStoreIdentity struct {
	reservation identity.Reservation
	incarnation identity.Incarnation
}

func seedRuntimeStoreIdentity(t *testing.T, db *sql.DB, store *identity.Store) runtimeStoreIdentity {
	t.Helper()
	commandStore, err := orchestrationcommand.NewStore(db)
	if err != nil {
		t.Fatalf("create command store: %v", err)
	}
	commandID := "runtime-tx-command"
	if _, err := commandStore.Reserve(t.Context(), orchestrationcommand.Intent{
		CommandID: commandID, CommandKind: "launch_agent", CallerPrincipalRef: "runtime-tx-principal",
		ScopeRef: "runtime-tx-scope", IdempotencyNamespace: "runtime-tx-command", IdempotencyKeyDigest: runtimeStoreDigest('a'),
		RequestDigest: runtimeStoreDigest('b'), DigestKeyID: "runtime-tx-key", RequestContentRef: "runtime-tx-content",
		SourceFenceRef: "runtime-tx-source-fence", TargetFenceRef: "runtime-tx-target-fence",
	}); err != nil {
		t.Fatalf("seed command intent: %v", err)
	}
	reservation, err := store.Reserve(t.Context(), identity.ReservationRequest{
		ReservationID: "runtime-tx-reservation", CommandID: commandID, AgentID: "runtime-tx-agent",
		IdentityKind: "managed_agent", SourceKind: "first_launch", CallerPrincipalRef: "runtime-tx-principal",
		ScopeRef: "runtime-tx-scope", IdempotencyNamespace: "runtime-tx-reservation", IdempotencyKeyDigest: runtimeStoreDigest('c'),
		RequestDigest: runtimeStoreDigest('d'), SourceFenceRef: "runtime-tx-source-fence", TargetFenceRef: "runtime-tx-target-fence",
		ReservationGeneration: 1,
	})
	if err != nil {
		t.Fatalf("seed identity reservation: %v", err)
	}
	incarnation, err := store.CreateIncarnation(t.Context(), identity.IncarnationRequest{
		IncarnationID: "runtime-tx-incarnation", ReservationID: reservation.ReservationID, AgentID: reservation.AgentID,
		IdentityKind: reservation.IdentityKind, ExternalIdentityRef: "runtime-tx-external", ExternalEpoch: "runtime-tx-epoch",
		SourceFenceRef: reservation.SourceFenceRef, IdentityDigest: runtimeStoreDigest('e'), IncarnationGeneration: 1,
	})
	if err != nil {
		t.Fatalf("seed identity incarnation: %v", err)
	}
	preparation, err := store.PrepareBinding(t.Context(), identity.BindingPreparationRequest{
		PreparationID: "runtime-tx-preparation", ReservationID: reservation.ReservationID, IncarnationID: incarnation.IncarnationID,
		AgentID: reservation.AgentID, IdentityKind: reservation.IdentityKind, SourceFenceRef: reservation.SourceFenceRef,
		BindingDigest: runtimeStoreDigest('f'), PreparationGeneration: 1, ExpectedBindingVersion: 0,
		ExpectedIncarnationGeneration: 1,
	})
	if err != nil {
		t.Fatalf("seed binding preparation: %v", err)
	}
	if _, err := store.FinalizeBinding(t.Context(), identity.FinalizeBindingRequest{
		PreparationID: preparation.PreparationID, AgentID: reservation.AgentID, ExpectedPreparationGeneration: 1,
		ExpectedBindingVersion: 0, IncarnationID: incarnation.IncarnationID, ExternalIdentityRef: incarnation.ExternalIdentityRef,
		IdentityKind: reservation.IdentityKind, SourceFenceRef: reservation.SourceFenceRef,
		ActiveProcessReceiptRef: "runtime-tx-process", BindingDigest: preparation.BindingDigest,
	}); err != nil {
		t.Fatalf("seed active binding: %v", err)
	}
	return runtimeStoreIdentity{reservation: reservation, incarnation: incarnation}
}

func newRuntimeEventTxSQLOwner(db *sql.DB) RuntimeEventTransactionOwner {
	return RuntimeEventTransactionOwnerFunc(func(ctx context.Context, callback func(storedb.DBTX) error) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		callbackErr := callback(tx)
		if callbackErr != nil {
			_ = tx.Rollback()
			return callbackErr
		}
		return tx.Commit()
	})
}

func mustRuntimeEventTxStoreCandidate(
	t *testing.T,
	owner RuntimeEventTransactionOwner,
	identities RuntimeEventTransactionIdentityReader,
	states RuntimeEventTransactionSourceReader,
	appender RuntimeEventTransactionAppender,
) *RuntimeEventTxCandidate {
	t.Helper()
	candidate, err := NewRuntimeEventTxCandidate(owner, identities, states, appender)
	if err != nil {
		t.Fatalf("NewRuntimeEventTxCandidate() error = %v", err)
	}
	return candidate
}

func runtimeStoreEvent(agentID, eventID string) RuntimeEvent {
	return RuntimeEvent{AgentID: agentID, Lane: eventstore.LaneOutput, EventID: eventID, EventType: "task.output",
		Provenance: "runtime-test", TrustClass: "trusted", ContentRole: "none", ObservedAt: "2026-09-03T00:00:00Z", WriterEpoch: "writer-1"}
}

func assertRuntimeStoreAppendResult(t *testing.T, result eventstore.AppendResult) {
	t.Helper()
	if result.Receipt.Status != "materialized" || result.Event == nil || result.State.NextSequence != 2 {
		t.Fatalf("append result = %#v, want materialized event and next=2", result)
	}
}

func assertRuntimeStorePersistedEvent(t *testing.T, store *eventstore.Store, agentID, incarnationID, eventID string) {
	t.Helper()
	event, err := store.GetEvent(t.Context(), eventID)
	if err != nil {
		t.Fatalf("GetEvent() error = %v", err)
	}
	if event.SourceID != "task/"+agentID || event.SourceEpoch != incarnationID || event.SourceSequence != 1 {
		t.Fatalf("persisted event source = %#v", event)
	}
}

func assertRuntimeStoreState(t *testing.T, store *eventstore.Store, agentID, incarnationID string, next, materialized int64) {
	t.Helper()
	state, err := store.GetSourceState(t.Context(), "task/"+agentID, incarnationID, eventstore.LaneOutput)
	if err != nil {
		t.Fatalf("GetSourceState() error = %v", err)
	}
	if state.NextSequence != next || state.MaterializedSequence != materialized {
		t.Fatalf("source state = %#v, want next=%d materialized=%d", state, next, materialized)
	}
}

func runtimeStoreDigest(char byte) string {
	return strings.Repeat(string(char), 64)
}

type runtimeStoreRollbackAppender struct {
	store *eventstore.Store
	err   error
}

func (a *runtimeStoreRollbackAppender) AppendTx(ctx context.Context, txdb storedb.DBTX, frame eventstore.IngressFrame) (eventstore.AppendResult, error) {
	result, err := a.store.AppendTx(ctx, txdb, frame)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	return result, a.err
}

var (
	_ RuntimeEventTransactionOwner          = (*eventstore.Store)(nil)
	_ RuntimeEventTransactionIdentityReader = (*identity.Store)(nil)
	_ RuntimeEventTransactionSourceReader   = (*eventstore.Store)(nil)
	_ RuntimeEventTransactionAppender       = (*eventstore.Store)(nil)
)
