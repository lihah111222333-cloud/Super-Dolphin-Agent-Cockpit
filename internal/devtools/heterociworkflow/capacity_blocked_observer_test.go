package heterociworkflow

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
)

type capacityBlockedOwner struct {
	*fakeOwner
	planErr error
}

func (owner *capacityBlockedOwner) PlanAndReserve(_ context.Context, _ heteroci.OwnerFence, request heteroci.TopologyPlanRequest) (heterociowner.DurableReservedRoutingPlan, error) {
	owner.record("plan")
	return heterociowner.DurableReservedRoutingPlan{}, owner.planErr
}

type capacityBlockedObserver struct {
	events    *[]string
	snapshots []CapacityBlockedRunSnapshot
	err       error
	mutate    bool
}

func (observer *capacityBlockedObserver) ObserveCapacityBlocked(_ context.Context, snapshot CapacityBlockedRunSnapshot) error {
	*observer.events = append(*observer.events, "blocked-observe")
	if err := snapshot.Validate(); err != nil {
		return err
	}
	if observer.mutate {
		snapshot.Units[0].UnitID = "observer-mutation"
		snapshot.Manifest.Nodes[0].NodeID = "observer-mutation"
		snapshot.Plan.UnassignedUnitIDs[0] = "observer-mutation"
		snapshot.Projection.Blockers[0].UnitID = "observer-mutation"
	}
	observer.snapshots = append(observer.snapshots, snapshot)
	return observer.err
}

func TestWorkflowCapacityBlockedObserverRunsOnceBeforeAnyBindingOrStart(t *testing.T) {
	fixture := newWorkflowFixture(t, &fakeExecutor{})
	for index := range fixture.snapshot.Nodes {
		fixture.snapshot.Nodes[index].Available = heteroci.ResourceCapacity{}
	}
	blocker := buildTestCapacityBlocker(t, fixture)
	owner := &capacityBlockedOwner{fakeOwner: fixture.owner, planErr: blocker}
	fixture.deps.Owner = owner
	observer := &capacityBlockedObserver{events: &fixture.events, mutate: true}
	fixture.deps.CapacityBlockedObserver = observer

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	var blocked *CapacityBlockedRunError
	if !errors.As(err, &blocked) {
		t.Fatalf("Execute error = %v, want CapacityBlockedRunError", err)
	}
	if len(observer.snapshots) != 1 || fixture.owner.startCount != 0 || fixture.binder.count != 0 {
		t.Fatalf("observer/start/bind counts = %d/%d/%d, want 1/0/0", len(observer.snapshots), fixture.owner.startCount, fixture.binder.count)
	}
	if !reflect.DeepEqual(fixture.events, []string{"load", "acquire", "locate", "snapshot", "plan", "blocked-observe"}) {
		t.Fatalf("events = %v", fixture.events)
	}
	if blocked.Snapshot.Units[0].UnitID == "observer-mutation" || blocked.Snapshot.Projection.Blockers[0].UnitID == "observer-mutation" {
		t.Fatal("observer mutation escaped into blocked error snapshot")
	}
}

func TestWorkflowCapacityBlockedObserverFailureIsFailFast(t *testing.T) {
	fixture := newWorkflowFixture(t, &fakeExecutor{})
	for index := range fixture.snapshot.Nodes {
		fixture.snapshot.Nodes[index].Available = heteroci.ResourceCapacity{}
	}
	owner := &capacityBlockedOwner{fakeOwner: fixture.owner, planErr: buildTestCapacityBlocker(t, fixture)}
	fixture.deps.Owner = owner
	fixture.deps.CapacityBlockedObserver = &capacityBlockedObserver{events: &fixture.events, err: errors.New("blocked receipt unavailable")}

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	var blocked *CapacityBlockedRunError
	if errors.As(err, &blocked) || err == nil {
		t.Fatalf("Execute error = %v, observer failure must not publish blocked error", err)
	}
	if fixture.owner.startCount != 0 || fixture.binder.count != 0 {
		t.Fatalf("start/bind counts = %d/%d", fixture.owner.startCount, fixture.binder.count)
	}
}

func TestWorkflowNonCapacityPlanErrorDoesNotInvokeBlockedObserver(t *testing.T) {
	fixture := newWorkflowFixture(t, &fakeExecutor{})
	owner := &capacityBlockedOwner{fakeOwner: fixture.owner, planErr: errors.New("owner journal failed")}
	fixture.deps.Owner = owner
	observer := &capacityBlockedObserver{events: &fixture.events}
	fixture.deps.CapacityBlockedObserver = observer

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	var blocked *CapacityBlockedRunError
	if err == nil || errors.As(err, &blocked) {
		t.Fatalf("Execute error = %v, non-capacity failure was misclassified", err)
	}
	if len(observer.snapshots) != 0 {
		t.Fatalf("blocked observer calls = %d, want 0", len(observer.snapshots))
	}
}

func buildTestCapacityBlocker(t *testing.T, fixture *workflowFixture) error {
	t.Helper()
	manifest := fixture.manifest
	snapshot := fixture.snapshot
	_, err := heteroci.BuildAnchorWavePlan(manifest, snapshot, fixture.units, fixture.now, fixture.request.HeartbeatTTL)
	if err == nil {
		t.Fatal("BuildAnchorWavePlan unexpectedly succeeded")
	}
	var blocker *heteroci.CapacityBlockerError
	if !errors.As(err, &blocker) {
		t.Fatalf("BuildAnchorWavePlan error = %v, want CapacityBlockerError", err)
	}
	return blocker
}
