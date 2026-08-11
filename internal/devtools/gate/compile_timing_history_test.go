package gate

import (
	"testing"
	"time"
)

func compileTimingHistoryFixture(identity CompileTimingIdentity, generation uint64, durationMS int64, jobID string) CompileTimingSample {
	started := time.UnixMilli(100_000 + int64(generation)*10_000).UTC()
	return CompileTimingSample{
		Identity: identity, DurationMS: durationMS, AcceptedGeneration: generation, JobID: jobID,
		StartedAt: started, CompletedAt: started.Add(time.Duration(durationMS) * time.Millisecond),
	}
}

func TestCompileTimingIndexUsesRobustThreeGenerationMedian(t *testing.T) {
	identity := CompileTimingIdentity{
		PackageTarget: "./internal/devtools/gate", SemanticKey: CompileGroupSemanticGoTestNormal,
		Platform: "linux/amd64", RunnerIdentityDigest: "runner-v1", ToolchainDigest: "go1.26",
		ExecutionMode: DurationExecutionModeNormal, ResourceClassID: "small", ResourceCPU: 2, ResourceMemoryGiB: 4,
	}
	samples := []CompileTimingSample{
		compileTimingHistoryFixture(identity, 3, 1, "new-a"),
		compileTimingHistoryFixture(identity, 3, 1, "new-b"),
		compileTimingHistoryFixture(identity, 3, 1_000, "new-outlier"),
	}
	index, err := BuildCompileTimingIndex(samples)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := index.EstimateMS(identity)
	if err != nil || !found {
		t.Fatalf("EstimateMS() found=%t err=%v", found, err)
	}
	if got.DurationMS != 6_000 {
		t.Fatalf("single sparse compile estimate = %d, want 6000", got.DurationMS)
	}
	if err := got.Representative.Validate(); err != nil {
		t.Fatalf("representative compile timing sample is not raw-valid: %v", err)
	}
	if got.Representative.DurationMS == got.DurationMS {
		t.Fatal("robust estimate unexpectedly overwrote representative raw duration")
	}
	permuted, err := BuildCompileTimingIndex([]CompileTimingSample{samples[2], samples[0], samples[1]})
	if err != nil {
		t.Fatal(err)
	}
	permutedGot, _, err := permuted.EstimateMS(identity)
	if err != nil {
		t.Fatal(err)
	}
	if permutedGot.DurationMS != got.DurationMS {
		t.Fatalf("permutation changed compile estimate: got %d want %d", permutedGot.DurationMS, got.DurationMS)
	}
}

func TestCompileTimingIndexLatestGenerationWeightAndNineDIdentity(t *testing.T) {
	identity := CompileTimingIdentity{
		PackageTarget: "./internal/devtools/gate", SemanticKey: CompileGroupSemanticGoTestNormal,
		Platform: "linux/amd64", RunnerIdentityDigest: "runner-v1", ToolchainDigest: "go1.26",
		ExecutionMode: DurationExecutionModeNormal, ResourceClassID: "small", ResourceCPU: 2, ResourceMemoryGiB: 4,
	}
	samples := []CompileTimingSample{
		compileTimingHistoryFixture(identity, 3, 100, "g3-a"), compileTimingHistoryFixture(identity, 3, 100, "g3-b"),
		compileTimingHistoryFixture(identity, 2, 10, "g2-a"), compileTimingHistoryFixture(identity, 2, 10, "g2-b"),
		compileTimingHistoryFixture(identity, 1, 1, "g1-a"),
	}
	index, err := BuildCompileTimingIndex(samples)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := index.EstimateMS(identity)
	if err != nil || !found || got.DurationMS != 9_040 {
		t.Fatalf("latest generation weighted estimate = %#v found=%t err=%v, want 9040 after confidence shrink", got, found, err)
	}
	if len(index.AcceptedGenerations) != 3 || len(index.Samples) != 5 {
		t.Fatalf("retained generations=%v samples=%d, want [3 2 1]/5", index.AcceptedGenerations, len(index.Samples))
	}
	for name, mutate := range map[string]func(*CompileTimingIdentity){
		"package":   func(value *CompileTimingIdentity) { value.PackageTarget = "./internal/devtools/other" },
		"semantic":  func(value *CompileTimingIdentity) { value.SemanticKey = CompileGroupSemanticGoTestRace },
		"platform":  func(value *CompileTimingIdentity) { value.Platform = "darwin" },
		"runner":    func(value *CompileTimingIdentity) { value.RunnerIdentityDigest = "runner-v2" },
		"toolchain": func(value *CompileTimingIdentity) { value.ToolchainDigest = "go1.27" },
		"mode": func(value *CompileTimingIdentity) {
			value.ExecutionMode, value.ResourceClassID, value.ResourceCPU, value.ResourceMemoryGiB = DurationExecutionModeCalibration, "calibration", 4, 8
		},
		"class": func(value *CompileTimingIdentity) {
			value.ResourceClassID, value.ResourceCPU, value.ResourceMemoryGiB = "medium", 4, 8
		},
		"cpu": func(value *CompileTimingIdentity) {
			value.ResourceClassID, value.ResourceCPU, value.ResourceMemoryGiB = "medium", 4, 8
		},
		"memory": func(value *CompileTimingIdentity) {
			value.ResourceClassID, value.ResourceCPU, value.ResourceMemoryGiB = "medium", 4, 8
		},
	} {
		mutated := identity
		mutate(&mutated)
		if _, found, err := index.EstimateMS(mutated); err == nil && found {
			t.Fatalf("9D mismatch %q unexpectedly found", name)
		}
	}
}

func TestCompileTimingIndexSparseNewestGenerationShrinksSinglePoint(t *testing.T) {
	identity := CompileTimingIdentity{
		PackageTarget: "./internal/devtools/gate", SemanticKey: CompileGroupSemanticGoTestNormal,
		Platform: "linux/amd64", RunnerIdentityDigest: "runner-v1", ToolchainDigest: "go1.26",
		ExecutionMode: DurationExecutionModeNormal, ResourceClassID: "small", ResourceCPU: 2, ResourceMemoryGiB: 4,
	}
	samples := []CompileTimingSample{compileTimingHistoryFixture(identity, 3, 1_000, "new-outlier")}
	for index := range 5 {
		samples = append(samples, compileTimingHistoryFixture(identity, 2, 100, "old-stable-"+string(rune('a'+index))))
	}
	index, err := BuildCompileTimingIndex(samples)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := index.EstimateMS(identity)
	if err != nil || !found {
		t.Fatalf("EstimateMS() found=%t err=%v", found, err)
	}
	if got.DurationMS != 12_200 {
		t.Fatalf("sparse newest compile estimate = %d, want 12200 after confidence shrink", got.DurationMS)
	}
}
