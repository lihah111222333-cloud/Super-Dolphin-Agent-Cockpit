package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

type schedulerReconnectState struct {
	mu        sync.Mutex
	workloads map[string]localci.WorkloadRequest
	order     []string
}

type schedulerReconnectStub struct {
	state                 *schedulerReconnectState
	available             bool
	acceptWithoutResponse bool
	snapshotErr           error
	enqueueCalls          int
	snapshotCalls         int
	closeCalls            int
}

func (client *schedulerReconnectStub) Enqueue(_ context.Context, request localci.WorkloadRequest) error {
	client.enqueueCalls++
	if !client.available {
		return localci.ErrSchedulerClosed
	}
	client.state.mu.Lock()
	if _, exists := client.state.workloads[request.ID]; !exists {
		client.state.order = append(client.state.order, request.ID)
	}
	client.state.workloads[request.ID] = request
	client.state.mu.Unlock()
	if client.acceptWithoutResponse {
		client.available = false
		return context.DeadlineExceeded
	}
	return nil
}

func (client *schedulerReconnectStub) Snapshot(context.Context) (localci.SchedulerSnapshot, error) {
	client.snapshotCalls++
	if !client.available {
		return localci.SchedulerSnapshot{}, localci.ErrSchedulerClosed
	}
	if client.snapshotErr != nil {
		client.available = false
		return localci.SchedulerSnapshot{}, client.snapshotErr
	}
	client.state.mu.Lock()
	defer client.state.mu.Unlock()
	snapshot := localci.SchedulerSnapshot{Workloads: make([]localci.WorkloadSnapshot, 0, len(client.state.workloads))}
	for _, request := range client.state.workloads {
		snapshot.Workloads = append(snapshot.Workloads, localci.WorkloadSnapshot{
			Request: request, Status: localci.WorkloadStatusQueued,
		})
	}
	return snapshot, nil
}

func (client *schedulerReconnectStub) Available() bool {
	return client != nil && client.available
}

func (client *schedulerReconnectStub) Close() error {
	client.closeCalls++
	client.available = false
	return nil
}

func TestEnsureSchedulerReconnectsDiscardedNonNilClient(t *testing.T) {
	state := &schedulerReconnectState{workloads: make(map[string]localci.WorkloadRequest)}
	discarded := &schedulerReconnectStub{state: state}
	reconnected := &schedulerReconnectStub{state: state, available: true}
	connectCalls := 0
	client := &coordinatorTransportClient{
		scheduler: discarded,
		schedulerConnector: func(context.Context) (coordinatorSchedulerClient, error) {
			connectCalls++
			return reconnected, nil
		},
	}
	if err := client.ensureScheduler(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.scheduler != reconnected || connectCalls != 1 || discarded.closeCalls != 1 {
		t.Fatalf(
			"scheduler reconnect client=%T calls=%d staleClose=%d",
			client.scheduler, connectCalls, discarded.closeCalls,
		)
	}
}

func TestEnqueueAcceptedWithoutResponseReconnectsBeforeSnapshot(t *testing.T) {
	fixture := newAcceptedWithoutResponseEnqueueFixture(t)
	if err := fixture.client.enqueuePersistedJob(context.Background(), fixture.record); err != nil {
		t.Fatalf("enqueue accepted without response: %v", err)
	}
	fixture.assertReconciledEnqueue(t)
}

func TestEnsurePersistedJobScheduledEnqueuesDurablePredecessorsFirst(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	store, err := openCoordinatorStore(context.Background(), checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	state := &schedulerReconnectState{workloads: make(map[string]localci.WorkloadRequest)}
	scheduler := &schedulerReconnectStub{state: state, available: true}
	client := &coordinatorTransportClient{store: store, scheduler: scheduler}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close coordinator: %v", err)
		}
	})

	first, err := store.createJob(
		context.Background(), "hook-"+strings.Repeat("1", 64), "job-durable-first",
		mustWorkingDirectory(t), mustTestGatePlan(t, "1"), localci.PromotionCandidatePlan{}, manualSubmissionAuthority(),
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.createJob(
		context.Background(), "hook-"+strings.Repeat("2", 64), "job-durable-second",
		mustWorkingDirectory(t), mustTestGatePlan(t, "2"), localci.PromotionCandidatePlan{}, manualSubmissionAuthority(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := client.ensurePersistedJobScheduled(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	order := append([]string(nil), state.order...)
	state.mu.Unlock()
	if got, want := order, []string{first.JobID, second.JobID}; !slices.Equal(got, want) {
		t.Fatalf("scheduler enqueue order = %v, want %v", got, want)
	}
}

type acceptedWithoutResponseEnqueueFixture struct {
	client       *coordinatorTransportClient
	store        *coordinatorStore
	record       coordinatorJobRecord
	timedOut     *schedulerReconnectStub
	reconnected  *schedulerReconnectStub
	connectCalls *int
}

func newAcceptedWithoutResponseEnqueueFixture(t *testing.T) acceptedWithoutResponseEnqueueFixture {
	t.Helper()
	checkpoint := coordinatorTestCheckpoint(t)
	store, err := openCoordinatorStore(context.Background(), checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	state := &schedulerReconnectState{workloads: make(map[string]localci.WorkloadRequest)}
	timedOut := &schedulerReconnectStub{state: state, available: true, acceptWithoutResponse: true}
	reconnected := &schedulerReconnectStub{state: state, available: true}
	connectCalls := 0
	client := &coordinatorTransportClient{
		store: store, scheduler: timedOut,
		schedulerConnector: func(context.Context) (coordinatorSchedulerClient, error) {
			connectCalls++
			return reconnected, nil
		},
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close reconnecting coordinator: %v", err)
		}
	})
	plan := mustTestGatePlan(t, "f")
	record, err := store.createJob(
		context.Background(), "hook-"+strings.Repeat("f", 64), "job-reconnect-enqueue",
		mustWorkingDirectory(t), plan, localci.PromotionCandidatePlan{}, manualSubmissionAuthority(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return acceptedWithoutResponseEnqueueFixture{
		client: client, store: store, record: record, timedOut: timedOut,
		reconnected: reconnected, connectCalls: &connectCalls,
	}
}

func (fixture acceptedWithoutResponseEnqueueFixture) assertReconciledEnqueue(t *testing.T) {
	t.Helper()
	reloaded, err := fixture.store.job(context.Background(), fixture.record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State != jobStateQueued || fixture.timedOut.snapshotCalls != 0 || fixture.reconnected.snapshotCalls != 1 ||
		fixture.reconnected.enqueueCalls != 0 || *fixture.connectCalls != 1 {
		t.Fatalf(
			"reconcile state=%q oldSnapshots=%d newSnapshots=%d retryEnqueues=%d connects=%d",
			reloaded.State, fixture.timedOut.snapshotCalls, fixture.reconnected.snapshotCalls,
			fixture.reconnected.enqueueCalls, *fixture.connectCalls,
		)
	}
}

func TestEnqueueUncertainAfterReconnectLeavesDurableJobQueued(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	store, err := openCoordinatorStore(context.Background(), checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	state := &schedulerReconnectState{workloads: make(map[string]localci.WorkloadRequest)}
	timedOut := &schedulerReconnectStub{state: state, available: true, acceptWithoutResponse: true}
	observeErr := errors.New("injected snapshot disconnect")
	reconnected := &schedulerReconnectStub{state: state, available: true, snapshotErr: observeErr}
	client := &coordinatorTransportClient{
		store: store, scheduler: timedOut,
		schedulerConnector: func(context.Context) (coordinatorSchedulerClient, error) {
			return reconnected, nil
		},
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close reconnecting coordinator: %v", err)
		}
	})
	plan := mustTestGatePlan(t, "e")
	record, err := store.createJob(
		context.Background(), "hook-"+strings.Repeat("e", 64), "job-uncertain-enqueue",
		mustWorkingDirectory(t), plan, localci.PromotionCandidatePlan{}, manualSubmissionAuthority(),
	)
	if err != nil {
		t.Fatal(err)
	}
	err = client.enqueuePersistedJob(context.Background(), record)
	if !errors.Is(err, observeErr) {
		t.Fatalf("enqueue uncertain error = %v, want %v", err, observeErr)
	}
	reloaded, err := store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State != jobStateQueued || timedOut.snapshotCalls != 0 || reconnected.snapshotCalls != 1 {
		t.Fatalf(
			"uncertain state=%q oldSnapshots=%d newSnapshots=%d",
			reloaded.State, timedOut.snapshotCalls, reconnected.snapshotCalls,
		)
	}
}

func TestDeferredCoordinatorPlansBeforeSchedulerConnect(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	starter := &competingOwnerStarter{checkpoint: checkpoint, dependencies: coordinatorDependencies{
		ImageEnsurer: fakeImageEnsurer{}, CandidateBuilder: fakeCandidateBuildService{},
		PromotionWatcher: fakePromotionWatcher{}, SourceMaterializer: fakeSourceMaterializer{},
		FreshRunner: immediateFreshRunner{}, RecoveryRunner: &capturingFreshContainerRunner{},
		ReceiptSigner:    mustTestResultReceiptSigner(t),
		SchedulingPolicy: testCoordinatorSchedulingPolicy(),
	}}
	delay := coordinatorConnectTimeout + 100*time.Millisecond
	client, err := newDeferredCoordinatorClient(
		context.Background(), checkpoint, delayedCandidatePlanner{delay: delay},
		func(ctx context.Context) (*localci.SchedulerClient, error) {
			return connectScheduler(ctx, checkpoint, starter)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	status := submitTestPlan(t, client, "b")
	if elapsed := time.Since(started); elapsed < delay {
		t.Fatalf("Submit() elapsed = %v, planner delay = %v", elapsed, delay)
	}
	if client.scheduler == nil {
		t.Fatal("Submit() did not connect scheduler after candidate planning")
	}
	if _, err := client.scheduler.Snapshot(context.Background()); err != nil {
		t.Fatalf("scheduler first-frame path after delayed planning: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), coordinatorConnectTimeout)
	defer cancel()
	if _, err := client.Wait(waitCtx, status.JobID); err != nil {
		t.Fatalf("wait submitted deferred job before stopping owner: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Errorf("client.Close() error = %v", err)
	}
	starter.stop(t)
}

func TestDeferredCoordinatorPlannerFailureDoesNotConnectScheduler(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	plannerErr := errors.New("injected candidate planning failure")
	connectorCalled := false
	client, err := newDeferredCoordinatorClient(
		context.Background(), checkpoint, delayedCandidatePlanner{err: plannerErr},
		func(context.Context) (*localci.SchedulerClient, error) {
			connectorCalled = true
			return nil, errors.New("unexpected scheduler connect")
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Submit(context.Background(), submitRequest{
		RepositoryRoot: mustWorkingDirectory(t), Plan: mustTestGatePlan(t, "c"),
		Entrypoint: manualSubmissionAuthority().Entrypoint, AuthorityOwner: manualSubmissionAuthority().Owner,
	})
	if !errors.Is(err, plannerErr) {
		t.Fatalf("Submit() error = %v, want %v", err, plannerErr)
	}
	if connectorCalled || client.scheduler != nil {
		t.Fatal("planner failure dialed scheduler or started owner")
	}
	if err := client.Close(); err != nil {
		t.Errorf("client.Close() error = %v", err)
	}
}

type failFirstSchedulerConnector struct {
	checkpoint localci.DockerDaemonIdentityCheckpoint
	err        error
	calls      int
}

func (connector *failFirstSchedulerConnector) connect(ctx context.Context) (*localci.SchedulerClient, error) {
	connector.calls++
	if connector.calls == 1 {
		return nil, connector.err
	}
	return waitForScheduler(ctx, connector.checkpoint, nil)
}

func TestDeferredCoordinatorRetryRepairsDurableJobAfterConnectFailure(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	startTestCoordinatorOwner(t, checkpoint, immediateFreshRunner{})
	probe, err := waitForScheduler(context.Background(), checkpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	planner := &countingCandidatePlanner{}
	connector := &failFirstSchedulerConnector{
		checkpoint: checkpoint,
		err:        errors.New("injected scheduler connect failure"),
	}
	client, err := newDeferredCoordinatorClient(context.Background(), checkpoint, planner, connector.connect)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("client.Close() error = %v", err)
		}
	})
	request := submitRequest{
		RepositoryRoot: mustWorkingDirectory(t), InvocationID: "hook-" + strings.Repeat("9", 64),
		Plan:                 mustTestGatePlan(t, "d"),
		Entrypoint:           gatecontract.CIEntrypointGitPreCommit,
		AuthorityOwner:       gatecontract.CIEntrypointOwnerManagedGitPreCommit,
		AuthorityAttestation: "sha256:" + strings.Repeat("9", 64),
	}
	if _, err := client.Submit(context.Background(), request); !errors.Is(err, connector.err) {
		t.Fatalf("first Submit() error = %v, want %v", err, connector.err)
	}
	record, err := client.store.jobByInvocation(context.Background(), request.InvocationID)
	requireQueuedDurableJob(t, record, err)
	status, err := client.Submit(context.Background(), request)
	requireDeferredRetryStatus(t, status, err, record.JobID, connector.calls, planner.callCount())
	exists, err := client.schedulerWorkloadExists(context.Background(), record.JobID)
	requireSchedulerWorkload(t, exists, err)
	terminal, err := client.Wait(context.Background(), record.JobID)
	requireTerminalPass(t, terminal, err)
}

func requireQueuedDurableJob(t *testing.T, record coordinatorJobRecord, err error) {
	t.Helper()
	if err != nil || record.State != jobStateQueued {
		t.Fatalf("durable record after connect failure = %#v, error = %v", record, err)
	}
}

func requireDeferredRetryStatus(t *testing.T, status jobStatus, err error, jobID string, connectCalls int, plannerCalls int) {
	t.Helper()
	if err != nil || status.JobID != jobID || connectCalls != 2 || plannerCalls != 1 {
		t.Fatalf("retry status=%#v error=%v connectCalls=%d plannerCalls=%d", status, err, connectCalls, plannerCalls)
	}
}

func requireSchedulerWorkload(t *testing.T, exists bool, err error) {
	t.Helper()
	if err != nil || !exists {
		t.Fatalf("repaired scheduler workload exists=%t error=%v", exists, err)
	}
}

func requireTerminalPass(t *testing.T, terminal jobStatus, err error) {
	t.Helper()
	if err != nil || terminal.State != jobStatePassed || !terminal.Terminal {
		t.Fatalf("deferred durable job did not execute: status=%#v error=%v", terminal, err)
	}
}

func TestCoordinatorRetriesPersistedStateTransitions(t *testing.T) {
	calls := 0
	status, err := retryCoordinatorTransition(context.Background(), func() (jobStatus, error) {
		calls++
		if calls <= 20 {
			return jobStatus{}, reconcileCoordinatorState(jobStateQueued, localci.WorkloadStatusStarted)
		}
		return jobStatus{State: jobStateStarted}, nil
	})
	if err != nil || status.State != jobStateStarted || calls != 21 {
		t.Fatalf("transition retry status=%#v error=%v calls=%d", status, err, calls)
	}
	for _, transition := range []struct {
		durable   jobState
		scheduler localci.WorkloadStatus
	}{
		{durable: jobStateQueued, scheduler: localci.WorkloadStatusPassed},
		{durable: jobStateStarted, scheduler: localci.WorkloadStatusPassed},
		{durable: jobStatePassed, scheduler: localci.WorkloadStatusStarted},
	} {
		err := reconcileCoordinatorState(transition.durable, transition.scheduler)
		if !errors.Is(err, errCoordinatorTransition) {
			t.Fatalf("persisted transition durable=%q scheduler=%q classification=%v", transition.durable, transition.scheduler, err)
		}
	}
	for _, drift := range []struct {
		durable   jobState
		scheduler localci.WorkloadStatus
	}{
		{durable: jobStateStarted, scheduler: localci.WorkloadStatusQueued},
		{durable: jobStatePassed, scheduler: localci.WorkloadStatusFailed},
		{durable: jobStateInfraFailed, scheduler: localci.WorkloadStatusPassed},
	} {
		err := reconcileCoordinatorState(drift.durable, drift.scheduler)
		if !errors.Is(err, errCoordinatorState) || errors.Is(err, errCoordinatorTransition) {
			t.Fatalf("state drift durable=%q scheduler=%q classification=%v", drift.durable, drift.scheduler, err)
		}
	}
}
