package remoteci

import (
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRemoteTimingObservationsProjectsSixRealShardPhasesWithoutSummingConcurrentWorkloads(t *testing.T) {
	started := time.Date(2026, 8, 3, 4, 0, 0, 0, time.UTC)
	identity := "sha256:" + strings.Repeat("a", 64)
	profile := gate.ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: "miss", CacheMeasurement: "measured", StartupMS: 5, TestBodyMS: 10, TotalMS: 20}
	result := RunResult{
		JobID: "job-shard-timing", StartedAt: started, CompletedAt: started.Add(40 * time.Millisecond),
		Shards: []ShardResult{{
			ShardIdentity: identity, ExecutedWorkloads: []gate.GateID{"guard:one", "guard:two"},
			ECIWaitStartedAt: started, ECIWaitCompletedAt: started.Add(5 * time.Millisecond), ECITerminalAt: started.Add(40 * time.Millisecond),
			MaterializationTiming: gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: identity,
				Source:           gate.MaterializationPhaseTiming{StartedAtUnixMS: started.Add(time.Millisecond).UnixMilli(), CompletedAtUnixMS: started.Add(2 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
				CandidateCompile: gate.MaterializationPhaseTiming{StartedAtUnixMS: started.Add(3 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: started.Add(4 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
			},
		}},
		WorkloadExecutions: []gate.PlanGateExecution{
			{GateID: "guard:one", ShardIdentity: identity, StartedAt: started, CompletedAt: started.Add(20 * time.Millisecond), ExecutionProfile: profile},
			{GateID: "guard:two", ShardIdentity: identity, StartedAt: started.Add(10 * time.Millisecond), CompletedAt: started.Add(30 * time.Millisecond), ExecutionProfile: profile},
		},
	}
	observations, err := remoteTimingObservations(result)
	if err != nil {
		t.Fatal(err)
	}
	shard := make(map[cicontract.TimingPhase]gate.TimingObservation)
	var run gate.TimingObservation
	for _, observation := range observations {
		if observation.Scope == cicontract.TimingScopeShard {
			shard[observation.Phase] = observation
		}
		if observation.Scope == cicontract.TimingScopeRun && observation.Phase == cicontract.TimingTotal {
			run = observation
		}
	}
	if len(shard) != len(cicontract.TimingPhases()) {
		t.Fatalf("shard phase count = %d, want %d", len(shard), len(cicontract.TimingPhases()))
	}
	if got := shard[cicontract.TimingStartup]; !got.StartedAt.Equal(started) || !got.CompletedAt.Equal(started.Add(15*time.Millisecond)) || got.DurationMS != 10 || got.Aggregation != cicontract.TimingAggregationIntervalUnion {
		t.Fatalf("startup shard interval = %#v", got)
	}
	if got := shard[cicontract.TimingTestBody]; !got.StartedAt.Equal(started.Add(10*time.Millisecond)) || !got.CompletedAt.Equal(started.Add(30*time.Millisecond)) || got.DurationMS != 20 || got.Aggregation != cicontract.TimingAggregationIntervalUnion {
		t.Fatalf("test_body shard interval = %#v", got)
	}
	if got := shard[cicontract.TimingTotal]; !got.StartedAt.Equal(started) || !got.CompletedAt.Equal(started.Add(40*time.Millisecond)) || got.Aggregation != cicontract.TimingAggregationCriticalPath {
		t.Fatalf("total shard interval = %#v", got)
	}
	if !run.StartedAt.Equal(started) || !run.CompletedAt.Equal(started.Add(40*time.Millisecond)) || run.Aggregation != cicontract.TimingAggregationCriticalPath {
		t.Fatalf("run critical-path envelope = %#v", run)
	}
	for _, phase := range []cicontract.TimingPhase{cicontract.TimingECIWait, cicontract.TimingSourceMaterialize, cicontract.TimingCandidateCompile} {
		if shard[phase].Measurement != cicontract.ObservationMeasured || shard[phase].Aggregation != cicontract.TimingAggregationRaw {
			t.Fatalf("shard phase %q = %#v", phase, shard[phase])
		}
	}
}

func TestMergeWorkloadIntervalsPreservesEnvelopeAndExcludesGaps(t *testing.T) {
	started := time.Date(2026, 8, 3, 6, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name       string
		intervals  []phaseIntervalUnion
		completed  time.Time
		durationMS int64
	}{
		{name: "disjoint", intervals: []phaseIntervalUnion{{startedAt: started, completedAt: started.Add(5 * time.Millisecond)}, {startedAt: started.Add(10 * time.Millisecond), completedAt: started.Add(15 * time.Millisecond)}}, completed: started.Add(15 * time.Millisecond), durationMS: 10},
		{name: "overlapping concurrent", intervals: []phaseIntervalUnion{{startedAt: started, completedAt: started.Add(10 * time.Millisecond)}, {startedAt: started.Add(5 * time.Millisecond), completedAt: started.Add(15 * time.Millisecond)}, {startedAt: started.Add(8 * time.Millisecond), completedAt: started.Add(12 * time.Millisecond)}}, completed: started.Add(15 * time.Millisecond), durationMS: 15},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := mergeWorkloadIntervals(testCase.intervals)
			if err != nil {
				t.Fatal(err)
			}
			if !got.startedAt.Equal(started) || !got.completedAt.Equal(testCase.completed) || got.durationMS != testCase.durationMS {
				t.Fatalf("merged interval = %#v, want completed=%s duration_ms=%d", got, testCase.completed, testCase.durationMS)
			}
		})
	}
}

func TestRemoteTimingObservationsRejectsMissingProducerIntervalAndWrongShardBinding(t *testing.T) {
	started := time.Date(2026, 8, 3, 5, 0, 0, 0, time.UTC)
	identity := "sha256:" + strings.Repeat("b", 64)
	result := RunResult{JobID: "job-missing", StartedAt: started, CompletedAt: started.Add(time.Second), Shards: []ShardResult{{ShardIdentity: identity, ExecutedWorkloads: []gate.GateID{"guard:one"}, ECIWaitStartedAt: started, ECIWaitCompletedAt: started.Add(2 * time.Millisecond), ECITerminalAt: started.Add(time.Second)}}, WorkloadExecutions: []gate.PlanGateExecution{{GateID: "guard:one", ShardIdentity: identity, StartedAt: started, CompletedAt: started.Add(time.Second), ExecutionProfile: gate.ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: "miss", CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 1, TotalMS: 1_000}}}}
	if _, err := remoteTimingObservations(result); err == nil || !strings.Contains(err.Error(), "source_materialize") {
		t.Fatalf("missing source interval error = %v", err)
	}
	result.Shards[0].MaterializationTiming = gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: identity, Source: gate.MaterializationPhaseTiming{StartedAtUnixMS: started.UnixMilli(), CompletedAtUnixMS: started.Add(time.Millisecond).UnixMilli(), MaterializeMS: 1}, CandidateCompile: gate.MaterializationPhaseTiming{StartedAtUnixMS: started.Add(2 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: started.Add(3 * time.Millisecond).UnixMilli(), MaterializeMS: 1}}
	result.WorkloadExecutions[0].ShardIdentity = "sha256:wrong"
	if _, err := remoteTimingObservations(result); err == nil || !strings.Contains(err.Error(), "shard identity") {
		t.Fatalf("wrong shard binding error = %v", err)
	}
}
