package waitoperation

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCommitMarkerProofMismatchFencesOwner(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	clock := fixture.now
	base := mustBaseStore(t, fixture.db, clock)
	request := markerPlanRequest(fixture, "marker-proof-mismatch")
	operation := mustPlanMarker(t, base, request)
	descriptor := mustStateAdvanceDescriptor(t, operation)
	insertPendingMarkerWithoutPostDigest(t, fixture.db, descriptor, clock)
	setMarkerPostDigest(t, fixture.db, descriptor, strings.Repeat("e", 64))
	store := mustMarkedStore(t, fixture.db, clock)
	assertProofMismatch(t, store, descriptor.MarkerKey())
	assertFencedExactGet(t, store, request)
}

func assertProofMismatch(t *testing.T, store *Store, markerKey string) {
	t.Helper()
	if _, err := store.ResolveCommitMarker(t.Context(), markerKey); !errors.Is(err, ErrCommitMarkerProofMismatch) {
		t.Fatalf("proof mismatch resolution = %v, want ErrCommitMarkerProofMismatch", err)
	}
	if !store.fenced.Load() {
		t.Fatal("proof mismatch did not fence owner")
	}
}

func assertFencedExactGet(t *testing.T, store *Store, request PlanRequest) {
	t.Helper()
	if _, err := store.Get(t.Context(), OperationLookup{Key: request.OperationKey, Digest: request.OperationDigest}); err != nil {
		t.Fatalf("exact Get after proof mismatch = %v", err)
	}
}

func TestCommitMarkerProcessFenceRequiresOwnerRestartAfterProof(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	clock := fixture.now
	base := mustBaseStore(t, fixture.db, clock)
	request := markerPlanRequest(fixture, "marker-process-fence")
	operation := mustPlanMarker(t, base, request)
	descriptor := mustStateAdvanceDescriptor(t, operation)
	insertPendingMarkerWithoutPostDigest(t, fixture.db, descriptor, clock)
	store := mustMarkedStore(t, fixture.db, clock)
	assertMissingProofFence(t, store, descriptor.MarkerKey())
	setMarkerPostDigest(t, fixture.db, descriptor, operationStateDigest(operation))
	mustResolveMarker(t, store, descriptor.MarkerKey())
	assertProcessFenceRetained(t, store)
	restarted := mustMarkedStore(t, fixture.db, clock)
	assertRestartClearsFence(t, restarted)
}

func assertMissingProofFence(t *testing.T, store *Store, markerKey string) {
	t.Helper()
	if _, err := store.ResolveCommitMarker(t.Context(), markerKey); !errors.Is(err, ErrCommitMarkerProofRequired) {
		t.Fatalf("missing proof resolution = %v, want ErrCommitMarkerProofRequired", err)
	}
	if !store.processFenced.Load() || !store.fenced.Load() {
		t.Fatal("missing proof did not install process fence")
	}
}

func assertProcessFenceRetained(t *testing.T, store *Store) {
	t.Helper()
	if !store.fenced.Load() {
		t.Fatal("successful proof cleared process fence without owner restart")
	}
}

func assertRestartClearsFence(t *testing.T, store *Store) {
	t.Helper()
	if store.fenced.Load() || store.processFenced.Load() {
		t.Fatal("owner restart retained fence after all markers resolved")
	}
}

func TestCommitMarkerRejectsForgedCanonicalKey(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	markerKey := strings.Repeat("f", 64)
	insertForgedMarker(t, fixture.db, markerKey, fixture.now)
	markers := mustCommitMarkerStore(t, fixture.db, fixture.now)
	if _, err := markers.ListPending(t.Context()); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("forged marker key decode = %v, want ErrInvalidOperation", err)
	}
}

func insertForgedMarker(t *testing.T, db *sql.DB, markerKey string, clock time.Time) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO orchestration_wait_operation_commit_markers
 (marker_key, operation_key, operation_digest, target_key, mutation_kind, request_digest, pre_state_digest, state, created_at, resolved_at, schema_version)
 VALUES (?, 'forged-marker', ?, '', 'plan', ?, '', 'pending', ?, '', 1)`, markerKey, strings.Repeat("a", 64), strings.Repeat("b", 64), formatUTC(clock)); err != nil {
		t.Fatal(err)
	}
}
