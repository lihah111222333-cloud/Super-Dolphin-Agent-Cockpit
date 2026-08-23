package heterociworkflow

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

type recordingLifecycleObserver struct {
	events    *[]string
	snapshots []PreparedRunSnapshot
	err       error
	mutate    bool
}

func (observer *recordingLifecycleObserver) ObservePreparedRun(_ context.Context, snapshot PreparedRunSnapshot) error {
	*observer.events = append(*observer.events, "observe")
	if err := snapshot.Validate(); err != nil {
		return err
	}
	if observer.mutate {
		snapshot.Units[0].UnitID = "observer-mutation"
		snapshot.Manifest.Nodes[0].Capabilities[0].Image = "observer-mutation"
		snapshot.Plan.NodeShards[0].Waves[0].UnitIDs[0] = "observer-mutation"
		snapshot.BindingPlan.Bindings[0].BatchIDs[0] = "observer-mutation"
	}
	observer.snapshots = append(observer.snapshots, snapshot)
	return observer.err
}

func TestWorkflowLifecycleObserverRunsOnceAfterBindingBeforeStart(t *testing.T) {
	executor := &fakeExecutor{result: BatchResult{Authority: heteroci.AuthorityLocalNonAuthoritative, Status: ResultStatusNotVerified, Outcomes: []BatchOutcome{{UnitID: "unit-1", State: heteroci.LeaseStateSucceeded}}}}
	fixture := newWorkflowFixture(t, executor)
	executor.events = &fixture.events
	observer := &recordingLifecycleObserver{events: &fixture.events, mutate: true}
	fixture.deps.LifecycleObserver = observer

	if _, err := Execute(t.Context(), fixture.deps, fixture.request); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(observer.snapshots) != 1 || fixture.owner.startCount != 1 {
		t.Fatalf("observer/start counts = %d/%d, want 1/1", len(observer.snapshots), fixture.owner.startCount)
	}
	assertPreparedExternalAudit(t, observer.snapshots[0], fixture.request)
	want := []string{"load", "acquire", "locate", "snapshot", "plan", "read", "observe", "bind", "start", "execute", "finish"}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
	if fixture.owner.planRequest.Units[0].UnitID == "observer-mutation" || fixture.binder.plan.Bindings[0].BatchIDs[0] == "observer-mutation" {
		t.Fatal("observer mutation escaped into workflow-owned binding or plan")
	}
}

func assertPreparedExternalAudit(t *testing.T, snapshot PreparedRunSnapshot, request RunRequest) {
	t.Helper()
	got := [3]string{snapshot.ExternalBindingDigest, snapshot.ManifestBlobSHA256, snapshot.RuntimePolicyDigest}
	want := [3]string{request.ExternalBindingDigest, request.ManifestBlobSHA256, request.RuntimePolicyDigest}
	if got != want {
		t.Fatalf("prepared external audit = %v, want %v", got, want)
	}
}

func TestWorkflowLifecycleObserverFailureCleansUpWithoutStart(t *testing.T) {
	fixture := newWorkflowFixture(t, &fakeExecutor{})
	observer := &recordingLifecycleObserver{events: &fixture.events, err: errors.New("event store unavailable")}
	fixture.deps.LifecycleObserver = observer

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	if !errors.Is(err, ErrExecutorBinding) {
		t.Fatalf("Execute error = %v, want ErrExecutorBinding", err)
	}
	if len(observer.snapshots) != 1 || fixture.owner.startCount != 0 {
		t.Fatalf("observer/start counts = %d/%d, want 1/0", len(observer.snapshots), fixture.owner.startCount)
	}
	if fixture.owner.cancelCount != len(fixture.units) {
		t.Fatalf("cancel count = %d, want %d", fixture.owner.cancelCount, len(fixture.units))
	}
	if !reflect.DeepEqual(fixture.events, []string{"load", "acquire", "locate", "snapshot", "plan", "read", "observe", "cancel"}) {
		t.Fatalf("events = %v", fixture.events)
	}
}

func TestWorkflowLifecycleObserverPrecedesExecutorBindFailure(t *testing.T) {
	fixture := newWorkflowFixture(t, &fakeExecutor{})
	fixture.binder.err = errors.New("runtime binder unavailable")
	observer := &recordingLifecycleObserver{events: &fixture.events}
	fixture.deps.LifecycleObserver = observer

	_, err := Execute(t.Context(), fixture.deps, fixture.request)
	if !errors.Is(err, ErrExecutorBinding) {
		t.Fatalf("Execute error = %v, want executor binding failure", err)
	}
	if len(observer.snapshots) != 1 || fixture.binder.count != 1 || fixture.owner.startCount != 0 {
		t.Fatalf("observer/bind/start counts = %d/%d/%d, want 1/1/0", len(observer.snapshots), fixture.binder.count, fixture.owner.startCount)
	}
	if fixture.owner.cancelCount != len(fixture.units) {
		t.Fatalf("cancel count = %d, want %d", fixture.owner.cancelCount, len(fixture.units))
	}
}

func TestWorkflowLifecycleObserverIsOnceBeforeAllWaveStarts(t *testing.T) {
	executor := &multiWaveTestExecutor{}
	fixture := newWorkflowFixture(t, executor)
	executor.events = &fixture.events
	capacity := heteroci.ResourceCapacity{CPUMilli: 4_000, MemoryBytes: 8 << 30, MaxConcurrency: 1}
	fixture.snapshot.Nodes[0].Total = capacity
	fixture.snapshot.Nodes[0].Available = capacity
	fixture.snapshot.Nodes[1].Available = heteroci.ResourceCapacity{}
	fixture.units = []heteroci.ExecutionUnit{fixture.units[0], {UnitID: "unit-2", TargetPlatform: heteroci.PlatformLinuxAMD64, RequiredRuntime: heteroci.RuntimeUbuntuContainer, EstimatedDurationMS: 100, CPUMilli: 500, MemoryBytes: 256 << 20}}
	fixture.request = strictWorkflowTestRequest(t, fixture.manifest, fixture.request.RepositoryID, fixture.request.SourceTreeSHA, fixture.request.RunID, fixture.units, time.Second)
	observer := &recordingLifecycleObserver{events: &fixture.events}
	fixture.deps.LifecycleObserver = observer

	if _, err := Execute(t.Context(), fixture.deps, fixture.request); err != nil {
		t.Fatalf("Execute across waves: %v", err)
	}
	if len(observer.snapshots) != 1 || executor.calls != 2 || fixture.owner.startCount != 2 {
		t.Fatalf("observer/wave/start counts = %d/%d/%d, want 1/2/2", len(observer.snapshots), executor.calls, fixture.owner.startCount)
	}
	assertObserverBeforeStart(t, fixture.events)
}

// assertObserverBeforeStart 断言 observer 只发生在第一个 wave Start 之前。
func assertObserverBeforeStart(t *testing.T, events []string) {
	t.Helper()
	observeIndex, startIndex := -1, -1
	for index, event := range events {
		if event == "observe" {
			observeIndex = index
		}
		if event == "start" && startIndex < 0 {
			startIndex = index
		}
	}
	if observeIndex < 0 || startIndex < 0 || observeIndex >= startIndex {
		t.Fatalf("observe/start ordering is invalid: %v", events)
	}
}
