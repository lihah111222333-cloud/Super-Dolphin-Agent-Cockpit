package gate

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func repeatCompileTimingSample(base CompileTimingSample) []CompileTimingSample {
	samples := make([]CompileTimingSample, 5)
	for index := range samples {
		samples[index] = base
		samples[index].JobID = fmt.Sprintf("%s-%d", base.JobID, index)
		samples[index].StartedAt = base.StartedAt.Add(time.Duration(index+1) * time.Second)
		samples[index].CompletedAt = samples[index].StartedAt.Add(base.CompletedAt.Sub(base.StartedAt))
	}
	return samples
}

func repeatCompileTimingSamples(bases []CompileTimingSample) []CompileTimingSample {
	samples := make([]CompileTimingSample, 0, len(bases)*5)
	for _, base := range bases {
		samples = append(samples, repeatCompileTimingSample(base)...)
	}
	return samples
}

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
	compileIndex, err := BuildCompileTimingIndex(repeatCompileTimingSample(CompileTimingSample{
		Identity: CompileTimingIdentity{
			PackageTarget: input.PackageTarget, SemanticKey: input.SemanticKey,
			Platform: context.Platform, RunnerIdentityDigest: context.Runner,
			ToolchainDigest: context.Toolchain, ExecutionMode: DurationExecutionModeNormal,
			ResourceClassID: "small", ResourceCPU: 2, ResourceMemoryGiB: 4,
		},
		DurationMS: 240_053, AcceptedGeneration: 3, JobID: "compile-history-240053",
		StartedAt: time.UnixMilli(1_000).UTC(), CompletedAt: time.UnixMilli(241_053).UTC(),
	}))
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
	compileIndex, err := BuildCompileTimingIndex(repeatCompileTimingSamples([]CompileTimingSample{
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
	}))
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
	compileIndex, err := BuildCompileTimingIndex(repeatCompileTimingSample(CompileTimingSample{
		Identity: CompileTimingIdentity{
			PackageTarget: input.PackageTarget, SemanticKey: input.SemanticKey,
			Platform: context.Platform, RunnerIdentityDigest: context.Runner,
			ToolchainDigest: context.Toolchain, ExecutionMode: DurationExecutionModeCalibration,
			ResourceClassID: context.CalibrationResourceClassID, ResourceCPU: 4, ResourceMemoryGiB: 8,
		},
		DurationMS: 240_053, AcceptedGeneration: 3, JobID: "compile-history-calibration",
		StartedAt: time.UnixMilli(2_000).UTC(), CompletedAt: time.UnixMilli(242_053).UTC(),
	}))
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

func TestPlannedWorkloadsFromEstimatesKeepsHigherBodyOrCompileOwnerTier(t *testing.T) {
	workload := Workload{ID: "guard:resource-merge", Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("e", 64), BootstrapEstimateMS: 1_000, Shardable: true}
	tests := []struct {
		name                string
		bodyCPU, bodyMemory float64
		hintTier            cicontract.WorkloadResourceTier
		hintClass           string
		hintCPU, hintMemory float64
		wantCPU, wantMemory float64
	}{
		{name: "body slow wins", bodyCPU: 8, bodyMemory: 16, hintTier: cicontract.WorkloadResourceTierFast, hintClass: "small", hintCPU: 2, hintMemory: 4, wantCPU: 8, wantMemory: 16},
		{name: "owner slow wins", bodyCPU: 2, bodyMemory: 4, hintTier: cicontract.WorkloadResourceTierSlow, hintClass: "maximum", hintCPU: 8, hintMemory: 16, wantCPU: 8, wantMemory: 16},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			planned, err := plannedWorkloadsFromEstimates(
				[]shardableWorkloadEstimate{{
					workload: workload, estimateMS: workload.BootstrapEstimateMS,
					resource: durationSampleResource{classID: "body", cpu: test.bodyCPU, memoryGiB: test.bodyMemory},
				}},
				CompileOwnerHints{workload.ID: {
					OwnerKey: workload.ID, SharedCompileEstimateMS: compileParentBootstrapEstimateMS,
					ResourceTier: test.hintTier, ResourceClassID: test.hintClass,
					ResourceCPU: test.hintCPU, ResourceMemoryGiB: test.hintMemory,
				}},
			)
			if err != nil {
				t.Fatalf("plannedWorkloadsFromEstimates() error = %v", err)
			}
			if len(planned) != 1 || planned[0].ResourceCPU != test.wantCPU || planned[0].ResourceMemoryGiB != test.wantMemory {
				t.Fatalf("planned workload = %#v, want %.0fC/%.0fGiB", planned, test.wantCPU, test.wantMemory)
			}
		})
	}
}

func TestPlannedWorkloadsFromEstimatesRejectsCompileOwnerResourceIdentityDrift(t *testing.T) {
	workload := Workload{ID: "guard:resource-identity", Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("f", 64), BootstrapEstimateMS: 1_000, Shardable: true}
	_, err := plannedWorkloadsFromEstimates(
		[]shardableWorkloadEstimate{{
			workload: workload, estimateMS: workload.BootstrapEstimateMS,
			resource: durationSampleResource{classID: "body", cpu: 2, memoryGiB: 4},
		}},
		CompileOwnerHints{workload.ID: {
			OwnerKey: workload.ID, SharedCompileEstimateMS: compileParentBootstrapEstimateMS,
			ResourceTier: cicontract.WorkloadResourceTierSlow, ResourceClassID: "small",
			ResourceCPU: 8, ResourceMemoryGiB: 16,
		}},
	)
	if err == nil || !strings.Contains(err.Error(), "resource identity") {
		t.Fatalf("plannedWorkloadsFromEstimates() error = %v, want fail-fast resource identity guard", err)
	}
}

// testCompileTimingIndex 构造一条已通过、同环境、同资源的 compile history。
func testCompileTimingIndex(t *testing.T, input CompileGroupInput, context PlanningContext, classID string, cpu, memoryGiB float64, durationMS int64) CompileTimingIndex {
	t.Helper()
	started := time.UnixMilli(10_000).UTC()
	base := CompileTimingSample{
		Identity: CompileTimingIdentity{
			PackageTarget: input.PackageTarget, SemanticKey: input.SemanticKey,
			Platform: context.Platform, RunnerIdentityDigest: context.Runner,
			ToolchainDigest: context.Toolchain, ExecutionMode: DurationExecutionModeNormal,
			ResourceClassID: classID, ResourceCPU: cpu, ResourceMemoryGiB: memoryGiB,
		},
		DurationMS: durationMS, AcceptedGeneration: 3, JobID: "compile-history-fixture",
		StartedAt: started, CompletedAt: started.Add(time.Duration(durationMS) * time.Millisecond),
	}
	samples := make([]CompileTimingSample, 5)
	for index := range samples {
		samples[index] = base
		samples[index].JobID = fmt.Sprintf("compile-history-fixture-%d", index)
		samples[index].StartedAt = started.Add(time.Duration(index+1) * time.Second)
		samples[index].CompletedAt = samples[index].StartedAt.Add(time.Duration(durationMS) * time.Millisecond)
	}
	index, err := BuildCompileTimingIndex(samples)
	if err != nil {
		t.Fatal(err)
	}
	return index
}
