package remoteci

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCompileGroupTimingAllHitDoesNotSynthesizeObservation(t *testing.T) {
	observations, err := remoteTimingObservations(RunResult{JobID: "job-all-hit"})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 0 {
		t.Fatalf("all-hit timing observations = %d, want zero", len(observations))
	}
}

func TestCompileGroupTimingGenericShardWithoutReportNeedsNoMode(t *testing.T) {
	observations, err := remoteCompileGroupTimingObservations(RunResult{JobID: "job-generic", Shards: []ShardResult{{ShardIdentity: "sha256:" + strings.Repeat("g", 64)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 0 {
		t.Fatalf("generic shard compile observations = %d, want zero", len(observations))
	}
}

func TestCompileGroupTimingPartialHitProjectsOnlyFreshMissShard(t *testing.T) {
	now := time.UnixMilli(1_000).UTC()
	execution := testCompileGroupExecution(now)
	result := RunResult{
		JobID: "job-partial-hit", ExecutionMode: gate.DurationExecutionModeNormal,
		Shards: []ShardResult{{
			ShardIdentity: "sha256:" + strings.Repeat("s", 64), ResourceClass: "small", Resources: eci.Resources{CPU: 2, MemoryGiB: 4},
			Report: gate.PlanExecutionReport{CompileGroupExecutions: []gate.CompileGroupExecution{execution}},
		}, {ShardIdentity: "sha256:" + strings.Repeat("r", 64)}},
	}
	observations, err := remoteCompileGroupTimingObservations(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || observations[0].Scope != cicontract.TimingScopeCompileGroup || observations[0].Phase != cicontract.TimingTestBinaryCompile {
		t.Fatalf("partial-hit compile observations = %#v, want one compile_group/test_binary_compile row", observations)
	}
}

func TestRecordRemoteCIRunRejectsDuplicateCompileGroup(t *testing.T) {
	now := time.UnixMilli(2_000).UTC()
	shardIdentity := "sha256:" + strings.Repeat("d", 64)
	execution := testCompileGroupExecution(now)
	workloadID := gate.GateIDWhitespaceCheck
	workloadExecution := gate.PlanGateExecution{
		GateID: workloadID, ShardIdentity: shardIdentity, Status: gate.ResultStatusFailed,
		StartedAt: now.Add(6 * time.Millisecond), CompletedAt: now.Add(16 * time.Millisecond),
		ExecutionProfile: gate.ExecutionProfile{
			CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured",
			StartupMS: 1, TestBodyMS: 9, TotalMS: 10,
		},
	}
	result := RunResult{
		JobID: "job-failed-compile-duplicate", AgentTokenDigest: testRemoteAgentTokenDigest,
		AcceptedGeneration: 1, ImageCacheSnapshotID: "snap-baseline-1", Entrypoint: gate.CIEntrypointManualCLI,
		Profile: gate.ProfileLocalFast, PlanDigest: "sha256:" + strings.Repeat("b", 64),
		CatalogDigest: "sha256:" + strings.Repeat("c", 64), SourceTreeSHA: strings.Repeat("e", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("f", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("1", 64),
		RunnerImage: "runner@sha256:" + strings.Repeat("2", 64), Status: gate.ResultStatusFailed,
		ExecutionMode: gate.DurationExecutionModeNormal, StartedAt: now, CompletedAt: now.Add(time.Second),
		Shards: []ShardResult{{
			ShardIdentity: shardIdentity, ContainerGroup: "eci-group", ContainerStatus: "Failed", ResourceClass: "small", Resources: eci.Resources{CPU: 2, MemoryGiB: 4},
			ExecutedWorkloads: []gate.GateID{workloadID}, ECIWaitStartedAt: now, ECIWaitCompletedAt: now.Add(time.Millisecond), ECITerminalAt: now.Add(20 * time.Millisecond),
			MaterializationTiming: gate.ShardMaterializationTiming{
				Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: shardIdentity,
				Source:                gate.MaterializationPhaseTiming{StartedAtUnixMS: now.Add(2 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: now.Add(3 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
				Baseline:              gate.MaterializationPhaseTiming{StartedAtUnixMS: now.Add(3 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: now.Add(4 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
				CandidateCompile:      gate.MaterializationPhaseTiming{StartedAtUnixMS: now.Add(4 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: now.Add(5 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
				CandidateTestBinaries: gate.MaterializationPhaseTiming{StartedAtUnixMS: now.Add(5 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: now.Add(6 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
			},
			Report: gate.PlanExecutionReport{Gates: []gate.PlanGateExecution{workloadExecution}, CompileGroupExecutions: []gate.CompileGroupExecution{execution, execution}},
		}},
		FreshWorkloadExecutions: []gate.PlanGateExecution{workloadExecution}, WorkloadExecutions: []gate.PlanGateExecution{workloadExecution},
	}
	store, _ := newRemoteRunLedgerAuthority(t, gate.NewDurationLedger())
	catalog := gate.WorkloadCatalog{Version: 1, Authoritative: true, Workloads: []gate.Workload{{
		ID: string(workloadID), Kind: gate.WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64), InputDigest: "sha256:" + strings.Repeat("b", 64), BootstrapEstimateMS: 1_000, Shardable: true,
	}}}
	catalogDigest, err := gate.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	result.CatalogDigest = catalogDigest
	if err := store.RecordWorkloadCatalog(catalog, gate.WorkloadCatalogObservation{SourceTreeSHA: result.SourceTreeSHA, Entrypoint: result.Entrypoint, Profile: result.Profile, AcceptedGeneration: result.AcceptedGeneration, ObservedAt: now}); err != nil {
		t.Fatalf("record failed-run workload catalog: %v", err)
	}
	if err := recordRemoteCIRun(store, result, nil); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("record failed duplicate compile run error = %v, want duplicate rejection", err)
	}
	if _, err := store.LoadRemoteCIRun(result.JobID); err == nil {
		t.Fatal("failed duplicate compile run was persisted after fail-fast rejection")
	}
}

func testCompileGroupExecution(start time.Time) gate.CompileGroupExecution {
	digest := "sha256:" + strings.Repeat("a", 64)
	return gate.CompileGroupExecution{
		Scope: cicontract.TimingScopeCompileGroup, Phase: cicontract.TimingTestBinaryCompile,
		GroupID: digest, ArtifactKey: digest, PackageTarget: "./pkg", WorkloadIDs: []gate.GateID{"go-test/pkg/Test"},
		StartedAtUnixMS: start.UnixMilli(), CompletedAtUnixMS: start.Add(25 * time.Millisecond).UnixMilli(), DurationMS: 25,
		ArtifactSHA256: digest, ArtifactSize: 128, CacheHits: 1, Status: gate.ResultStatusPassed, ExitCode: 0,
		CompileCommandDigest: digest, ProfileDigest: digest, ResourceClassID: "small",
	}
}

func TestRemoteCompileTimingProjectionKeepsSharedGroupSingleWithManySelectors(t *testing.T) {
	start := time.UnixMilli(10_000).UTC()
	workloadIDs := compileTimingProjectionSelectorIDs(t, 422)
	result := remoteCompileTimingProjectionTestResult(start, measuredCompileTimingProjectionExecution(start, workloadIDs))
	observations, err := remoteCompileTimingObservations(result)
	if err != nil {
		t.Fatalf("remoteCompileTimingObservations() error = %v", err)
	}
	if len(observations) != 1 {
		t.Fatalf("compile timing observations = %d, want one shared-group row", len(observations))
	}
	assertSharedCompileTimingObservation(t, observations[0], start)
}

// compileTimingProjectionSelectorIDs 构造大量同语义 selector，验证组级行不会按成员复制。
func compileTimingProjectionSelectorIDs(t *testing.T, count int) []gate.GateID {
	t.Helper()
	workloadIDs := make([]gate.GateID, 0, count)
	for index := 0; index < count; index++ {
		workload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/archtest", fmt.Sprintf("TestSharedCompile%03d", index), 1)
		if err != nil {
			t.Fatalf("build selector %d: %v", index, err)
		}
		workloadIDs = append(workloadIDs, gate.GateID(workload.ID))
	}
	return workloadIDs
}

// assertSharedCompileTimingObservation 锁定共享编译行的身份、真实区间和 measured/raw 证据。
func assertSharedCompileTimingObservation(t *testing.T, observation gate.CompileTimingObservation, start time.Time) {
	t.Helper()
	if observation.Identity.PackageTarget != "./internal/archtest" || observation.Identity.SemanticKey != gate.CompileGroupSemanticGoTestNormal {
		t.Fatalf("compile timing identity = %+v, want package and normal Go test semantic", observation.Identity)
	}
	if observation.DurationMS != 25 || !observation.StartedAt.Equal(start) || !observation.CompletedAt.Equal(start.Add(25*time.Millisecond)) {
		t.Fatalf("compile timing interval = %+v, want measured 25ms interval", observation)
	}
	if observation.Measurement != cicontract.ObservationMeasured || observation.Aggregation != cicontract.TimingAggregationRaw {
		t.Fatalf("compile timing evidence = measurement %q aggregation %q, want measured/raw", observation.Measurement, observation.Aggregation)
	}
}

func TestRemoteCompileTimingProjectionRequiresIdentityAndMeasuredPhase(t *testing.T) {
	start := time.UnixMilli(11_000).UTC()
	workload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/archtest", "TestCompileTimingIdentity", 1)
	if err != nil {
		t.Fatal(err)
	}
	execution := measuredCompileTimingProjectionExecution(start, []gate.GateID{gate.GateID(workload.ID)})
	cases := map[string]func(*RunResult){
		"platform":  func(result *RunResult) { result.Platform = "" },
		"runner":    func(result *RunResult) { result.RunnerIdentityDigest = "" },
		"toolchain": func(result *RunResult) { result.ToolchainDigest = "" },
		"phase": func(result *RunResult) {
			result.Shards[0].Report.CompileGroupExecutions[0].Phase = cicontract.TimingTestBody
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			result := remoteCompileTimingProjectionTestResult(start, execution)
			mutate(&result)
			if _, err := remoteCompileTimingObservations(result); err == nil {
				t.Fatal("remoteCompileTimingObservations() accepted incomplete compile identity or phase")
			}
		})
	}
}

// TestRunResultCompileTimingIdentityFieldsHaveStableJSONTags 锁定计时身份字段的 JSON 名称。
func TestRunResultCompileTimingIdentityFieldsHaveStableJSONTags(t *testing.T) {
	resultType := reflect.TypeFor[RunResult]()
	for name, tag := range map[string]string{
		"Platform":             "platform",
		"RunnerIdentityDigest": "runner_identity_digest",
		"ToolchainDigest":      "toolchain_digest",
	} {
		field, found := resultType.FieldByName(name)
		if !found || field.Tag.Get("json") != tag {
			t.Fatalf("RunResult %s field = %#v, want json tag %q", name, field, tag)
		}
	}
}

// TestNewRunResultCopiesCompileTimingIdentityFromRunInput 确认结果身份逐字继承已校验输入。
func TestNewRunResultCopiesCompileTimingIdentityFromRunInput(t *testing.T) {
	coordinator := &Coordinator{now: func() time.Time { return time.UnixMilli(12_000).UTC() }}
	input := RunInput{
		Platform:             "linux/amd64",
		RunnerIdentityDigest: "sha256:" + strings.Repeat("r", 64),
		ToolchainDigest:      "sha256:" + strings.Repeat("t", 64),
	}
	plan := gate.GatePlan{Profile: gate.ProfileLocalFast, Source: gate.SourceSpec{SourceTreeSHA: strings.Repeat("s", 40)}}
	result := coordinator.newRunResult(plan, "catalog-digest", gate.CIEntrypoint{ID: gate.CIEntrypointGitPreCommit}, input, "job-identity")
	if result.Platform != input.Platform || result.RunnerIdentityDigest != input.RunnerIdentityDigest || result.ToolchainDigest != input.ToolchainDigest {
		t.Fatalf("RunResult compile timing identity = platform=%q runner=%q toolchain=%q, want exact RunInput values", result.Platform, result.RunnerIdentityDigest, result.ToolchainDigest)
	}
}

func remoteCompileTimingProjectionTestResult(start time.Time, execution gate.CompileGroupExecution) RunResult {
	digest := "sha256:" + strings.Repeat("b", 64)
	return RunResult{
		JobID: "job-compile-timing-projection", Platform: "linux/amd64", RunnerIdentityDigest: digest, ToolchainDigest: digest,
		ExecutionMode: gate.DurationExecutionModeNormal,
		Shards: []ShardResult{{
			ShardIdentity: digest, ResourceClass: "small", Resources: eci.Resources{CPU: 2, MemoryGiB: 4},
			Report: gate.PlanExecutionReport{CompileGroupExecutions: []gate.CompileGroupExecution{execution}},
		}},
		StartedAt: start, CompletedAt: start.Add(time.Second),
	}
}

func measuredCompileTimingProjectionExecution(start time.Time, workloadIDs []gate.GateID) gate.CompileGroupExecution {
	digest := "sha256:" + strings.Repeat("a", 64)
	return gate.CompileGroupExecution{
		Scope: cicontract.TimingScopeCompileGroup, Phase: cicontract.TimingTestBinaryCompile,
		GroupID: digest, ArtifactKey: digest, PackageTarget: "./internal/archtest", WorkloadIDs: workloadIDs,
		StartedAtUnixMS: start.UnixMilli(), CompletedAtUnixMS: start.Add(25 * time.Millisecond).UnixMilli(), DurationMS: 25,
		ArtifactSHA256: digest, ArtifactSize: 128, CacheHits: 1, Status: gate.ResultStatusPassed, ExitCode: 0,
		CompileCommandDigest: digest, ProfileDigest: digest, ResourceClassID: "small",
	}
}
