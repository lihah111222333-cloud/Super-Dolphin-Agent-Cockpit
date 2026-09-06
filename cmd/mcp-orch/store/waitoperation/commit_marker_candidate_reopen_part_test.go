package waitoperation

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

func TestCommitMarkerPendingReopenResolveAndReplay(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	clock := fixture.now
	base := mustBaseStore(t, fixture.db, clock)
	request := markerPlanRequest(fixture, "marker-reopen")
	operation := mustPlanMarker(t, base, request)
	target := operation.Targets[0]
	transition := reopenTransition(request, target)
	descriptor := mustReopenDescriptor(t, request, target, transition)
	insertPendingMarkerWithoutPostDigest(t, fixture.db, descriptor, clock)
	setMarkerPostDigest(t, fixture.db, descriptor, targetStateDigest(target))
	reopened := mustReopenStore(t, fixture, clock)
	assertPendingFence(t, reopened, request, transition)
	assertExactGet(t, reopened, request)
	mustResolveMarker(t, reopened, descriptor.MarkerKey())
	assertOwnerUnfenced(t, reopened)
	assertReopenReplay(t, reopened, transition, target)
}

func mustBaseStore(t *testing.T, db *sql.DB, clock time.Time) *Store {
	t.Helper()
	store, err := NewStore(db, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func reopenTransition(request PlanRequest, target TargetState) TargetTransitionRequest {
	return TargetTransitionRequest{OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: target.Key, ExpectedVersion: target.Version, ExpectedPhase: target.Phase, ExpectedRecordDigest: target.RecordDigest, NextPhase: "grant_pending", GrantStatus: "planned", WaitStatus: "absent", GrantCleanupState: "none", WaitCleanupState: "none"}
}

func mustReopenDescriptor(t *testing.T, request PlanRequest, target TargetState, transition TargetTransitionRequest) CommitMarkerDescriptor {
	t.Helper()
	descriptor, err := NewMutationDescriptor(MutationTargetAppend, request.OperationKey, request.OperationDigest, target.Key, transition, targetStateDigest(target))
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func mustReopenStore(t *testing.T, fixture waitOperationFixture, clock time.Time) *Store {
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
	return mustMarkedStore(t, db, clock)
}

func assertPendingFence(t *testing.T, store *Store, request PlanRequest, transition TargetTransitionRequest) {
	t.Helper()
	if _, err := store.AppendTarget(t.Context(), transition); !errors.Is(err, ErrCommitMarkerPending) {
		t.Fatalf("fresh write with pending marker = %v, want ErrCommitMarkerPending", err)
	}
	if !store.fenced.Load() {
		t.Fatal("reopened owner did not retain pending-marker fence")
	}
}

func assertExactGet(t *testing.T, store *Store, request PlanRequest) {
	t.Helper()
	if _, err := store.Get(t.Context(), OperationLookup{Key: request.OperationKey, Digest: request.OperationDigest}); err != nil {
		t.Fatalf("exact Get while marker pending = %v", err)
	}
}

func mustResolveMarker(t *testing.T, store *Store, markerKey string) {
	t.Helper()
	if _, err := store.ResolveCommitMarker(t.Context(), markerKey); err != nil {
		t.Fatal(err)
	}
}

func assertOwnerUnfenced(t *testing.T, store *Store) {
	t.Helper()
	if store.fenced.Load() {
		t.Fatal("owner remained fenced after proven marker resolution")
	}
}

func assertReopenReplay(t *testing.T, store *Store, transition TargetTransitionRequest, target TargetState) {
	t.Helper()
	replayed, err := store.AppendTarget(t.Context(), transition)
	if err != nil || replayed.Version != target.Version || replayed.RecordDigest != target.RecordDigest {
		t.Fatalf("resolved marker replay = %#v err=%v", replayed, err)
	}
}

func TestCommitMarkerResolvedReplayWithoutDerivedRecordDigest(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	clock := fixture.now
	base := mustBaseStore(t, fixture.db, clock)
	request := markerPlanRequest(fixture, "marker-derived-replay")
	operation := mustPlanMarker(t, base, request)
	predecessor := operation.Targets[0]
	transition := reopenTransition(request, predecessor)
	advanced := mustAppendMarker(t, base, transition)
	descriptor := mustReopenDescriptor(t, request, predecessor, transition)
	insertPendingMarkerWithoutPostDigest(t, fixture.db, descriptor, clock)
	setMarkerPostDigest(t, fixture.db, descriptor, targetStateDigest(advanced))
	store := mustMarkedStore(t, fixture.db, clock)
	mustResolveMarker(t, store, descriptor.MarkerKey())
	replayed := mustAppendMarker(t, store, transition)
	assertDerivedReplay(t, fixture.db, replayed, advanced, request)
}

func mustAppendMarker(t *testing.T, store *Store, transition TargetTransitionRequest) TargetState {
	t.Helper()
	result, err := store.AppendTarget(t.Context(), transition)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func insertPendingMarkerWithoutPostDigest(t *testing.T, db *sql.DB, descriptor CommitMarkerDescriptor, clock time.Time) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO orchestration_wait_operation_commit_markers
 (marker_key, operation_key, operation_digest, target_key, mutation_kind, request_digest, pre_state_digest, state, created_at, resolved_at, schema_version)
 VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, '', 1)`, descriptor.MarkerKey(), descriptor.OperationKey, descriptor.OperationDigest, descriptor.TargetKey, descriptor.MutationKind, descriptor.RequestDigest, descriptor.PreStateDigest, formatUTC(clock)); err != nil {
		t.Fatal(err)
	}
}

func setMarkerPostDigest(t *testing.T, db *sql.DB, descriptor CommitMarkerDescriptor, digest string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `UPDATE orchestration_wait_operation_commit_markers SET post_state_digest = ? WHERE marker_key = ?`, digest, descriptor.MarkerKey()); err != nil {
		t.Fatal(err)
	}
}

func assertDerivedReplay(t *testing.T, db *sql.DB, replayed, advanced TargetState, request PlanRequest) {
	t.Helper()
	if replayed.Version != advanced.Version || replayed.RecordDigest != advanced.RecordDigest {
		t.Fatalf("derived-digest replay = %#v, want original successor", replayed)
	}
	var journalCount int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM orchestration_wait_operation_target_journal WHERE operation_key = ?`, request.OperationKey).Scan(&journalCount); err != nil {
		t.Fatal(err)
	}
	if journalCount != 2 {
		t.Fatalf("derived-digest replay journal rows = %d, want 2", journalCount)
	}
}

func TestCommitMarkerCallbackRollbackLeavesNoMarker(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	clock := fixture.now
	base := mustBaseStore(t, fixture.db, clock)
	request := markerPlanRequest(fixture, "marker-rollback")
	operation := mustPlanMarker(t, base, request)
	descriptor := mustStateAdvanceDescriptor(t, operation)
	marked := mustMarkedStore(t, fixture.db, clock)
	want := errors.New("callback failure")
	assertCallbackRollback(t, marked, descriptor, want)
	assertCommitMarkerCounts(t, fixture.db, 0, 0)
}

func mustStateAdvanceDescriptor(t *testing.T, operation Operation) CommitMarkerDescriptor {
	t.Helper()
	descriptor, err := NewMutationDescriptor(MutationStateAdvance, operation.OperationKey, operation.OperationDigest, "", stateRequest(operation, "partial", "", "", ""), operationStateDigest(operation))
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func assertCallbackRollback(t *testing.T, store *Store, descriptor CommitMarkerDescriptor, want error) {
	t.Helper()
	if err := store.withImmediateMutation(t.Context(), &descriptor, func(*storedb.Queries, storedb.DBTX) error { return want }); !errors.Is(err, want) {
		t.Fatalf("callback rollback error = %v, want %v", err, want)
	}
}
