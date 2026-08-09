package remoteci

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
)

// malformedEvidenceObserved 统计当前状态批次是否仍包含畸形分片。
func malformedEvidenceObserved(ids []string, malformedID string) int {
	if len(ids) == 0 || ids[0] != malformedID {
		return 0
	}
	return 1
}

// malformedTerminalFanoutRuntime 在 sibling 首轮仍为 Scheduling 时返回一个畸形终态，验证 waitShards 会排空整个扇出。
type malformedTerminalFanoutRuntime struct {
	*coordinatorRuntime
	mu             sync.Mutex
	calls          int
	malformedID    string
	malformedCalls int
}

func (runtime *malformedTerminalFanoutRuntime) DescribeContainerGroups(ctx context.Context, ids ...string) ([]eci.ContainerGroup, error) {
	runtime.mu.Lock()
	call := runtime.calls
	runtime.calls++
	if runtime.malformedID == "" && len(ids) != 0 {
		runtime.malformedID = ids[0]
	}
	malformedID := runtime.malformedID
	runtime.malformedCalls += malformedEvidenceObserved(ids, malformedID)
	runtime.mu.Unlock()

	groups, err := runtime.coordinatorRuntime.DescribeContainerGroups(ctx, ids...)
	if err != nil {
		return nil, err
	}
	for index := range groups {
		if groups[index].ID == malformedID {
			// 提供方终态响应属于权威证据但字段畸形：worker 没有 FinishTime，
			// 因此不得用本地时间戳替换它。
			groups[index].Status = "Succeeded"
			for containerIndex := range groups[index].Containers {
				if groups[index].Containers[containerIndex].Name == "worker" {
					groups[index].Containers[containerIndex].CurrentState.FinishTime = time.Time{}
				}
			}
			continue
		}
		if call == 0 {
			groups[index].Status = "Scheduling"
			groups[index].SucceededTime = time.Time{}
			groups[index].FailedTime = time.Time{}
		}
	}
	return groups, nil
}

func (runtime *malformedTerminalFanoutRuntime) statusCallCount() int {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.calls
}

// malformedEvidenceCallCount 返回畸形分片仍在终态重读集合中的次数。
func (runtime *malformedTerminalFanoutRuntime) malformedEvidenceCallCount() int {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.malformedCalls
}

// eventualTerminalEvidenceRuntime 模拟 ECI 终态先到、worker FinishTime 后到的最终一致性窗口。
type eventualTerminalEvidenceRuntime struct {
	*coordinatorRuntime
	mu        sync.Mutex
	calls     int
	delayedID string
}

func (runtime *eventualTerminalEvidenceRuntime) DescribeContainerGroups(ctx context.Context, ids ...string) ([]eci.ContainerGroup, error) {
	runtime.mu.Lock()
	call := runtime.calls
	runtime.calls++
	if runtime.delayedID == "" && len(ids) != 0 {
		runtime.delayedID = ids[0]
	}
	delayedID := runtime.delayedID
	runtime.mu.Unlock()

	groups, err := runtime.coordinatorRuntime.DescribeContainerGroups(ctx, ids...)
	if err != nil {
		return nil, err
	}
	if call == 0 {
		for index := range groups {
			if groups[index].ID != delayedID {
				continue
			}
			for containerIndex := range groups[index].Containers {
				if groups[index].Containers[containerIndex].Name == "worker" {
					groups[index].Containers[containerIndex].CurrentState.FinishTime = time.Time{}
				}
			}
		}
	}
	return groups, nil
}

func (runtime *eventualTerminalEvidenceRuntime) statusCallCount() int {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.calls
}

func TestCoordinatorRereadsTerminalWorkerFinishTimeBeforeSuccess(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	runtime := &eventualTerminalEvidenceRuntime{coordinatorRuntime: &coordinatorRuntime{}}
	coordinator := newTestCoordinator(t, &coordinatorStore{}, runtime)

	planned := mustBuildAllMissRemoteExecutionShardSet(t, input)
	result, err := runCoordinatorTest(t, coordinator, context.Background(), input)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if runtime.statusCallCount() < 2 {
		t.Fatalf("DescribeContainerGroups calls = %d, want terminal evidence reread", runtime.statusCallCount())
	}
	if len(result.Shards) != len(planned.Shards) || result.Shards[0].ECITerminalAt.IsZero() {
		t.Fatalf("terminal shard result = %#v", result.Shards)
	}
	wantTerminalAt := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC).Add(2 * time.Second)
	if !result.Shards[0].ECITerminalAt.Equal(wantTerminalAt) {
		t.Fatalf("terminal time = %s, want provider worker FinishTime %s", result.Shards[0].ECITerminalAt, wantTerminalAt)
	}
	if len(result.Shards[0].Report.Gates) == 0 {
		t.Fatal("terminal worker report was not collected after evidence reread")
	}
}

func TestCoordinatorDrainsSiblingTerminalEvidenceAfterMissingWorkerFinishTime(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	store := &coordinatorStore{}
	runtime := &malformedTerminalFanoutRuntime{coordinatorRuntime: &coordinatorRuntime{}}
	coordinator := newTestCoordinator(t, store, runtime)

	planned := mustBuildAllMissRemoteExecutionShardSet(t, input)
	if len(planned.Shards) < 2 {
		t.Fatalf("planned shards = %d, want siblings", len(planned.Shards))
	}
	result, err := runCoordinatorTest(t, coordinator, context.Background(), input)
	assertMalformedFanoutRunError(t, err)
	assertMalformedFanoutCleanup(t, result, runtime, len(planned.Shards))
	assertMalformedFanoutPolling(t, runtime)
	assertMalformedFanoutResults(t, result, len(planned.Shards))
}

// assertMalformedFanoutRunError 锁定畸形 worker 终态是本分片的基础设施失败。
func assertMalformedFanoutRunError(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "missing worker CurrentState.FinishTime") {
		t.Fatalf("Run() error = %v, want malformed worker terminal evidence", err)
	}
	if !strings.Contains(err.Error(), "worker log=") {
		t.Fatalf("Run() error = %v, want bounded worker diagnostic", err)
	}
}

// assertMalformedFanoutCleanup 验证畸形分片不会阻止所有已创建容器清理。
func assertMalformedFanoutCleanup(t *testing.T, result RunResult, runtime *malformedTerminalFanoutRuntime, planned int) {
	t.Helper()
	if !result.CleanupComplete {
		t.Fatal("Run() cleanup_complete = false, want all created groups cleaned")
	}
	if len(runtime.deletes) != planned {
		t.Fatalf("cleanup deletes = %d, want %d created groups", len(runtime.deletes), planned)
	}
}

// assertMalformedFanoutPolling 验证 Scheduling sibling 会在畸形终态之后继续轮询。
func assertMalformedFanoutPolling(t *testing.T, runtime *malformedTerminalFanoutRuntime) {
	t.Helper()
	if runtime.statusCallCount() < 2 {
		t.Fatalf("DescribeContainerGroups calls = %d, want a second poll for Scheduling siblings", runtime.statusCallCount())
	}
	if runtime.malformedEvidenceCallCount() > maxTerminalEvidenceRereads+1 {
		t.Fatalf("malformed shard status calls = %d, want bounded terminal rereads", runtime.malformedEvidenceCallCount())
	}
}

// assertMalformedFanoutResults 验证 sibling 的真实终态和报告被保留下来且畸形分片没有伪造时间。
func assertMalformedFanoutResults(t *testing.T, result RunResult, planned int) {
	t.Helper()
	if len(result.Shards) != planned {
		t.Fatalf("result shards = %d, want %d", len(result.Shards), planned)
	}
	if !result.Shards[0].ECITerminalAt.IsZero() {
		t.Fatalf("malformed shard received fabricated terminal time %s", result.Shards[0].ECITerminalAt)
	}
	for index, shard := range result.Shards[1:] {
		if shard.ContainerStatus != "Succeeded" || shard.ECIWaitStartedAt.IsZero() || shard.ECITerminalAt.IsZero() {
			t.Fatalf("sibling shard %d was not drained: %+v", index+1, shard)
		}
		if len(shard.Report.Gates) == 0 {
			t.Fatalf("sibling shard %d lost its terminal worker report", index+1)
		}
	}
}

func TestTerminalFanoutKeepsFailedTimeStrictlyProviderBound(t *testing.T) {
	createdAt := time.Date(2026, 8, 3, 4, 0, 0, 0, time.UTC)
	workerFinishedAt := createdAt.Add(2 * time.Second)
	zeroTime := time.Time{}
	for name, failedAt := range map[string]time.Time{
		"nonzero": createdAt.Add(time.Second),
		"missing": zeroTime,
	} {
		t.Run(name, func(t *testing.T) {
			result := ShardResult{}
			err := bindObservedECIShardTiming(&result, eci.ContainerGroup{
				Status: "Failed", CreationTime: createdAt, FailedTime: failedAt,
				InitContainers: []eci.ContainerStatus{{Name: "materializer", CurrentState: eci.ContainerState{StartTime: createdAt.Add(time.Millisecond)}}},
				Containers:     []eci.ContainerStatus{{Name: "worker", CurrentState: eci.ContainerState{FinishTime: workerFinishedAt}}},
			})
			if name == "missing" {
				if err == nil {
					t.Fatal("missing provider FailedTime unexpectedly accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("bindObservedECIShardTiming() error = %v", err)
			}
			if !result.ECITerminalAt.Equal(workerFinishedAt) {
				t.Fatalf("terminal time = %s, want worker provider FinishTime %s", result.ECITerminalAt, workerFinishedAt)
			}
		})
	}
}

var _ Runtime = (*malformedTerminalFanoutRuntime)(nil)
var _ Runtime = (*eventualTerminalEvidenceRuntime)(nil)
