package remoteci

import (
	"context"
	"fmt"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"golang.org/x/sync/errgroup"
)

type coordinatorOverlapBarrier struct {
	mu             sync.Mutex
	expectedCall   int
	expectedJobs   int
	releaseOnStart bool
	calls          int
	jobs           map[string]struct{}
	started        chan struct{}
	release        chan struct{}
	startOnce      sync.Once
	releaseOnce    sync.Once
}

// newCoordinatorOverlapBarrierReleaseOnStart 创建只记录跨 job 到达、不会阻塞清理上下文的测试屏障。
func newCoordinatorOverlapBarrierReleaseOnStart(expectedCall int, expectedJobs int) *coordinatorOverlapBarrier {
	barrier := newCoordinatorOverlapBarrier(expectedCall, expectedJobs)
	barrier.releaseOnStart = true
	return barrier
}

func newCoordinatorOverlapBarrier(expectedCall int, expectedJobs int) *coordinatorOverlapBarrier {
	return &coordinatorOverlapBarrier{
		expectedCall: expectedCall, expectedJobs: expectedJobs, jobs: make(map[string]struct{}),
		started: make(chan struct{}), release: make(chan struct{}),
	}
}

func (barrier *coordinatorOverlapBarrier) wait(ctx context.Context, jobID string) error {
	barrier.mu.Lock()
	barrier.calls++
	barrier.jobs[jobID] = struct{}{}
	if barrier.calls >= barrier.expectedCall && len(barrier.jobs) >= barrier.expectedJobs {
		barrier.startOnce.Do(func() {
			close(barrier.started)
			if barrier.releaseOnStart {
				barrier.releaseOnce.Do(func() { close(barrier.release) })
			}
		})
	}
	barrier.mu.Unlock()
	if barrier.releaseOnStart {
		// 该屏障只观测跨 job 的并发到达；不得让一个 job 的有界清理
		// 上下文等待另一个 job，否则调度延迟会被误化为 ECI 删除失败。
		return nil
	}
	select {
	case <-barrier.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (barrier *coordinatorOverlapBarrier) unblock() {
	barrier.releaseOnce.Do(func() { close(barrier.release) })
}

type jobScopedCleanupRuntime struct {
	*coordinatorRuntime
	mu       sync.Mutex
	groupJob map[string]string
	barrier  *coordinatorOverlapBarrier
}

func (runtime *jobScopedCleanupRuntime) CreateContainerGroup(ctx context.Context, request eci.CreateRequest) (eci.ContainerGroup, error) {
	group, err := runtime.coordinatorRuntime.CreateContainerGroup(ctx, request)
	if err != nil {
		return eci.ContainerGroup{}, err
	}
	runtime.mu.Lock()
	if runtime.groupJob == nil {
		runtime.groupJob = make(map[string]string)
	}
	runtime.groupJob[group.ID] = request.Tags["super-dolphin-job"]
	runtime.mu.Unlock()
	return group, nil
}

func (runtime *jobScopedCleanupRuntime) DeleteContainerGroup(ctx context.Context, groupID string) error {
	runtime.mu.Lock()
	jobID := runtime.groupJob[groupID]
	runtime.mu.Unlock()
	if jobID == "" {
		return fmt.Errorf("cleanup group %q has no owning job", groupID)
	}
	if err := runtime.barrier.wait(ctx, jobID); err != nil {
		return err
	}
	return runtime.coordinatorRuntime.DeleteContainerGroup(ctx, groupID)
}

func TestCoordinatorCleanupStartsAllECIDeletesWithoutCPUBatchLimit(t *testing.T) {
	count := goruntime.GOMAXPROCS(0) + 1
	barrier := newCoordinatorOverlapBarrier(count, count)
	runtime := &coordinatorRuntime{deleteBarrier: barrier}
	coordinator := newTestCoordinator(t, &coordinatorStore{}, runtime)
	groupIDs := make([]string, count)
	for index := range groupIDs {
		groupIDs[index] = fmt.Sprintf("eci-cleanup-%03d", index)
	}
	var cleanup errgroup.Group
	cleanup.Go(func() error {
		return coordinator.cleanup("job-0123456789abcdef01234567", groupIDs, nil)
	})
	select {
	case <-barrier.started:
	case <-time.After(time.Second):
		barrier.unblock()
		t.Fatal("ECI cleanup retained a CPU-sized batch limit")
	}
	barrier.unblock()
	if err := cleanup.Wait(); err != nil {
		t.Fatalf("cleanup() error = %v", err)
	}
}

func TestCoordinatorRunConcurrentlyUploadsAndCreatesCacheMissShards(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	plannedSet := mustBuildAllMissRemoteExecutionShardSet(t, input)
	if len(plannedSet.Shards) <= 1 {
		t.Fatalf("planned shards=%d, want concurrent shards", len(plannedSet.Shards))
	}
	uploadBarrier := newCoordinatorOverlapBarrier(len(plannedSet.Shards), 1)
	createBarrier := newCoordinatorOverlapBarrier(len(plannedSet.Shards), 1)
	defer uploadBarrier.unblock()
	defer createBarrier.unblock()
	store := &coordinatorStore{uploadBarrier: uploadBarrier}
	runtime := &coordinatorRuntime{createBarrier: createBarrier}
	coordinator := newTestCoordinator(t, store, runtime)
	var runs errgroup.Group
	runs.Go(func() error {
		_, err := runCoordinatorTest(t, coordinator, context.Background(), input)
		return err
	})
	assertCoordinatorBarrierReached(t, uploadBarrier, "shard request uploads")
	uploadBarrier.unblock()
	assertCoordinatorBarrierReached(t, createBarrier, "ECI creates")
	createBarrier.unblock()
	if err := runs.Wait(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestIndependentCoordinatorRunsOverlapAndKeepJobObjectPrefixesSeparate(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	store := &coordinatorStore{uploadBarrier: newCoordinatorOverlapBarrier(2, 2)}
	runtime := &coordinatorRuntime{createBarrier: newCoordinatorOverlapBarrier(2, 2)}
	defer store.uploadBarrier.unblock()
	defer runtime.createBarrier.unblock()
	first := newTestCoordinator(t, store, runtime)
	second := newTestCoordinator(t, store, runtime)
	first.newID = func() (string, error) { return "job-0123456789abcdef0123456a", nil }
	second.newID = func() (string, error) { return "job-0123456789abcdef0123456b", nil }
	var runs errgroup.Group
	for _, coordinator := range []*Coordinator{first, second} {
		runs.Go(func() error {
			_, err := runCoordinatorTest(t, coordinator, context.Background(), input)
			return err
		})
	}
	assertCoordinatorBarrierReached(t, store.uploadBarrier, "cross-job shard request uploads")
	store.uploadBarrier.unblock()
	assertCoordinatorBarrierReached(t, runtime.createBarrier, "cross-job ECI creates")
	runtime.createBarrier.unblock()
	if err := runs.Wait(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	assertIndependentCoordinatorObjectPrefixes(t, store.uploads, runtime.creates)
}

// TestIndependentCoordinatorRunsKeepCleanupAndAgentTokensJobScoped 验证普通分片执行
// 没有共享清理注册表或 agent 身份状态。此路径刻意不含刷新租约：
// 只有独立 SQLite owner 可以串行刷新，绝不能串行普通 job 或分片。
func TestIndependentCoordinatorRunsKeepCleanupAndAgentTokensJobScoped(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	store := &coordinatorStore{}
	// 清理拥有真实的有界上下文；两个 job 进入屏障后立即放行测试会合，避免
	// 测试调度延迟耗尽该上下文，把 fixture 误化为合成的清理超时。
	cleanupBarrier := newCoordinatorOverlapBarrierReleaseOnStart(2, 2)
	runtime := &jobScopedCleanupRuntime{coordinatorRuntime: &coordinatorRuntime{}, barrier: cleanupBarrier}
	defer cleanupBarrier.unblock()
	first := newTestCoordinator(t, store, runtime)
	second := newTestCoordinator(t, store, runtime)
	first.newID = func() (string, error) { return "job-0123456789abcdef0123456a", nil }
	second.newID = func() (string, error) { return "job-0123456789abcdef0123456b", nil }
	firstInput := input
	secondInput := input
	firstInput.AgentTokenDigest = "sha256:" + strings.Repeat("a", 64)
	secondInput.AgentTokenDigest = "sha256:" + strings.Repeat("b", 64)
	// 准备阶段无副作用，但包含源码指纹和账本工作，在 -race 下可能超过短屏障的等待时间。
	// 先冻结两份计划，再启动有副作用的执行，使屏障只观测本测试关注的并发阶段。
	firstPrepared, err := first.Prepare(context.Background(), firstInput)
	if err != nil {
		t.Fatalf("first Prepare() error = %v", err)
	}
	secondPrepared, err := second.Prepare(context.Background(), secondInput)
	if err != nil {
		t.Fatalf("second Prepare() error = %v", err)
	}
	var runs errgroup.Group
	runs.Go(func() error {
		_, err := first.RunPrepared(context.Background(), firstPrepared)
		return err
	})
	runs.Go(func() error {
		_, err := second.RunPrepared(context.Background(), secondPrepared)
		return err
	})
	assertCoordinatorBarrierReached(t, cleanupBarrier, "cross-job ECI cleanup")
	cleanupBarrier.unblock()
	if err := runs.Wait(); err != nil {
		t.Fatalf("concurrent Run() error = %v", err)
	}
	assertCoordinatorBarrierCoverage(t, cleanupBarrier, "cross-job ECI cleanup")
	wantPrefixes := map[string]bool{
		"baseline-artifacts/source-bundles/job-0123456789abcdef0123456a/": false,
		"baseline-artifacts/source-bundles/job-0123456789abcdef0123456b/": false,
	}
	for _, prefix := range store.deletePrefixes {
		if _, ok := wantPrefixes[prefix]; !ok {
			t.Fatalf("cleanup prefix %q is not job-scoped", prefix)
		}
		wantPrefixes[prefix] = true
	}
	for prefix, found := range wantPrefixes {
		if !found {
			t.Fatalf("missing independent cleanup prefix %q in %v", prefix, store.deletePrefixes)
		}
	}
	wantTokens := map[string]string{
		"job-0123456789abcdef0123456a": firstInput.AgentTokenDigest,
		"job-0123456789abcdef0123456b": secondInput.AgentTokenDigest,
	}
	for _, request := range runtime.creates {
		jobID := request.Tags["super-dolphin-job"]
		if got, want := request.Environment[gate.ExecutorAgentTokenDigestEnvironment], wantTokens[jobID]; got != want {
			t.Fatalf("job %q agent token digest = %q, want %q", jobID, got, want)
		}
	}
}

func assertCoordinatorBarrierCoverage(t *testing.T, barrier *coordinatorOverlapBarrier, operation string) {
	t.Helper()
	barrier.mu.Lock()
	defer barrier.mu.Unlock()
	if barrier.calls < barrier.expectedCall || len(barrier.jobs) < barrier.expectedJobs {
		t.Fatalf("%s observed calls=%d jobs=%d, want at least calls=%d jobs=%d", operation, barrier.calls, len(barrier.jobs), barrier.expectedCall, barrier.expectedJobs)
	}
}

func assertCoordinatorBarrierReached(t *testing.T, barrier *coordinatorOverlapBarrier, operation string) {
	t.Helper()
	if deadline, ok := t.Deadline(); ok {
		wait := time.Until(deadline)
		if wait <= 0 {
			t.Fatalf("%s did not overlap before the test deadline", operation)
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-barrier.started:
			return
		case <-timer.C:
			t.Fatalf("%s did not overlap before the test deadline", operation)
		}
	}
	<-barrier.started
}

func assertIndependentCoordinatorObjectPrefixes(t *testing.T, uploads []string, creates []eci.CreateRequest) {
	t.Helper()
	prefixes := map[string]bool{
		"baseline-artifacts/source-bundles/job-0123456789abcdef0123456a/": false,
		"baseline-artifacts/source-bundles/job-0123456789abcdef0123456b/": false,
	}
	temporary := coordinatorTemporaryUploads(uploads)
	for _, key := range temporary {
		matched := false
		for prefix := range prefixes {
			if strings.HasPrefix(key, prefix) {
				prefixes[prefix] = true
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("temporary object key = %q", key)
		}
	}
	for prefix, found := range prefixes {
		if !found {
			t.Fatalf("missing temporary object prefix %q in %v", prefix, temporary)
		}
	}
	for _, request := range creates {
		jobID := request.Tags["super-dolphin-job"]
		requestKey := request.InitContainer.Environment["SUPER_DOLPHIN_REMOTE_REQUEST_KEY"]
		if !strings.HasPrefix(requestKey, "baseline-artifacts/source-bundles/"+jobID+"/") {
			t.Fatalf("job=%q request key=%q", jobID, requestKey)
		}
	}
}
