package heterociowner

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

func TestPostgreSQLOwnerLifecycleSurface(t *testing.T) {
	t.Helper()
	var _ interface {
		Start(context.Context, heteroci.OwnerFence, heteroci.OperationID, heteroci.TopologyRunID, string, heteroci.SnapshotSet) (heteroci.StartedRoutingLease, error)
		Finish(context.Context, heteroci.OwnerFence, heteroci.OperationID, heteroci.TopologyRunID, string, heteroci.RoutingLeaseState) (DurableLeaseTransition, error)
		CancelPlanned(context.Context, heteroci.OwnerFence, heteroci.OperationID, heteroci.TopologyRunID, string) (DurableLeaseTransition, error)
		LocateRun(context.Context, heteroci.TopologyRunID) ([]DurableRunState, error)
		ReadRun(context.Context, heteroci.TopologyRunID) (DurableRunState, error)
	} = (*PostgreSQLOwner)(nil)
	var _ interface {
		Acquire(context.Context) (heteroci.OwnerFence, error)
		LocateRun(context.Context, heteroci.TopologyRunID) ([]DurableRunState, error)
		PlanAndReserve(context.Context, heteroci.OwnerFence, heteroci.TopologyPlanRequest) (DurableReservedRoutingPlan, error)
		BindExecutors(context.Context, heteroci.ExecutorBindingMutation) (heteroci.ExecutorBindingMutationResult, error)
		Start(context.Context, heteroci.OwnerFence, heteroci.OperationID, heteroci.TopologyRunID, string, heteroci.SnapshotSet) (heteroci.StartedRoutingLease, error)
		Finish(context.Context, heteroci.OwnerFence, heteroci.OperationID, heteroci.TopologyRunID, string, heteroci.RoutingLeaseState) (DurableLeaseTransition, error)
		CancelPlanned(context.Context, heteroci.OwnerFence, heteroci.OperationID, heteroci.TopologyRunID, string) (DurableLeaseTransition, error)
		ReadRun(context.Context, heteroci.TopologyRunID) (DurableRunState, error)
	} = (*PostgreSQLDurableOwner)(nil)
}

func TestPostgreSQLOwnerLifecycleExactReplayAndRelease(t *testing.T) {
	owner, state, request, planned := newPostgreSQLLifecycleFixture(t)
	started := assertPostgreSQLStartReplay(t, owner, state, request, planned)
	finished := assertPostgreSQLFinishReplay(t, owner, state, request, started)
	runState, err := owner.ReadRun(context.Background(), request.RunIdentity.DurableRunID)
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if !runState.Released || len(runState.Leases) != 1 || runState.Leases[0].State != heteroci.LeaseStateSucceeded || finished.Lease.State != heteroci.LeaseStateSucceeded {
		t.Fatalf("ReadRun release projection = %#v", runState)
	}
}

func TestPostgreSQLOwnerStartsTwoUnitsWithOneReservationUpdate(t *testing.T) {
	owner, request, planned := newPostgreSQLTwoUnitFixture(t)
	startPostgreSQLTwoUnits(t, owner, request, planned)
	run, err := owner.ReadRun(context.Background(), request.RunIdentity.DurableRunID)
	if err != nil || len(run.Leases) != 2 || run.Released {
		t.Fatalf("two-unit ReadRun = %#v, %v", run, err)
	}
	for _, lease := range run.Leases {
		if lease.State != heteroci.LeaseStateStarted {
			t.Fatalf("two-unit lease state = %#v", run.Leases)
		}
	}
}

func newPostgreSQLTwoUnitFixture(t *testing.T) (*PostgreSQLOwner, heteroci.TopologyPlanRequest, heteroci.TopologyReservedPlan) {
	t.Helper()
	database, _ := openPostgreSQLOwnerTestDB(t)
	now := time.UnixMilli(durableRoutingNowUnixMilli)
	owner, err := NewPostgreSQLOwnerWithClock(context.Background(), database, validOwnerScope(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewPostgreSQLOwnerWithClock: %v", err)
	}
	fence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	session, err := heteroci.NewTopologySession(fence)
	if err != nil {
		t.Fatalf("NewTopologySession: %v", err)
	}
	request := postgresqlPlanRequestFixture(t, fence, now)
	request.Units = append(request.Units, heteroci.ExecutionUnit{UnitID: "unit-b", TargetPlatform: heteroci.PlatformLinuxAMD64, RequiredRuntime: heteroci.RuntimeUbuntuContainer, EstimatedDurationMS: 1_000, CPUMilli: 500, MemoryBytes: 512 * 1024 * 1024})
	planned, err := owner.PlanAndReserve(context.Background(), session, request)
	if err != nil || len(planned.Leases) != 2 {
		t.Fatalf("PlanAndReserve two-unit result = %#v, %v", planned, err)
	}
	return owner, request, planned
}

func startPostgreSQLTwoUnits(t *testing.T, owner *PostgreSQLOwner, request heteroci.TopologyPlanRequest, planned heteroci.TopologyReservedPlan) {
	t.Helper()
	firstID, err := heteroci.DeriveTopologyOperationID(request.RunIdentity.DurableRunID, heteroci.TopologyOperationPhaseStart, "unit-a")
	if err != nil {
		t.Fatalf("derive first start: %v", err)
	}
	first, err := owner.Start(context.Background(), planned.Fence, firstID, request.RunIdentity.DurableRunID, "unit-a", request.SourceSnapshot)
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	secondID, err := heteroci.DeriveTopologyOperationID(request.RunIdentity.DurableRunID, heteroci.TopologyOperationPhaseStart, "unit-b")
	if err != nil {
		t.Fatalf("derive second start: %v", err)
	}
	if _, err := owner.Start(context.Background(), first.Fence, secondID, request.RunIdentity.DurableRunID, "unit-b", request.SourceSnapshot); err != nil {
		t.Fatalf("second Start: %v", err)
	}
}

func TestPostgreSQLOwnerStartReusesPersistedSnapshotAfterTTL(t *testing.T) {
	database, _ := openPostgreSQLOwnerTestDB(t)
	now := time.UnixMilli(durableRoutingNowUnixMilli)
	owner, err := NewPostgreSQLOwnerWithClock(context.Background(), database, validOwnerScope(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewPostgreSQLOwnerWithClock: %v", err)
	}
	fence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	session, err := heteroci.NewTopologySession(fence)
	if err != nil {
		t.Fatalf("NewTopologySession: %v", err)
	}
	request := postgresqlPlanRequestFixture(t, fence, now)
	planned, err := owner.PlanAndReserve(context.Background(), session, request)
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	now = now.Add(request.HeartbeatTTL * 2)
	startID, err := heteroci.DeriveTopologyOperationID(request.RunIdentity.DurableRunID, heteroci.TopologyOperationPhaseStart, "unit-a")
	if err != nil {
		t.Fatalf("derive start: %v", err)
	}
	if _, err := owner.Start(context.Background(), planned.Fence, startID, request.RunIdentity.DurableRunID, "unit-a", request.SourceSnapshot); err != nil {
		t.Fatalf("Start after TTL: %v", err)
	}
	drifted := request.SourceSnapshot
	drifted.Nodes[0].Available.CPUMilli++
	if _, err := owner.Start(context.Background(), planned.Fence, startID, request.RunIdentity.DurableRunID, "unit-a", drifted); err == nil {
		t.Fatal("Start accepted snapshot content drift on replay")
	}
}

func newPostgreSQLLifecycleFixture(t *testing.T) (*PostgreSQLOwner, *postgresqlOwnerTestState, heteroci.TopologyPlanRequest, heteroci.TopologyReservedPlan) {
	t.Helper()
	database, state := openPostgreSQLOwnerTestDB(t)
	now := time.UnixMilli(durableRoutingNowUnixMilli)
	owner, err := NewPostgreSQLOwnerWithClock(context.Background(), database, validOwnerScope(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewPostgreSQLOwnerWithClock: %v", err)
	}
	fence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	session, err := heteroci.NewTopologySession(fence)
	if err != nil {
		t.Fatalf("NewTopologySession: %v", err)
	}
	request := postgresqlPlanRequestFixture(t, fence, now)
	planned, err := owner.PlanAndReserve(context.Background(), session, request)
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	return owner, state, request, planned
}

func assertPostgreSQLStartReplay(t *testing.T, owner *PostgreSQLOwner, state *postgresqlOwnerTestState, request heteroci.TopologyPlanRequest, planned heteroci.TopologyReservedPlan) heteroci.StartedRoutingLease {
	t.Helper()
	state.mu.Lock()
	state.commitAppliedError = errors.New("network dropped after Start commit")
	state.mu.Unlock()
	startID, err := heteroci.DeriveTopologyOperationID(request.RunIdentity.DurableRunID, heteroci.TopologyOperationPhaseStart, "unit-a")
	if err != nil {
		t.Fatalf("derive start operation: %v", err)
	}
	started, err := owner.Start(context.Background(), planned.Fence, startID, request.RunIdentity.DurableRunID, "unit-a", request.SourceSnapshot)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	replayed, err := owner.Start(context.Background(), planned.Fence, startID, request.RunIdentity.DurableRunID, "unit-a", request.SourceSnapshot)
	if err != nil {
		t.Fatalf("Start replay: %v", err)
	}
	if !reflect.DeepEqual(started, replayed) {
		t.Fatalf("Start replay drifted: got %#v want %#v", replayed, started)
	}
	return started
}

func assertPostgreSQLFinishReplay(t *testing.T, owner *PostgreSQLOwner, state *postgresqlOwnerTestState, request heteroci.TopologyPlanRequest, started heteroci.StartedRoutingLease) DurableLeaseTransition {
	t.Helper()
	state.mu.Lock()
	state.commitAppliedError = errors.New("network dropped after Finish commit")
	state.mu.Unlock()
	finishID, err := heteroci.DeriveTopologyOperationID(request.RunIdentity.DurableRunID, heteroci.TopologyOperationPhaseFinish, "unit-a")
	if err != nil {
		t.Fatalf("derive finish operation: %v", err)
	}
	finished, err := owner.Finish(context.Background(), started.Fence, finishID, request.RunIdentity.DurableRunID, "unit-a", heteroci.LeaseStateSucceeded)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	replayed, err := owner.Finish(context.Background(), started.Fence, finishID, request.RunIdentity.DurableRunID, "unit-a", heteroci.LeaseStateSucceeded)
	if err != nil {
		t.Fatalf("Finish replay: %v", err)
	}
	if !reflect.DeepEqual(finished, replayed) {
		t.Fatalf("Finish replay drifted: got %#v want %#v", replayed, finished)
	}
	return finished
}

func assertPostgreSQLCancelReplay(t *testing.T, owner *PostgreSQLOwner, request heteroci.TopologyPlanRequest, planned heteroci.TopologyReservedPlan) {
	t.Helper()
	cancelID, err := heteroci.DeriveTopologyOperationID(request.RunIdentity.DurableRunID, heteroci.TopologyOperationPhaseCancelPlanned, "unit-a")
	if err != nil {
		t.Fatalf("derive cancel operation: %v", err)
	}
	cancelled, err := owner.CancelPlanned(context.Background(), planned.Fence, cancelID, request.RunIdentity.DurableRunID, "unit-a")
	if err != nil {
		t.Fatalf("CancelPlanned: %v", err)
	}
	replayed, err := owner.CancelPlanned(context.Background(), planned.Fence, cancelID, request.RunIdentity.DurableRunID, "unit-a")
	if err != nil {
		t.Fatalf("CancelPlanned replay: %v", err)
	}
	if !reflect.DeepEqual(cancelled, replayed) || cancelled.Lease.State != heteroci.LeaseStateCancelled {
		t.Fatalf("CancelPlanned replay = %#v want %#v", replayed, cancelled)
	}
}

func TestPostgreSQLOwnerCancelPlannedReplayAndRelease(t *testing.T) {
	owner, _, request, planned := newPostgreSQLLifecycleFixture(t)
	assertPostgreSQLCancelReplay(t, owner, request, planned)
	runState, err := owner.ReadRun(context.Background(), request.RunIdentity.DurableRunID)
	if err != nil {
		t.Fatalf("ReadRun after CancelPlanned: %v", err)
	}
	if !runState.Released || runState.Leases[0].State != heteroci.LeaseStateCancelled {
		t.Fatalf("cancelled run state = %#v", runState)
	}
}

func TestPostgreSQLOwnerLifecycleReplayConflictMatrix(t *testing.T) {
	owner, state, request, planned := newPostgreSQLLifecycleFixture(t)
	started := assertPostgreSQLStartReplay(t, owner, state, request, planned)
	assertPostgreSQLStartConflicts(t, owner, request, planned, started)
	finished := assertPostgreSQLFinishReplay(t, owner, state, request, started)
	assertPostgreSQLFinishConflict(t, owner, request, started, finished)
	cancelOwner, _, cancelRequest, cancelPlan := newPostgreSQLLifecycleFixture(t)
	assertPostgreSQLCancelConflict(t, cancelOwner, cancelRequest, cancelPlan)
}

func assertPostgreSQLStartConflicts(t *testing.T, owner *PostgreSQLOwner, request heteroci.TopologyPlanRequest, planned heteroci.TopologyReservedPlan, started heteroci.StartedRoutingLease) {
	t.Helper()
	startID, err := heteroci.DeriveTopologyOperationID(request.RunIdentity.DurableRunID, heteroci.TopologyOperationPhaseStart, "unit-a")
	if err != nil {
		t.Fatalf("derive start operation: %v", err)
	}
	differentSnapshot := request.SourceSnapshot
	differentSnapshot.CapturedAtUnixMilli++
	_, conflictErr := owner.Start(context.Background(), planned.Fence, startID, request.RunIdentity.DurableRunID, "unit-a", differentSnapshot)
	mustPostgreSQLOwnerConflict(t, conflictErr)
	_, conflictErr = owner.Start(context.Background(), planned.Fence, startID, request.RunIdentity.DurableRunID, "unit-other", request.SourceSnapshot)
	mustPostgreSQLOwnerConflict(t, conflictErr)
	newOperation, err := heteroci.DeriveTopologyOperationID(request.RunIdentity.DurableRunID, heteroci.TopologyOperationPhaseStart, "unit-other")
	if err != nil {
		t.Fatalf("derive stale-fence operation: %v", err)
	}
	if _, err := owner.Start(context.Background(), planned.Fence, newOperation, request.RunIdentity.DurableRunID, "unit-other", request.SourceSnapshot); err == nil {
		t.Fatal("Start accepted stale fence for a new operation")
	}
	current, err := owner.CurrentFence(context.Background())
	if err != nil || current != started.Fence {
		t.Fatalf("stale/conflict Start advanced fence: current=%#v err=%v want=%#v", current, err, started.Fence)
	}
}

func assertPostgreSQLFinishConflict(t *testing.T, owner *PostgreSQLOwner, request heteroci.TopologyPlanRequest, started heteroci.StartedRoutingLease, finished DurableLeaseTransition) {
	t.Helper()
	finishID, err := heteroci.DeriveTopologyOperationID(request.RunIdentity.DurableRunID, heteroci.TopologyOperationPhaseFinish, "unit-a")
	if err != nil {
		t.Fatalf("derive finish operation: %v", err)
	}
	_, conflictErr := owner.Finish(context.Background(), started.Fence, finishID, request.RunIdentity.DurableRunID, "unit-a", heteroci.LeaseStateFailed)
	mustPostgreSQLOwnerConflict(t, conflictErr)
	current, err := owner.CurrentFence(context.Background())
	if err != nil || current != finished.Fence {
		t.Fatalf("conflict Finish advanced fence: current=%#v err=%v want=%#v", current, err, finished.Fence)
	}
}

func assertPostgreSQLCancelConflict(t *testing.T, owner *PostgreSQLOwner, request heteroci.TopologyPlanRequest, planned heteroci.TopologyReservedPlan) {
	t.Helper()
	cancelID, err := heteroci.DeriveTopologyOperationID(request.RunIdentity.DurableRunID, heteroci.TopologyOperationPhaseCancelPlanned, "unit-a")
	if err != nil {
		t.Fatalf("derive cancel operation: %v", err)
	}
	cancelled, err := owner.CancelPlanned(context.Background(), planned.Fence, cancelID, request.RunIdentity.DurableRunID, "unit-a")
	if err != nil {
		t.Fatalf("CancelPlanned: %v", err)
	}
	differentRun := heteroci.TopologyRunID(strings.Repeat("f", len(string(request.RunIdentity.DurableRunID))))
	_, conflictErr := owner.CancelPlanned(context.Background(), planned.Fence, cancelID, differentRun, "unit-a")
	mustPostgreSQLOwnerConflict(t, conflictErr)
	current, err := owner.CurrentFence(context.Background())
	if err != nil || current != cancelled.Fence {
		t.Fatalf("conflict CancelPlanned advanced fence: current=%#v err=%v want=%#v", current, err, cancelled.Fence)
	}
}

func mustPostgreSQLOwnerConflict(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("operation conflict error = %v, want ErrOperationConflict", err)
	}
}

func TestPostgreSQLOwnerRejectsCorruptLifecycleJournal(t *testing.T) {
	owner, state, request, planned := newPostgreSQLLifecycleFixture(t)
	assertPostgreSQLStartReplay(t, owner, state, request, planned)
	startID, err := heteroci.DeriveTopologyOperationID(request.RunIdentity.DurableRunID, heteroci.TopologyOperationPhaseStart, "unit-a")
	if err != nil {
		t.Fatalf("derive start operation: %v", err)
	}
	state.mu.Lock()
	state.data.operations[string(startID)][9] = "sha256:" + strings.Repeat("0", 64)
	state.mu.Unlock()
	if _, err := owner.Start(context.Background(), planned.Fence, startID, request.RunIdentity.DurableRunID, "unit-a", request.SourceSnapshot); err == nil || !strings.Contains(err.Error(), "journal chain") {
		t.Fatalf("corrupt lifecycle journal error = %v", err)
	}
}

func TestPostgreSQLOwnerRejectsCorruptPersistedPlanOperation(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func([]driver.Value, heteroci.TopologyReservedPlan)
		startAlso bool
	}{
		{name: "journal_digest", mutate: func(operation []driver.Value, _ heteroci.TopologyReservedPlan) {
			operation[9] = "sha256:" + strings.Repeat("0", 64)
		}, startAlso: true},
		{name: "previous_journal_digest", mutate: func(operation []driver.Value, _ heteroci.TopologyReservedPlan) {
			operation[8] = "sha256:" + strings.Repeat("1", 64)
		}},
		{name: "owner_epoch", mutate: func(operation []driver.Value, planned heteroci.TopologyReservedPlan) {
			operation[1] = int64(planned.Fence.OwnerEpoch) + 1
		}},
		{name: "revision", mutate: func(operation []driver.Value, _ heteroci.TopologyReservedPlan) { operation[2] = int64(1) }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) { assertCorruptPostgreSQLPlanOperation(t, testCase.mutate, testCase.startAlso) })
	}
}

func assertCorruptPostgreSQLPlanOperation(t *testing.T, mutate func([]driver.Value, heteroci.TopologyReservedPlan), startAlso bool) {
	t.Helper()
	owner, state, request, planned := newPostgreSQLLifecycleFixture(t)
	state.mu.Lock()
	mutate(state.data.operations[string(planned.OperationID)], planned)
	state.mu.Unlock()
	before, err := owner.CurrentFence(context.Background())
	if err != nil {
		t.Fatalf("CurrentFence before corruption: %v", err)
	}
	if _, err := owner.ReadRun(context.Background(), request.RunIdentity.DurableRunID); err == nil {
		t.Fatal("ReadRun accepted corrupt persisted plan operation")
	}
	if startAlso {
		startID, err := heteroci.DeriveTopologyOperationID(request.RunIdentity.DurableRunID, heteroci.TopologyOperationPhaseStart, "unit-a")
		if err != nil {
			t.Fatalf("derive start: %v", err)
		}
		if _, err := owner.Start(context.Background(), planned.Fence, startID, request.RunIdentity.DurableRunID, "unit-a", request.SourceSnapshot); err == nil {
			t.Fatal("Start accepted corrupt persisted plan operation")
		}
	}
	after, err := owner.CurrentFence(context.Background())
	if err != nil || after != before {
		t.Fatalf("corrupt plan operation advanced fence: before=%#v after=%#v err=%v", before, after, err)
	}
}

func TestPostgreSQLOwnerRejectsReservationCapacityDrift(t *testing.T) {
	owner, state, request, _ := newPostgreSQLLifecycleFixture(t)
	state.mu.Lock()
	for _, reservation := range state.data.reservations {
		reservation[7] = int64(reservation[7].(int64) + 1)
	}
	state.mu.Unlock()
	if _, err := owner.ReadRun(context.Background(), request.RunIdentity.DurableRunID); err == nil {
		t.Fatalf("ReadRun capacity drift error = %v", err)
	}
}

func TestPostgreSQLOwnerReleaseRowsFailFast(t *testing.T) {
	t.Run("missing reservation", func(t *testing.T) { assertPostgreSQLReleaseRowsFail(t, false) })
	t.Run("zero update", func(t *testing.T) { assertPostgreSQLReleaseRowsFail(t, true) })
}

func assertPostgreSQLReleaseRowsFail(t *testing.T, zeroUpdate bool) {
	t.Helper()
	owner, state, request, planned := newPostgreSQLLifecycleFixture(t)
	started := assertPostgreSQLStartReplay(t, owner, state, request, planned)
	state.mu.Lock()
	if zeroUpdate {
		state.zeroReservationReleaseUpdate = true
	} else {
		state.data.reservations = make(map[string][]driver.Value)
	}
	state.mu.Unlock()
	finishID, err := heteroci.DeriveTopologyOperationID(request.RunIdentity.DurableRunID, heteroci.TopologyOperationPhaseFinish, "unit-a")
	if err != nil {
		t.Fatalf("derive finish operation: %v", err)
	}
	if _, err := owner.Finish(context.Background(), started.Fence, finishID, request.RunIdentity.DurableRunID, "unit-a", heteroci.LeaseStateSucceeded); err == nil {
		t.Fatal("Finish accepted missing or zero reservation release rows")
	}
	current, err := owner.CurrentFence(context.Background())
	if err != nil || current != started.Fence {
		t.Fatalf("failed release advanced fence: current=%#v err=%v want=%#v", current, err, started.Fence)
	}
}

func TestPostgreSQLOwnerReadRetainsRollbackFailure(t *testing.T) {
	database, state := openPostgreSQLOwnerTestDB(t)
	owner, err := NewPostgreSQLOwner(context.Background(), database, validOwnerScope())
	if err != nil {
		t.Fatalf("NewPostgreSQLOwner: %v", err)
	}
	if _, err := owner.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	rollbackErr := errors.New("read rollback failed")
	state.mu.Lock()
	state.rollbackError = rollbackErr
	state.data.head = append([]driver.Value(nil), state.data.head...)
	state.data.head[0] = "corrupt-owner-id"
	state.mu.Unlock()
	if _, err := owner.LocateRun(context.Background(), heteroci.TopologyRunID(strings.Repeat("a", 64))); !errors.Is(err, rollbackErr) {
		t.Fatalf("LocateRun rollback error = %v, want %v", err, rollbackErr)
	}
}

func TestPostgreSQLOwnerReadAndRollbackFailuresJoin(t *testing.T) {
	database, state := openPostgreSQLOwnerTestDB(t)
	owner, err := NewPostgreSQLOwner(context.Background(), database, validOwnerScope())
	if err != nil {
		t.Fatalf("NewPostgreSQLOwner: %v", err)
	}
	rollbackErr := errors.New("read rollback failed after missing head")
	state.mu.Lock()
	state.rollbackError = rollbackErr
	state.mu.Unlock()
	if _, err := owner.LocateRun(context.Background(), heteroci.TopologyRunID(strings.Repeat("a", 64))); err == nil || !errors.Is(err, rollbackErr) {
		t.Fatalf("LocateRun read+rollback error = %v", err)
	}
}

func TestPostgreSQLOwnerAuthorityAcquireAndCurrentFence(t *testing.T) {
	database, state := openPostgreSQLOwnerTestDB(t)
	owner, err := NewPostgreSQLOwner(context.Background(), database, validOwnerScope())
	if err != nil {
		t.Fatalf("NewPostgreSQLOwner: %v", err)
	}
	fence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	current, err := owner.CurrentFence(context.Background())
	if err != nil || current != fence {
		t.Fatalf("CurrentFence = %#v, %v; want %#v", current, err, fence)
	}
	t.Run("untrusted_authority", TestPostgreSQLOwnerRejectsUntrustedAuthorityBeforeMutation)
	t.Run("standby", TestPostgreSQLOwnerRejectsStandbyBeforeDDL)
	t.Run("post_construction_drift", TestPostgreSQLOwnerRejectsPostConstructionOwnershipDrift)
	t.Run("ddl", TestPostgreSQLOwnerDDLIsIndependentCanonicalBYTEASchema)
	state.mu.Lock()
	defer state.mu.Unlock()
	assertPostgreSQLOwnerTransactionEvidence(t, state, 2)
}

func assertPostgreSQLOwnerTransactionEvidence(t *testing.T, state *postgresqlOwnerTestState, minimumTransactions int) {
	t.Helper()
	if state.authorityBeforeLock || state.serializableBegins < minimumTransactions || state.advisoryLocks != state.serializableBegins {
		t.Fatalf("transaction evidence: serializable=%d locks=%d authority_before_lock=%v", state.serializableBegins, state.advisoryLocks, state.authorityBeforeLock)
	}
}

func TestPostgreSQLOwnerRejectsStandbyBeforeDDL(t *testing.T) {
	database, state := openPostgreSQLOwnerTestDB(t)
	state.inRecovery = true
	_, err := NewPostgreSQLOwner(context.Background(), database, validOwnerScope())
	if !errors.Is(err, ErrPostgreSQLOwnerTopology) {
		t.Fatalf("NewPostgreSQLOwner error = %v, want ErrPostgreSQLOwnerTopology", err)
	}
	if state.schemaStatementCount != 0 {
		t.Fatalf("standby validation executed %d DDL statements", state.schemaStatementCount)
	}
}

func TestPostgreSQLOwnerRejectsUntrustedAuthorityBeforeMutation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*postgresqlOwnerTestState)
	}{
		{name: "loopback", mutate: func(state *postgresqlOwnerTestState) { state.serverAddress = "127.0.0.1" }},
		{name: "wrong_role", mutate: func(state *postgresqlOwnerTestState) { state.currentUser = "postgres" }},
		{name: "missing_schema", mutate: func(state *postgresqlOwnerTestState) { state.schemaOwner = "" }},
		{name: "schema_version_drift", mutate: func(state *postgresqlOwnerTestState) { state.schemaVersion = "heteroci-owner-postgresql/old" }},
		{name: "missing_table", mutate: func(state *postgresqlOwnerTestState) { delete(state.tableOwners, "routing_leases") }},
		{name: "missing_index", mutate: func(state *postgresqlOwnerTestState) { delete(state.indexNames, PostgreSQLOwnerScopeRunIndex) }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			database, state := openPostgreSQLOwnerTestDB(t)
			testCase.mutate(state)
			_, err := NewPostgreSQLOwner(context.Background(), database, validOwnerScope())
			if !errors.Is(err, ErrPostgreSQLOwnerTopology) {
				t.Fatalf("NewPostgreSQLOwner error = %v, want ErrPostgreSQLOwnerTopology", err)
			}
			if len(state.data.head) != 0 || state.schemaStatementCount != 0 {
				t.Fatalf("untrusted authority mutated runtime state: %#v", state)
			}
		})
	}
}

func TestPostgreSQLOwnerRuntimeContractSurface(t *testing.T) {
	var database *sql.DB
	if _, err := NewPostgreSQLOwnerWithClock(context.Background(), database, validOwnerScope(), time.Now); err == nil {
		t.Fatal("NewPostgreSQLOwnerWithClock accepted nil database")
	}
}

func TestPostgreSQLOwnerRejectsPostConstructionOwnershipDrift(t *testing.T) {
	database, state := openPostgreSQLOwnerTestDB(t)
	owner, err := NewPostgreSQLOwner(context.Background(), database, validOwnerScope())
	if err != nil {
		t.Fatalf("NewPostgreSQLOwner: %v", err)
	}
	state.tableOwners["routing_leases"] = "postgres"
	if _, err := owner.Acquire(context.Background()); !errors.Is(err, ErrPostgreSQLOwnerTopology) {
		t.Fatalf("Acquire ownership drift error = %v, want ErrPostgreSQLOwnerTopology", err)
	}
	if len(state.data.head) != 0 || state.advisoryLocks != 0 {
		t.Fatalf("ownership drift reached authority mutation or lock: %#v", state)
	}
}

func TestPostgreSQLOwnerDDLIsIndependentCanonicalBYTEASchema(t *testing.T) {
	statements := postgresqlOwnerSchemaStatements()
	if len(statements) < 2 || statements[0] != "CREATE SCHEMA "+PostgreSQLOwnerSchema+" AUTHORIZATION "+PostgreSQLOwnerRole {
		t.Fatalf("PostgreSQL owner DDL does not create %s first: %#v", PostgreSQLOwnerSchema, statements)
	}
	joined := PostgreSQLOwnerDDL()
	additive := PostgreSQLOwnerAdditiveIndexDDL()
	if !strings.HasSuffix(joined, ";") {
		t.Fatal("PostgreSQL owner operator DDL is not statement terminated")
	}
	for _, required := range []string{
		PostgreSQLOwnerSchema + ".owner_heads",
		PostgreSQLOwnerSchema + ".owner_epochs",
		PostgreSQLOwnerSchema + ".operation_journal",
		PostgreSQLOwnerSchema + ".routing_runs",
		PostgreSQLOwnerSchema + ".routing_reservations",
		PostgreSQLOwnerSchema + ".routing_leases",
		"request_bytes BYTEA",
		"result_bytes BYTEA",
		"lease_bytes BYTEA",
		"OWNER TO " + PostgreSQLOwnerRole,
		"COMMENT ON SCHEMA " + PostgreSQLOwnerSchema + " IS '" + PostgreSQLOwnerRuntimeSchemaVersion + "'",
		"REVOKE ALL ON SCHEMA " + PostgreSQLOwnerSchema + " FROM PUBLIC",
		"REVOKE ALL ON TABLE " + PostgreSQLOwnerSchema + ".owner_heads FROM PUBLIC",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("PostgreSQL owner DDL missing %q", required)
		}
	}
	for _, forbidden := range []string{"?", "PRAGMA", "sqlite", "IF NOT EXISTS", "heteroci_receipt"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("PostgreSQL owner DDL contains forbidden %q", forbidden)
		}
	}
	assertPostgreSQLOwnerAdditiveIndexDDL(t, joined, additive)
}

func assertPostgreSQLOwnerAdditiveIndexDDL(t *testing.T, clean, additive string) {
	t.Helper()
	for _, definition := range postgresqlOwnerIndexDefinitions() {
		if strings.Count(clean, definition.statement) != 1 {
			t.Fatalf("clean owner DDL count for %q = %d, want 1", definition.name, strings.Count(clean, definition.statement))
		}
		if strings.Count(additive, definition.statement) != 1 {
			t.Fatalf("additive owner DDL count for %q = %d, want 1", definition.name, strings.Count(additive, definition.statement))
		}
	}
	for _, forbidden := range []string{"CREATE SCHEMA", "CREATE TABLE", "IF NOT EXISTS", "heteroci_receipt"} {
		if strings.Contains(additive, forbidden) {
			t.Fatalf("PostgreSQL owner additive index DDL contains forbidden %q", forbidden)
		}
	}
}

func TestPostgreSQLOwnerIndexRegistryIsMinimalAndQueryBound(t *testing.T) {
	definitions := postgresqlOwnerIndexDefinitions()
	if len(definitions) != 3 {
		t.Fatalf("explicit PostgreSQL owner indexes = %d, want 3", len(definitions))
	}
	seen := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if definition.name == "" || definition.statement == "" {
			t.Fatalf("empty PostgreSQL owner index definition: %#v", definition)
		}
		if _, exists := seen[definition.name]; exists {
			t.Fatalf("duplicate PostgreSQL owner index %q", definition.name)
		}
		seen[definition.name] = struct{}{}
	}
	for _, required := range []string{
		"CREATE UNIQUE INDEX " + PostgreSQLOwnerActiveEpochIndex + " ON heteroci_owner.owner_epochs (scope) WHERE retired_at_unix_milli IS NULL",
		"CREATE INDEX " + PostgreSQLOwnerScopeRunIndex + " ON heteroci_owner.routing_runs (scope, run_id, owner_epoch)",
		"CREATE INDEX " + PostgreSQLOwnerActiveReservationIndex + " ON heteroci_owner.routing_reservations (scope, owner_epoch, run_id) WHERE state IN ('planned', 'started')",
	} {
		if !strings.Contains(PostgreSQLOwnerAdditiveIndexDDL(), required) {
			t.Fatalf("PostgreSQL owner index registry missing %q", required)
		}
	}
	for _, redundant := range []string{
		"ON heteroci_owner.owner_heads (scope)",
		"ON heteroci_owner.operation_journal (scope, operation_id)",
		"ON heteroci_owner.routing_leases (scope, owner_epoch, run_id, unit_id)",
	} {
		if strings.Contains(PostgreSQLOwnerAdditiveIndexDDL(), redundant) {
			t.Fatalf("PostgreSQL owner index registry duplicates constraint index %q", redundant)
		}
	}
}

func TestPostgreSQL14IndexExplainFixtureCoversOwnerAndReceiptPaths(t *testing.T) {
	fixture, err := os.ReadFile("postgresql_index_explain_pg14_test.sql")
	if err != nil {
		t.Fatalf("read PostgreSQL 14 EXPLAIN fixture: %v", err)
	}
	sqlText := string(fixture)
	for _, required := range []string{
		"EXPLAIN (FORMAT JSON, COSTS OFF)",
		PostgreSQLOwnerActiveEpochIndex,
		PostgreSQLOwnerScopeRunIndex,
		PostgreSQLOwnerActiveReservationIndex,
		"routing_leases_pkey",
		"batch_receipts_pkey",
		"ORDER BY owner_epoch",
		"ORDER BY unit_id",
		"state IN ('planned', 'started')",
	} {
		if !strings.Contains(sqlText, required) {
			t.Fatalf("PostgreSQL 14 EXPLAIN fixture missing %q", required)
		}
	}
	if strings.Contains(sqlText, "EXPLAIN ANALYZE") || !strings.HasSuffix(sqlText, "ROLLBACK;\n") {
		t.Fatal("PostgreSQL 14 EXPLAIN fixture may execute mutations or is not rollback bounded")
	}
}
