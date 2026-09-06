package waitoperation

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func TestCommitMarkerPlanAndStateLifecycle(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	clock := fixture.now
	store := mustMarkedStore(t, fixture.db, clock)
	request := markerPlanRequest(fixture, "marker-plan-state")
	derived := mustDerivedTargets(t, request)
	withReferences := markerRequestWithReferences(request, derived)
	assertCanonicalPlanDescriptors(t, request, withReferences, derived)
	first := mustPlanMarker(t, store, request)
	replay := mustPlanMarker(t, store, request)
	assertPlanReplay(t, first, replay)
	partial := mustAdvanceMarkerState(t, store, first)
	assertPartialState(t, partial)
	assertStateReplayConflict(t, store, first)
	assertCommitMarkerCounts(t, fixture.db, 0, 2)
}

func mustMarkedStore(t *testing.T, db *sql.DB, clock time.Time) *Store {
	t.Helper()
	markers, err := NewCommitMarkerStore(db, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithCommitMarkers(db, func() time.Time { return clock }, markers)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func mustDerivedTargets(t *testing.T, request PlanRequest) []storedb.CreateOrchestrationWaitTargetPlanParams {
	t.Helper()
	derived, _, err := deriveTargets(request)
	if err != nil {
		t.Fatal(err)
	}
	return derived
}

func markerRequestWithReferences(request PlanRequest, derived []storedb.CreateOrchestrationWaitTargetPlanParams) PlanRequest {
	request.Targets = append([]TargetPlan(nil), request.Targets...)
	request.Targets[0].OriginalGrantID = derived[0].OriginalGrantID
	request.Targets[0].GrantNamespace = derived[0].GrantNamespace
	request.Targets[0].GrantKeyDigest = derived[0].GrantKeyDigest
	request.Targets[0].GrantRequestDigest = derived[0].GrantRequestDigest
	request.Targets[0].WaitNamespace = derived[0].WaitNamespace
	request.Targets[0].WaitKeyDigest = derived[0].WaitKeyDigest
	request.Targets[0].WaitRequestDigest = derived[0].WaitRequestDigest
	return request
}

func assertCanonicalPlanDescriptors(t *testing.T, request, withReferences PlanRequest, derived []storedb.CreateOrchestrationWaitTargetPlanParams) {
	t.Helper()
	first, err := NewMutationDescriptor(MutationPlan, request.OperationKey, request.OperationDigest, "", canonicalPlanMarkerRequest(request, derived), "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewMutationDescriptor(MutationPlan, withReferences.OperationKey, withReferences.OperationDigest, "", canonicalPlanMarkerRequest(withReferences, derived), "")
	if err != nil {
		t.Fatal(err)
	}
	if first.MarkerKey() != second.MarkerKey() || first.RequestDigest != second.RequestDigest {
		t.Fatal("canonical plan references produced different marker identity")
	}
}

func mustPlanMarker(t *testing.T, store *Store, request PlanRequest) Operation {
	t.Helper()
	operation, err := store.Plan(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func assertPlanReplay(t *testing.T, first, replay Operation) {
	t.Helper()
	if replay.OperationKey != first.OperationKey || replay.Targets[0].RecordDigest != first.Targets[0].RecordDigest {
		t.Fatalf("marked Plan replay = %#v, want original operation", replay)
	}
}

func mustAdvanceMarkerState(t *testing.T, store *Store, operation Operation) Operation {
	t.Helper()
	partial, err := store.AdvanceState(t.Context(), stateRequest(operation, "partial", "", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	return partial
}

func assertPartialState(t *testing.T, operation Operation) {
	t.Helper()
	if operation.Status != "partial" {
		t.Fatalf("marked AdvanceState() = %#v, want partial", operation)
	}
}

func assertStateReplayConflict(t *testing.T, store *Store, operation Operation) {
	t.Helper()
	if _, err := store.AdvanceState(t.Context(), stateRequest(operation, "partial", "", "", "")); !errors.Is(err, ErrOperationStateConflict) {
		t.Fatalf("strict marked state replay = %v, want ErrOperationStateConflict", err)
	}
}

func TestCommitMarkerProvisionAndReplayAvoidsOwnerEffects(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	clock := fixture.now
	identities := mustIdentityStore(t, fixture.db)
	access := mustAccessStore(t, fixture.db, identities, clock)
	store := mustMarkedAccessStore(t, fixture.db, access, clock)
	request := markerPlanRequest(fixture, "marker-provision")
	operation := mustPlanMarker(t, store, request)
	provision := ProvisionTargetRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: operation.Targets[0].Key, ExpectedVersion: 1, ExpectedPhase: "planned", ExpectedRecordDigest: operation.Targets[0].RecordDigest}
	first := mustProvisionMarker(t, store, provision)
	assertProvisionedMarker(t, first)
	second := mustProvisionMarker(t, store, provision)
	assertProvisionReplay(t, first, second)
	if got := countPendingMarkers(t, fixture.db); got != 0 {
		t.Fatalf("pending markers after provision replay = %d, want 0", got)
	}
}

func mustIdentityStore(t *testing.T, db *sql.DB) *identity.Store {
	t.Helper()
	store, err := identity.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func mustAccessStore(t *testing.T, db *sql.DB, identities *identity.Store, clock time.Time) *accesswait.Store {
	t.Helper()
	store, err := accesswait.NewStore(db, identities, candidateEvents{}, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func mustMarkedAccessStore(t *testing.T, db *sql.DB, access *accesswait.Store, clock time.Time) *Store {
	t.Helper()
	markers := mustCommitMarkerStore(t, db, clock)
	store, err := NewStoreWithAccessAndCommitMarkers(db, func() time.Time { return clock }, access, markers)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func mustCommitMarkerStore(t *testing.T, db *sql.DB, clock time.Time) *CommitMarkerStore {
	t.Helper()
	markers, err := NewCommitMarkerStore(db, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	return markers
}

func mustProvisionMarker(t *testing.T, store *Store, request ProvisionTargetRequest) TargetState {
	t.Helper()
	result, err := store.ProvisionTarget(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertProvisionedMarker(t *testing.T, target TargetState) {
	t.Helper()
	if target.Phase != "waiting" {
		t.Fatalf("marked ProvisionTarget() = %#v, want waiting", target)
	}
}

func assertProvisionReplay(t *testing.T, first, second TargetState) {
	t.Helper()
	if second.RecordDigest != first.RecordDigest || second.Version != first.Version {
		t.Fatalf("marked ProvisionTarget replay = %#v, want original target", second)
	}
}

func TestCommitMarkerCleanupAndReplayPreserveResourceProof(t *testing.T) {
	store, request, operation := statePlanWithProvisioned(t, 1, 1)
	terminal := makeTargetPhase(t, store, operation, request, operation.Targets[0], "cancelled")
	reason := "marker-cleanup"
	digest := mustCleanupDigest(t, request, reason)
	cleanupOperation := mustCleanupPending(t, store, operation, digest, reason)
	clock := operation.IssuedAt.Add(5 * time.Second)
	marked := mustMarkedAccessStore(t, store.db, store.access, clock)
	cleanupRequest := markerCleanupRequest(request, terminal, digest, reason)
	cleared := mustCleanupMarker(t, marked, cleanupRequest)
	assertCleanupMarker(t, cleared)
	replay := mustCleanupMarker(t, marked, cleanupRequest)
	assertCleanupReplay(t, cleared, replay)
	assertCleanupGrantTerminal(t, cleared)
	if got := countPendingMarkers(t, store.db); got != 0 {
		t.Fatalf("pending markers after cleanup replay = %d", got)
	}
	_ = cleanupOperation
}

func mustCleanupDigest(t *testing.T, request PlanRequest, reason string) string {
	t.Helper()
	digest, err := CanonicalCleanupRequestDigest(request.OperationKey, request.OperationDigest, reason)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func mustCleanupPending(t *testing.T, store *Store, operation Operation, digest, reason string) Operation {
	t.Helper()
	result, err := store.AdvanceState(t.Context(), stateRequest(operation, "cleanup_pending", "cleanup", digest, reason))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "cleanup_pending" {
		t.Fatalf("cleanup operation state = %#v, want cleanup_pending", result)
	}
	return result
}

func markerCleanupRequest(request PlanRequest, target TargetState, digest, reason string) CleanupTargetRequest {
	return CleanupTargetRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: target.Key, ExpectedVersion: target.Version, ExpectedPhase: target.Phase, ExpectedRecordDigest: target.RecordDigest, CleanupRequestDigest: digest, CleanupReasonRef: reason}
}

func mustCleanupMarker(t *testing.T, store *Store, request CleanupTargetRequest) TargetState {
	t.Helper()
	result, err := store.CleanupTarget(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertCleanupMarker(t *testing.T, target TargetState) {
	t.Helper()
	if target.Phase != "cleanup_cleared" {
		t.Fatalf("marked CleanupTarget() = %#v, want cleanup_cleared", target)
	}
}

func assertCleanupReplay(t *testing.T, first, replay TargetState) {
	t.Helper()
	if replay.Phase != "cleanup_cleared" || replay.Version != first.Version {
		t.Fatalf("marked CleanupTarget replay = %#v, want original target", replay)
	}
}

func assertCleanupGrantTerminal(t *testing.T, target TargetState) {
	t.Helper()
	if target.GrantStatus != "revoked" && target.GrantStatus != "expired" {
		t.Fatalf("cleanup grant status = %q, want terminal", target.GrantStatus)
	}
}
