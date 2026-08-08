package remoteci

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
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

func TestMarkRemoteRunContextTerminalStatusClassifiesCancellationWithoutRewritingPass(t *testing.T) {
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	result := RunResult{Status: gate.ResultStatusFailed}
	markRemoteRunContextTerminalStatus(cancelledCtx, &result)
	if result.Status != gate.ResultStatusCancelled {
		t.Fatalf("cancelled context status = %s, want cancelled", result.Status)
	}
	deadlineCtx, cancelDeadline := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancelDeadline()
	result = RunResult{Status: gate.ResultStatusFailed}
	markRemoteRunContextTerminalStatus(deadlineCtx, &result)
	if result.Status != gate.ResultStatusTimeout {
		t.Fatalf("deadline context status = %s, want timeout", result.Status)
	}
	result = RunResult{Status: gate.ResultStatusPassed}
	markRemoteRunContextTerminalStatus(deadlineCtx, &result)
	if result.Status != gate.ResultStatusPassed {
		t.Fatalf("passed result status = %s, want unchanged passed", result.Status)
	}
}

func TestRunPreparedPersistsCancellationOnlyAfterRunIdentity(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)
	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	prepared, err := coordinator.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	cancelledContext, cancel := context.WithCancel(context.Background())
	jobID := "job-0123456789abcdef01234572"
	coordinator.newID = func() (string, error) {
		cancel()
		return jobID, nil
	}
	result, runErr := coordinator.RunPrepared(cancelledContext, prepared)
	if runErr == nil || !errors.Is(runErr, context.Canceled) {
		t.Fatalf("RunPrepared() error = %v, want context cancellation", runErr)
	}
	if result.JobID != jobID || result.Status != gate.ResultStatusCancelled {
		t.Fatalf("cancelled result = %#v, want exact job identity and cancelled status", result)
	}
	recorded, err := input.LedgerStore.LoadRemoteCIRun(jobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun(%q) error = %v", jobID, err)
	}
	if recorded.JobID != jobID || recorded.Authoritative || recorded.Status != gate.ResultStatusCancelled {
		t.Fatalf("persisted cancellation = %#v, want non-authoritative cancelled run", recorded)
	}

	prepared, err = coordinator.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() before pre-start cancellation error = %v", err)
	}
	preStartContext, cancelBeforeStart := context.WithCancel(context.Background())
	cancelBeforeStart()
	preStartJobID := "job-0123456789abcdef01234573"
	coordinator.newID = func() (string, error) {
		t.Fatal("newID called for cancellation before run identity")
		return preStartJobID, nil
	}
	preStartResult, preStartErr := coordinator.RunPrepared(preStartContext, prepared)
	if preStartErr == nil || !strings.Contains(preStartErr.Error(), "not started") {
		t.Fatalf("pre-start RunPrepared() result=%#v error=%v, want explicit not-started error", preStartResult, preStartErr)
	}
	if preStartResult.JobID != "" {
		t.Fatalf("pre-start cancellation fabricated job identity %q", preStartResult.JobID)
	}
	if _, err := input.LedgerStore.LoadRemoteCIRun(preStartJobID); err == nil {
		t.Fatalf("pre-start cancellation unexpectedly persisted job %q", preStartJobID)
	}
}

func TestAppendRemotePartialWorkloadTargetWarningsUsesMeasuredFailureIntervals(t *testing.T) {
	started := time.Date(2026, 8, 5, 2, 0, 0, 0, time.UTC)
	identity := "sha256:" + strings.Repeat("e", 64)
	workloadID := gate.GateID("guard:failed-warning")
	bodyMS := cicontract.ShardTargetDuration.Milliseconds() + 1
	totalMS := bodyMS + 1
	execution := gate.PlanGateExecution{
		GateID: workloadID, ShardIdentity: identity, Status: gate.ResultStatusFailed,
		StartedAt: started, CompletedAt: started.Add(time.Duration(totalMS) * time.Millisecond),
		ExecutionProfile: gate.ExecutionProfile{
			CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured",
			StartupMS: 1, TestBodyMS: bodyMS, TotalMS: totalMS,
		},
	}
	result := RunResult{
		JobID: "job-failed-warning", AgentTokenDigest: testRemoteAgentTokenDigest, AcceptedGeneration: 1,
		Shards: []ShardResult{{
			ShardIdentity: identity, ExecutedWorkloads: []gate.GateID{workloadID},
			Report:           gate.PlanExecutionReport{Gates: []gate.PlanGateExecution{execution}},
			ECIWaitStartedAt: started, ECIWaitCompletedAt: started.Add(time.Millisecond),
			ECITerminalAt: started.Add(time.Duration(totalMS+10) * time.Millisecond),
			MaterializationTiming: gate.ShardMaterializationTiming{
				Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: identity,
				Source: gate.MaterializationPhaseTiming{
					StartedAtUnixMS:   started.Add(2 * time.Millisecond).UnixMilli(),
					CompletedAtUnixMS: started.Add(3 * time.Millisecond).UnixMilli(), MaterializeMS: 1,
				},
				CandidateCompile: gate.MaterializationPhaseTiming{
					StartedAtUnixMS:   started.Add(4 * time.Millisecond).UnixMilli(),
					CompletedAtUnixMS: started.Add(5 * time.Millisecond).UnixMilli(), MaterializeMS: 1,
				},
			},
		}},
		FreshWorkloadExecutions: []gate.PlanGateExecution{execution},
		WorkloadExecutions:      []gate.PlanGateExecution{execution},
	}
	observations, projectionErr := remoteFailedTimingObservations(result)
	if projectionErr != nil {
		t.Fatalf("remoteFailedTimingObservations() error = %v", projectionErr)
	}
	result, err := appendRemotePartialWorkloadTargetWarnings(result, observations)
	if err != nil {
		t.Fatalf("appendRemotePartialWorkloadTargetWarnings() error = %v", err)
	}
	if len(result.TimingWarnings) != 2 {
		t.Fatalf("failed workload timing warnings = %#v, want test_body and total", result.TimingWarnings)
	}
	for _, warning := range result.TimingWarnings {
		if warning.Scope != cicontract.TimingScopeWorkload || warning.WorkloadID != workloadID {
			t.Fatalf("failed workload warning identity = %#v", warning)
		}
	}
}

func TestCompleteRemoteRunRetainsFailedWorkloadWarningsBeforeGateError(t *testing.T) {
	started := time.Date(2026, 8, 5, 3, 0, 0, 0, time.UTC)
	identity := "sha256:" + strings.Repeat("f", 64)
	workloadID := gate.GateID("guard:failed-complete-warning")
	bodyMS := cicontract.ShardTargetDuration.Milliseconds() + 1
	totalMS := bodyMS + 1
	execution := gate.PlanGateExecution{
		GateID: workloadID, ShardIdentity: identity, Status: gate.ResultStatusFailed,
		StartedAt: started, CompletedAt: started.Add(time.Duration(totalMS) * time.Millisecond),
		ExecutionProfile: gate.ExecutionProfile{
			CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured",
			StartupMS: 1, TestBodyMS: bodyMS, TotalMS: totalMS,
		},
	}
	shard := ShardResult{
		ShardIdentity: identity, ContainerStatus: "Failed", ResourceClass: "small",
		Resources: eci.Resources{CPU: 2, MemoryGiB: 4}, ExecutedWorkloads: []gate.GateID{workloadID},
		Report:           gate.PlanExecutionReport{SchemaVersion: gate.ExecutorPlanReportSchemaVersion, ExecutionOutcome: gate.SuccessfulWorkerExecutionOutcome(), Gates: []gate.PlanGateExecution{execution}},
		ECIWaitStartedAt: started, ECIWaitCompletedAt: started.Add(time.Millisecond),
		ECITerminalAt: started.Add(time.Duration(totalMS+10) * time.Millisecond),
		MaterializationTiming: gate.ShardMaterializationTiming{
			Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: identity,
			Source: gate.MaterializationPhaseTiming{
				StartedAtUnixMS:   started.Add(2 * time.Millisecond).UnixMilli(),
				CompletedAtUnixMS: started.Add(3 * time.Millisecond).UnixMilli(), MaterializeMS: 1,
			},
			CandidateCompile: gate.MaterializationPhaseTiming{
				StartedAtUnixMS:   started.Add(4 * time.Millisecond).UnixMilli(),
				CompletedAtUnixMS: started.Add(5 * time.Millisecond).UnixMilli(), MaterializeMS: 1,
			},
		},
	}
	catalog := gate.WorkloadCatalog{Version: 1, Authoritative: true, Workloads: []gate.Workload{{
		ID: string(workloadID), Kind: gate.WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64),
		InputDigest: "sha256:" + strings.Repeat("b", 64), BootstrapEstimateMS: 1_000, Shardable: true,
	}}}
	coordinator := &Coordinator{now: func() time.Time { return started }}
	result, err := coordinator.completeRemoteRun(
		catalog,
		RunInput{
			Platform: "linux/amd64", RunnerIdentityDigest: "runner", ToolchainDigest: "toolchain",
			WorkloadInputDigests: map[string]string{string(workloadID): catalog.Workloads[0].InputDigest},
		},
		[]ShardResult{shard},
		map[string]gate.PlanGateExecution{string(workloadID): execution},
		RunResult{JobID: "job-failed-complete-warning", AgentTokenDigest: testRemoteAgentTokenDigest, AcceptedGeneration: 1},
	)
	if err == nil || !strings.Contains(err.Error(), "gate execution failed") {
		t.Fatalf("completeRemoteRun() error = %v, want failed gate error", err)
	}
	if len(result.TimingWarnings) != 2 {
		t.Fatalf("failed complete timing warnings = %#v, want test_body and total", result.TimingWarnings)
	}
}

func TestCompleteRemoteRunKeepsFailedGuardBeforeCancelledTimingGap(t *testing.T) {
	started := time.Date(2026, 8, 6, 1, 0, 0, 0, time.UTC)
	identity := "sha256:" + strings.Repeat("9", 64)
	failedID := gate.GateID("guard:code-size")
	cancelledID := gate.GateID("guard:cancelled")
	bodyMS := cicontract.ShardTargetDuration.Milliseconds() + 1
	failed := gate.PlanGateExecution{
		GateID: failedID, ShardIdentity: identity, Status: gate.ResultStatusFailed, ExitCode: 1,
		StartedAt: started, CompletedAt: started.Add(time.Duration(bodyMS+1) * time.Millisecond),
		ExecutionProfile: gate.ExecutionProfile{
			CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured",
			StartupMS: 1, TestBodyMS: bodyMS, TotalMS: bodyMS + 1,
		},
		Log: gate.PlainTextLog("max_complexity got=12 limit=10"),
	}
	cancelledAt := failed.CompletedAt
	cancelled := gate.PlanGateExecution{
		GateID: cancelledID, ShardIdentity: identity, Status: gate.ResultStatusCancelled, ExitCode: -1,
		StartedAt: cancelledAt, CompletedAt: cancelledAt,
		ExecutionProfile: gate.ExecutionProfile{
			CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured",
		},
	}
	shard := completeFailedShardFixture(started, identity, failed, cancelled)
	catalog := completeFailedCatalogFixture(failedID, cancelledID)
	inputDigests := map[string]string{
		string(failedID):    catalog.Workloads[0].InputDigest,
		string(cancelledID): catalog.Workloads[1].InputDigest,
	}
	coordinator := &Coordinator{now: func() time.Time { return started }}
	result, err := coordinator.completeRemoteRun(
		catalog,
		RunInput{Platform: "linux/amd64", RunnerIdentityDigest: "runner", ToolchainDigest: "toolchain", WorkloadInputDigests: inputDigests},
		[]ShardResult{shard},
		map[string]gate.PlanGateExecution{string(failedID): failed, string(cancelledID): cancelled},
		RunResult{JobID: "job-failed-before-cancelled-gap", AgentTokenDigest: testRemoteAgentTokenDigest, AcceptedGeneration: 1},
	)
	wantPrefix := ErrGateFailed.Error() + `; gate="guard:code-size" status="failed" exit_code=1 log_tail="max_complexity got=12 limit=10"`
	if err == nil || !errors.Is(err, ErrGateFailed) || !strings.HasPrefix(err.Error(), wantPrefix) {
		t.Fatalf("completeRemoteRun() error = %v, want original guard failure before cancelled timing gap", err)
	}
	failedWarnings := 0
	for _, warning := range result.TimingWarnings {
		if warning.WorkloadID == cancelledID {
			t.Fatalf("cancelled workload received timing warning: %#v", warning)
		}
		if warning.WorkloadID == failedID {
			failedWarnings++
		}
	}
	if failedWarnings != 2 {
		t.Fatalf("failed workload timing warnings = %#v, want test_body and total", result.TimingWarnings)
	}
	observations, projectionErr := remoteFailedTimingObservations(result)
	if projectionErr == nil || !strings.Contains(projectionErr.Error(), string(cancelledID)) ||
		strings.Contains(projectionErr.Error(), string(failedID)) {
		t.Fatalf("remoteFailedTimingObservations() error = %v, want only cancelled timing gap", projectionErr)
	}
	failedPhases := make(map[cicontract.TimingPhase]bool)
	for _, observation := range observations {
		if observation.WorkloadID == cancelledID {
			t.Fatalf("cancelled workload received fabricated timing: %#v", observation)
		}
		if observation.WorkloadID == failedID {
			failedPhases[observation.Phase] = true
		}
	}
	for _, phase := range []cicontract.TimingPhase{cicontract.TimingStartup, cicontract.TimingTestBody, cicontract.TimingTotal} {
		if !failedPhases[phase] {
			t.Fatalf("failed workload observations = %#v, missing phase %s", observations, phase)
		}
	}
}

func TestCompleteRemoteRunStillRejectsPassedWorkloadWithoutMeasuredTiming(t *testing.T) {
	started := time.Date(2026, 8, 6, 2, 0, 0, 0, time.UTC)
	identity := "sha256:" + strings.Repeat("8", 64)
	workloadID := gate.GateID("guard:passed-without-timing")
	execution := gate.PlanGateExecution{
		GateID: workloadID, ShardIdentity: identity, Status: gate.ResultStatusPassed, ExitCode: 0,
		StartedAt: started, CompletedAt: started,
		ExecutionProfile: gate.ExecutionProfile{
			CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured",
		},
	}
	shard := completeFailedShardFixture(started, identity, execution)
	shard.ContainerStatus = "Succeeded"
	catalog := completeFailedCatalogFixture(workloadID)
	coordinator := &Coordinator{now: func() time.Time { return started }}
	_, err := coordinator.completeRemoteRun(
		catalog,
		RunInput{
			Platform: "linux/amd64", RunnerIdentityDigest: "runner", ToolchainDigest: "toolchain",
			WorkloadInputDigests: map[string]string{string(workloadID): catalog.Workloads[0].InputDigest},
		},
		[]ShardResult{shard},
		map[string]gate.PlanGateExecution{string(workloadID): execution},
		RunResult{JobID: "job-passed-without-measured-timing", AgentTokenDigest: testRemoteAgentTokenDigest, AcceptedGeneration: 1},
	)
	if err == nil || !strings.Contains(err.Error(), "timing") {
		t.Fatalf("completeRemoteRun() error = %v, want strict passed-workload timing rejection", err)
	}
}

func completeFailedShardFixture(started time.Time, identity string, executions ...gate.PlanGateExecution) ShardResult {
	workloads := make([]gate.GateID, len(executions))
	for index, execution := range executions {
		workloads[index] = execution.GateID
	}
	return ShardResult{
		ShardIdentity: identity, ContainerStatus: "Failed", ResourceClass: "small",
		Resources: eci.Resources{CPU: 2, MemoryGiB: 4}, ExecutedWorkloads: workloads,
		Report:           gate.PlanExecutionReport{SchemaVersion: gate.ExecutorPlanReportSchemaVersion, ExecutionOutcome: gate.SuccessfulWorkerExecutionOutcome(), Gates: executions},
		ECIWaitStartedAt: started, ECIWaitCompletedAt: started.Add(time.Millisecond),
		ECITerminalAt: executions[len(executions)-1].CompletedAt.Add(10 * time.Millisecond),
		MaterializationTiming: gate.ShardMaterializationTiming{
			Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: identity,
			Source: gate.MaterializationPhaseTiming{
				StartedAtUnixMS: started.Add(2 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: started.Add(3 * time.Millisecond).UnixMilli(), MaterializeMS: 1,
			},
			CandidateCompile: gate.MaterializationPhaseTiming{
				StartedAtUnixMS: started.Add(4 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: started.Add(5 * time.Millisecond).UnixMilli(), MaterializeMS: 1,
			},
		},
	}
}

func completeFailedCatalogFixture(ids ...gate.GateID) gate.WorkloadCatalog {
	workloads := make([]gate.Workload, len(ids))
	for index, id := range ids {
		seed := strings.Repeat(string(rune('a'+index)), 64)
		workloads[index] = gate.Workload{
			ID: string(id), Kind: gate.WorkloadKindGuard, CommandDigest: seed,
			InputDigest: "sha256:" + seed, BootstrapEstimateMS: 1_000, Shardable: true,
		}
	}
	return gate.WorkloadCatalog{Version: 1, Authoritative: true, Workloads: workloads}
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

func TestRemoteShardSummaryTimingContainsWorkerPhaseBeyondProviderTerminal(t *testing.T) {
	started := time.Date(2026, 8, 8, 0, 33, 34, 0, time.UTC)
	providerTerminal := started.Add(170 * time.Second)
	workerCompleted := providerTerminal.Add(109 * time.Millisecond)
	shard := ShardResult{ShardIdentity: "sha256:" + strings.Repeat("1", 64), ECIWaitStartedAt: started, ECITerminalAt: providerTerminal}
	startup := phaseIntervalUnion{startedAt: started.Add(60 * time.Second), completedAt: started.Add(90 * time.Second), durationMS: 30_000}
	body := phaseIntervalUnion{startedAt: started.Add(62*time.Second + 503*time.Millisecond), completedAt: workerCompleted, durationMS: 107_606}

	observations, err := remoteShardSummaryObservations("job-provider-terminal-precision", shard, startup, body)
	if err != nil {
		t.Fatalf("remoteShardSummaryObservations() error = %v", err)
	}
	invalidShard := shard
	invalidShard.ECITerminalAt = time.Time{}
	if _, err := remoteShardSummaryObservations("job-provider-terminal-precision", invalidShard, startup, body); err == nil || !strings.Contains(err.Error(), "total interval") {
		t.Fatalf("missing provider terminal was accepted: %v", err)
	}
	for _, observation := range observations {
		if observation.Phase != cicontract.TimingTotal {
			continue
		}
		if !observation.CompletedAt.Equal(workerCompleted) {
			t.Fatalf("total completed_at = %s, want worker boundary %s", observation.CompletedAt, workerCompleted)
		}
		if observation.DurationMS != workerCompleted.Sub(started).Milliseconds() {
			t.Fatalf("total duration_ms = %d, want %d", observation.DurationMS, workerCompleted.Sub(started).Milliseconds())
		}
		return
	}
	t.Fatal("remoteShardSummaryObservations() did not emit total phase")
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

func TestRemoteFailedTimingObservationsKeepsMeasuredStartupAndTotalWithoutBody(t *testing.T) {
	started := time.Date(2026, 8, 4, 1, 0, 0, 0, time.UTC)
	identity := "sha256:" + strings.Repeat("c", 64)
	workloadID := gate.GateID("guard:failed-startup")
	execution := gate.PlanGateExecution{
		GateID: workloadID, ShardIdentity: identity, Status: gate.ResultStatusFailed,
		StartedAt: started, CompletedAt: started.Add(20 * time.Millisecond),
		ExecutionProfile: gate.ExecutionProfile{CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 5, TotalMS: 20},
	}
	result := RunResult{
		JobID: "job-failed-startup",
		Shards: []ShardResult{{
			ShardIdentity: identity, ExecutedWorkloads: []gate.GateID{workloadID},
			ECIWaitStartedAt: started, ECIWaitCompletedAt: started.Add(2 * time.Millisecond), ECITerminalAt: started.Add(30 * time.Millisecond),
			MaterializationTiming: gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: identity,
				Source:           gate.MaterializationPhaseTiming{StartedAtUnixMS: started.Add(3 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: started.Add(4 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
				CandidateCompile: gate.MaterializationPhaseTiming{StartedAtUnixMS: started.Add(5 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: started.Add(6 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
			},
		}},
		FreshWorkloadExecutions: []gate.PlanGateExecution{execution},
	}
	observations, err := remoteFailedTimingObservations(result)
	if err == nil || !strings.Contains(err.Error(), "test-body") {
		t.Fatalf("failed timing projection error = %v, want missing test body error", err)
	}
	workloadPhases := make(map[cicontract.TimingPhase]gate.TimingObservation)
	for _, observation := range observations {
		if observation.Scope == cicontract.TimingScopeWorkload && observation.WorkloadID == workloadID {
			if observation.Measurement != cicontract.ObservationMeasured {
				t.Fatalf("failed workload observation is not measured: %#v", observation)
			}
			workloadPhases[observation.Phase] = observation
		}
	}
	if _, exists := workloadPhases[cicontract.TimingStartup]; !exists {
		t.Fatal("failed startup observation was dropped")
	}
	if _, exists := workloadPhases[cicontract.TimingTotal]; !exists {
		t.Fatal("failed total observation was dropped")
	}
	if _, exists := workloadPhases[cicontract.TimingTestBody]; exists {
		t.Fatal("missing failed test body was fabricated")
	}
	for _, observation := range observations {
		if observation.Measurement == cicontract.ObservationNotApplicable {
			t.Fatalf("failed timing projection contains not_applicable observation: %#v", observation)
		}
	}
}

func TestRemoteFailedTimingObservationsSkipsPartialShardAggregate(t *testing.T) {
	started := time.Date(2026, 8, 4, 2, 0, 0, 0, time.UTC)
	identity := "sha256:" + strings.Repeat("d", 64)
	profile := gate.ExecutionProfile{CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 5, TestBodyMS: 10, TotalMS: 20}
	execution := gate.PlanGateExecution{GateID: "guard:only", ShardIdentity: identity, Status: gate.ResultStatusFailed, StartedAt: started, CompletedAt: started.Add(20 * time.Millisecond), ExecutionProfile: profile}
	result := RunResult{
		JobID: "job-partial-shard", Shards: []ShardResult{{
			ShardIdentity: identity, ExecutedWorkloads: []gate.GateID{"guard:only", "guard:missing"},
			ECIWaitStartedAt: started, ECIWaitCompletedAt: started.Add(2 * time.Millisecond), ECITerminalAt: started.Add(30 * time.Millisecond),
			MaterializationTiming: gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: identity,
				Source:           gate.MaterializationPhaseTiming{StartedAtUnixMS: started.Add(3 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: started.Add(4 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
				CandidateCompile: gate.MaterializationPhaseTiming{StartedAtUnixMS: started.Add(5 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: started.Add(6 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
			},
		}}, FreshWorkloadExecutions: []gate.PlanGateExecution{execution},
	}
	observations, err := remoteFailedTimingObservations(result)
	if err == nil || !strings.Contains(err.Error(), "coverage") {
		t.Fatalf("partial failed timing projection error = %v", err)
	}
	for _, observation := range observations {
		if observation.Scope == cicontract.TimingScopeShard &&
			(observation.Phase == cicontract.TimingStartup || observation.Phase == cicontract.TimingTestBody || observation.Phase == cicontract.TimingTotal) {
			t.Fatalf("partial shard emitted aggregate phase: %#v", observation)
		}
	}
}
