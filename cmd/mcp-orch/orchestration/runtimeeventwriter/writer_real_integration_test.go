package runtimeeventwriter

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

// TestWriterRealSQLiteAllocatorIntegration exercises the complete bounded
// runtime ingress seam against one durable SQLite database. It deliberately
// excludes Fx, providers, and public adapters: those layers are not required
// to prove identity-fenced allocation, dense concurrent sequences, and reopen
// persistence.
func TestWriterRealSQLiteAllocatorIntegration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-event-real.sqlite")
	db := openRealWriterDB(t, path)
	identityStore := mustRealIdentityStore(t, db)
	active := seedRealActiveIdentity(t, identityStore)
	eventStore := mustRealEventStore(t, db, identityStore)
	writer := mustRealWriter(t, eventStore, identityStore)

	assertRealFirstAndReplay(t, writer, eventStore, active.AgentID)
	appendRealConcurrent(t, eventStore, identityStore, active.AgentID)
	assertRealDense(t, eventStore, active)
	if err := db.Close(); err != nil {
		t.Fatalf("close first database = %v", err)
	}
	assertRealReopen(t, path, active)
}

func assertRealFirstAndReplay(t *testing.T, writer *Writer, eventStore *eventstore.Store, agentID string) {
	t.Helper()
	first := realRuntimeEvent(agentID, "runtime-real-first")
	firstResult, err := writer.Append(t.Context(), first)
	assertRealMaterializedResult(t, "first bound append", firstResult, err, 1)
	replay, err := eventStore.Append(t.Context(), firstResult.Receipt.IngressFrame)
	assertRealMaterializedResult(t, "canonical replay", replay, err, 1)
	replay, err = writer.Append(t.Context(), first)
	assertRealConflict(t, replay, err)
}

// assertRealMaterializedResult 校验真实 SQLite 写入返回的物化事件和连续序号。
func assertRealMaterializedResult(t *testing.T, operation string, result eventstore.AppendResult, err error, sequence int64) {
	t.Helper()
	if err != nil || result.Receipt.Status != "materialized" || result.Receipt.SourceSequence != sequence || result.Event == nil {
		t.Fatalf("%s = %#v, error=%v, want materialized sequence %d", operation, result, err, sequence)
	}
}

// assertRealConflict 校验重复写入被冲突错误拒绝且不产生结果。
func assertRealConflict(t *testing.T, result eventstore.AppendResult, err error) {
	t.Helper()
	if !errors.Is(err, eventstore.ErrEventConflict) || result != (eventstore.AppendResult{}) {
		t.Fatalf("replayed writer append = %#v, error=%v, want conflict and zero result", result, err)
	}
}

func appendRealConcurrent(t *testing.T, eventStore *eventstore.Store, identityStore *identity.Store, agentID string) {
	t.Helper()
	const concurrent = 8
	var wg sync.WaitGroup
	errs := make(chan error, concurrent)
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		runtimesafe.SafeGo(t.Context(), nil, fmt.Sprintf("runtimeeventwriter.test.real-concurrent-%02d", i), func(context.Context) {
			defer wg.Done()
			writer, err := New(eventStore, identityStore, eventStore, eventStore)
			if err != nil {
				errs <- err
				return
			}
			result, err := writer.Append(context.Background(), realRuntimeEvent(agentID, fmt.Sprintf("runtime-real-concurrent-%02d", i)))
			if err != nil {
				errs <- err
			} else if result.Receipt.Status != "materialized" || result.Event == nil {
				errs <- fmt.Errorf("concurrent result = %#v, want materialized event", result)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent bound append error = %v", err)
	}
}

func assertRealDense(t *testing.T, eventStore *eventstore.Store, active identity.CurrentBinding) {
	t.Helper()
	const concurrent = 8
	sourceID := "task/" + active.AgentID
	state, err := eventStore.GetSourceState(t.Context(), sourceID, active.IncarnationID, eventstore.LaneOutput)
	if err != nil || state.NextSequence != concurrent+2 || state.MaterializedSequence != concurrent+1 || state.Status != "active" {
		t.Fatalf("source state after concurrent append = %#v, error=%v, want next=%d materialized=%d active", state, err, concurrent+2, concurrent+1)
	}
	events, err := eventStore.ListEvents(t.Context(), sourceID, active.IncarnationID, eventstore.LaneOutput, 0, concurrent+1)
	if err != nil || len(events) != concurrent+1 || events[0].SourceSequence != 1 || events[len(events)-1].SourceSequence != concurrent+1 {
		t.Fatalf("events before reopen = %d, error=%v, want dense 1..%d", len(events), err, concurrent+1)
	}
}

func assertRealReopen(t *testing.T, path string, active identity.CurrentBinding) {
	t.Helper()
	reopened := openRealWriterDB(t, path)
	t.Cleanup(func() { _ = reopened.Close() })
	reopenedIdentity := mustRealIdentityStore(t, reopened)
	reopenedEventStore := mustRealEventStore(t, reopened, reopenedIdentity)
	snapshot, err := reopenedIdentity.GetActiveSnapshot(t.Context(), active.AgentID)
	if err != nil || snapshot.Binding.IncarnationID != active.IncarnationID || snapshot.Binding.State != "active" {
		t.Fatalf("reopened identity snapshot = %#v, error=%v, want active incarnation %q", snapshot, err, active.IncarnationID)
	}
	const concurrent = 8
	events, err := reopenedEventStore.ListEvents(t.Context(), "task/"+active.AgentID, active.IncarnationID, eventstore.LaneOutput, 0, concurrent+1)
	if err != nil || len(events) != concurrent+1 || events[0].SourceSequence != 1 || events[len(events)-1].SourceSequence != concurrent+1 {
		t.Fatalf("reopened events = %d, error=%v, want dense 1..%d", len(events), err, concurrent+1)
	}
}

func openRealWriterDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(t.Context(), sqlite.OpenOptions{Path: path})
	if err != nil {
		t.Fatalf("sqlite.Open(%q) error = %v", path, err)
	}
	if err := sqlite.RunEmbeddedMigrations(t.Context(), db); err != nil {
		_ = db.Close()
		t.Fatalf("RunEmbeddedMigrations() error = %v", err)
	}
	return db
}

func mustRealIdentityStore(t *testing.T, db *sql.DB) *identity.Store {
	t.Helper()
	store, err := identity.NewStore(db)
	if err != nil {
		t.Fatalf("identity.NewStore() error = %v", err)
	}
	return store
}

func mustRealEventStore(t *testing.T, db *sql.DB, identityStore *identity.Store) *eventstore.Store {
	t.Helper()
	verifier, err := eventstore.NewIdentityFenceVerifier(identityStore)
	if err != nil {
		t.Fatalf("eventstore.NewIdentityFenceVerifier() error = %v", err)
	}
	store, err := eventstore.NewStore(db, verifier)
	if err != nil {
		t.Fatalf("eventstore.NewStore() error = %v", err)
	}
	return store
}

func mustRealWriter(t *testing.T, eventStore *eventstore.Store, identityStore *identity.Store) *Writer {
	t.Helper()
	writer, err := New(eventStore, identityStore, eventStore, eventStore)
	if err != nil {
		t.Fatalf("runtimeeventwriter.New() error = %v", err)
	}
	return writer
}

func seedRealActiveIdentity(t *testing.T, store *identity.Store) identity.CurrentBinding {
	t.Helper()
	const agentID = "runtime-real-agent"
	reservation, err := store.Reserve(t.Context(), identity.ReservationRequest{
		ReservationID: "runtime-real-reservation", AgentID: agentID, IdentityKind: "a2a_remote_agent", SourceKind: "a2a_observation",
		CallerPrincipalRef: "runtime-real-principal", ScopeRef: "runtime-real-scope",
		IdempotencyNamespace: "runtime-real-reservation", IdempotencyKeyDigest: realDigest("reservation-key"), RequestDigest: realDigest("reservation-request"),
		SourceFenceRef: "runtime-real-source-fence", TargetFenceRef: "runtime-real-target-fence", ReservationGeneration: 1,
	})
	if err != nil {
		t.Fatalf("seed identity reservation error = %v", err)
	}
	incarnation, err := store.CreateIncarnation(t.Context(), identity.IncarnationRequest{
		IncarnationID: "runtime-real-incarnation", ReservationID: reservation.ReservationID, AgentID: reservation.AgentID,
		IdentityKind: reservation.IdentityKind, ExternalIdentityRef: "runtime-real-external", ExternalEpoch: "runtime-real-epoch",
		SourceFenceRef: reservation.SourceFenceRef, IdentityDigest: realDigest("identity"), IncarnationGeneration: 1,
	})
	if err != nil {
		t.Fatalf("seed identity incarnation error = %v", err)
	}
	preparation, err := store.PrepareBinding(t.Context(), identity.BindingPreparationRequest{
		PreparationID: "runtime-real-preparation", ReservationID: reservation.ReservationID, IncarnationID: incarnation.IncarnationID,
		AgentID: reservation.AgentID, IdentityKind: reservation.IdentityKind, SourceFenceRef: reservation.SourceFenceRef,
		BindingDigest: realDigest("binding"), PreparationGeneration: 1, ExpectedBindingVersion: 0, ExpectedIncarnationGeneration: 1,
	})
	if err != nil {
		t.Fatalf("seed identity preparation error = %v", err)
	}
	binding, err := store.FinalizeBinding(t.Context(), identity.FinalizeBindingRequest{
		PreparationID: preparation.PreparationID, AgentID: reservation.AgentID, ExpectedPreparationGeneration: 1, ExpectedBindingVersion: 0,
		IncarnationID: incarnation.IncarnationID, ExternalIdentityRef: incarnation.ExternalIdentityRef, IdentityKind: reservation.IdentityKind,
		SourceFenceRef: reservation.SourceFenceRef, ActiveProcessReceiptRef: "runtime-real-process", BindingDigest: preparation.BindingDigest,
	})
	if err != nil {
		t.Fatalf("seed identity finalize error = %v", err)
	}
	return binding
}

func realRuntimeEvent(agentID, eventID string) Event {
	return Event{AgentID: agentID, Lane: eventstore.LaneOutput, EventID: eventID, EventType: "task.output", Provenance: "runtime", TrustClass: "trusted", ContentRole: "none", ObservedAt: "2026-09-03T00:00:00Z", WriterEpoch: "runtime-real-writer"}
}

func realDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
