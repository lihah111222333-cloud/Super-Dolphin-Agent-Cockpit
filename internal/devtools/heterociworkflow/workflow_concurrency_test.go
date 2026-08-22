package heterociworkflow

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
)

const (
	concurrencyUnitCPU    = int64(600)
	concurrencyUnitMemory = int64(256 << 20)
)

// TestExecuteRunBatchesAllowsIndependentLocalLanesToOverlap guards the missing cross-lane overlap.
func TestExecuteRunBatchesAllowsIndependentLocalLanesToOverlap(t *testing.T) {
	fixture := newWorkflowConcurrencyFixture(2, batchOverlapMode)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := Execute(ctx, fixture.deps, fixture.request)
	if err != nil {
		t.Fatalf("cross-lane execution returned %v; observations=%v max_active=%d, want independent lanes to overlap", err, fixture.harness.observationsSnapshot(), fixture.harness.maxActiveSnapshot())
	}
	observations := fixture.harness.observationsSnapshot()
	if len(observations) != 2 || fixture.harness.maxActiveSnapshot() < 2 {
		t.Fatalf("cross-lane observations=%v max_active=%d, want two overlapping executor calls", observations, fixture.harness.maxActiveSnapshot())
	}
	if observations[0].key == observations[1].key {
		t.Fatalf("cross-lane observations use one executor key: %v", observations)
	}
}

// TestExecuteRunBatchesKeepsSameNodeWaveBarrier guards the per-node wave ordering contract.
func TestExecuteRunBatchesKeepsSameNodeWaveBarrier(t *testing.T) {
	fixture := newWorkflowConcurrencyFixture(1, batchBarrierMode)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := Execute(ctx, fixture.deps, fixture.request); err != nil {
		t.Fatalf("same-node wave execution: %v", err)
	}
	observations := fixture.harness.observationsSnapshot()
	if len(observations) != 2 {
		t.Fatalf("same-node observations=%v, want two waves", observations)
	}
	sort.Slice(observations, func(left, right int) bool { return observations[left].wave < observations[right].wave })
	if observations[0].key != observations[1].key || observations[0].wave != 0 || observations[1].wave != 1 {
		t.Fatalf("same-node observations=%v, want one key with waves 0 then 1", observations)
	}
	if observations[1].started.Before(observations[0].ended) {
		t.Fatalf("same-node wave overlap: first=%v second=%v", observations[0], observations[1])
	}
}

type batchHarnessMode uint8

const (
	batchOverlapMode batchHarnessMode = iota + 1
	batchBarrierMode
)

type batchObservation struct {
	key     ExecutorKey
	wave    int
	started time.Time
	ended   time.Time
}

type batchConcurrencyHarness struct {
	mode         batchHarnessMode
	mu           sync.Mutex
	active       int
	maxActive    int
	startedCount int
	startedBoth  chan struct{}
	startedOnce  sync.Once
	observations []*batchObservation
}

func newBatchConcurrencyHarness(mode batchHarnessMode) *batchConcurrencyHarness {
	return &batchConcurrencyHarness{mode: mode, startedBoth: make(chan struct{})}
}

func (h *batchConcurrencyHarness) execute(ctx context.Context, batch ExecutionBatch) (BatchResult, error) {
	observation := &batchObservation{key: batch.Key, wave: batch.Wave, started: time.Now()}
	h.mu.Lock()
	h.startedCount++
	h.active++
	if h.active > h.maxActive {
		h.maxActive = h.active
	}
	if h.startedCount >= 2 {
		h.startedOnce.Do(func() { close(h.startedBoth) })
	}
	h.observations = append(h.observations, observation)
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		observation.ended = time.Now()
		h.active--
		h.mu.Unlock()
	}()

	if h.mode == batchOverlapMode {
		select {
		case <-h.startedBoth:
		case <-ctx.Done():
			return BatchResult{}, ctx.Err()
		}
	} else {
		timer := time.NewTimer(25 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return BatchResult{}, ctx.Err()
		}
	}
	return concurrencyBatchResult(batch), nil
}

func (h *batchConcurrencyHarness) observationsSnapshot() []batchObservation {
	h.mu.Lock()
	defer h.mu.Unlock()
	observations := make([]batchObservation, len(h.observations))
	for index, observation := range h.observations {
		observations[index] = *observation
	}
	return observations
}

func (h *batchConcurrencyHarness) maxActiveSnapshot() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.maxActive
}

type concurrencyBatchExecutor struct {
	harness *batchConcurrencyHarness
}

func (executor *concurrencyBatchExecutor) ExecuteBatch(ctx context.Context, batch ExecutionBatch) (BatchResult, error) {
	return executor.harness.execute(ctx, batch)
}

type concurrencyExecutorBinder struct {
	mu        sync.Mutex
	harness   *batchConcurrencyHarness
	executors map[ExecutorKey]BatchExecutor
}

func (binder *concurrencyExecutorBinder) Bind(plan ExecutorBindingPlan) (map[ExecutorKey]BatchExecutor, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	binder.mu.Lock()
	defer binder.mu.Unlock()
	bound := make(map[ExecutorKey]BatchExecutor, len(plan.Bindings))
	for _, binding := range plan.Bindings {
		key := binding.Key
		executor, ok := binder.executors[key]
		if !ok {
			executor = &concurrencyBatchExecutor{harness: binder.harness}
			binder.executors[key] = executor
		}
		bound[key] = executor
	}
	return bound, nil
}

type concurrencyOwner struct {
	mu    sync.Mutex
	owner *fakeOwner
}

func (owner *concurrencyOwner) Acquire(ctx context.Context) (heteroci.OwnerFence, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.owner.Acquire(ctx)
}

func (owner *concurrencyOwner) LocateRun(ctx context.Context, runID heteroci.TopologyRunID) ([]heterociowner.DurableRunState, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.owner.LocateRun(ctx, runID)
}

func (owner *concurrencyOwner) PlanAndReserve(ctx context.Context, fence heteroci.OwnerFence, request heteroci.TopologyPlanRequest) (heterociowner.DurableReservedRoutingPlan, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.owner.PlanAndReserve(ctx, fence, request)
}

func (owner *concurrencyOwner) Start(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, snapshot heteroci.SnapshotSet) (heteroci.StartedRoutingLease, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.owner.Start(ctx, fence, operationID, runID, unitID, snapshot)
}

func (owner *concurrencyOwner) Finish(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, terminal heteroci.RoutingLeaseState) (heterociowner.DurableLeaseTransition, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.owner.Finish(ctx, fence, operationID, runID, unitID, terminal)
}

func (owner *concurrencyOwner) CancelPlanned(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string) (heterociowner.DurableLeaseTransition, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.owner.CancelPlanned(ctx, fence, operationID, runID, unitID)
}

func (owner *concurrencyOwner) ReadRun(ctx context.Context, runID heteroci.TopologyRunID) (heterociowner.DurableRunState, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.owner.ReadRun(ctx, runID)
}

var _ DurableOwner = (*concurrencyOwner)(nil)

type workflowConcurrencyFixture struct {
	deps    Dependencies
	request RunRequest
	harness *batchConcurrencyHarness
}

func newWorkflowConcurrencyFixture(nodeCount int, mode batchHarnessMode) *workflowConcurrencyFixture {
	base := newWorkflowFixture(&fakeExecutor{})
	manifest := base.manifest
	manifest.Nodes = append([]heteroci.NodeConfig(nil), manifest.Nodes...)
	snapshot := base.snapshot
	snapshot.Nodes = append([]heteroci.NodeSnapshot(nil), snapshot.Nodes...)
	capacity := heteroci.ResourceCapacity{CPUMilli: 1_000, MemoryBytes: 1 << 30, MaxConcurrency: 1}
	for index := range snapshot.Nodes {
		snapshot.Nodes[index].Total = capacity
		snapshot.Nodes[index].Available = capacity
	}
	if nodeCount == 1 {
		snapshot.Nodes[1].Available = heteroci.ResourceCapacity{}
	}
	units := []heteroci.ExecutionUnit{
		{UnitID: "unit-1", TargetPlatform: heteroci.PlatformLinuxAMD64, RequiredRuntime: heteroci.RuntimeUbuntuContainer, EstimatedDurationMS: 100, CPUMilli: concurrencyUnitCPU, MemoryBytes: concurrencyUnitMemory},
		{UnitID: "unit-2", TargetPlatform: heteroci.PlatformLinuxAMD64, RequiredRuntime: heteroci.RuntimeUbuntuContainer, EstimatedDurationMS: 100, CPUMilli: concurrencyUnitCPU, MemoryBytes: concurrencyUnitMemory},
	}
	events := make([]string, 0, 16)
	owner := &concurrencyOwner{owner: &fakeOwner{events: &events, now: base.now}}
	harness := newBatchConcurrencyHarness(mode)
	binder := &concurrencyExecutorBinder{harness: harness, executors: make(map[ExecutorKey]BatchExecutor)}
	deps := Dependencies{
		Owner:          owner,
		ProbeTransport: fakeProbeTransport{},
		Executors:      binder,
		LoadManifest:   func(string) (heteroci.Manifest, error) { return manifest, nil },
		AcquireSnapshot: func(_ context.Context, _ heteroci.Manifest, epoch heteroci.RoutingOwnerEpoch, _ heteroci.ProbeTransport, at time.Time, _ time.Duration) (heteroci.SnapshotSet, error) {
			copySnapshot := cloneConcurrencySnapshot(snapshot, epoch, at)
			return copySnapshot, nil
		},
		Now:            func() time.Time { return base.now },
		CleanupTimeout: time.Second,
	}
	return &workflowConcurrencyFixture{deps: deps, request: RunRequest{ManifestPath: "manifest.json", RepositoryID: "workflow-concurrency-repository", SourceTreeSHA: strings.Repeat("a", 40), RunID: fmt.Sprintf("workflow-concurrency-%d", nodeCount), Units: units, HeartbeatTTL: time.Minute}, harness: harness}
}

func cloneConcurrencySnapshot(source heteroci.SnapshotSet, epoch heteroci.RoutingOwnerEpoch, at time.Time) heteroci.SnapshotSet {
	copySnapshot := source
	copySnapshot.OwnerEpoch = epoch
	copySnapshot.CapturedAtUnixMilli = at.UnixMilli()
	copySnapshot.Nodes = append([]heteroci.NodeSnapshot(nil), source.Nodes...)
	for index := range copySnapshot.Nodes {
		copySnapshot.Nodes[index].HeartbeatUnixMilli = at.UnixMilli()
		copySnapshot.Nodes[index].ActiveCapabilities = append([]heteroci.ActiveCapabilitySnapshot(nil), source.Nodes[index].ActiveCapabilities...)
	}
	return copySnapshot
}

func concurrencyBatchResult(batch ExecutionBatch) BatchResult {
	outcomes := make([]BatchOutcome, len(batch.Units))
	for index, unit := range batch.Units {
		outcomes[index] = BatchOutcome{UnitID: unit.UnitID, State: heteroci.LeaseStateSucceeded}
	}
	result := BatchResult{BatchID: batch.BatchID, RunID: batch.RunID, Executor: batch.Key, Authority: heteroci.AuthorityLocalNonAuthoritative, Status: ResultStatusNotVerified, Outcomes: outcomes}
	fillFakeBatchResultIdentity(&result, batch)
	return result
}
