package main

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

type blockingFreshRunner struct {
	mu                sync.Mutex
	seen              map[string]bool
	gates             map[string]chan struct{}
	completed         map[string]int
	started           chan freshContainerRequest
	release           chan struct{}
	lifecycleComplete chan struct{}
}

func (runner *blockingFreshRunner) RunFreshContainer(
	ctx context.Context,
	request freshContainerRequest,
) (localci.FreshContainerResult, error) {
	gate, first := runner.groupGate(request.JobSourceTreeSHA)
	if err := runner.waitForGroupRelease(ctx, request, gate, first); err != nil {
		return localci.FreshContainerResult{}, err
	}
	result, err := successfulFreshContainerResult(ctx, request)
	runner.recordGroupLifecycleCompletion(request.JobSourceTreeSHA)
	return result, err
}

func (runner *blockingFreshRunner) groupGate(sourceTreeSHA string) (chan struct{}, bool) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.gates == nil {
		runner.gates = make(map[string]chan struct{})
		runner.completed = make(map[string]int)
	}
	if runner.seen == nil {
		runner.seen = make(map[string]bool)
	}
	first := !runner.seen[sourceTreeSHA]
	runner.seen[sourceTreeSHA] = true
	gate := runner.gates[sourceTreeSHA]
	if first {
		gate = make(chan struct{})
		runner.gates[sourceTreeSHA] = gate
	}
	return gate, first
}

func (runner *blockingFreshRunner) waitForGroupRelease(
	ctx context.Context,
	request freshContainerRequest,
	gate chan struct{},
	first bool,
) error {
	if first {
		runner.started <- request
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-runner.release:
			close(gate)
		}
	} else {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-gate:
		}
	}
	return nil
}

func (runner *blockingFreshRunner) recordGroupLifecycleCompletion(sourceTreeSHA string) {
	runner.mu.Lock()
	runner.completed[sourceTreeSHA]++
	completed := runner.completed[sourceTreeSHA] == testCoordinatorSchedulingPolicy().ShardsPerJob
	runner.mu.Unlock()
	if completed && runner.lifecycleComplete != nil {
		runner.lifecycleComplete <- struct{}{}
	}
}

type countingPlanFreshRunner struct {
	mu             sync.Mutex
	requests       []freshContainerRequest
	completed      int
	release        <-chan struct{}
	lifecycleReady chan struct{}
}

type failingPlanFreshRunner struct {
	countingPlanFreshRunner
	readyCount int
	ready      chan struct{}
	started    chan struct{}
}

func (runner *failingPlanFreshRunner) RunFreshContainer(
	ctx context.Context,
	request freshContainerRequest,
) (localci.FreshContainerResult, error) {
	runner.mu.Lock()
	runner.requests = append(runner.requests, request)
	if len(runner.requests) == testCoordinatorSchedulingPolicy().ShardsPerJob && runner.started != nil {
		close(runner.started)
	}
	ready := runner.ready
	runner.mu.Unlock()
	if err := runner.waitForRelease(ctx); err != nil {
		return localci.FreshContainerResult{}, err
	}
	result, err := successfulFreshContainerResult(ctx, request)
	runner.recordLifecycleCompletion()
	runner.mu.Lock()
	runner.readyCount++
	if runner.readyCount == testCoordinatorSchedulingPolicy().ShardsPerJob {
		close(runner.ready)
	}
	runner.mu.Unlock()
	<-ready
	if err != nil {
		return result, err
	}
	failureGate := request.Plan.Gates[0].ID
	if !containsShardGate(request.ShardGateIDs, failureGate) {
		<-ctx.Done()
		result.Status = gatecontract.ResultStatusCancelled
		result.ExitCode = -1
		for index := range result.PlanGateResults {
			result.PlanGateResults[index].Status = gatecontract.ResultStatusCancelled
			result.PlanGateResults[index].GateResult.Status = gatecontract.GateStatusCancelled
			result.PlanGateResults[index].GateResult.ExitCode = -1
		}
		return result, ctx.Err()
	}
	result.Status = gatecontract.ResultStatusFailed
	result.ExitCode = 1
	result.PlanGateResults[0].Status = gatecontract.ResultStatusFailed
	result.PlanGateResults[0].GateResult.Status = gatecontract.GateStatusFailed
	result.PlanGateResults[0].GateResult.ExitCode = 1
	for index := 1; index < len(result.PlanGateResults); index++ {
		result.PlanGateResults[index].Status = gatecontract.ResultStatusCancelled
		result.PlanGateResults[index].GateResult.Status = gatecontract.GateStatusCancelled
		result.PlanGateResults[index].GateResult.ExitCode = -1
	}
	return result, errors.New("gate container exited with code 1")
}

func containsShardGate(gates []gatecontract.GateID, target gatecontract.GateID) bool {
	return slices.Contains(gates, target)
}

func (runner *countingPlanFreshRunner) RunFreshContainer(
	ctx context.Context,
	request freshContainerRequest,
) (localci.FreshContainerResult, error) {
	runner.mu.Lock()
	runner.requests = append(runner.requests, request)
	runner.mu.Unlock()
	if err := runner.waitForRelease(ctx); err != nil {
		return localci.FreshContainerResult{}, err
	}
	result, err := successfulFreshContainerResult(ctx, request)
	runner.recordLifecycleCompletion()
	return result, err
}

func (runner *countingPlanFreshRunner) recordLifecycleCompletion() {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.completed++
	if runner.completed == testCoordinatorSchedulingPolicy().ShardsPerJob {
		close(runner.lifecycleReady)
	}
}

func (runner *countingPlanFreshRunner) waitForRelease(ctx context.Context) error {
	if runner.release == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-runner.release:
		return nil
	}
}

func (runner *countingPlanFreshRunner) waitForLifecycle(t *testing.T) {
	t.Helper()
	select {
	case <-runner.lifecycleReady:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for shard lifecycle completion")
	}
}

func (runner *blockingFreshRunner) waitForLifecycle(t *testing.T, count int) {
	t.Helper()
	for range count {
		select {
		case <-runner.lifecycleComplete:
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for shard lifecycle completion")
		}
	}
}

func waitCoordinatorSharedSlots(t *testing.T, fixture coordinatorSlotTestFixture) {
	t.Helper()
	assertCoordinatorCandidateBuildStarted(t, fixture.buildService.started)
	assertCoordinatorShardGroupWaitsForBuildSlot(t, fixture.runner.started)
	assertCoordinatorBuildLeaseHeld(t, fixture.client)
	fixture.buildService.release <- struct{}{}
	waitCoordinatorShardGroupAfterBuildRelease(t, fixture.runner.started)
}

func isExpectedCoordinatorServeShutdown(err error) bool {
	if err == nil {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !isExpectedCoordinatorServeShutdown(child) {
				return false
			}
		}
		return true
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, localci.ErrSchedulerClosed)
}

func (runner *countingPlanFreshRunner) snapshot() []freshContainerRequest {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return append([]freshContainerRequest(nil), runner.requests...)
}

func TestCoordinatorExecutesCanonicalPlanInThreeContainerShards(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	release := make(chan struct{})
	runner := &countingPlanFreshRunner{release: release, lifecycleReady: make(chan struct{})}
	owner := startTestCoordinatorOwner(t, checkpoint, runner)
	client := dialTestCoordinator(t, checkpoint)
	submitted := submitTestPlan(t, client, "b")
	close(release)
	runner.waitForLifecycle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := client.Wait(ctx, submitted.JobID)
	if err != nil {
		t.Fatal(err)
	}
	assertThreeShardPlanStatus(t, owner, runner, status)
}

func TestCoordinatorPersistsExactPlanResultsWhenContainerFails(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	release := make(chan struct{})
	runner := &failingPlanFreshRunner{countingPlanFreshRunner: countingPlanFreshRunner{release: release, lifecycleReady: make(chan struct{})}, ready: make(chan struct{})}
	owner := startTestCoordinatorOwner(t, checkpoint, runner)
	client := dialTestCoordinator(t, checkpoint)
	submitted := submitTestPlan(t, client, "c")
	close(release)
	runner.waitForLifecycle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := client.Wait(ctx, submitted.JobID)
	if err != nil {
		t.Fatal(err)
	}
	assertFailedPlanPersistsCancelledCompanion(t, owner, runner, status)
}

func TestCoordinatorShardFinalizationBudgetStartsAfterLongExecution(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	release := make(chan struct{})
	runner := &failingPlanFreshRunner{
		countingPlanFreshRunner: countingPlanFreshRunner{release: release, lifecycleReady: make(chan struct{})},
		ready:                   make(chan struct{}),
		started:                 make(chan struct{}),
	}
	owner := startTestCoordinatorOwner(t, checkpoint, runner)
	owner.shardCleanupTimeout = 500 * time.Millisecond
	client := dialTestCoordinator(t, checkpoint)
	submitted := submitTestPlan(t, client, "d")
	waitForShardRequests(t, runner.started)
	time.Sleep(750 * time.Millisecond)
	close(release)
	runner.waitForLifecycle(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := client.Wait(ctx, submitted.JobID)
	if err != nil {
		t.Fatal(err)
	}
	assertFailedPlanPersistsCancelledCompanion(t, owner, runner, status)
}

func waitForShardRequests(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for all shard requests")
	}
}

func assertFailedPlanPersistsCancelledCompanion(
	t *testing.T,
	owner *coordinatorOwner,
	runner *failingPlanFreshRunner,
	status jobStatus,
) {
	t.Helper()
	requests := runner.snapshot()
	assertFailedPlanStatus(t, requests, status)
	assertCancelledCompanionStored(t, owner, status)
}

func assertFailedPlanStatus(t *testing.T, requests []freshContainerRequest, status jobStatus) {
	t.Helper()
	assertFailedPlanRequests(t, requests)
	assertFailedPlanTerminalStatus(t, requests, status)
	assertFailedPlanGateEvidence(t, requests, status)
}

func assertFailedPlanRequests(t *testing.T, requests []freshContainerRequest) {
	t.Helper()
	if len(requests) != testCoordinatorSchedulingPolicy().ShardsPerJob {
		t.Fatalf("failed plan request count=%d want=%d", len(requests), testCoordinatorSchedulingPolicy().ShardsPerJob)
	}
	for _, request := range requests {
		if request.PlanExecution || request.ShardIdentity == "" || len(request.ShardGateIDs) == 0 {
			t.Fatalf("failed plan used noncanonical shard request=%#v", request)
		}
	}
}

func assertFailedPlanTerminalStatus(t *testing.T, requests []freshContainerRequest, status jobStatus) {
	t.Helper()
	if status.State != jobStateFailed || !status.Terminal || len(status.GateResults) != len(requests[0].Plan.Gates) {
		t.Fatalf("failed plan terminal status = %#v", status)
	}
}

func assertFailedPlanGateEvidence(t *testing.T, requests []freshContainerRequest, status jobStatus) {
	t.Helper()
	failureGateID := string(requests[0].Plan.Gates[0].ID)
	failed, cancelled := false, false
	for _, result := range status.GateResults {
		if result.GateID == failureGateID && result.Status == gatecontract.GateStatusFailed && result.LogDigest != "" {
			failed = true
		}
		if result.GateID != failureGateID && result.Status == gatecontract.GateStatusCancelled && result.ExitCode == -1 {
			cancelled = true
		}
	}
	if !failed || !cancelled {
		t.Fatalf("failed/cancelled shard evidence missing: %#v", status.GateResults)
	}
}

func assertCancelledCompanionStored(t *testing.T, owner *coordinatorOwner, status jobStatus) {
	t.Helper()
	record, err := owner.store.job(context.Background(), status.JobID)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range record.GateResults {
		if result.Status == gatecontract.GateStatusCancelled && result.ExitCode == -1 {
			return
		}
	}
	t.Fatalf("stored cancelled companion missing: %#v", record.GateResults)
}

func assertThreeShardPlanStatus(
	t *testing.T,
	owner *coordinatorOwner,
	runner *countingPlanFreshRunner,
	status jobStatus,
) {
	t.Helper()
	requests := runner.snapshot()
	assertThreeShardPlanRequests(t, requests, status)
	record, err := owner.store.job(context.Background(), status.JobID)
	if err != nil {
		t.Fatal(err)
	}
	assertThreeShardPlanReceipt(t, record, requests)
	assertThreeShardPlanDeadlines(t, record.ContainerShards, *record.Deadline)
}

func assertThreeShardPlanRequests(t *testing.T, requests []freshContainerRequest, status jobStatus) {
	t.Helper()
	if status.State != jobStatePassed || !status.Terminal || len(requests) != testCoordinatorSchedulingPolicy().ShardsPerJob {
		t.Fatalf("three-shard plan status=%#v requests=%#v", status, requests)
	}
	for _, request := range requests {
		if request.PlanExecution || request.ShardIdentity == "" || len(request.ShardGateIDs) == 0 || request.ClaimDeadline == nil || !request.Deadline.IsZero() {
			t.Fatalf("noncanonical shard request=%#v", request)
		}
	}
}

func assertThreeShardPlanReceipt(t *testing.T, record coordinatorJobRecord, requests []freshContainerRequest) {
	t.Helper()
	if record.Receipt == nil || record.Receipt.Status != gatecontract.ResultStatusPassed ||
		len(record.Receipt.GateResults) != len(requests[0].Plan.Gates) || len(record.ContainerShards) != testCoordinatorSchedulingPolicy().ShardsPerJob || record.Deadline == nil {
		t.Fatalf("three-shard receipt = %#v", record.Receipt)
	}
}

func assertThreeShardPlanDeadlines(t *testing.T, shards []coordinatorShardRecord, deadline time.Time) {
	t.Helper()
	for _, shard := range shards {
		if shard.Deadline == nil || !shard.Deadline.Equal(deadline) {
			t.Fatalf("shard deadline drifted: %#v", shard)
		}
	}
}

func assertCoordinatorCandidateBuildStarted(t *testing.T, started <-chan string) {
	t.Helper()
	select {
	case workloadID := <-started:
		if workloadID != "build-candidate-slot-proof" {
			t.Fatalf("build workload = %q", workloadID)
		}
	case <-time.After(time.Second):
		t.Fatal("candidate build did not start")
	}
}

func assertCoordinatorShardGroupWaitsForBuildSlot(t *testing.T, started <-chan freshContainerRequest) {
	t.Helper()
	select {
	case request := <-started:
		t.Fatalf("shard group started while candidate build held one slot: %#v", request)
	case <-time.After(100 * time.Millisecond):
	}
}

func assertCoordinatorBuildLeaseHeld(t *testing.T, client *coordinatorTransportClient) {
	t.Helper()
	snapshot, err := client.scheduler.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Leases) != 1 || snapshot.Leases[0].Kind != localci.WorkloadKindBuild {
		t.Fatalf("candidate build leases=%+v want one build lease", snapshot.Leases)
	}
}

func waitCoordinatorShardGroupAfterBuildRelease(t *testing.T, started <-chan freshContainerRequest) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("three-shard gang did not start after candidate build released its slot")
	}
}

func sortCoordinatorJobStatusesByEnqueueSequence(statuses []jobStatus) {
	slices.SortFunc(statuses, func(left, right jobStatus) int {
		if left.EnqueueSequence < right.EnqueueSequence {
			return -1
		}
		if left.EnqueueSequence > right.EnqueueSequence {
			return 1
		}
		return 0
	})
}

func assertCoordinatorShardGroupsRunFIFO(
	t *testing.T,
	client *coordinatorTransportClient,
	runner *blockingFreshRunner,
	statuses []jobStatus,
) {
	t.Helper()
	for index, submitted := range statuses {
		var request freshContainerRequest
		select {
		case request = <-runner.started:
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for shard group %d", index)
		}
		if request.JobSourceTreeSHA != submitted.JobSourceTreeSHA {
			t.Fatalf("shard FIFO drift at %d: source=%s want=%s", index, request.JobSourceTreeSHA, submitted.JobSourceTreeSHA)
		}
		snapshot, err := client.scheduler.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		assertSingleThreeShardGang(t, snapshot, submitted.JobID)
		runner.release <- struct{}{}
	}
	runner.waitForLifecycle(t, len(statuses))
}

func assertCoordinatorFIFOJobsTerminal(t *testing.T, client *coordinatorTransportClient, statuses []jobStatus) {
	t.Helper()
	for _, submitted := range statuses {
		terminal, waitErr := client.Wait(context.Background(), submitted.JobID)
		if waitErr != nil || terminal.State != jobStatePassed || !terminal.Terminal {
			t.Fatalf("Wait(%s) status=%#v error=%v", submitted.JobID, terminal, waitErr)
		}
		if terminal.ImageProvenanceSourceTreeSHA == terminal.JobSourceTreeSHA {
			t.Fatalf("job %s collapsed image provenance tree into job source tree", submitted.JobID)
		}
	}
}

func assertSingleThreeShardGang(t *testing.T, snapshot localci.SchedulerSnapshot, jobID string) {
	t.Helper()
	if len(snapshot.Leases) != testCoordinatorSchedulingPolicy().ShardsPerJob {
		t.Fatalf("job %s active leases=%d want=%d", jobID, len(snapshot.Leases), testCoordinatorSchedulingPolicy().ShardsPerJob)
	}
	groupIdentity := snapshot.Leases[0].GroupIdentity
	for _, lease := range snapshot.Leases {
		if lease.GroupIdentity == "" || lease.GroupIdentity != groupIdentity || lease.WorkloadID != jobID+"/shards" {
			t.Fatalf("job %s has non-atomic gang leases=%+v", jobID, snapshot.Leases)
		}
	}
}
