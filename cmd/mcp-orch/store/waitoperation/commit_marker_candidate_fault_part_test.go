package waitoperation

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

func TestCommitMarkerCandidateFaultAfterCommitReopenQueryResolveAndReplay(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	clock := fixture.now
	request := markerPlanRequest(fixture, "marker-commit-fault")
	store := newMarkedCandidateStore(t, fixture.db, clock)
	operation := mustPlanCandidate(t, store, request)
	predecessor := operation.Targets[0]
	transition := candidateFaultTransition(request, predecessor)
	descriptor := mustCandidateDescriptor(t, request, predecessor, transition)
	committed := mustInjectedCommit(t, store, transition, predecessor)
	assertCommitMarkerCounts(t, fixture.db, 1, 1)
	reopenedDB, reopened := reopenMarkedCandidateStore(t, fixture, clock)
	assertReopenedQueryOnly(t, reopened, request, committed)
	mustResolveMarker(t, reopened, descriptor.MarkerKey())
	assertOwnerUnfenced(t, reopened)
	assertCandidateReplay(t, reopenedDB, reopened, request, transition, committed)
}

func newMarkedCandidateStore(t *testing.T, db *sql.DB, clock time.Time) *Store {
	t.Helper()
	markers := mustCommitMarkerStore(t, db, clock)
	store, err := NewStoreWithCommitMarkers(db, func() time.Time { return clock }, markers)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func mustPlanCandidate(t *testing.T, store *Store, request PlanRequest) Operation {
	t.Helper()
	operation, err := store.Plan(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func candidateFaultTransition(request PlanRequest, predecessor TargetState) TargetTransitionRequest {
	return reopenTransition(request, predecessor)
}

func mustCandidateDescriptor(t *testing.T, request PlanRequest, predecessor TargetState, transition TargetTransitionRequest) CommitMarkerDescriptor {
	t.Helper()
	descriptor, err := NewMutationDescriptor(MutationTargetAppend, request.OperationKey, request.OperationDigest, predecessor.Key, transition, targetStateDigest(predecessor))
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func mustInjectedCommit(t *testing.T, store *Store, transition TargetTransitionRequest, predecessor TargetState) TargetState {
	t.Helper()
	committed, err := store.AppendTarget(withCommitAfterCallbackFault(t.Context()), transition)
	if !errors.Is(err, ErrCommitUnknown) || !errors.Is(err, ErrFenced) {
		t.Fatalf("post-commit fault = %#v err=%v, want commit-unknown fence", committed, err)
	}
	if committed.Version != predecessor.Version+1 {
		t.Fatalf("post-commit result = %#v, want committed successor", committed)
	}
	return committed
}

func reopenMarkedCandidateStore(t *testing.T, fixture waitOperationFixture, clock time.Time) (*sql.DB, *Store) {
	t.Helper()
	path := sqliteDatabasePath(t, fixture.db)
	if err := fixture.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sqliteruntime.Open(context.Background(), sqliteruntime.OpenOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteruntime.RunEmbeddedMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db, newMarkedCandidateStore(t, db, clock)
}

func assertReopenedQueryOnly(t *testing.T, store *Store, request PlanRequest, committed TargetState) {
	t.Helper()
	if !store.fenced.Load() {
		t.Fatal("reopened owner did not retain pending-marker fence")
	}
	queryOnly, err := store.Get(context.Background(), OperationLookup{Key: request.OperationKey, Digest: request.OperationDigest})
	if err != nil || len(queryOnly.Targets) != 1 || queryOnly.Targets[0].Version != committed.Version {
		t.Fatalf("reopened query-only Get = %#v err=%v, want committed successor", queryOnly, err)
	}
}

func assertCandidateReplay(t *testing.T, db *sql.DB, store *Store, request PlanRequest, transition TargetTransitionRequest, committed TargetState) {
	t.Helper()
	replayed := mustAppendMarker(t, store, transition)
	if replayed.Version != committed.Version || replayed.RecordDigest != committed.RecordDigest {
		t.Fatalf("resolved replay = %#v, want original successor", replayed)
	}
	var journalCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM orchestration_wait_operation_target_journal WHERE operation_key = ?`, request.OperationKey).Scan(&journalCount); err != nil {
		t.Fatal(err)
	}
	if journalCount != 2 {
		t.Fatalf("resolved replay journal rows = %d, want callback not rerun", journalCount)
	}
}
