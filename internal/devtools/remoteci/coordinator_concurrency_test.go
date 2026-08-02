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
	"golang.org/x/sync/errgroup"
)

type coordinatorOverlapBarrier struct {
	mu           sync.Mutex
	expectedCall int
	expectedJobs int
	calls        int
	jobs         map[string]struct{}
	started      chan struct{}
	release      chan struct{}
	startOnce    sync.Once
	releaseOnce  sync.Once
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
		barrier.startOnce.Do(func() { close(barrier.started) })
	}
	barrier.mu.Unlock()
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
	plannedSet := mustBuildRemoteExecutionShardSet(t, input)
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
		_, err := coordinator.Run(context.Background(), input)
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
			_, err := coordinator.Run(context.Background(), input)
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

func assertCoordinatorBarrierReached(t *testing.T, barrier *coordinatorOverlapBarrier, operation string) {
	t.Helper()
	select {
	case <-barrier.started:
	case <-time.After(time.Second):
		t.Fatalf("%s did not overlap", operation)
	}
}

func assertIndependentCoordinatorObjectPrefixes(t *testing.T, uploads []string, creates []eci.CreateRequest) {
	t.Helper()
	prefixes := map[string]bool{
		"baseline-artifacts/source-deltas/job-0123456789abcdef0123456a/": false,
		"baseline-artifacts/source-deltas/job-0123456789abcdef0123456b/": false,
	}
	temporary, _ := partitionCoordinatorUploads(uploads)
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
		if !strings.HasPrefix(requestKey, "baseline-artifacts/source-deltas/"+jobID+"/") {
			t.Fatalf("job=%q request key=%q", jobID, requestKey)
		}
	}
}
