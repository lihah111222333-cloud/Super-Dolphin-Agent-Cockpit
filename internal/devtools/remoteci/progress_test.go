package remoteci

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type progressCollector struct {
	mu     sync.Mutex
	events []ProgressEvent
}

func (collector *progressCollector) ObserveRemoteCIProgress(event ProgressEvent) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.events = append(collector.events, event)
}

func (collector *progressCollector) last() ProgressEvent {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return collector.events[len(collector.events)-1]
}

func (collector *progressCollector) snapshot() []ProgressEvent {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return append([]ProgressEvent(nil), collector.events...)
}

// TestJSONProgressObserverWritesMachineReadableSideChannel 验证旁路事件为安全 NDJSON。
func TestJSONProgressObserverWritesMachineReadableSideChannel(t *testing.T) {
	var output bytes.Buffer
	observer := NewJSONProgressObserver(&output)
	observer.ObserveRemoteCIProgress(ProgressEvent{Phase: ProgressPhaseRun, State: "updated", TotalShards: 2})
	var decoded ProgressEvent
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &decoded); err != nil {
		t.Fatalf("progress NDJSON is invalid: %v", err)
	}
	if decoded.SchemaVersion != ProgressEventSchemaVersion || decoded.Kind != "remote_ci_progress" || decoded.TotalShards != 2 {
		t.Fatalf("decoded progress event = %#v", decoded)
	}
	if strings.Contains(output.String(), "token") || strings.Contains(output.String(), "secret") {
		t.Fatalf("progress event leaked secret-like text: %q", output.String())
	}
}

// TestProgressTrackerReportsShardCacheAndTimingCounters 验证阶段、计数、耗时和心跳。
func TestProgressTrackerReportsShardCacheAndTimingCounters(t *testing.T) {
	collector, tracker, clockTime := progressTrackerFixture()
	tracker.setCacheCounts(2, 3, 2)
	tracker.setTotal(2)
	tracker.phase(ProgressPhasePrepare, "started")
	*clockTime = clockTime.Add(37 * time.Millisecond)
	tracker.markCreated(eci.ContainerGroup{ID: "group-a", Status: "Running"})
	tracker.markCreated(eci.ContainerGroup{ID: "group-b", Status: "Running"})
	tracker.observeGroups([]eci.ContainerGroup{{ID: "group-a", Status: "Succeeded"}, {ID: "group-b", Status: "Failed"}})
	compile, testBody := int64(11), int64(19)
	tracker.emit(ProgressPhaseTerminal, "completed", &compile, &testBody)
	tracker.beginCleanup(2)
	tracker.markCleanup(true)
	tracker.markCleanup(false)
	events := collector.snapshot()
	terminal, cleanup := progressPhaseEvents(t, events)
	assertProgressSequence(t, events)
	assertProgressTerminal(t, terminal)
	assertProgressCleanup(t, cleanup)
	beforeHeartbeat := len(events)
	*clockTime = clockTime.Add(9 * time.Second)
	tracker.observeGroups([]eci.ContainerGroup{{ID: "group-a", Status: "Succeeded"}, {ID: "group-b", Status: "Failed"}})
	assertNoProgressHeartbeat(t, collector.snapshot(), beforeHeartbeat)
	*clockTime = clockTime.Add(time.Second)
	tracker.observeGroups([]eci.ContainerGroup{{ID: "group-a", Status: "Succeeded"}, {ID: "group-b", Status: "Failed"}})
	assertProgressHeartbeat(t, collector.last())
}

// TestProgressTrackerLifecyclePhasesStayInOrder 锁定成功与失败旁路阶段不可逆序。
func TestProgressTrackerLifecyclePhasesStayInOrder(t *testing.T) {
	tests := []struct {
		name       string
		finish     func(*progressTracker)
		requireAll bool
	}{
		{
			name: "completed",
			finish: func(tracker *progressTracker) {
				tracker.phase(ProgressPhasePrepare, "started")
				tracker.phase(ProgressPhaseUpload, "completed")
				tracker.phase(ProgressPhaseCreate, "completed")
				tracker.runFinished(nil, nil)
				tracker.beginCleanup(0)
				tracker.cleanupFinished(nil)
				tracker.emitFinal(RunResult{Status: gate.ResultStatusPassed})
			},
			requireAll: true,
		},
		{
			name: "failed-with-missing-prefix",
			finish: func(tracker *progressTracker) {
				tracker.phase(ProgressPhasePrepare, "started")
				tracker.phase(ProgressPhaseCreate, progressFailureState)
				tracker.runFinished(nil, errors.New("run failed"))
				tracker.beginCleanup(0)
				tracker.cleanupFinished(errors.New("cleanup failed"))
				tracker.emitFinal(RunResult{Status: gate.ResultStatusFailed})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			collector, tracker, _ := progressTrackerFixture()
			test.finish(tracker)
			events := collector.snapshot()
			assertProgressSequence(t, events)
			assertProgressLifecycleOrder(t, events)
			if test.requireAll {
				assertProgressCompleteLifecycle(t, events)
			}
		})
	}
}

// assertProgressCompleteLifecycle 验证成功路径覆盖所有约定阶段。
func assertProgressCompleteLifecycle(t *testing.T, events []ProgressEvent) {
	t.Helper()
	want := []ProgressPhase{
		ProgressPhasePrepare, ProgressPhaseUpload, ProgressPhaseCreate, ProgressPhaseRun,
		ProgressPhaseTerminal, ProgressPhaseCleanup, ProgressPhaseComplete,
	}
	index := 0
	for _, event := range events {
		if event.Phase == want[index] {
			index++
			if index == len(want) {
				return
			}
		}
	}
	t.Fatalf("completed lifecycle phases = %#v, want %v", events, want)
}

// assertProgressLifecycleOrder 验证阶段可省略但不能回退到更早阶段。
func assertProgressLifecycleOrder(t *testing.T, events []ProgressEvent) {
	t.Helper()
	order := []ProgressPhase{
		ProgressPhasePrepare, ProgressPhaseUpload, ProgressPhaseCreate, ProgressPhaseRun,
		ProgressPhaseTerminal, ProgressPhaseCleanup, ProgressPhaseComplete,
	}
	ranks := make(map[ProgressPhase]int, len(order))
	for rank, phase := range order {
		ranks[phase] = rank
	}
	lastRank := -1
	seen := make(map[ProgressPhase]bool, len(order))
	for _, event := range events {
		rank, ok := ranks[event.Phase]
		if !ok {
			t.Fatalf("unknown progress phase %q", event.Phase)
		}
		if rank < lastRank {
			t.Fatalf("progress phase order regressed from rank %d to %d: %#v", lastRank, rank, events)
		}
		lastRank, seen[event.Phase] = rank, true
	}
	if !seen[ProgressPhaseRun] || !seen[ProgressPhaseTerminal] || !seen[ProgressPhaseCleanup] || !seen[ProgressPhaseComplete] {
		t.Fatalf("lifecycle terminal phases missing: %#v", events)
	}
}

// progressTrackerFixture 构造带虚拟时钟的旁路聚合器测试夹具。
func progressTrackerFixture() (*progressCollector, *progressTracker, *time.Time) {
	collector := &progressCollector{}
	clockTime := time.Unix(100, 0)
	tracker := newProgressTracker(collector, func() time.Time { return clockTime })
	return collector, tracker, &clockTime
}

// progressPhaseEvents 提取并校验终态和清理阶段事件。
func progressPhaseEvents(t *testing.T, events []ProgressEvent) (ProgressEvent, ProgressEvent) {
	t.Helper()
	var terminal, cleanup ProgressEvent
	for _, event := range events {
		if event.Phase == ProgressPhaseTerminal {
			terminal = event
		}
		if event.Phase == ProgressPhaseCleanup {
			cleanup = event
		}
	}
	if terminal.Phase == "" || cleanup.Phase == "" {
		t.Fatalf("progress phases = %#v", events)
	}
	return terminal, cleanup
}

// assertProgressSequence 验证旁路事件 sequence 连续且无重排。
func assertProgressSequence(t *testing.T, events []ProgressEvent) {
	t.Helper()
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("progress sequence[%d] = %d, want %d", index, event.Sequence, index+1)
		}
	}
}

// assertProgressTerminal 验证终态分片计数和旁路耗时字段。
func assertProgressTerminal(t *testing.T, event ProgressEvent) {
	t.Helper()
	if event.TotalShards != 2 || event.CompletedShards != 1 || event.FailedShards != 1 {
		t.Fatalf("terminal shard counters = %#v", event)
	}
	assertProgressCacheCounters(t, event)
	assertProgressTiming(t, event)
}

// assertProgressCacheCounters 验证命中、未命中和复用计数。
func assertProgressCacheCounters(t *testing.T, event ProgressEvent) {
	t.Helper()
	if event.CacheHits != 2 || event.CacheMisses != 3 || event.CacheReused != 2 {
		t.Fatalf("cache counters = %#v", event)
	}
}

// assertProgressTiming 验证编译和测试耗时只暴露已测量值。
func assertProgressTiming(t *testing.T, event ProgressEvent) {
	t.Helper()
	if event.CompileTimingMS == nil || *event.CompileTimingMS != 11 || event.TestTimingMS == nil || *event.TestTimingMS != 19 {
		t.Fatalf("timing fields = %#v", event)
	}
	if event.ElapsedMS < 37 {
		t.Fatalf("elapsed_ms = %d, want at least 37", event.ElapsedMS)
	}
}

// assertProgressCleanup 验证清理总数、完成数和失败数。
func assertProgressCleanup(t *testing.T, event ProgressEvent) {
	t.Helper()
	if event.CleanupTotal != 2 || event.CleanupComplete != 2 || event.CleanupFailed != 1 {
		t.Fatalf("cleanup counters = %#v", event)
	}
}

// assertNoProgressHeartbeat 验证十秒窗口前不重复发出心跳。
func assertNoProgressHeartbeat(t *testing.T, events []ProgressEvent, want int) {
	t.Helper()
	if len(events) != want {
		t.Fatalf("progress emitted before heartbeat interval: got %d, want %d", len(events), want)
	}
}

// assertProgressHeartbeat 验证十秒窗口后发出 heartbeat 状态。
func assertProgressHeartbeat(t *testing.T, event ProgressEvent) {
	t.Helper()
	if event.State != "heartbeat" || event.Phase != ProgressPhaseRun {
		t.Fatalf("heartbeat event = %#v", event)
	}
}

// TestRemoteProgressTimingsOnlyExposeMeasuredReportPhases 验证未测量阶段不会伪造耗时。
func TestRemoteProgressTimingsOnlyExposeMeasuredReportPhases(t *testing.T) {
	shards := []ShardResult{{Report: gate.PlanExecutionReport{
		Gates:                  []gate.PlanGateExecution{{ExecutionProfile: gate.ExecutionProfile{TestBodyMS: 7}}},
		CompileGroupExecutions: []gate.CompileGroupExecution{{DurationMS: 5, Phase: cicontract.TimingTestBinaryCompile}},
	}}}
	compile, testBody := remoteProgressTimings(shards)
	if compile == nil || *compile != 5 || testBody == nil || *testBody != 7 {
		t.Fatalf("timings = %v/%v", compile, testBody)
	}
	compile, testBody = remoteProgressTimings([]ShardResult{{}})
	if compile != nil || testBody != nil {
		t.Fatalf("unmeasured timings = %v/%v", compile, testBody)
	}
}
