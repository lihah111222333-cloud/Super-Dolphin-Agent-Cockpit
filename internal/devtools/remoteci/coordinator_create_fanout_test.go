package remoteci

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"golang.org/x/sync/errgroup"
)

// createFailureFanoutStore 在首个创建失败前阻塞 sibling 请求上传，验证调用方取消边界不会扩散。
type createFailureFanoutStore struct {
	*coordinatorStore
	expectedRequests int

	mu                   sync.Mutex
	requestStarts        int
	allRequestsStarted   chan struct{}
	allRequestsOnce      sync.Once
	siblingUploadRelease chan struct{}
	siblingUploadCancel  chan struct{}
	siblingCancelOnce    sync.Once
	siblingReleaseOnce   sync.Once
}

func newCreateFailureFanoutStore(expectedRequests int) *createFailureFanoutStore {
	return &createFailureFanoutStore{
		coordinatorStore:     &coordinatorStore{},
		expectedRequests:     expectedRequests,
		allRequestsStarted:   make(chan struct{}),
		siblingUploadRelease: make(chan struct{}),
		siblingUploadCancel:  make(chan struct{}),
	}
}

func (store *createFailureFanoutStore) Create(ctx context.Context, localPath, key string) error {
	if !strings.HasSuffix(key, ".request.json") || strings.HasSuffix(key, ".bootstrap.request.json") {
		return store.coordinatorStore.Create(ctx, localPath, key)
	}
	index, err := createFailureFanoutShardIndex(localPath)
	if err != nil {
		return err
	}
	store.mu.Lock()
	store.requestStarts++
	if store.requestStarts >= store.expectedRequests {
		store.allRequestsOnce.Do(func() { close(store.allRequestsStarted) })
	}
	store.mu.Unlock()

	if index == 0 {
		if err := store.coordinatorStore.Create(ctx, localPath, key); err != nil {
			return err
		}
		<-store.allRequestsStarted
		return nil
	}
	select {
	case <-store.siblingUploadRelease:
		if err := ctx.Err(); err != nil {
			store.siblingCancelOnce.Do(func() { close(store.siblingUploadCancel) })
			return err
		}
		return store.coordinatorStore.Create(ctx, localPath, key)
	case <-ctx.Done():
		store.siblingCancelOnce.Do(func() { close(store.siblingUploadCancel) })
		return ctx.Err()
	}
}

func createFailureFanoutShardIndex(localPath string) (int, error) {
	name := filepath.Base(localPath)
	name = strings.TrimSuffix(strings.TrimPrefix(name, "shard-"), ".request.json")
	index, err := strconv.Atoi(name)
	if err != nil {
		return 0, fmt.Errorf("parse remote shard request path %q: %w", localPath, err)
	}
	return index, nil
}

type createFailureFanoutRuntime struct {
	*coordinatorRuntime

	mu               sync.Mutex
	createCalls      int
	cancelledCreates int
	failureObserved  chan struct{}
	failureOnce      sync.Once
}

func newCreateFailureFanoutRuntime() *createFailureFanoutRuntime {
	return &createFailureFanoutRuntime{
		coordinatorRuntime: &coordinatorRuntime{},
		failureObserved:    make(chan struct{}),
	}
}

func (runtime *createFailureFanoutRuntime) CreateContainerGroup(ctx context.Context, request eci.CreateRequest) (eci.ContainerGroup, error) {
	runtime.mu.Lock()
	index := runtime.createCalls
	runtime.createCalls++
	runtime.mu.Unlock()
	if index == 0 {
		runtime.failureOnce.Do(func() { close(runtime.failureObserved) })
		return eci.ContainerGroup{}, errors.New("Code: InvalidParameter.ValueExceeded, Message: vCPU max is 600")
	}
	if err := ctx.Err(); err != nil {
		runtime.mu.Lock()
		runtime.cancelledCreates++
		runtime.mu.Unlock()
		return eci.ContainerGroup{}, err
	}
	return runtime.coordinatorRuntime.CreateContainerGroup(ctx, request)
}

func (runtime *createFailureFanoutRuntime) fanoutCounts() (int, int) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.createCalls, runtime.cancelledCreates
}

type createFailureFanoutCase struct {
	input       RunInput
	planned     int
	store       *createFailureFanoutStore
	runtime     *createFailureFanoutRuntime
	coordinator *Coordinator
	prepared    *PreparedRun
}

type createFailureFanoutRun struct {
	done   chan struct{}
	group  errgroup.Group
	result RunResult
	err    error
}

func TestCoordinatorCreateFailureDoesNotCancelSiblingUploadsOrCreates(t *testing.T) {
	testCase := newCreateFailureFanoutCase(t)
	run := startCreateFailureFanoutRun(testCase.coordinator, testCase.prepared)
	settleCreateFailureFanout(t, testCase, run)
	assertCreateFailureFanout(t, testCase, run)
}

func newCreateFailureFanoutCase(t *testing.T) createFailureFanoutCase {
	t.Helper()
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	store := newCreateFailureFanoutStore(0)
	runtime := newCreateFailureFanoutRuntime()
	coordinator := newTestCoordinator(t, store, runtime)
	// 先冻结无副作用计划，再并发消费 RunPrepared；屏障只覆盖上传与创建阶段。
	prepared, err := coordinator.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	planned, err := buildRemoteExecutionShardSetForWorkloads(
		prepared.plan,
		prepared.catalog,
		prepared.reuse.cacheMisses,
		prepared.input,
	)
	if err != nil {
		t.Fatalf("buildRemoteExecutionShardSetForWorkloads() error = %v", err)
	}
	if len(planned.Shards) < 2 {
		t.Fatalf("planned shards = %d, want sibling fanout", len(planned.Shards))
	}
	store.expectedRequests = len(planned.Shards)
	return createFailureFanoutCase{
		input: input, planned: len(planned.Shards), store: store, runtime: runtime,
		coordinator: coordinator, prepared: prepared,
	}
}

func startCreateFailureFanoutRun(coordinator *Coordinator, prepared *PreparedRun) *createFailureFanoutRun {
	run := &createFailureFanoutRun{done: make(chan struct{})}
	run.group.Go(func() error {
		run.result, run.err = coordinator.RunPrepared(context.Background(), prepared)
		close(run.done)
		return nil
	})
	return run
}

func settleCreateFailureFanout(t *testing.T, testCase createFailureFanoutCase, run *createFailureFanoutRun) {
	t.Helper()
	waitCreateFailureSignal(t, testCase.store.allRequestsStarted, "sibling request uploads did not start")
	waitCreateFailureSignal(t, testCase.runtime.failureObserved, "injected create failure did not occur")
	releaseCreateFailureSiblings(t, testCase.store)
	waitCreateFailureSignal(t, run.done, "coordinator did not finish after sibling fanout settled")
	if err := run.group.Wait(); err != nil {
		t.Fatalf("fanout runner error = %v", err)
	}
}

func waitCreateFailureSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	if deadline, ok := t.Deadline(); ok {
		wait := time.Until(deadline)
		if wait <= 0 {
			t.Fatal(message)
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-signal:
			return
		case <-timer.C:
			t.Fatal(message)
		}
	}
	<-signal
}

func releaseCreateFailureSiblings(t *testing.T, store *createFailureFanoutStore) {
	t.Helper()
	select {
	case <-store.siblingUploadCancel:
		t.Fatal("sibling upload inherited create failure cancellation")
	default:
	}
	store.siblingReleaseOnce.Do(func() { close(store.siblingUploadRelease) })
}

func assertCreateFailureFanout(t *testing.T, testCase createFailureFanoutCase, run *createFailureFanoutRun) {
	t.Helper()
	select {
	case <-testCase.store.siblingUploadCancel:
		t.Fatal("sibling upload inherited create failure cancellation")
	default:
	}
	if run.err == nil || !strings.Contains(run.err.Error(), "InvalidParameter.ValueExceeded") {
		t.Fatalf("Run() error = %v, want provider create failure", run.err)
	}
	assertCreateFailureRuntime(t, testCase)
	assertCreateFailureResult(t, testCase.planned, run.result)
	assertCreateFailureLedger(t, testCase, run.result)
}

func assertCreateFailureRuntime(t *testing.T, testCase createFailureFanoutCase) {
	t.Helper()
	createCalls, cancelledCreates := testCase.runtime.fanoutCounts()
	if createCalls != testCase.planned {
		t.Fatalf("CreateContainerGroup calls = %d, want %d", createCalls, testCase.planned)
	}
	if cancelledCreates != 0 {
		t.Fatalf("CreateContainerGroup calls observed canceled context = %d", cancelledCreates)
	}
	if len(testCase.runtime.coordinatorRuntime.deletes) != testCase.planned-1 {
		t.Fatalf("cleanup deletes = %d, want %d successful groups", len(testCase.runtime.coordinatorRuntime.deletes), testCase.planned-1)
	}
}

func assertCreateFailureResult(t *testing.T, planned int, result RunResult) {
	t.Helper()
	unknown := assertResultShardGroups(t, result.Shards)
	if unknown != 1 || len(result.Shards) != planned {
		t.Fatalf("result shards = %d with unknown=%d, want %d shards and one unknown", len(result.Shards), unknown, planned)
	}
}

func assertResultShardGroups(t *testing.T, shards []ShardResult) int {
	t.Helper()
	unknown := 0
	for _, shard := range shards {
		if shard.ContainerStatus == "" || shard.ContainerStatus == "Unknown" {
			unknown++
			if shard.ContainerGroup != "" {
				t.Fatalf("unknown shard %q unexpectedly has group %q", shard.ShardIdentity, shard.ContainerGroup)
			}
			continue
		}
		if shard.ContainerGroup == "" {
			t.Fatalf("successful shard %q lost its created group", shard.ShardIdentity)
		}
	}
	return unknown
}

func assertCreateFailureLedger(t *testing.T, testCase createFailureFanoutCase, result RunResult) {
	t.Helper()
	record, err := testCase.input.LedgerStore.LoadRemoteCIRun(result.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() error = %v", err)
	}
	if record.Authoritative || !record.CleanupComplete || len(record.Shards) != testCase.planned {
		t.Fatalf("provisional run record = %+v", record)
	}
	unknown := assertLedgerShardGroups(t, record.Shards)
	if unknown != 1 || !strings.Contains(record.ErrorText, "InvalidParameter.ValueExceeded") {
		t.Fatalf("ledger shards/error = unknown=%d error=%q", unknown, record.ErrorText)
	}
}

func assertLedgerShardGroups(t *testing.T, shards []gate.RemoteCIShardRecord) int {
	t.Helper()
	unknown := 0
	for _, shard := range shards {
		if shard.ContainerStatus == "Unknown" {
			unknown++
			continue
		}
		if shard.ContainerGroup == "" {
			t.Fatalf("ledger shard %q lost created group", shard.ShardIdentity)
		}
	}
	return unknown
}
