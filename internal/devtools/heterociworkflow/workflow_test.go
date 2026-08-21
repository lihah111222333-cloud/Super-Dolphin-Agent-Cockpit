package heterociworkflow

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
)

func TestWorkflowRunExecutesOneBoundBatchAndFinishesLocalNonAuthoritative(t *testing.T) {
	executor := &fakeExecutor{}
	fixture := newWorkflowFixture(executor)
	executor.events = &fixture.events
	executor.result = BatchResult{
		Authority: heteroci.AuthorityLocalNonAuthoritative,
		Status:    ResultStatusNotVerified,
		Outcomes:  []BatchOutcome{{UnitID: "unit-1", State: heteroci.LeaseStateSucceeded}},
	}

	result, err := Execute(t.Context(), fixture.deps, fixture.request)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertSuccessfulWorkflowResult(t, result)
	assertSuccessfulWorkflowTrace(t, fixture, executor)
}

func TestWorkflowValidatesStartedLeaseWithPostStartClock(t *testing.T) {
	executor := &fakeExecutor{}
	fixture := newWorkflowFixture(executor)
	executor.events = &fixture.events
	executor.result = BatchResult{Authority: heteroci.AuthorityLocalNonAuthoritative, Status: ResultStatusNotVerified, Outcomes: []BatchOutcome{{UnitID: "unit-1", State: heteroci.LeaseStateSucceeded}}}
	clockCalls := 0
	fixture.deps.Now = func() time.Time {
		clockCalls++
		if clockCalls < 3 {
			return fixture.now
		}
		return fixture.now.Add(20 * time.Second)
	}
	snapshotCalls := 0
	acquireSnapshot := fixture.deps.AcquireSnapshot
	fixture.deps.AcquireSnapshot = func(ctx context.Context, manifest heteroci.Manifest, epoch heteroci.RoutingOwnerEpoch, transport heteroci.ProbeTransport, at time.Time, ttl time.Duration) (heteroci.SnapshotSet, error) {
		snapshotCalls++
		result, err := acquireSnapshot(ctx, manifest, epoch, transport, at, ttl)
		if snapshotCalls == 2 {
			fixture.owner.now = fixture.now.Add(10 * time.Second)
		}
		return result, err
	}
	if _, err := Execute(t.Context(), fixture.deps, fixture.request); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if clockCalls < 3 {
		t.Fatalf("clock calls = %d, want post-Start validation read", clockCalls)
	}
}

func assertSuccessfulWorkflowResult(t *testing.T, result RunResult) {
	t.Helper()
	if result.Authority != heteroci.AuthorityLocalNonAuthoritative || result.Status != ResultStatusNotVerified {
		t.Fatalf("result authority/status = %q/%q", result.Authority, result.Status)
	}
	if len(result.Units) != 1 || result.Units[0].Authority != heteroci.AuthorityLocalNonAuthoritative || result.Units[0].Status != ResultStatusNotVerified || result.Units[0].State != heteroci.LeaseStateSucceeded {
		t.Fatalf("result units = %+v", result.Units)
	}
}

func assertSuccessfulWorkflowTrace(t *testing.T, fixture *workflowFixture, executor *fakeExecutor) {
	t.Helper()
	if executor.executeCount != 1 || fixture.owner.startCount != 1 || fixture.owner.finishCount != 1 || fixture.owner.cancelCount != 0 {
		t.Fatalf("counts execute/start/finish/cancel = %d/%d/%d/%d", executor.executeCount, fixture.owner.startCount, fixture.owner.finishCount, fixture.owner.cancelCount)
	}
	want := []string{"load", "acquire", "locate", "snapshot", "plan", "read", "bind", "snapshot", "start", "execute", "finish"}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
}

func TestWorkflowNewRejectsMissingExplicitDependencies(t *testing.T) {
	fixture := newWorkflowFixture(&fakeExecutor{})
	tests := []struct {
		name   string
		mutate func(*Dependencies)
	}{
		{name: "loader", mutate: func(deps *Dependencies) { deps.LoadManifest = nil }},
		{name: "snapshot acquirer", mutate: func(deps *Dependencies) { deps.AcquireSnapshot = nil }},
		{name: "cleanup timeout", mutate: func(deps *Dependencies) { deps.CleanupTimeout = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := fixture.deps
			test.mutate(&deps)
			if _, err := New(deps); !errors.Is(err, ErrInvalidDependencies) {
				t.Fatalf("New error = %v, want ErrInvalidDependencies", err)
			}
		})
	}
}

func TestWorkflowBindingFailureCancelsEveryPlannedLease(t *testing.T) {
	fixture := newWorkflowFixture(nil)
	fixture.binder.err = errors.New("binder unavailable")

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	if !errors.Is(err, ErrExecutorBinding) {
		t.Fatalf("Execute error = %v, want ErrExecutorBinding", err)
	}
	if fixture.owner.cancelCount != len(fixture.units) || fixture.owner.startCount != 0 {
		t.Fatalf("cancel/start count = %d/%d", fixture.owner.cancelCount, fixture.owner.startCount)
	}
	if !reflect.DeepEqual(fixture.events, []string{"load", "acquire", "locate", "snapshot", "plan", "read", "bind", "cancel"}) {
		t.Fatalf("events = %v", fixture.events)
	}
}

func TestWorkflowExecutorErrorReadsReceiptOnceAndNeverReexecutes(t *testing.T) {
	executor := &fakeExecutor{executeErr: errors.New("transport acknowledgement lost")}
	fixture := newWorkflowFixture(executor)
	executor.events = &fixture.events

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	if !errors.Is(err, ErrBatchReceiptRequired) || !errors.Is(err, ErrCleanupIncomplete) {
		t.Fatalf("Execute error = %v, want receipt and cleanup blockers", err)
	}
	if executor.executeCount != 1 || executor.receiptCount != 1 {
		t.Fatalf("execute/receipt count = %d/%d", executor.executeCount, executor.receiptCount)
	}
	if fixture.owner.finishCount != 0 || fixture.owner.cancelCount != 0 {
		t.Fatalf("ambiguous result mutated lease: finish/cancel = %d/%d", fixture.owner.finishCount, fixture.owner.cancelCount)
	}
}

func TestWorkflowRejectsNonLocalBatchAuthority(t *testing.T) {
	executor := &fakeExecutor{result: BatchResult{
		Authority: "REMOTE_QUALIFIED",
		Status:    ResultStatusNotVerified,
		Outcomes:  []BatchOutcome{{UnitID: "unit-1", State: heteroci.LeaseStateSucceeded}},
	}}
	fixture := newWorkflowFixture(executor)
	executor.events = &fixture.events

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	if !errors.Is(err, ErrBatchResult) || !errors.Is(err, ErrCleanupIncomplete) {
		t.Fatalf("Execute error = %v, want batch authority blocker", err)
	}
	if executor.executeCount != 1 || fixture.owner.finishCount != 0 {
		t.Fatalf("execute/finish count = %d/%d", executor.executeCount, fixture.owner.finishCount)
	}
}

func TestWorkflowRejectsExistingDurableRunBeforeProbeOrPlan(t *testing.T) {
	fixture := newWorkflowFixture(&fakeExecutor{})
	fixture.owner.locateStates = []heterociowner.DurableRunState{{RunID: fixture.request.RunID}}

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	if !errors.Is(err, ErrExecutionState) {
		t.Fatalf("Execute error = %v, want ErrExecutionState", err)
	}
	if !reflect.DeepEqual(fixture.events, []string{"load", "acquire", "locate"}) {
		t.Fatalf("events = %v", fixture.events)
	}
	if fixture.owner.startCount != 0 || fixture.owner.cancelCount != 0 {
		t.Fatalf("start/cancel count = %d/%d", fixture.owner.startCount, fixture.owner.cancelCount)
	}
}

func TestDeriveBatchIDUsesCanonicalDecimalWaveEncoding(t *testing.T) {
	key := ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-1"}
	first, err := DeriveBatchID("run-1", key, 0, []string{"unit-1"})
	if err != nil {
		t.Fatalf("DeriveBatchID(0): %v", err)
	}
	boundary, err := DeriveBatchID("run-1", key, 1<<16, []string{"unit-1"})
	if err != nil {
		t.Fatalf("DeriveBatchID(boundary): %v", err)
	}
	if first == boundary {
		t.Fatal("wave 0 collided with wave 65536")
	}
}
