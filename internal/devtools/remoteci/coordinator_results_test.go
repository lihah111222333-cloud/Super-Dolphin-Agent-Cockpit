package remoteci

import (
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

func TestRemoteShardDurationSamplesIncludeStructuredGoTestTargets(t *testing.T) {
	workload, err := gate.NewGoPackageWorkload(
		gate.GateIDBackendTestWithGuard,
		"./internal/module/turn",
		1000,
	)
	if err != nil {
		t.Fatal(err)
	}
	workloadID := gate.GateID(workload.ID)
	startedAt := time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC)
	execution := gate.PlanGateExecution{
		GateID: workloadID, Status: gate.ResultStatusPassed, ExitCode: 0,
		StartedAt: startedAt, CompletedAt: startedAt.Add(time.Second),
		TestTimings: []gate.GoTestTiming{
			{Name: "TestOne/subcase", Status: gate.GoTestStatusPass, DurationMS: 125},
		},
	}
	input := RunInput{
		Platform: "linux/amd64", RunnerIdentityDigest: "runner-v1", ToolchainDigest: "toolchain-v1",
	}
	inputDigests := map[string]string{workload.ID: "sha256:" + strings.Repeat("a", 64)}
	samples, err := remoteShardDurationSamples(
		map[string]gate.Workload{workload.ID: workload},
		map[gate.GateID]struct{}{workloadID: {}},
		[]gate.PlanGateExecution{execution},
		input,
		inputDigests,
		ShardResult{ResourceClass: "small", Resources: eci.Resources{CPU: 2, MemoryGiB: 4}},
	)
	if err != nil {
		t.Fatalf("remoteShardDurationSamples() error = %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("duration samples = %d, want package plus test", len(samples))
	}
	assertStructuredGoTestDurationSample(t, samples[1], workload)
	if err := gate.ValidateDurationLedger(gate.DurationLedger{Version: 1, Samples: samples}); err != nil {
		t.Fatalf("ValidateDurationLedger() error = %v", err)
	}
}

func assertStructuredGoTestDurationSample(
	t *testing.T,
	sample gate.DurationSample,
	parent gate.Workload,
) {
	t.Helper()
	if sample.TargetKind != gate.WorkloadKindGoTest ||
		sample.ParentWorkloadID != parent.ID ||
		sample.ParentCommandDigest != parent.CommandDigest ||
		sample.TargetName != "TestOne/subcase" ||
		sample.TargetStatus != gate.GoTestStatusPass ||
		sample.DurationMS != 125 {
		t.Fatalf("test duration sample = %#v", sample)
	}
}

func TestRemoteAtomicGoTestDurationUsesPackageParentIdentity(t *testing.T) {
	parent, err := gate.NewGoPackageWorkload(
		gate.GateIDBackendTestWithGuard,
		"./internal/module/turn",
		1000,
	)
	if err != nil {
		t.Fatal(err)
	}
	workload, err := gate.NewGoTestWorkload(
		gate.GateIDBackendTestWithGuard,
		"./internal/module/turn",
		"TestRedact",
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, time.July, 28, 1, 0, 0, 0, time.UTC)
	samples, err := remoteGoTestDurationSamples(
		workload,
		gate.PlanGateExecution{
			GateID: gate.GateID(workload.ID), Status: gate.ResultStatusPassed, ExitCode: 0,
			StartedAt: startedAt, CompletedAt: startedAt.Add(100 * time.Millisecond),
			TestTimings: []gate.GoTestTiming{{
				Name: "TestRedact", Status: gate.GoTestStatusPass, DurationMS: 25,
			}},
		},
		RunInput{
			Platform: "linux/amd64", RunnerIdentityDigest: "runner-v1", ToolchainDigest: "toolchain-v1",
		},
		map[string]string{workload.ID: "sha256:" + strings.Repeat("b", 64)},
		gate.DurationBucket{
			InputDigest: "sha256:" + strings.Repeat("b", 64), ExecutionMode: gate.DurationExecutionModeNormal,
			ResourceClassID: "small", ResourceCPU: 2, ResourceMemoryGiB: 4,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || samples[0].ParentWorkloadID != parent.ID ||
		samples[0].ParentCommandDigest != parent.CommandDigest {
		t.Fatalf("atomic test duration sample = %#v, want package parent %#v", samples, parent)
	}
}

func TestRemoteCalibrationParentDurationSamplesAggregateAtomicGoTests(t *testing.T) {
	parent := mustRemoteGoPackageWorkload(t, "./internal/module/turn")
	first := mustRemoteGoTestWorkload(t, "./internal/module/turn", "TestOne")
	second := mustRemoteGoTestWorkload(t, "./internal/module/turn", "TestTwo")
	startedAt := time.Date(2026, time.July, 28, 2, 0, 0, 0, time.UTC)
	observed := map[string]gate.PlanGateExecution{
		first.ID:  {GateID: gate.GateID(first.ID), Status: gate.ResultStatusPassed, StartedAt: startedAt, CompletedAt: startedAt.Add(40 * time.Millisecond)},
		second.ID: {GateID: gate.GateID(second.ID), Status: gate.ResultStatusPassed, StartedAt: startedAt, CompletedAt: startedAt.Add(60 * time.Millisecond)},
	}
	input := RunInput{Calibration: true, Platform: "linux/amd64", RunnerIdentityDigest: "runner-v1", ToolchainDigest: "toolchain-v1", CalibrationResource: shardresource.Class{ID: "calibration", VCPU: 4, MemoryGiB: 8}}
	inputDigests := map[string]string{
		first.ID:  "sha256:" + strings.Repeat("c", 64),
		second.ID: "sha256:" + strings.Repeat("d", 64),
	}
	samples, err := remoteCalibrationParentDurationSamples(
		gate.WorkloadCatalog{Version: 1, Workloads: []gate.Workload{first, second}}, observed, input, inputDigests,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertRemoteCalibrationParentSample(t, samples, parent)
	delete(observed, second.ID)
	partial, err := remoteCalibrationParentDurationSamples(
		gate.WorkloadCatalog{Version: 1, Workloads: []gate.Workload{first, second}}, observed, input, inputDigests,
	)
	if err != nil || len(partial) != 0 {
		t.Fatalf("partial fresh calibration parent samples = %#v, error = %v", partial, err)
	}
	input.Calibration = false
	if samples, err := remoteCalibrationParentDurationSamples(
		gate.WorkloadCatalog{Version: 1, Workloads: []gate.Workload{first, second}}, observed, input, inputDigests,
	); err != nil || len(samples) != 0 {
		t.Fatalf("non-calibration parent samples = %#v, error = %v", samples, err)
	}
}

func mustRemoteGoPackageWorkload(t *testing.T, packagePath string) gate.Workload {
	t.Helper()
	workload, err := gate.NewGoPackageWorkload(gate.GateIDBackendTestWithGuard, packagePath, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return workload
}

func mustRemoteGoTestWorkload(t *testing.T, packagePath, testName string) gate.Workload {
	t.Helper()
	workload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, packagePath, testName, 100)
	if err != nil {
		t.Fatal(err)
	}
	return workload
}

func assertRemoteCalibrationParentSample(t *testing.T, samples []gate.DurationSample, parent gate.Workload) {
	t.Helper()
	if len(samples) != 1 || samples[0].Bucket.WorkloadID != parent.ID ||
		samples[0].Bucket.CommandDigest != parent.CommandDigest || !samples[0].Succeeded || samples[0].DurationMS != 100 {
		t.Fatalf("calibration parent samples = %#v", samples)
	}
}

func TestCompleteRemoteRunExcludesCalibrationParentAggregateFromOptimizationWarnings(t *testing.T) {
	first := mustRemoteGoTestWorkload(t, "./internal/module/turn", "TestSlow")
	second := mustRemoteGoTestWorkload(t, "./internal/module/turn", "TestFast")
	startedAt := time.Date(2026, time.July, 28, 3, 0, 0, 0, time.UTC)
	shardIdentity := "shard-structured-warning"
	firstExecution := gate.PlanGateExecution{
		GateID: gate.GateID(first.ID), ShardIdentity: shardIdentity, Status: gate.ResultStatusPassed, ExitCode: 0,
		StartedAt: startedAt, CompletedAt: startedAt.Add(gate.FullCITargetDuration + time.Millisecond),
		ExecutionProfile: gate.ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: gate.CacheObservationMiss, CacheMeasurement: "measured", CacheMissCount: 1, StartupMS: 1, TestBodyMS: gate.FullCITargetDuration.Milliseconds(), TotalMS: gate.FullCITargetDuration.Milliseconds() + 1},
	}
	secondExecution := gate.PlanGateExecution{
		GateID: gate.GateID(second.ID), ShardIdentity: shardIdentity, Status: gate.ResultStatusPassed, ExitCode: 0,
		StartedAt: startedAt, CompletedAt: startedAt.Add(2 * time.Millisecond),
		ExecutionProfile: gate.ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: gate.CacheObservationMiss, CacheMeasurement: "measured", CacheMissCount: 1, StartupMS: 1, TestBodyMS: 1, TotalMS: 2},
	}
	catalog := gate.WorkloadCatalog{Version: 1, Workloads: []gate.Workload{first, second}}
	observed := map[string]gate.PlanGateExecution{first.ID: firstExecution, second.ID: secondExecution}
	shards := []ShardResult{{
		ShardIdentity: shardIdentity, ContainerStatus: "Succeeded",
		ExecutedWorkloads: []gate.GateID{gate.GateID(first.ID), gate.GateID(second.ID)},
		Report:            gate.PlanExecutionReport{Gates: []gate.PlanGateExecution{firstExecution, secondExecution}},
		ResourceClass:     "calibration", Resources: eci.Resources{CPU: 4, MemoryGiB: 8},
		ECIWaitStartedAt: startedAt.Add(-10 * time.Millisecond), ECIWaitCompletedAt: startedAt.Add(-8 * time.Millisecond),
		ECITerminalAt: firstExecution.CompletedAt,
		MaterializationTiming: gate.ShardMaterializationTiming{
			Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: shardIdentity,
			Source:           gate.MaterializationPhaseTiming{StartedAtUnixMS: startedAt.Add(-7 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: startedAt.Add(-6 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
			CandidateCompile: gate.MaterializationPhaseTiming{StartedAtUnixMS: startedAt.Add(-5 * time.Millisecond).UnixMilli(), CompletedAtUnixMS: startedAt.Add(-4 * time.Millisecond).UnixMilli(), MaterializeMS: 1},
		},
	}}
	result, err := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{}).completeRemoteRunWithExecutionCatalog(
		catalog, catalog,
		RunInput{
			Calibration: true, Platform: "linux/amd64", RunnerIdentityDigest: "runner-v1", ToolchainDigest: "toolchain-v1",
			CalibrationResource: shardresource.Class{ID: "calibration", VCPU: 4, MemoryGiB: 8},
			WorkloadInputDigests: map[string]string{
				first.ID: "sha256:" + strings.Repeat("e", 64), second.ID: "sha256:" + strings.Repeat("f", 64),
			},
		},
		shards,
		observed, observed,
		RunResult{JobID: "job-structured-workload-warning", AgentTokenDigest: testRemoteAgentTokenDigest, AcceptedGeneration: 1},
	)
	if err != nil {
		t.Fatalf("completeRemoteRun() error = %v", err)
	}
	if result.Status != gate.ResultStatusPassed || len(result.TimingWarnings) != 1 ||
		result.TimingWarnings[0].EvidenceKind != cicontract.TimingWarningEvidenceTotal ||
		len(result.OptimizationWarnings) != 1 ||
		result.OptimizationWarnings[0] != result.TimingWarnings[0].WarningText {
		t.Fatalf("structured target warnings = %#v human=%#v status=%s", result.TimingWarnings, result.OptimizationWarnings, result.Status)
	}
	parentIdentity := "workload \"" + string(gate.GateIDBackendTestWithGuard) + "\""
	for _, warning := range result.OptimizationWarnings {
		if strings.Contains(warning, parentIdentity) {
			t.Fatalf("optimization warning references synthetic calibration parent: %q", warning)
		}
	}
	if len(result.DurationSamples) != 3 {
		t.Fatalf("duration samples = %#v, want two actual workloads plus one calibration parent aggregate", result.DurationSamples)
	}
}

func TestRemoteDurationResourceIdentityUsesThreeNormalReceiptTiers(t *testing.T) {
	input := RunInput{
		Platform: "linux/amd64", RunnerIdentityDigest: "runner-v1", ToolchainDigest: "toolchain-v1",
		// A normal run must not accidentally read this calibration selection.
		CalibrationResource: shardresource.Class{ID: "calibration", VCPU: 4, MemoryGiB: 8},
	}
	tests := []struct {
		name        string
		class       string
		cpu, memory float64
	}{
		{name: "small", class: "small", cpu: 2, memory: 4},
		{name: "medium", class: "medium", cpu: 4, memory: 8},
		{name: "maximum", class: "maximum", cpu: 8, memory: 16},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resource, err := remoteDurationResourceIdentity(input, ShardResult{
				ResourceClass: test.class,
				Resources:     eci.Resources{CPU: test.cpu, MemoryGiB: test.memory},
			})
			if err != nil {
				t.Fatalf("remoteDurationResourceIdentity() error = %v", err)
			}
			if resource.ExecutionMode != gate.DurationExecutionModeNormal ||
				resource.ResourceClassID != test.class || resource.ResourceCPU != test.cpu || resource.ResourceMemoryGiB != test.memory {
				t.Fatalf("normal duration resource = %#v, want %s %.gC/%.gGiB", resource, test.class, test.cpu, test.memory)
			}
		})
	}
}

func TestRemoteDurationResourceIdentityRejectsCalibrationManifestOrPartialReceiptDrift(t *testing.T) {
	input := RunInput{
		Calibration: true, Platform: "linux/amd64", RunnerIdentityDigest: "runner-v1", ToolchainDigest: "toolchain-v1",
		CalibrationResource: shardresource.Class{ID: "calibration", VCPU: 4, MemoryGiB: 8},
	}
	valid, err := remoteDurationResourceIdentity(input, ShardResult{
		ResourceClass: "calibration", Resources: eci.Resources{CPU: 4, MemoryGiB: 8},
	})
	if err != nil {
		t.Fatalf("valid calibration resource rejected: %v", err)
	}
	if valid.ExecutionMode != gate.DurationExecutionModeCalibration || valid.ResourceClassID != "calibration" ||
		valid.ResourceCPU != 4 || valid.ResourceMemoryGiB != 8 {
		t.Fatalf("valid calibration resource = %#v", valid)
	}
	for name, shard := range map[string]ShardResult{
		"normal compile tier leaked into shard class": {
			ResourceClass: "medium", Resources: eci.Resources{CPU: 4, MemoryGiB: 8},
		},
		"partial zero provider receipt": {
			ResourceClass: "calibration", Resources: eci.Resources{CPU: 0, MemoryGiB: 0},
		},
	} {
		t.Run(name, func(t *testing.T) {
			assertCalibrationDurationReceiptDrift(t, input, shard)
		})
	}
}

func assertCalibrationDurationReceiptDrift(t *testing.T, input RunInput, shard ShardResult) {
	t.Helper()
	_, err := remoteDurationResourceIdentity(input, shard)
	if err == nil || !strings.Contains(err.Error(), "remote CI calibration duration resource receipt drifted") {
		t.Fatalf("remoteDurationResourceIdentity() error = %v, want detailed calibration receipt drift", err)
	}
	if !strings.Contains(err.Error(), "observed") || !strings.Contains(err.Error(), "expected") {
		t.Fatalf("calibration drift error = %v, want observed and expected identities", err)
	}
}
