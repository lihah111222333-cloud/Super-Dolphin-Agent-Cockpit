package remoteci

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRemoteDurationSamplesPreserveSuccessfulShardsWhenBatchIsIncomplete(t *testing.T) {
	started := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	catalog := gate.WorkloadCatalog{Workloads: []gate.Workload{
		{ID: "guard:passed", Kind: gate.WorkloadKindGuard, CommandDigest: testDurationDigest("a")},
		{ID: "guard:missing", Kind: gate.WorkloadKindGuard, CommandDigest: testDurationDigest("b")},
	}}
	shards := []ShardResult{
		{
			ResourceClass: "small", Resources: eci.Resources{CPU: 2, MemoryGiB: 4},
			ExecutedWorkloads: []gate.GateID{"guard:passed"},
			Report: gate.PlanExecutionReport{Gates: []gate.PlanGateExecution{{
				GateID: "guard:passed", Status: gate.ResultStatusPassed, ExitCode: 0,
				StartedAt: started, CompletedAt: started.Add(25 * time.Millisecond),
			}}},
		},
		{ResourceClass: "small", Resources: eci.Resources{CPU: 2, MemoryGiB: 4}, ExecutedWorkloads: []gate.GateID{"guard:missing"}},
	}
	input := RunInput{
		Platform: "linux/amd64", RunnerIdentityDigest: testDurationDigest("c"),
		ToolchainDigest: testDurationDigest("d"),
	}

	samples, err := remoteDurationSamples(catalog, shards, input, map[string]string{
		"guard:passed":  "sha256:" + strings.Repeat("e", 64),
		"guard:missing": "sha256:" + strings.Repeat("f", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "duration execution coverage is incomplete") {
		t.Fatalf("remoteDurationSamples() error = %v", err)
	}
	if len(samples) != 1 || samples[0].Bucket.WorkloadID != "guard:passed" ||
		!samples[0].Succeeded || samples[0].DurationMS != 25 {
		t.Fatalf("remoteDurationSamples() = %#v", samples)
	}
}

func TestRemoteDurationSamplesSkipUncreatedShardPlaceholder(t *testing.T) {
	shards, _ := initializeRemoteShardResults(
		[]gate.ContainerShard{{IdentityDigest: "shard-uncreated", GateIDs: []gate.GateID{"guard:cache-miss"}}},
		[]string{""},
	)
	input := RunInput{Platform: "linux/amd64", RunnerIdentityDigest: testDurationDigest("c"), ToolchainDigest: testDurationDigest("d")}

	samples, err := remoteDurationSamples(gate.WorkloadCatalog{Workloads: []gate.Workload{{
		ID: "guard:cache-miss", Kind: gate.WorkloadKindGuard, CommandDigest: testDurationDigest("a"),
	}}}, shards, input, nil)
	if err != nil {
		t.Fatalf("remoteDurationSamples() error = %v, want no error for an unstarted placeholder", err)
	}
	if len(samples) != 0 {
		t.Fatalf("remoteDurationSamples() = %#v, want no samples for an unstarted placeholder", samples)
	}
}

func TestRecordRemoteCIRunPersistsUncreatedShardPlaceholderAudit(t *testing.T) {
	ledgerStore := newPartialResultsLedgerStore(t)
	started := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	runErr := errors.New("ECI create request failed after shard-000 was accepted")
	identity := "sha256:" + strings.Repeat("a", 64)
	agentDigest := "sha256:" + strings.Repeat("d", 64)
	passed := gate.PlanGateExecution{
		GateID: "guard:passed", ShardIdentity: identity, Status: gate.ResultStatusPassed, ExitCode: 0,
		StartedAt: started.Add(10 * time.Millisecond), CompletedAt: started.Add(40 * time.Millisecond),
		ExecutionProfile: gate.ExecutionProfile{CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 10, TestBodyMS: 20, TotalMS: 30},
	}
	cancelled := gate.PlanGateExecution{
		GateID: "guard:cancelled", ShardIdentity: identity, Status: gate.ResultStatusCancelled, ExitCode: -1,
		StartedAt: started.Add(40 * time.Millisecond), CompletedAt: started.Add(40 * time.Millisecond),
		ExecutionProfile: gate.ExecutionProfile{CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured"},
	}
	result := RunResult{
		AcceptedGeneration: 1, JobID: "job-partial-eci-create", AgentTokenDigest: agentDigest, Entrypoint: gate.CIEntrypointGitPreCommit,
		Profile: gate.ProfileLocalFast, PlanDigest: "sha256:plan",
		CatalogDigest: testDurationDigest("e"), SourceTreeSHA: strings.Repeat("f", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("b", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("c", 64),
		ImageCacheSnapshotID: "snapshot-1", RunnerImage: "ubuntu:22.04", Status: gate.ResultStatusFailed, Authoritative: true,
		StartedAt: started, CompletedAt: started.Add(101 * time.Second), CleanupComplete: true,
		DurationSamples: []gate.DurationSample{
			{Bucket: gate.DurationBucket{WorkloadID: "guard:passed", CommandDigest: strings.Repeat("1", 64), InputDigest: "sha256:" + strings.Repeat("0", 64), Platform: "linux/amd64", Runner: "runner", Toolchain: "toolchain", ExecutionMode: gate.DurationExecutionModeNormal, ResourceClassID: "small", ResourceCPU: 2, ResourceMemoryGiB: 4}, Succeeded: true, DurationMS: 30},
			{Bucket: gate.DurationBucket{WorkloadID: "guard:cancelled", CommandDigest: strings.Repeat("2", 64), InputDigest: "sha256:" + strings.Repeat("0", 64), Platform: "linux/amd64", Runner: "runner", Toolchain: "toolchain", ExecutionMode: gate.DurationExecutionModeNormal, ResourceClassID: "small", ResourceCPU: 2, ResourceMemoryGiB: 4}, Succeeded: false, DurationMS: 30},
		},
		Shards: []ShardResult{
			{
				ShardIdentity: identity, ContainerGroup: "eci-created", ContainerStatus: "Failed",
				ExecutedWorkloads: []gate.GateID{"guard:passed", "guard:cancelled"},
				ECIWaitStartedAt:  started, ECIWaitCompletedAt: started.Add(5 * time.Millisecond), ECITerminalAt: started.Add(50 * time.Millisecond),
				MaterializationTiming: gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: identity,
					Source:           gate.MaterializationPhaseTiming{StartedAtUnixMS: started.Add(time.Millisecond).UnixMilli(), CompletedAtUnixMS: started.Add(2 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
					CandidateCompile: gate.MaterializationPhaseTiming{StartedAtUnixMS: started.Add(3 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: started.Add(4 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
				},
				ResourceClass: "normal", Resources: eci.Resources{CPU: 4, MemoryGiB: 8},
			},
			{
				// initializeRemoteShardResults 在 ECI create call 未返回 group ID 时
				// 生成这个携带 identity/workload 的 placeholder。
				ShardIdentity:     "sha256:" + strings.Repeat("9", 64),
				ExecutedWorkloads: []gate.GateID{"guard:uncreated"},
				MaterializationTiming: gate.ShardMaterializationTiming{
					Measurement: gate.MaterializationMeasurementNotMeasured,
				},
			},
		},
		WorkloadExecutions:      []gate.PlanGateExecution{passed, cancelled},
		FreshWorkloadExecutions: []gate.PlanGateExecution{passed, cancelled},
	}
	warning := gate.RemoteCITimingWarning{
		JobID: result.JobID, AgentTokenDigest: agentDigest, AcceptedGeneration: 1,
		Scope: cicontract.TimingScopeShard, ShardIdentity: identity, EvidenceKind: cicontract.TimingWarningEvidenceRunning,
		Action: cicontract.TimingWarningWarnAndContinue, EvidenceStartedAt: started, ObservedAt: started.Add(cicontract.ShardTargetDuration),
		EvidenceDurationMS: cicontract.ShardTargetDuration.Milliseconds(), TargetMS: cicontract.ShardTargetDuration.Milliseconds(),
	}
	warning.WarningText = gate.CanonicalRemoteCITimingWarningText(warning)
	result.TimingWarnings, result.OptimizationWarnings = []gate.RemoteCITimingWarning{warning}, []string{warning.WarningText}
	catalog := gate.WorkloadCatalog{
		Version: 1, Authoritative: true,
		Workloads: []gate.Workload{
			{ID: "guard:passed", Kind: gate.WorkloadKindGuard, CommandDigest: strings.Repeat("1", 64), BootstrapEstimateMS: 1_000, Shardable: true},
			{ID: "guard:cancelled", Kind: gate.WorkloadKindGuard, CommandDigest: strings.Repeat("2", 64), BootstrapEstimateMS: 1_000, Shardable: true},
			{ID: "guard:uncreated", Kind: gate.WorkloadKindGuard, CommandDigest: strings.Repeat("3", 64), BootstrapEstimateMS: 1_000, Shardable: true},
		},
	}
	recordPartialResultsCatalog(t, ledgerStore, &result, catalog, started)
	if _, inserted, err := ledgerStore.RecordLiveRemoteCITimingWarning(warning); err != nil || !inserted {
		t.Fatalf("record live warning inserted=%t error=%v", inserted, err)
	}
	if err := recordRemoteCIRun(ledgerStore, result, runErr); err != nil {
		t.Fatalf("recordRemoteCIRun() = %v, want audited create failure", err)
	}
	stored, err := ledgerStore.LoadRemoteCIRun(result.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() = %v, want audited create failure", err)
	}
	if stored.Authoritative || !stored.CleanupComplete || !strings.Contains(stored.ErrorText, runErr.Error()) {
		t.Fatalf("stored create failure audit = auth=%t cleanup=%t error=%q", stored.Authoritative, stored.CleanupComplete, stored.ErrorText)
	}
}

func TestRecordRemoteCIRunRejectsIncompleteShardTiming(t *testing.T) {
	ledgerStore := newPartialResultsLedgerStore(t)
	started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	identity := "sha256:" + strings.Repeat("a", 64)
	result := RunResult{
		AcceptedGeneration: 1, JobID: "job-invalid-shard-timing", AgentTokenDigest: "sha256:" + strings.Repeat("d", 64),
		Entrypoint: gate.CIEntrypointGitPreCommit, Profile: gate.ProfileLocalFast, PlanDigest: "sha256:plan",
		CatalogDigest: testDurationDigest("f"), SourceTreeSHA: strings.Repeat("f", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("b", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("c", 64),
		ImageCacheSnapshotID: "snapshot-1", RunnerImage: "ubuntu:22.04", Status: gate.ResultStatusFailed,
		StartedAt: started, CompletedAt: started.Add(time.Second),
		Shards: []ShardResult{{
			ShardIdentity: identity, ContainerGroup: "eci-created", ContainerStatus: "Failed",
			ExecutedWorkloads:     []gate.GateID{"guard:failed"},
			MaterializationTiming: gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementUnavailable},
		}},
	}
	recordPartialResultsCatalog(t, ledgerStore, &result, gate.WorkloadCatalog{
		Version: 1, Authoritative: true,
		Workloads: []gate.Workload{{ID: "guard:failed", Kind: gate.WorkloadKindGuard, CommandDigest: strings.Repeat("1", 64), BootstrapEstimateMS: 1_000, Shardable: true}},
	}, started)
	runErr := errors.New("worker plan report exceeded record budget")
	if err := recordRemoteCIRun(ledgerStore, result, runErr); err == nil {
		t.Fatal("recordRemoteCIRun() accepted incomplete shard timing")
	}
	if _, err := ledgerStore.LoadRemoteCIRun(result.JobID); err == nil {
		t.Fatal("incomplete shard timing run was persisted after fail-fast rejection")
	}
}

func TestRecordRemoteCIRunRejectsIncompleteShardTimingWithLiveWarning(t *testing.T) {
	ledgerStore := newPartialResultsLedgerStore(t)
	started := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	identity := "sha256:" + strings.Repeat("a", 64)
	agentDigest := "sha256:" + strings.Repeat("d", 64)
	jobID := "job-invalid-shard-warning"
	warning := gate.RemoteCITimingWarning{
		JobID: jobID, AgentTokenDigest: agentDigest, AcceptedGeneration: 1,
		Scope: cicontract.TimingScopeShard, ShardIdentity: identity,
		EvidenceKind: cicontract.TimingWarningEvidenceRunning, Action: cicontract.TimingWarningWarnAndContinue,
		EvidenceStartedAt: started, ObservedAt: started.Add(cicontract.ShardTargetDuration),
		EvidenceDurationMS: cicontract.ShardTargetDuration.Milliseconds(), TargetMS: cicontract.ShardTargetDuration.Milliseconds(),
	}
	warning.WarningText = gate.CanonicalRemoteCITimingWarningText(warning)
	if _, inserted, err := ledgerStore.RecordLiveRemoteCITimingWarning(warning); err != nil || !inserted {
		t.Fatalf("record live warning inserted=%t error=%v", inserted, err)
	}
	result := RunResult{
		AcceptedGeneration: 1, JobID: jobID, AgentTokenDigest: agentDigest,
		Entrypoint: gate.CIEntrypointGitPreCommit, Profile: gate.ProfileLocalFast, PlanDigest: "sha256:plan",
		CatalogDigest: testDurationDigest("f"), SourceTreeSHA: strings.Repeat("f", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("b", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("c", 64),
		ImageCacheSnapshotID: "snapshot-1", RunnerImage: "ubuntu:22.04", Status: gate.ResultStatusFailed,
		StartedAt: started, CompletedAt: started.Add(time.Second),
		Shards: []ShardResult{{
			ShardIdentity: identity, ContainerGroup: "eci-created", ContainerStatus: "Failed",
			ExecutedWorkloads:     []gate.GateID{"guard:failed"},
			MaterializationTiming: gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementUnavailable},
		}},
		OptimizationWarnings: []string{warning.WarningText}, TimingWarnings: []gate.RemoteCITimingWarning{warning},
	}
	recordPartialResultsCatalog(t, ledgerStore, &result, gate.WorkloadCatalog{
		Version: 1, Authoritative: true,
		Workloads: []gate.Workload{{ID: "guard:failed", Kind: gate.WorkloadKindGuard, CommandDigest: strings.Repeat("1", 64), BootstrapEstimateMS: 1_000, Shardable: true}},
	}, started)
	runErr := errors.Join(errors.New("worker plan report exceeded record budget"), errors.New("remote CI shard resource binding failed"))
	if err := recordRemoteCIRun(ledgerStore, result, runErr); err == nil {
		t.Fatal("recordRemoteCIRun() accepted incomplete shard timing with live warning")
	}
	if _, err := ledgerStore.LoadRemoteCIRun(result.JobID); err == nil {
		t.Fatal("incomplete shard timing warning run was persisted after fail-fast rejection")
	}
}

func newPartialResultsLedgerStore(t *testing.T) *gate.DurationLedgerStore {
	t.Helper()
	ledgerStore, err := gate.NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledgerStore.CompareAndSwap(0, gate.NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedRemoteCITestAcceptedGeneration(t, ledgerStore, 1)
	return ledgerStore
}

func recordPartialResultsCatalog(t *testing.T, ledgerStore *gate.DurationLedgerStore, result *RunResult, catalog gate.WorkloadCatalog, observedAt time.Time) {
	t.Helper()
	catalogDigest, err := gate.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	result.CatalogDigest = catalogDigest
	if err := ledgerStore.RecordWorkloadCatalog(catalog, gate.WorkloadCatalogObservation{
		SourceTreeSHA: result.SourceTreeSHA, Entrypoint: result.Entrypoint, Profile: result.Profile, AcceptedGeneration: result.AcceptedGeneration, ObservedAt: observedAt,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAggregateRemoteReportsPreservesExactGoTestWorkloadExecutionProfile(t *testing.T) {
	started := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	workloadID := string(gate.GateIDBackendTestWithGuard)
	profile := gate.ExecutionProfile{
		CacheSource: "go_build_cache", CacheStatus: "miss", CacheMeasurement: "measured",
		CacheMissCount: 1, StartupMS: 10, TestBodyMS: 20, TotalMS: 30,
	}
	catalog := gate.WorkloadCatalog{Workloads: []gate.Workload{{
		ID: workloadID, Kind: gate.WorkloadKindGoTest, Shardable: true,
	}}}
	observed := map[string]gate.PlanGateExecution{workloadID: {
		GateID: gate.GateID(workloadID), Status: gate.ResultStatusPassed, ExitCode: 0,
		StartedAt: started, CompletedAt: started.Add(30 * time.Millisecond), ExecutionProfile: profile,
	}}
	parents, workloads, status, err := aggregateRemoteReports(catalog, observed, []ShardResult{{ContainerStatus: "Succeeded"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	assertAggregateRemoteReportsIdentity(t, parents, workloads, status)
	assertRemoteExecutionProfile(t, "workload", workloads[0].ExecutionProfile, profile)
	assertRemoteExecutionProfile(t, "parent aggregate", parents[0].ExecutionProfile, profile)
}

func TestAggregateRemoteReportsProvesReleaseOwnerAfterShardableResults(t *testing.T) {
	catalog, planDigest, observed := releaseRemoteAggregationFixture(t)
	parents, workloads, status, err := aggregateRemoteReports(
		catalog, observed, []ShardResult{{ContainerStatus: "Succeeded"}}, planDigest,
	)
	if err != nil {
		t.Fatalf("aggregateRemoteReports() error = %v", err)
	}
	if status != gate.ResultStatusPassed || len(workloads) == 0 || len(parents) == 0 {
		t.Fatalf("release aggregate status=%s workloads=%d parents=%d", status, len(workloads), len(parents))
	}
	last := parents[len(parents)-1]
	if last.GateID != gate.GateIDReleaseLayeredCheck || last.Status != gate.ResultStatusPassed || last.ExitCode != 0 {
		t.Fatalf("release owner attestation = %#v", last)
	}
}

func TestCompleteRemoteReuseProvesReleaseOwnerAfterAllHits(t *testing.T) {
	catalog, planDigest, observed := releaseRemoteAggregationFixture(t)
	reused := make(map[string]gate.WorkloadPassEvidence, len(observed))
	for workloadID, execution := range observed {
		reused[workloadID] = gate.WorkloadPassEvidence{OriginExecution: execution}
	}
	result, err := completeRemoteReuse(catalog, reused, RunResult{PlanDigest: planDigest}, func() time.Time {
		return time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("completeRemoteReuse() error = %v", err)
	}
	if result.Status != gate.ResultStatusPassed || len(result.WorkloadExecutions) != len(observed) || len(result.GateExecutions) == 0 {
		t.Fatalf("all-hit release result = %#v", result)
	}
	last := result.GateExecutions[len(result.GateExecutions)-1]
	if last.GateID != gate.GateIDReleaseLayeredCheck || last.Status != gate.ResultStatusPassed {
		t.Fatalf("all-hit release owner attestation = %#v", last)
	}
}

func releaseRemoteAggregationFixture(t *testing.T) (gate.WorkloadCatalog, string, map[string]gate.PlanGateExecution) {
	t.Helper()
	tree := strings.Repeat("a", 40)
	plan, err := gate.BuildGatePlan(gate.ProfileRelease, gate.SourceSpec{
		Kind: gate.SourceKindTree, ObjectFormat: gate.GitObjectFormatSHA1,
		Tree: &gate.TreeSource{SHA: tree, ParentCommitSHA: strings.Repeat("b", 40)}, SourceTreeSHA: tree,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Release aggregation consumes the expanded catalog: nilness is an
	// expansion-only gate and therefore needs a real package target here so its
	// prerequisite is represented as a shardable workload.
	catalog, err := gate.BuildExpandedWorkloadCatalog(plan, gate.DefaultWorkloadBootstrapPolicy(), gate.WorkloadInventory{
		GoPackages: []string{"./internal/fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	log := []byte("release prerequisite\n")
	logDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(log))
	started := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	observed := make(map[string]gate.PlanGateExecution)
	for index, workload := range catalog.Workloads {
		if !workload.Shardable {
			continue
		}
		begin := started.Add(time.Duration(index) * time.Second)
		observed[workload.ID] = gate.PlanGateExecution{
			GateID: gate.GateID(workload.ID), Status: gate.ResultStatusPassed, ExitCode: 0,
			StartedAt: begin, CompletedAt: begin.Add(time.Second), Log: log, LogDigest: logDigest,
			ExecutionProfile: gate.ExecutionProfile{
				CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured",
				StartupMS: 1, TestBodyMS: 1, TotalMS: 2,
			},
		}
	}
	return catalog, plan.PlanDigest, observed
}

func assertAggregateRemoteReportsIdentity(t *testing.T, parents []gate.PlanGateExecution, workloads []gate.PlanGateExecution, status gate.ResultStatus) {
	t.Helper()
	if status != gate.ResultStatusPassed || len(parents) != 1 || len(workloads) != 1 {
		t.Fatalf("aggregateRemoteReports() parents=%#v workloads=%#v status=%q", parents, workloads, status)
	}
}

func assertRemoteExecutionProfile(t *testing.T, label string, got gate.ExecutionProfile, want gate.ExecutionProfile) {
	t.Helper()
	if got.CacheSource != want.CacheSource || got.CacheStatus != want.CacheStatus || got.CacheMeasurement != want.CacheMeasurement ||
		got.StartupMS != want.StartupMS || got.TestBodyMS != want.TestBodyMS || got.TotalMS != want.TotalMS {
		t.Fatalf("%s execution profile = %#v, want %#v", label, got, want)
	}
}

func TestRemoteWorkloadExecutionProfileIsStrict(t *testing.T) {
	started := time.Date(2026, 8, 2, 11, 0, 0, 0, time.UTC)
	exact, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary", 1)
	if err != nil {
		t.Fatal(err)
	}
	guard := gate.Workload{ID: "guard:profile", Kind: gate.WorkloadKindGuard, Shardable: true}

	t.Run("legacy report schema is rejected", func(t *testing.T) {
		assertRemoteWorkloadProfileRejected(t, exact, "shard-legacy", 1, "report schema is unsupported", started)
	})

	t.Run("current guard requires structured profile", func(t *testing.T) {
		assertRemoteWorkloadProfileRejected(t, guard, "shard-current", gate.ExecutorPlanReportSchemaVersion, "execution profile cache source is invalid", started)
	})

	t.Run("current exact workload requires structured profile", func(t *testing.T) {
		assertRemoteWorkloadProfileRejected(t, exact, "shard-current", gate.ExecutorPlanReportSchemaVersion, "execution profile cache source is invalid", started)
	})
}

func assertRemoteWorkloadProfileRejected(t *testing.T, workload gate.Workload, shardID string, schema uint32, want string, started time.Time) {
	t.Helper()
	_, err := remoteFreshWorkloadExecutions([]gate.Workload{workload}, []ShardResult{{
		ShardIdentity: shardID,
		Report: gate.PlanExecutionReport{
			SchemaVersion:    schema,
			ExecutionOutcome: gate.SuccessfulWorkerExecutionOutcome(),
			Gates:            []gate.PlanGateExecution{zeroRemoteWorkloadProfileExecution(started, workload.ID)},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected workload profile rejection containing %q, got %v", want, err)
	}
}

func zeroRemoteWorkloadProfileExecution(started time.Time, id string) gate.PlanGateExecution {
	return gate.PlanGateExecution{
		GateID:      gate.GateID(id),
		Status:      gate.ResultStatusPassed,
		ExitCode:    0,
		StartedAt:   started,
		CompletedAt: started.Add(time.Millisecond),
	}
}

func testDurationDigest(seed string) string {
	return "sha256:" + strings.Repeat(seed, 64)
}

func TestRemoteFreshWorkloadExecutionsReportState(t *testing.T) {
	workload := gate.Workload{ID: "guard:missing-report", Kind: gate.WorkloadKindGuard, Shardable: true}
	shardWorkloads := []gate.GateID{gate.GateID(workload.ID)}
	tests := []struct {
		name    string
		result  ShardResult
		wantErr string
	}{
		{
			name: "uncreated placeholder is skipped",
			result: ShardResult{
				ShardIdentity:     "shard-uncreated",
				ExecutedWorkloads: shardWorkloads,
			},
		},
		{
			name: "created failed empty report is skipped",
			result: ShardResult{
				ShardIdentity:     "shard-failed",
				ContainerGroup:    "eci-failed",
				ContainerStatus:   "Failed",
				ExecutedWorkloads: shardWorkloads,
			},
		},
		{
			name: "succeeded empty report is rejected as missing",
			result: ShardResult{
				ShardIdentity:     "shard-succeeded",
				ContainerGroup:    "eci-succeeded",
				ContainerStatus:   "Succeeded",
				ExecutedWorkloads: shardWorkloads,
			},
			wantErr: "report is missing",
		},
		{
			name: "legacy report schema remains unsupported",
			result: ShardResult{
				ShardIdentity:   "shard-legacy",
				ContainerGroup:  "eci-legacy",
				ContainerStatus: "Succeeded",
				Report:          gate.PlanExecutionReport{SchemaVersion: 1},
			},
			wantErr: "report schema is unsupported",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fresh, err := remoteFreshWorkloadExecutions([]gate.Workload{workload}, []ShardResult{tt.result})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("remoteFreshWorkloadExecutions() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("remoteFreshWorkloadExecutions() error = %v", err)
			}
			if len(fresh) != 0 {
				t.Fatalf("remoteFreshWorkloadExecutions() = %#v, want no executions", fresh)
			}
			if _, err := collectFreshRemoteWorkloadExecutions([]gate.Workload{workload}, fresh); err == nil || !strings.Contains(err.Error(), "has no matching result") {
				t.Fatalf("collectFreshRemoteWorkloadExecutions() error = %v, want missing result", err)
			}
		})
	}
}

func TestRemoteFreshWorkloadExecutionsRejectsFailedWorkerOutcomeWithPassingGates(t *testing.T) {
	started := time.Date(2026, 8, 4, 10, 30, 0, 0, time.UTC)
	workload := gate.Workload{ID: "guard:worker-failed", Kind: gate.WorkloadKindGuard, Shardable: true}
	execution := partialRemoteExecution(workload.ID, started)
	report := gate.PlanExecutionReport{
		SchemaVersion: gate.ExecutorPlanReportSchemaVersion,
		ExecutionOutcome: gate.WorkerExecutionOutcome{
			Status:     gate.WorkerExecutionStatusFailed,
			ExitCode:   17,
			ReasonCode: gate.WorkerExecutionReasonExecutionError,
		},
		Gates: []gate.PlanGateExecution{execution},
	}
	fresh, err := remoteFreshWorkloadExecutions([]gate.Workload{workload}, []ShardResult{{
		ShardIdentity: "shard-worker-failed",
		Report:        report,
	}})
	if err == nil || !strings.Contains(err.Error(), "remote worker execution failed") || !strings.Contains(err.Error(), "exit_code=17") || !strings.Contains(err.Error(), "reason_code=execution_error") {
		t.Fatalf("remoteFreshWorkloadExecutions() error = %v, want bounded worker failure", err)
	}

	if len(fresh) != 1 {
		t.Fatalf("remoteFreshWorkloadExecutions() preserved %d executions after worker failure, want passing gate evidence", len(fresh))
	}
	if got, ok := fresh[workload.ID]; !ok || got.Status != gate.ResultStatusPassed || got.ShardIdentity != "shard-worker-failed" {
		t.Fatalf("remoteFreshWorkloadExecutions() preserved = %#v, want passing evidence from failed worker shard", fresh)
	}
}

func TestRemoteFreshWorkloadExecutionsPreservesPartialBeforeMalformedReport(t *testing.T) {
	started := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	passed := partialRemoteExecution("guard:passed", started)
	workloads := []gate.Workload{
		{ID: string(passed.GateID), Kind: gate.WorkloadKindGuard, Shardable: true},
		{ID: "guard:malformed", Kind: gate.WorkloadKindGuard, Shardable: true},
	}
	fresh, err := remoteFreshWorkloadExecutions(workloads, []ShardResult{
		{
			ShardIdentity: "shard-good",
			Report: gate.PlanExecutionReport{
				SchemaVersion:    gate.ExecutorPlanReportSchemaVersion,
				ExecutionOutcome: gate.SuccessfulWorkerExecutionOutcome(),
				Gates:            []gate.PlanGateExecution{passed},
			},
		},
		{
			ShardIdentity:   "shard-malformed",
			ContainerGroup:  "eci-malformed",
			ContainerStatus: "Succeeded",
			Report:          gate.PlanExecutionReport{SchemaVersion: 1},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "report schema is unsupported") {
		t.Fatalf("remoteFreshWorkloadExecutions() error = %v, want unsupported report schema", err)
	}
	if len(fresh) != 1 {
		t.Fatalf("remoteFreshWorkloadExecutions() preserved %d executions, want one", len(fresh))
	}
	if got, ok := fresh[string(passed.GateID)]; !ok || got.ShardIdentity != "shard-good" {
		t.Fatalf("remoteFreshWorkloadExecutions() preserved = %#v, want guard:passed from shard-good", fresh)
	}
}

func TestMergeRemoteWorkloadMissesPersistsPartialFailedRunEvidence(t *testing.T) {
	t.Helper()
	runPartialMergePersistenceTest(t)
}

func runPartialMergePersistenceTest(t *testing.T) {
	started := time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC)
	catalog, prepared, shards, _ := partialRemoteMergeFixture(started)
	result := RunResult{
		AcceptedGeneration:           1,
		ImageCacheSnapshotID:         "snapshot-1",
		JobID:                        "job-partial-merge",
		AgentTokenDigest:             "sha256:" + strings.Repeat("d", 64),
		Entrypoint:                   gate.CIEntrypointGitPreCommit,
		Profile:                      gate.ProfileLocalFast,
		PlanDigest:                   "sha256:" + strings.Repeat("e", 64),
		SourceTreeSHA:                strings.Repeat("f", 40),
		CandidateGateSourceSHA256:    "sha256:" + strings.Repeat("b", 64),
		CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("c", 64),
		RunnerImage:                  "ubuntu:22.04",
		Status:                       gate.ResultStatusFailed,
		StartedAt:                    started,
		CompletedAt:                  started.Add(2 * time.Minute),
		CleanupComplete:              true,
	}
	coordinator := &Coordinator{now: func() time.Time { return started }}
	result, freshErr, mergeErr := coordinator.mergeRemoteWorkloadMisses(
		catalog,
		RunInput{Platform: "linux/amd64", RunnerIdentityDigest: testDurationDigest("c"), ToolchainDigest: testDurationDigest("d")},
		prepared,
		nil,
		shards,
		result,
	)
	assertPartialMergeOutcome(t, result, freshErr, mergeErr)

	ledgerStore := newPartialResultsLedgerStore(t)
	recordPartialResultsCatalog(t, ledgerStore, &result, catalog, started)
	if err := recordRemoteCIRun(ledgerStore, result, errors.Join(freshErr, mergeErr)); err == nil {
		t.Fatal("recordRemoteCIRun() accepted incomplete merged failed run")
	}
	if _, err := ledgerStore.LoadRemoteCIRun(result.JobID); err == nil {
		t.Fatal("incomplete merged failed run was persisted after fail-fast rejection")
	}
}

func assertPartialMergeOutcome(t *testing.T, result RunResult, freshErr, mergeErr error) {
	t.Helper()
	if freshErr != nil {
		t.Fatalf("mergeRemoteWorkloadMisses() fresh error = %v, want none for failed shard placeholder", freshErr)
	}
	if mergeErr == nil || !strings.Contains(mergeErr.Error(), "has no matching result") {
		t.Fatalf("mergeRemoteWorkloadMisses() merge error = %v, want coverage error", mergeErr)
	}
	if result.Status != gate.ResultStatusFailed {
		t.Fatalf("partial result status = %s, want failed", result.Status)
	}
	if len(result.FreshWorkloadExecutions) != 1 || len(result.WorkloadExecutions) != 1 || len(result.DurationSamples) == 0 {
		t.Fatalf("partial result evidence fresh=%d workload=%d samples=%d, want one execution and samples", len(result.FreshWorkloadExecutions), len(result.WorkloadExecutions), len(result.DurationSamples))
	}
}

func partialRemoteMergeFixture(started time.Time) (gate.WorkloadCatalog, remoteWorkloadMissInputs, []ShardResult, gate.GateID) {
	passed := partialRemoteExecution("guard:passed", started)
	passedWorkload := gate.Workload{ID: string(passed.GateID), Kind: gate.WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64), InputDigest: testDurationDigest("a"), BootstrapEstimateMS: 1000, Shardable: true}
	missingWorkload := gate.Workload{ID: "guard:missing", Kind: gate.WorkloadKindGuard, CommandDigest: strings.Repeat("b", 64), InputDigest: testDurationDigest("b"), BootstrapEstimateMS: 1000, Shardable: true}
	catalog := gate.WorkloadCatalog{Version: 1, Authoritative: true, Workloads: []gate.Workload{passedWorkload, missingWorkload}}
	plan := gate.WorkloadExecutionPlan{Catalog: catalog, ExecutionWorkloadIDs: []gate.GateID{passed.GateID, gate.GateID(missingWorkload.ID)}}
	shards := []ShardResult{
		partialRemoteShardResult("sha256:"+strings.Repeat("a", 64), "eci-passed", "Succeeded", passed.GateID, started, gate.PlanExecutionReport{SchemaVersion: gate.ExecutorPlanReportSchemaVersion, ExecutionOutcome: gate.SuccessfulWorkerExecutionOutcome(), Gates: []gate.PlanGateExecution{passed}}),
		partialRemoteShardResult("sha256:"+strings.Repeat("b", 64), "eci-missing", "Failed", gate.GateID(missingWorkload.ID), started.Add(time.Second), gate.PlanExecutionReport{}),
	}
	prepared := remoteWorkloadMissInputs{set: gate.ContainerShardSet{WorkloadPlan: plan}}
	return catalog, prepared, shards, passed.GateID
}

func partialRemoteExecution(workloadID string, started time.Time) gate.PlanGateExecution {
	return gate.PlanGateExecution{
		GateID:      gate.GateID(workloadID),
		Status:      gate.ResultStatusPassed,
		ExitCode:    0,
		StartedAt:   started,
		CompletedAt: started.Add(30 * time.Millisecond),
		ExecutionProfile: gate.ExecutionProfile{
			CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured",
			StartupMS: 10, TestBodyMS: 20, TotalMS: 30,
		},
	}
}

func partialRemoteShardResult(identity, groupID, status string, workloadID gate.GateID, started time.Time, report gate.PlanExecutionReport) ShardResult {
	return ShardResult{
		ShardIdentity: identity, ContainerGroup: groupID, ContainerStatus: status,
		ExecutedWorkloads: []gate.GateID{workloadID}, Report: report,
		ECIWaitStartedAt: started, ECIWaitCompletedAt: started.Add(5 * time.Millisecond), ECITerminalAt: started.Add(50 * time.Millisecond),
		MaterializationTiming: gate.ShardMaterializationTiming{
			Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: identity,
			Source:           gate.MaterializationPhaseTiming{StartedAtUnixMS: started.Add(time.Millisecond).UnixMilli(), CompletedAtUnixMS: started.Add(2 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
			CandidateCompile: gate.MaterializationPhaseTiming{StartedAtUnixMS: started.Add(3 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: started.Add(4 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
		},
		ResourceClass: "normal", Resources: eci.Resources{CPU: 4, MemoryGiB: 8},
	}
}
