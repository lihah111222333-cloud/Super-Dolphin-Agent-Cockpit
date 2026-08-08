package gate

import (
	"strings"
	"testing"
	"time"
)

func TestCompileOwnerWithoutHistoryKeepsSmallResourceAndOneBootstrapCost(t *testing.T) {
	context := testPlanningContext()
	input := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("a", 64))
	workloads, inputs := compileTestWorkloads(t, input.PackageTarget, []string{"TestNoHistoryA", "TestNoHistoryB"}, 60_000, input)
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	emptyCompileIndex, err := BuildCompileTimingIndex(nil)
	if err != nil {
		t.Fatal(err)
	}
	index.CompileTimingIndex = emptyCompileIndex
	_, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(workloads...), index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("compile groups = %#v, want one owner group", groups)
	}
	if groups[0].ResourceClassID != "small" {
		t.Fatalf("resource class = %q, want small without history", groups[0].ResourceClassID)
	}
	if groups[0].CompileEstimateMS != compileParentBootstrapEstimateMS {
		t.Fatalf("compile estimate = %d, want deterministic bootstrap %d", groups[0].CompileEstimateMS, compileParentBootstrapEstimateMS)
	}
	if groups[0].EstimatedDurationMS != groups[0].CompileEstimateMS+groups[0].BodyEstimateMS {
		t.Fatalf("group total = %d, want one shared compile plus body", groups[0].EstimatedDurationMS)
	}
}

func TestCompileOwnerSmallHistory240053UpgradesAndSharesCostOnce(t *testing.T) {
	context := testPlanningContext()
	input := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("b", 64))
	workloads, inputs := compileTestWorkloads(t, input.PackageTarget, []string{"TestMeasuredA", "TestMeasuredB", "TestMeasuredC"}, 1_000, input)
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	compileIndex, err := BuildCompileTimingIndex([]CompileTimingSample{{
		Identity: CompileTimingIdentity{
			PackageTarget: input.PackageTarget, SemanticKey: input.SemanticKey,
			Platform: context.Platform, RunnerIdentityDigest: context.Runner,
			ToolchainDigest: context.Toolchain, ExecutionMode: DurationExecutionModeNormal,
			ResourceClassID: "small", ResourceCPU: 2, ResourceMemoryGiB: 4,
		},
		DurationMS: 240_053, AcceptedGeneration: 3, JobID: "compile-history-240053",
		StartedAt: time.UnixMilli(1_000).UTC(), CompletedAt: time.UnixMilli(241_053).UTC(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	index.CompileTimingIndex = compileIndex
	_, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(workloads...), index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("compile groups = %#v, want one owner group", groups)
	}
	if groups[0].ResourceClassID != "maximum" {
		t.Fatalf("resource class = %q, want maximum after 240053ms small sample", groups[0].ResourceClassID)
	}
	if groups[0].CompileEstimateMS != 240_053 {
		t.Fatalf("compile estimate = %d, want one 240053ms shared compile", groups[0].CompileEstimateMS)
	}
	if groups[0].EstimatedDurationMS != 240_053+groups[0].BodyEstimateMS {
		t.Fatalf("group total = %d, compile cost was duplicated or body changed", groups[0].EstimatedDurationMS)
	}
}

func TestCompileOwnerUpdaterFastToMediumKeepsMediumOnFasterMediumSample(t *testing.T) {
	context := testPlanningContext()
	input := compileTestInput("./cmd/super-dolphin-updater", "sha256:"+strings.Repeat("d", 64))
	workloads, inputs := compileTestWorkloads(t, input.PackageTarget, []string{"TestUpdaterOwner"}, 1_000, input)
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	compileIndex, err := BuildCompileTimingIndex([]CompileTimingSample{
		{
			Identity: CompileTimingIdentity{
				PackageTarget: input.PackageTarget, SemanticKey: input.SemanticKey,
				Platform: context.Platform, RunnerIdentityDigest: context.Runner,
				ToolchainDigest: context.Toolchain, ExecutionMode: DurationExecutionModeNormal,
				ResourceClassID: "small", ResourceCPU: 2, ResourceMemoryGiB: 4,
			},
			DurationMS: 7_139, AcceptedGeneration: 3, JobID: "compile-updater-small-7139",
			StartedAt: time.UnixMilli(1_000).UTC(), CompletedAt: time.UnixMilli(8_139).UTC(),
		},
		{
			Identity: CompileTimingIdentity{
				PackageTarget: input.PackageTarget, SemanticKey: input.SemanticKey,
				Platform: context.Platform, RunnerIdentityDigest: context.Runner,
				ToolchainDigest: context.Toolchain, ExecutionMode: DurationExecutionModeNormal,
				ResourceClassID: "medium", ResourceCPU: 4, ResourceMemoryGiB: 8,
			},
			DurationMS: 4_733, AcceptedGeneration: 3, JobID: "compile-updater-medium-4733",
			StartedAt: time.UnixMilli(10_000).UTC(), CompletedAt: time.UnixMilli(14_733).UTC(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	index.CompileTimingIndex = compileIndex
	_, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(workloads...), index, inputs)
	if err != nil {
		t.Fatalf("updater owner fixed-point planning: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("compile groups = %#v, want one updater owner group", groups)
	}
	if groups[0].ResourceClassID != "medium" {
		t.Fatalf("resource class = %q, want medium after fast-to-medium transition", groups[0].ResourceClassID)
	}
	if groups[0].CompileEstimateMS != 4_733 {
		t.Fatalf("compile estimate = %d, want medium-tier sample 4733", groups[0].CompileEstimateMS)
	}
}

func TestCompileOwnerCalibrationNeverReclassifiesResource(t *testing.T) {
	context := testCalibrationPlanningContext()
	input := compileTestInput("./internal/archtest", "sha256:"+strings.Repeat("c", 64))
	workloads, inputs := compileTestWorkloads(t, input.PackageTarget, []string{"TestCalibrationA", "TestCalibrationB"}, 80_000, input)
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	compileIndex, err := BuildCompileTimingIndex([]CompileTimingSample{{
		Identity: CompileTimingIdentity{
			PackageTarget: input.PackageTarget, SemanticKey: input.SemanticKey,
			Platform: context.Platform, RunnerIdentityDigest: context.Runner,
			ToolchainDigest: context.Toolchain, ExecutionMode: DurationExecutionModeCalibration,
			ResourceClassID: context.CalibrationResourceClassID, ResourceCPU: 4, ResourceMemoryGiB: 8,
		},
		DurationMS: 240_053, AcceptedGeneration: 3, JobID: "compile-history-calibration",
		StartedAt: time.UnixMilli(2_000).UTC(), CompletedAt: time.UnixMilli(242_053).UTC(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	index.CompileTimingIndex = compileIndex
	_, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(workloads...), index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].ResourceClassID != context.CalibrationResourceClassID {
		t.Fatalf("calibration groups = %#v, want fixed resource %q", groups, context.CalibrationResourceClassID)
	}
}

// testCompileTimingIndex 构造一条已通过、同环境、同资源的 compile history。
func testCompileTimingIndex(t *testing.T, input CompileGroupInput, context PlanningContext, classID string, cpu, memoryGiB float64, durationMS int64) CompileTimingIndex {
	t.Helper()
	started := time.UnixMilli(10_000).UTC()
	index, err := BuildCompileTimingIndex([]CompileTimingSample{{
		Identity: CompileTimingIdentity{
			PackageTarget: input.PackageTarget, SemanticKey: input.SemanticKey,
			Platform: context.Platform, RunnerIdentityDigest: context.Runner,
			ToolchainDigest: context.Toolchain, ExecutionMode: DurationExecutionModeNormal,
			ResourceClassID: classID, ResourceCPU: cpu, ResourceMemoryGiB: memoryGiB,
		},
		DurationMS: durationMS, AcceptedGeneration: 3, JobID: "compile-history-fixture",
		StartedAt: started, CompletedAt: started.Add(time.Duration(durationMS) * time.Millisecond),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return index
}
