package remoteci

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestFinalizePreparedCleanupMarksTempRemovalFailure(t *testing.T) {
	remoteCalls := 0
	complete, err := finalizePreparedCleanup("\x00", func() error {
		remoteCalls++
		return nil
	})
	if complete {
		t.Fatal("finalizePreparedCleanup() complete = true, want false")
	}
	if err == nil {
		t.Fatal("finalizePreparedCleanup() error = nil, want temp removal error")
	}
	if remoteCalls != 1 {
		t.Fatalf("remote cleanup calls = %d, want 1", remoteCalls)
	}
}

// TestPreparePartialFailedRunProjectsOnlyStrictMiss 覆盖同一失败 run store 到 Prepare、compile 与分片规划的生产闭环。
func TestPreparePartialFailedRunProjectsOnlyStrictMiss(t *testing.T) {
	runPreparePartialFailedRunProjectsOnlyStrictMiss(t)
}

// TestCoordinatorPrepareAllHitSkipsCompileGroupClosure 验证全命中时
// correctness-only Prepare 不物化 compile closure/profile 输入。
func TestCoordinatorPrepareAllHitSkipsCompileGroupClosure(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)
	input.CandidateGateSourceSHA256 = ""
	input.CandidateGateToolchainSHA256 = ""
	input.ExecutionRunnerImage = ""
	input.ExecutionImageCacheSnapshotID = ""
	input.ImageCacheOnly = false

	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	coordinator.newID = func() (string, error) { return "job-fedcba9876543210fedcba98", nil }
	prepared, err := coordinator.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !prepared.AllReused() {
		t.Fatal("Prepare() allReused = false, want true")
	}
	if got := len(prepared.input.WorkloadCompileGroupInputs); got != 0 {
		t.Fatalf("all-hit compile-group inputs = %d, want none", got)
	}
	result, err := coordinator.RunPrepared(context.Background(), prepared)
	if err != nil {
		t.Fatalf("RunPrepared() all-hit error = %v", err)
	}
	if result.Status != gate.ResultStatusPassed {
		t.Fatalf("RunPrepared() all-hit status = %q, want PASS", result.Status)
	}
}

// TestCoordinatorPreparePartialCompileInputsAreMissScoped 验证 compile
// closure/profile 只覆盖严格 MISS 投影。
func TestCoordinatorPreparePartialCompileInputsAreMissScoped(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	binding := MissExecutionBinding{
		CandidateGateSourceSHA256:     input.CandidateGateSourceSHA256,
		CandidateGateToolchainSHA256:  input.CandidateGateToolchainSHA256,
		ExecutionRunnerImage:          input.ExecutionRunnerImage,
		ExecutionImageCacheSnapshotID: input.ExecutionImageCacheSnapshotID,
		ImageCacheOnly:                input.ImageCacheOnly,
	}
	input.CandidateGateSourceSHA256 = ""
	input.CandidateGateToolchainSHA256 = ""
	input.ExecutionRunnerImage = ""
	input.ExecutionImageCacheSnapshotID = ""
	input.ImageCacheOnly = false

	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	prepared, err := coordinator.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if prepared.AllReused() {
		t.Fatal("Prepare() allReused = true, want strict MISS")
	}
	for workloadID := range prepared.input.WorkloadCompileGroupInputs {
		if !containsCoordinatorGateID(prepared.reuse.cacheMisses, gate.GateID(workloadID)) {
			t.Fatalf("compile-group input for %q is not a strict MISS; misses=%v", workloadID, prepared.reuse.cacheMisses)
		}
	}
	if err := coordinator.BindPreparedMissExecution(context.Background(), prepared, binding); err != nil {
		t.Fatalf("BindPreparedMissExecution() error = %v", err)
	}
	if prepared.input.CandidateGateSourceSHA256 != binding.CandidateGateSourceSHA256 ||
		prepared.input.ExecutionImageCacheSnapshotID != binding.ExecutionImageCacheSnapshotID {
		t.Fatalf("bound MISS input = %#v", prepared.input)
	}
}

// TestCoordinatorPrepareAllHitRejectsMissExecutionIdentity 验证 all-hit 不接受调用方预先注入的 MISS-only 身份。
func TestCoordinatorPrepareAllHitRejectsMissExecutionIdentity(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)
	clearCoordinatorAllHitExecutionIdentity(&input)
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, test := range []struct {
		name   string
		mutate func(*RunInput)
	}{
		{name: "candidate gate source", mutate: func(in *RunInput) { in.CandidateGateSourceSHA256 = digest }},
		{name: "candidate gate toolchain", mutate: func(in *RunInput) { in.CandidateGateToolchainSHA256 = digest }},
		{name: "execution runner image", mutate: func(in *RunInput) { in.ExecutionRunnerImage = "registry.example/runtime:miss" }},
		{name: "execution image cache snapshot", mutate: func(in *RunInput) { in.ExecutionImageCacheSnapshotID = "snapshot-miss" }},
		{name: "image cache only mode", mutate: func(in *RunInput) { in.ImageCacheOnly = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := input
			test.mutate(&changed)
			coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
			if _, err := coordinator.Prepare(context.Background(), changed); err == nil || !strings.Contains(err.Error(), "all-hit") {
				t.Fatalf("Prepare() all-hit identity error = %v, want fail-fast", err)
			}
		})
	}
}
