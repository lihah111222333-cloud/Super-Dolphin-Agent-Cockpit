package remoteci

import (
	"context"
	"testing"
)

// TestPreparedRunRefreshesCalibrationPassesBeforeMissBinding 验证 Prepare 与执行
// 之间由校准新增的 exact PASS 会收敛原 MISS，不再绑定 Gate/云资源或创建分片。
func TestPreparedRunRefreshesCalibrationPassesBeforeMissBinding(t *testing.T) {
	_, input := coordinatorReuseFixture(t)
	seed := runCoordinatorFreshWorkloads(t, input)
	clearCoordinatorAllHitExecutionIdentity(&input)
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123457a", nil }
	prepared, err := coordinator.Prepare(context.Background(), input)
	if err != nil || prepared.AllReused() || len(prepared.reuse.cacheMisses) == 0 {
		t.Fatalf("Prepare() prepared=%#v error=%v, want initial MISS", prepared, err)
	}
	seedCoordinatorWorkloadPassEvidence(t, input, seed, nil)
	recovered, err := prepared.RefreshWorkloadPassesAfterCalibration(input.LedgerStore)
	if err != nil {
		t.Fatalf("RefreshWorkloadPassesAfterCalibration() error = %v", err)
	}
	if recovered != len(prepared.reuse.identities) || !prepared.AllReused() || len(prepared.reuse.cacheMisses) != 0 || len(prepared.input.WorkloadCompileGroupInputs) != 0 {
		t.Fatalf("post-calibration reuse = recovered:%d identities:%d all_reused:%t misses:%d compile_inputs:%d", recovered, len(prepared.reuse.identities), prepared.AllReused(), len(prepared.reuse.cacheMisses), len(prepared.input.WorkloadCompileGroupInputs))
	}
	result, err := coordinator.RunPrepared(context.Background(), prepared)
	assertCoordinatorFullReuse(t, result, err, store, runtime)
}
