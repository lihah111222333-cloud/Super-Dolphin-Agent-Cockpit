package remoteci

import (
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
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
	samples, err := remoteShardDurationSamples(
		map[string]gate.Workload{workload.ID: workload},
		map[gate.GateID]struct{}{workloadID: {}},
		[]gate.PlanGateExecution{execution},
		input,
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
	input := RunInput{Calibration: true, Platform: "linux/amd64", RunnerIdentityDigest: "runner-v1", ToolchainDigest: "toolchain-v1"}
	samples, err := remoteCalibrationParentDurationSamples(
		gate.WorkloadCatalog{Version: 1, Workloads: []gate.Workload{first, second}}, observed, input,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertRemoteCalibrationParentSample(t, samples, parent)
	input.Calibration = false
	if samples, err := remoteCalibrationParentDurationSamples(
		gate.WorkloadCatalog{Version: 1, Workloads: []gate.Workload{first, second}}, observed, input,
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

func TestRemoteOptimizationWarningsAreAdvisoryForSlowPassAndFailure(t *testing.T) {
	warnings := remoteOptimizationWarnings([]gate.DurationSample{
		{Bucket: gate.DurationBucket{WorkloadID: "slow-pass"}, Succeeded: true, DurationMS: gate.FullCITargetDurationMS + 1},
		{Bucket: gate.DurationBucket{WorkloadID: "on-target"}, Succeeded: true, DurationMS: gate.FullCITargetDurationMS},
		{Bucket: gate.DurationBucket{WorkloadID: "slow-child"}, Succeeded: true, DurationMS: gate.FullCITargetDurationMS + 2, ParentWorkloadID: "parent"},
		{Bucket: gate.DurationBucket{WorkloadID: "slow-fail"}, Succeeded: false, DurationMS: gate.FullCITargetDurationMS + 3},
	})
	if len(warnings) != 2 {
		t.Fatalf("optimization warnings = %#v, want one per slow parent workload", warnings)
	}
	if warnings[0] != "CI optimization warning: workload \"slow-fail\" failed in 100003ms (target 100000ms); optimize or split this shard" {
		t.Fatalf("failure warning = %q", warnings[0])
	}
	if warnings[1] != "CI optimization warning: workload \"slow-pass\" passed in 100001ms (target 100000ms); optimize or split this shard" {
		t.Fatalf("pass warning = %q", warnings[1])
	}
}
