package heterociworkflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
)

const workflowTestImageDigest = "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

type fakeProbeTransport struct{}

func (fakeProbeTransport) Probe(context.Context, string, []byte) (heteroci.ProbeResponseBody, error) {
	return nil, errors.New("fake probe transport must not be called directly")
}

type fakeOwner struct {
	events         *[]string
	now            time.Time
	fence          heteroci.OwnerFence
	planned        heterociowner.DurableReservedRoutingPlan
	run            heterociowner.DurableRunState
	locateStates   []heterociowner.DurableRunState
	readErr        error
	startCount     int
	startSnapshots []heteroci.SnapshotSet
	finishCount    int
	cancelCount    int
	planRequest    heteroci.TopologyPlanRequest
}

func (owner *fakeOwner) record(event string) {
	*owner.events = append(*owner.events, event)
}

func (owner *fakeOwner) Acquire(context.Context) (heteroci.OwnerFence, error) {
	owner.record("acquire")
	fence, err := heteroci.NewOwnerFence(
		heteroci.RoutingOwnerScope("sha256:"+strings.Repeat("a", 64)),
		heteroci.RoutingOwnerID(strings.Repeat("b", 64)),
		heteroci.RoutingOwnerEpoch(1),
		heteroci.RoutingOwnerRevision(1),
	)
	if err != nil {
		return heteroci.OwnerFence{}, err
	}
	owner.fence = fence
	return fence, nil
}

func (owner *fakeOwner) LocateRun(context.Context, heteroci.TopologyRunID) ([]heterociowner.DurableRunState, error) {
	owner.record("locate")
	return append([]heterociowner.DurableRunState(nil), owner.locateStates...), nil
}

func (owner *fakeOwner) PlanAndReserve(_ context.Context, fence heteroci.OwnerFence, request heteroci.TopologyPlanRequest) (heterociowner.DurableReservedRoutingPlan, error) {
	owner.record("plan")
	owner.planRequest = request
	plan, decision, err := heteroci.PlanRoutingDecision(request.Manifest, request.SourceSnapshot, request.Units, owner.now, request.HeartbeatTTL)
	if err != nil {
		return heterociowner.DurableReservedRoutingPlan{}, err
	}
	nextFence, err := fakeNextFence(fence)
	if err != nil {
		return heterociowner.DurableReservedRoutingPlan{}, err
	}
	durableRunID := string(request.RunIdentity.DurableRunID)
	leases := fakePlannedLeases(durableRunID, decision, owner.now.UnixMilli())
	owner.planned = heterociowner.DurableReservedRoutingPlan{
		Fence:             nextFence,
		OperationID:       request.OperationID,
		SourceSnapshot:    request.SourceSnapshot,
		EffectiveSnapshot: request.SourceSnapshot,
		Plan:              plan,
		Decision:          decision,
		Leases:            leases,
	}
	owner.run = heterociowner.DurableRunState{
		Fence:        nextFence,
		RunID:        durableRunID,
		Manifest:     request.Manifest,
		Units:        append([]heteroci.ExecutionUnit(nil), request.Units...),
		Decision:     decision,
		Leases:       append([]heteroci.RoutingLease(nil), leases...),
		HeartbeatTTL: request.HeartbeatTTL,
		LastRevision: nextFence.Revision,
	}
	owner.fence = nextFence
	return owner.planned, nil
}

func (owner *fakeOwner) ReadRun(context.Context, heteroci.TopologyRunID) (heterociowner.DurableRunState, error) {
	owner.record("read")
	if owner.readErr != nil {
		return heterociowner.DurableRunState{}, owner.readErr
	}
	return owner.run, nil
}

func (owner *fakeOwner) BindExecutors(_ context.Context, mutation heteroci.ExecutorBindingMutation) (heteroci.ExecutorBindingMutationResult, error) {
	nextFence, err := fakeNextFence(mutation.Fence)
	if err != nil {
		return heteroci.ExecutorBindingMutationResult{}, err
	}
	return heteroci.ExecutorBindingMutationResult{Fence: nextFence, Plan: mutation.Plan}, nil
}
func (owner *fakeOwner) Start(_ context.Context, fence heteroci.OwnerFence, _ heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, snapshot heteroci.SnapshotSet) (heteroci.StartedRoutingLease, error) {
	owner.record("start")
	owner.startCount++
	owner.startSnapshots = append(owner.startSnapshots, snapshot)
	if snapshot.OwnerEpoch != fence.OwnerEpoch {
		return heteroci.StartedRoutingLease{}, errors.New("fake start snapshot epoch drifted")
	}
	index, lease, err := owner.findLease(string(runID), unitID, heteroci.LeaseStatePlanned)
	if err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	nextFence, err := fakeNextFence(fence)
	if err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	lease.State = heteroci.LeaseStateStarted
	lease.StartedAtUnixMilli = owner.now.UnixMilli()
	started, err := heteroci.NewStartedRoutingLease(nextFence, lease, owner.run.Decision, owner.now)
	if err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	owner.commitLease(index, nextFence, lease)
	return started, nil
}

func (owner *fakeOwner) Finish(_ context.Context, fence heteroci.OwnerFence, _ heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, terminal heteroci.RoutingLeaseState) (heterociowner.DurableLeaseTransition, error) {
	owner.record("finish")
	owner.finishCount++
	if terminal != heteroci.LeaseStateSucceeded && terminal != heteroci.LeaseStateFailed {
		return heterociowner.DurableLeaseTransition{}, errors.New("fake terminal state is invalid")
	}
	return owner.transition(fence, string(runID), unitID, heteroci.LeaseStateStarted, terminal)
}

func (owner *fakeOwner) CancelPlanned(_ context.Context, fence heteroci.OwnerFence, _ heteroci.OperationID, runID heteroci.TopologyRunID, unitID string) (heterociowner.DurableLeaseTransition, error) {
	owner.record("cancel")
	owner.cancelCount++
	return owner.transition(fence, string(runID), unitID, heteroci.LeaseStatePlanned, heteroci.LeaseStateCancelled)
}

func (owner *fakeOwner) transition(fence heteroci.OwnerFence, runID, unitID string, from, to heteroci.RoutingLeaseState) (heterociowner.DurableLeaseTransition, error) {
	index, lease, err := owner.findLease(runID, unitID, from)
	if err != nil {
		return heterociowner.DurableLeaseTransition{}, err
	}
	nextFence, err := fakeNextFence(fence)
	if err != nil {
		return heterociowner.DurableLeaseTransition{}, err
	}
	lease.State = to
	lease.FinishedAtUnixMilli = owner.now.UnixMilli()
	if to == heteroci.LeaseStateCancelled {
		lease.StartedAtUnixMilli = 0
	}
	owner.commitLease(index, nextFence, lease)
	return heterociowner.DurableLeaseTransition{Fence: nextFence, Lease: lease}, nil
}

func (owner *fakeOwner) findLease(runID, unitID string, state heteroci.RoutingLeaseState) (int, heteroci.RoutingLease, error) {
	for index, lease := range owner.run.Leases {
		if lease.RunID == runID && lease.UnitID == unitID && lease.State == state {
			return index, lease, nil
		}
	}
	return 0, heteroci.RoutingLease{}, errors.New("fake durable lease state mismatch")
}

func (owner *fakeOwner) commitLease(index int, fence heteroci.OwnerFence, lease heteroci.RoutingLease) {
	owner.run.Leases[index] = lease
	owner.run.Fence = fence
	owner.run.LastRevision = fence.Revision
	owner.fence = fence
	owner.run.Released = fakeAllTerminal(owner.run.Leases)
}

func fakeNextFence(fence heteroci.OwnerFence) (heteroci.OwnerFence, error) {
	return heteroci.NewOwnerFence(fence.Scope, fence.OwnerID, fence.OwnerEpoch, fence.Revision+1)
}

func fakePlannedLeases(runID string, decision heteroci.RoutingDecision, reservedAt int64) []heteroci.RoutingLease {
	leases := make([]heteroci.RoutingLease, 0, len(decision.Assignments))
	for _, assignment := range decision.Assignments {
		leases = append(leases, heteroci.RoutingLease{
			SchemaVersion:       heteroci.RoutingLeaseSchemaVersion,
			Authority:           heteroci.AuthorityLocalNonAuthoritative,
			RunID:               runID,
			DecisionDigest:      decision.DecisionDigest,
			UnitID:              assignment.UnitID,
			Executor:            assignment.Executor,
			NodeID:              assignment.NodeID,
			Endpoint:            assignment.Endpoint,
			ProbeIdentity:       assignment.ProbeIdentity,
			ProbeBootID:         assignment.ProbeBootID,
			State:               heteroci.LeaseStatePlanned,
			ReservedAtUnixMilli: reservedAt,
		})
	}
	return leases
}

func fakeAllTerminal(leases []heteroci.RoutingLease) bool {
	for _, lease := range leases {
		if lease.State != heteroci.LeaseStateSucceeded && lease.State != heteroci.LeaseStateFailed && lease.State != heteroci.LeaseStateCancelled {
			return false
		}
	}
	return len(leases) != 0
}

type fakeExecutor struct {
	events       *[]string
	result       BatchResult
	executeErr   error
	receipt      BatchResult
	receiptFound bool
	receiptErr   error
	executeCount int
	receiptCount int
	lastBatch    ExecutionBatch
}

func (executor *fakeExecutor) ExecuteBatch(_ context.Context, batch ExecutionBatch) (BatchResult, error) {
	*executor.events = append(*executor.events, "execute")
	executor.executeCount++
	executor.lastBatch = batch
	result := executor.result
	fillFakeBatchResultIdentity(&result, batch)
	return result, executor.executeErr
}

func (executor *fakeExecutor) ReadBatchReceipt(_ context.Context, batch ExecutionBatch) (BatchResult, bool, error) {
	*executor.events = append(*executor.events, "receipt")
	executor.receiptCount++
	receipt := executor.receipt
	fillFakeBatchResultIdentity(&receipt, batch)
	return receipt, executor.receiptFound, executor.receiptErr
}

// fillFakeBatchResultIdentity 只为测试 fixture 补齐当前 batch identity，生产 normalize 仍严格拒绝缺失值。
func fillFakeBatchResultIdentity(result *BatchResult, batch ExecutionBatch) {
	if result.BatchID == "" {
		result.BatchID = batch.BatchID
	}
	if result.RunID == "" {
		result.RunID = batch.RunID
	}
	if result.Executor == (ExecutorKey{}) {
		result.Executor = batch.Key
	}
	if result.MaterialDigest == "" {
		result.MaterialDigest = batch.MaterialDigest
	}
	if result.CommandDigest == "" {
		result.CommandDigest = "sha256:" + strings.Repeat("c", 64)
	}
}

type fakeBinder struct {
	events   *[]string
	executor BatchExecutor
	err      error
	count    int
	plan     ExecutorBindingPlan
}

func (binder *fakeBinder) Bind(plan ExecutorBindingPlan) (map[ExecutorKey]BatchExecutor, error) {
	*binder.events = append(*binder.events, "bind")
	binder.count++
	binder.plan = cloneExecutorBindingPlan(plan)
	if binder.err != nil {
		return nil, binder.err
	}
	bound := make(map[ExecutorKey]BatchExecutor, len(plan.Bindings))
	for _, binding := range plan.Bindings {
		bound[ExecutorKey{Kind: binding.ExecutorKind, NodeID: binding.NodeID}] = binder.executor
	}
	return bound, nil
}

type workflowFixture struct {
	now      time.Time
	manifest heteroci.Manifest
	snapshot heteroci.SnapshotSet
	units    []heteroci.ExecutionUnit
	events   []string
	owner    *fakeOwner
	binder   *fakeBinder
	deps     Dependencies
	request  RunRequest
}

func newWorkflowFixture(t testing.TB, executor BatchExecutor) *workflowFixture {
	t.Helper()
	now := time.UnixMilli(1_750_000_000_000)
	manifest := heteroci.Manifest{
		SchemaVersion: heteroci.ManifestSchemaVersion,
		Authority:     heteroci.AuthorityLocalNonAuthoritative,
		Nodes: []heteroci.NodeConfig{
			{NodeID: "workflow-node-1", Endpoint: "https://workflow-node-1.invalid/runner", ProbeIdentity: "sha256:" + strings.Repeat("1", 64), Platform: heteroci.PlatformLinuxAMD64, Capabilities: []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: workflowTestImageDigest}}},
			{NodeID: "workflow-node-2", Endpoint: "https://workflow-node-2.invalid/runner", ProbeIdentity: "sha256:" + strings.Repeat("2", 64), Platform: heteroci.PlatformLinuxAMD64, Capabilities: []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: workflowTestImageDigest}}},
			{NodeID: "workflow-node-3", Endpoint: "https://workflow-node-3.invalid/runner", ProbeIdentity: "sha256:" + strings.Repeat("3", 64), Platform: heteroci.PlatformWindowsAMD64, Capabilities: []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeWindowsNative}}},
			{NodeID: "workflow-node-4", Endpoint: "https://workflow-node-4.invalid/runner", ProbeIdentity: "sha256:" + strings.Repeat("4", 64), Platform: heteroci.PlatformWindowsAMD64, Capabilities: []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeWindowsNative}}},
		},
	}
	capacity := heteroci.ResourceCapacity{CPUMilli: 4_000, MemoryBytes: 8 << 30, MaxConcurrency: 4}
	snapshot := heteroci.SnapshotSet{
		SchemaVersion:       heteroci.SnapshotSchemaVersion,
		Authority:           heteroci.AuthorityLocalNonAuthoritative,
		OwnerEpoch:          1,
		CapacityBasis:       heteroci.SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations,
		CapturedAtUnixMilli: now.UnixMilli(),
		Nodes: []heteroci.NodeSnapshot{
			{NodeID: manifest.Nodes[0].NodeID, Endpoint: manifest.Nodes[0].Endpoint, Platform: manifest.Nodes[0].Platform, ProbeIdentity: manifest.Nodes[0].ProbeIdentity, ProbeBootID: "boot-workflow-node-1", ActiveCapabilities: []heteroci.ActiveCapabilitySnapshot{{Runtime: heteroci.RuntimeUbuntuContainer, ImageDigest: workflowTestImageDigest}}, Status: heteroci.NodeStatusLive, HeartbeatUnixMilli: now.UnixMilli(), Total: capacity, Available: capacity},
			{NodeID: manifest.Nodes[1].NodeID, Endpoint: manifest.Nodes[1].Endpoint, Platform: manifest.Nodes[1].Platform, ProbeIdentity: manifest.Nodes[1].ProbeIdentity, ProbeBootID: "boot-workflow-node-2", ActiveCapabilities: []heteroci.ActiveCapabilitySnapshot{{Runtime: heteroci.RuntimeUbuntuContainer, ImageDigest: workflowTestImageDigest}}, Status: heteroci.NodeStatusLive, HeartbeatUnixMilli: now.UnixMilli(), Total: capacity, Available: capacity},
			{NodeID: manifest.Nodes[2].NodeID, Endpoint: manifest.Nodes[2].Endpoint, Platform: manifest.Nodes[2].Platform, ProbeIdentity: manifest.Nodes[2].ProbeIdentity, ProbeBootID: "boot-workflow-node-3", ActiveCapabilities: []heteroci.ActiveCapabilitySnapshot{{Runtime: heteroci.RuntimeWindowsNative}}, Status: heteroci.NodeStatusLive, HeartbeatUnixMilli: now.UnixMilli(), Total: capacity, Available: capacity},
			{NodeID: manifest.Nodes[3].NodeID, Endpoint: manifest.Nodes[3].Endpoint, Platform: manifest.Nodes[3].Platform, ProbeIdentity: manifest.Nodes[3].ProbeIdentity, ProbeBootID: "boot-workflow-node-4", ActiveCapabilities: []heteroci.ActiveCapabilitySnapshot{{Runtime: heteroci.RuntimeWindowsNative}}, Status: heteroci.NodeStatusLive, HeartbeatUnixMilli: now.UnixMilli(), Total: capacity, Available: capacity},
		},
	}
	snapshot.Nodes[1].Available = heteroci.ResourceCapacity{}
	units := []heteroci.ExecutionUnit{{UnitID: "unit-1", TargetPlatform: heteroci.PlatformLinuxAMD64, RequiredRuntime: heteroci.RuntimeUbuntuContainer, EstimatedDurationMS: 100, CPUMilli: 500, MemoryBytes: 256 << 20}}
	events := make([]string, 0, 16)
	owner := &fakeOwner{events: &events, now: now}
	binder := &fakeBinder{events: &events, executor: executor}
	fixture := &workflowFixture{now: now, manifest: manifest, snapshot: snapshot, units: units, events: events, owner: owner, binder: binder}
	owner.events = &fixture.events
	binder.events = &fixture.events
	fixture.deps = Dependencies{
		Owner:          owner,
		ProbeTransport: fakeProbeTransport{},
		Executors:      binder,
		LoadManifest: func(string) (heteroci.Manifest, error) {
			fixture.events = append(fixture.events, "load")
			return manifest, nil
		},
		AcquireSnapshot: func(_ context.Context, _ heteroci.Manifest, epoch heteroci.RoutingOwnerEpoch, _ heteroci.ProbeTransport, at time.Time, _ time.Duration) (heteroci.SnapshotSet, error) {
			fixture.events = append(fixture.events, "snapshot")
			copySnapshot := fixture.snapshot
			copySnapshot.OwnerEpoch = epoch
			copySnapshot.CapturedAtUnixMilli = at.UnixMilli()
			copySnapshot.Nodes = append([]heteroci.NodeSnapshot(nil), fixture.snapshot.Nodes...)
			copySnapshot.Nodes[0].HeartbeatUnixMilli = at.UnixMilli()
			return copySnapshot, nil
		},
		Now:            func() time.Time { return now },
		CleanupTimeout: time.Second,
	}
	fixture.request = strictWorkflowTestRequest(t, manifest, "workflow-repository", strings.Repeat("a", 40), "workflow-run-1", units, time.Minute)
	return fixture
}

func strictWorkflowTestRequest(t testing.TB, manifest heteroci.Manifest, repositoryID, sourceTreeSHA, runID string, units []heteroci.ExecutionUnit, ttl time.Duration) RunRequest {
	t.Helper()
	runIdentity, err := heteroci.DeriveTopologyRunIdentity(repositoryID, sourceTreeSHA, runID)
	if err != nil {
		t.Fatalf("DeriveTopologyRunIdentity: %v", err)
	}
	externalOperationID := heteroci.OperationID(strings.Repeat("d", 64))
	topologyManifestDigest, err := DeriveManifestDigest(manifest)
	if err != nil {
		t.Fatalf("DeriveManifestDigest: %v", err)
	}
	request := RunRequest{SchemaVersion: RunRequestSchemaVersion, ExternalOperationID: externalOperationID, DurableRunID: runIdentity.DurableRunID, ManifestDigest: "sha256:" + strings.Repeat("9", 64), TopologyManifestDigest: topologyManifestDigest, ManifestPath: "manifest.json", RepositoryID: repositoryID, SourceTreeSHA: sourceTreeSHA, RunID: runID, Units: units, HeartbeatTTL: ttl}
	request.RequestDigest, err = DeriveRunRequestDigest(request)
	if err != nil {
		t.Fatalf("DeriveRunRequestDigest: %v", err)
	}
	return request
}
func workflowPlanRequest(t *testing.T, fixture *workflowFixture, operationID heteroci.OperationID, units []heteroci.ExecutionUnit) heteroci.TopologyPlanRequest {
	t.Helper()
	runIdentity := workflowRunIdentity(t, fixture)
	return heteroci.TopologyPlanRequest{
		OperationID:    operationID,
		RunIdentity:    runIdentity,
		Manifest:       fixture.manifest,
		SourceSnapshot: fixture.snapshot,
		Units:          units,
		HeartbeatTTL:   fixture.request.HeartbeatTTL,
	}
}

func workflowRunIdentity(t *testing.T, fixture *workflowFixture) heteroci.TopologyRunIdentity {
	t.Helper()
	runIdentity, err := heteroci.DeriveTopologyRunIdentity(fixture.request.RepositoryID, fixture.request.SourceTreeSHA, fixture.request.RunID)
	if err != nil {
		t.Fatalf("DeriveTopologyRunIdentity: %v", err)
	}
	return runIdentity
}

func assertWorkflowEvents(got []string, want ...string) error {
	if len(got) != len(want) {
		return fmt.Errorf("events = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			return fmt.Errorf("events = %v, want %v", got, want)
		}
	}
	return nil
}
