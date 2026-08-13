package remoteci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
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

// TestJSONProgressObserverWritesReuseDecisionDiagnostic 验证直接查询、两类重放、
// 重放后 MISS 与包原子降级可以独立交叉观测。
func TestJSONProgressObserverWritesReuseDecisionDiagnostic(t *testing.T) {
	var output bytes.Buffer
	observer := NewJSONProgressObserver(&output)
	replay := ReuseReplayDiagnostic{
		DirectSameTreeCompileGroups: 52,
		SourceCandidateWorkloads:    11, SourceCandidates: 12, SourceCandidateTrees: 28, SourceCandidateEvaluations: 38,
		SourceInputUnavailable: 13, SourceInputMismatch: 14,
		SourceSingleVoteRecovered: 23, SourceDeclarationMissVotes: 24,
		SourceRuntimeMissVotes: 25, SourceCompileMissVotes: 26,
		SourceCompileObligations: 29, SourceCompileCoveredRecoveries: 30, SourceAlgorithmCompatibleRecoveries: 39, SourceConfirmedMisses: 27,
		EnvironmentHintWorkloads: 15, EnvironmentHints: 16,
		EnvironmentGenerationMismatch: 17, EnvironmentTargetUnavailable: 18,
		EnvironmentSourceUnavailable: 19, EnvironmentHistoricalMismatch: 20,
		EnvironmentCurrentWorkerMismatch: 21, EnvironmentInputMismatch: 22,
		EnvironmentSingleVoteRecovered: 45, EnvironmentDeclarationMissVotes: 46,
		EnvironmentRuntimeMissVotes: 47, EnvironmentCompileMissVotes: 48,
		EnvironmentCompileOwners: 49, EnvironmentCompileCoveredRecoveries: 50,
		EnvironmentConfirmedMisses:          51,
		EnvironmentAlgorithmCompatibleTrees: 41, EnvironmentInputPrewarmSkipped: 42,
		CacheSnapshotComputations: 31, CacheSnapshotLoads: 32, CacheInputComputations: 33,
		CacheCompileComputations: 34, CacheSemanticComputations: 35, CacheEnvironmentComputations: 36,
		CacheWorkerComputations: 37, CacheAlgorithmComputations: 40,
		CachePersistentInputHits: 43, CachePersistentInputWrites: 44,
	}
	observer.ObserveRemoteCIReuseDiagnostic(ReuseDiagnostic{
		MissConfirmationThreshold: 2,
		DirectHits:                280, SourceReplayHits: 2900, EnvironmentReplayHits: 33,
		ExactHits: 3213, DirectMisses: 2938, RecoveredDirectMisses: 2933,
		ReplayMisses: 5, AtomicDemoted: 2927,
		EffectiveHits: 286, EffectiveMisses: 2932,
		Replay: replay,
		MissGroups: []ReuseDiagnosticGroup{{
			TargetKind: "go-test", TargetGroup: "./internal/devtools/gate",
			ExactHits: 100, DirectMisses: 2, AtomicDemoted: 98,
			EffectiveMisses: 100,
		}},
	})
	var decoded ReuseDiagnostic
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &decoded); err != nil {
		t.Fatalf("reuse diagnostic NDJSON is invalid: %v", err)
	}
	expected := ReuseDiagnostic{
		SchemaVersion: ReuseDiagnosticSchemaVersion, Kind: "remote_ci_reuse_diagnostic",
		MissConfirmationThreshold: 2,
		DirectHits:                280, SourceReplayHits: 2900, EnvironmentReplayHits: 33,
		ExactHits: 3213, DirectMisses: 2938, RecoveredDirectMisses: 2933,
		ReplayMisses: 5, AtomicDemoted: 2927,
		EffectiveHits: 286, EffectiveMisses: 2932,
		Replay: replay,
		MissGroups: []ReuseDiagnosticGroup{{
			TargetKind: "go-test", TargetGroup: "./internal/devtools/gate",
			ExactHits: 100, DirectMisses: 2, AtomicDemoted: 98,
			EffectiveMisses: 100,
		}},
	}
	if !reflect.DeepEqual(decoded, expected) {
		t.Fatalf("decoded reuse diagnostic = %#v", decoded)
	}
}

// TestJSONProgressObserverWritesShardPlanDiagnostic 验证分片数量、workload 密度与估时范围可交叉观测。
func TestJSONProgressObserverWritesShardPlanDiagnostic(t *testing.T) {
	var output bytes.Buffer
	observer := NewJSONProgressObserver(&output)
	plan := gate.WorkloadExecutionPlan{Context: gate.PlanningContext{Calibration: true, TargetDurationMS: 100_000}, Shards: []gate.ShardPlan{
		{Workloads: make([]gate.PlannedWorkload, 18), EstimatedDurationMS: 99_000},
		{Workloads: make([]gate.PlannedWorkload, 2), EstimatedDurationMS: 120_000},
	}}
	observer.ObserveRemoteCIShardPlanDiagnostic(newShardPlanDiagnostic(plan))
	var decoded ShardPlanDiagnostic
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &decoded); err != nil {
		t.Fatal(err)
	}
	assertShardPlanDiagnostic(t, decoded)
}

func assertShardPlanDiagnostic(t *testing.T, decoded ShardPlanDiagnostic) {
	t.Helper()
	want := ShardPlanDiagnostic{SchemaVersion: ShardPlanDiagnosticSchemaVersion, Kind: "remote_ci_shard_plan_diagnostic", Calibration: true, TargetDurationMS: 100_000, TotalShards: 2, TotalWorkloads: 20, MinWorkloadsPerShard: 2, MaxWorkloadsPerShard: 18, MinEstimatedShardDurationMS: 99_000, MaxEstimatedShardDurationMS: 120_000, OverTargetEstimatedShardCount: 1}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("decoded shard-plan diagnostic = %#v, want %#v", decoded, want)
	}
}

// TestCoordinatorPrepareReportsInternalStages 验证长耗时 Prepare 不再只有首尾日志，
// 每个安全阶段都携带累计 elapsed_ms 供外部定位瓶颈。
func TestCoordinatorPrepareReportsInternalStages(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	collector := &progressCollector{}
	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	coordinator.progress = newProgressTracker(collector, coordinator.now)
	if _, err := coordinator.Prepare(context.Background(), input); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	states := make([]string, 0)
	for _, event := range collector.snapshot() {
		if event.Phase == ProgressPhasePrepare {
			states = append(states, event.State)
		}
	}
	want := []string{
		"started",
		"input_validated",
		"plan_built",
		"identity_started",
		"identity_completed",
		"reuse_started",
		"reuse_direct_lookup_started",
		"reuse_direct_lookup_completed",
		"reuse_compile_coverage_seed_started",
		"reuse_compile_coverage_seed_completed",
		"reuse_source_candidate_query_started",
		"reuse_source_candidate_query_completed",
		"reuse_source_rank_started",
		"reuse_source_rank_completed",
		"reuse_source_input_cache_started",
		"reuse_source_input_cache_completed",
		"reuse_source_vote_started",
		"reuse_source_vote_completed",
		"reuse_environment_replay_started",
		"reuse_environment_hint_query_started",
		"reuse_environment_hint_query_completed",
		"reuse_environment_filter_started",
		"reuse_environment_filter_completed",
		"reuse_environment_tree_partitions_started",
		"reuse_environment_preferred_partition_started",
		"reuse_environment_preferred_partition_completed",
		"reuse_environment_remaining_partition_started",
		"reuse_environment_remaining_partition_completed",
		"reuse_environment_tree_partitions_completed",
		"reuse_environment_authorization_started",
		"reuse_environment_authorization_completed",
		"reuse_environment_replay_completed",
		"reuse_input_cache_persist_started",
		"reuse_input_cache_persist_completed",
		"reuse_outcome_projection_started",
		"reuse_outcome_projection_completed",
		"reuse_completed",
		"compile_inputs_started",
		"compile_inputs_completed",
		"scope_built",
		"completed",
	}
	if !reflect.DeepEqual(states, want) {
		t.Fatalf("prepare progress states = %v, want %v", states, want)
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
