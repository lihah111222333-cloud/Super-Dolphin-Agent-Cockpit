package heterociworkflow

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
)

func TestWorkflowNewRejectsEveryMissingOrTypedNilDependency(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Dependencies)
	}{
		{name: "owner", mutate: func(deps *Dependencies) { deps.Owner = nil }},
		{name: "typed nil owner", mutate: func(deps *Dependencies) { deps.Owner = (*fakeOwner)(nil) }},
		{name: "probe transport", mutate: func(deps *Dependencies) { deps.ProbeTransport = nil }},
		{name: "typed nil probe transport", mutate: func(deps *Dependencies) { deps.ProbeTransport = (*fakeProbeTransport)(nil) }},
		{name: "executor binder", mutate: func(deps *Dependencies) { deps.Executors = nil }},
		{name: "typed nil executor binder", mutate: func(deps *Dependencies) { deps.Executors = (*fakeBinder)(nil) }},
		{name: "manifest loader", mutate: func(deps *Dependencies) { deps.LoadManifest = nil }},
		{name: "snapshot acquirer", mutate: func(deps *Dependencies) { deps.AcquireSnapshot = nil }},
		{name: "clock", mutate: func(deps *Dependencies) { deps.Now = nil }},
		{name: "cleanup timeout", mutate: func(deps *Dependencies) { deps.CleanupTimeout = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkflowFixture(&fakeExecutor{})
			deps := fixture.deps
			test.mutate(&deps)
			if _, err := New(deps); !errors.Is(err, ErrInvalidDependencies) {
				t.Fatalf("New error = %v, want ErrInvalidDependencies", err)
			}
			if len(fixture.events) != 0 {
				t.Fatalf("dependency validation caused side effects: %v", fixture.events)
			}
		})
	}
}

func TestWorkflowExecutorErrorRecoversFromSingleDurableReceipt(t *testing.T) {
	executor := &fakeExecutor{
		executeErr:   errors.New("transport acknowledgement lost"),
		receiptFound: true,
		receipt:      successfulBatchResult("unit-1"),
	}
	fixture := newWorkflowFixture(executor)
	executor.events = &fixture.events

	result, err := Execute(t.Context(), fixture.deps, fixture.request)
	if err != nil {
		t.Fatalf("Execute with receipt recovery: %v", err)
	}
	if executor.executeCount != 1 || executor.receiptCount != 1 {
		t.Fatalf("execute/receipt count = %d/%d", executor.executeCount, executor.receiptCount)
	}
	if fixture.owner.finishCount != 1 || fixture.owner.cancelCount != 0 || len(result.Units) != 1 || result.Units[0].State != heteroci.LeaseStateSucceeded {
		t.Fatalf("receipt recovery result=%+v finish/cancel=%d/%d", result.Units, fixture.owner.finishCount, fixture.owner.cancelCount)
	}
	if err := assertWorkflowEvents(fixture.events, "load", "acquire", "locate", "snapshot", "plan", "read", "bind", "snapshot", "start", "execute", "receipt", "finish"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowNilBoundExecutorCancelsEveryPlannedLease(t *testing.T) {
	fixture := newWorkflowFixture(nil)

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	if !errors.Is(err, ErrExecutorBinding) {
		t.Fatalf("Execute error = %v, want ErrExecutorBinding", err)
	}
	if fixture.owner.cancelCount != len(fixture.units) || fixture.owner.startCount != 0 {
		t.Fatalf("cancel/start count = %d/%d", fixture.owner.cancelCount, fixture.owner.startCount)
	}
	if err := assertWorkflowEvents(fixture.events, "load", "acquire", "locate", "snapshot", "plan", "read", "bind", "cancel"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowPostPlanReadValidationCancelsEveryPlannedLease(t *testing.T) {
	fixture := newWorkflowFixture(&fakeExecutor{})
	fixture.deps.Owner = &readDriftOwner{fakeOwner: fixture.owner}

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	if !errors.Is(err, ErrExecutionState) {
		t.Fatalf("Execute error = %v, want ErrExecutionState", err)
	}
	if fixture.owner.cancelCount != len(fixture.units) || fixture.owner.startCount != 0 {
		t.Fatalf("cancel/start count = %d/%d", fixture.owner.cancelCount, fixture.owner.startCount)
	}
	if err := assertWorkflowEvents(fixture.events, "load", "acquire", "locate", "snapshot", "plan", "read", "cancel"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowRejectsBatchStatusOutsideNotVerified(t *testing.T) {
	executor := &fakeExecutor{result: BatchResult{
		Authority: heteroci.AuthorityLocalNonAuthoritative,
		Status:    "PASS",
		Outcomes:  []BatchOutcome{{UnitID: "unit-1", State: heteroci.LeaseStateSucceeded}},
	}}
	fixture := newWorkflowFixture(executor)
	executor.events = &fixture.events

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	if !errors.Is(err, ErrBatchResult) || !errors.Is(err, ErrCleanupIncomplete) {
		t.Fatalf("Execute error = %v, want status and cleanup blockers", err)
	}
	if executor.executeCount != 1 || fixture.owner.finishCount != 0 || fixture.owner.cancelCount != 0 {
		t.Fatalf("execute/finish/cancel count = %d/%d/%d", executor.executeCount, fixture.owner.finishCount, fixture.owner.cancelCount)
	}
}

func TestWorkflowOperationIDsAreDeterministicAndPhaseSeparated(t *testing.T) {
	units := []heteroci.ExecutionUnit{{UnitID: "unit-1"}, {UnitID: "unit-2"}}
	first, err := deriveRunOperationIDs("workflow-run-ids", units)
	if err != nil {
		t.Fatalf("deriveRunOperationIDs first: %v", err)
	}
	second, err := deriveRunOperationIDs("workflow-run-ids", units)
	if err != nil {
		t.Fatalf("deriveRunOperationIDs second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("operation IDs are nondeterministic: first=%#v second=%#v", first, second)
	}
	seen := map[heteroci.OperationID]struct{}{first.plan: {}}
	for _, unit := range units {
		for _, id := range []heteroci.OperationID{first.start[unit.UnitID], first.finish[unit.UnitID], first.cancel[unit.UnitID]} {
			if id == "" {
				t.Fatalf("operation ID for %s is empty", unit.UnitID)
			}
			if _, duplicate := seen[id]; duplicate {
				t.Fatalf("operation ID %s is reused across phase or unit", id)
			}
			seen[id] = struct{}{}
		}
	}
}

func TestWorkflowBuildsOneDeterministicLocalWaveBatch(t *testing.T) {
	fixture := newWorkflowFixture(&fakeExecutor{})
	units := append([]heteroci.ExecutionUnit(nil), fixture.units...)
	second := units[0]
	second.UnitID = "unit-2"
	units = append(units, second)
	fence, err := fixture.owner.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	planID, err := DeriveOperationID(fixture.request.RunID, OperationPhasePlan, "")
	if err != nil {
		t.Fatalf("DeriveOperationID: %v", err)
	}
	planned, err := fixture.owner.PlanAndReserve(t.Context(), fence, planID, fixture.request.RunID, fixture.manifest, fixture.snapshot, units, fixture.request.HeartbeatTTL)
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	batches, err := BuildBatches(fixture.request.RunID, fixture.manifest, planned, units, fixture.now, fixture.request.HeartbeatTTL)
	if err != nil {
		t.Fatalf("BuildBatches: %v", err)
	}
	assertSingleLocalWaveBatch(t, fixture, batches)
}

func assertSingleLocalWaveBatch(t *testing.T, fixture *workflowFixture, batches []BatchPlan) {
	t.Helper()
	if len(batches) != 1 {
		t.Fatalf("batch count = %d, want 1", len(batches))
	}
	batch := batches[0].Batch
	if batch.Key != (ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: fixture.manifest.Nodes[0].NodeID}) || batch.Wave != 0 {
		t.Fatalf("batch key/wave = %#v/%d", batch.Key, batch.Wave)
	}
	if len(batch.Units) != 2 || batch.Units[0].UnitID != "unit-1" || batch.Units[1].UnitID != "unit-2" || len(batch.Assignments) != 2 {
		t.Fatalf("batch units/assignments = %#v/%#v", batch.Units, batch.Assignments)
	}
	wantID, err := DeriveBatchID(fixture.request.RunID, batch.Key, batch.Wave, []string{"unit-1", "unit-2"})
	if err != nil {
		t.Fatalf("DeriveBatchID: %v", err)
	}
	if batch.BatchID != wantID {
		t.Fatalf("batch ID = %s, want %s", batch.BatchID, wantID)
	}
}

func successfulBatchResult(unitID string) BatchResult {
	return BatchResult{
		Authority: heteroci.AuthorityLocalNonAuthoritative,
		Status:    ResultStatusNotVerified,
		Outcomes:  []BatchOutcome{{UnitID: unitID, State: heteroci.LeaseStateSucceeded}},
	}
}

type readDriftOwner struct {
	*fakeOwner
}

func (owner *readDriftOwner) ReadRun(ctx context.Context, runID string) (heterociowner.DurableRunState, error) {
	state, err := owner.fakeOwner.ReadRun(ctx, runID)
	if err != nil {
		return heterociowner.DurableRunState{}, err
	}
	state.LastRevision++
	return state, nil
}

var _ DurableOwner = (*readDriftOwner)(nil)
