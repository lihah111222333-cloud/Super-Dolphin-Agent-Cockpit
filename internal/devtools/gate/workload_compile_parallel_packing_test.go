package gate

import (
	"fmt"
	"testing"
)

func TestParallelRaceResourcePackingUsesLongLowCPUShardsInsteadOfOneShardPerPackage(t *testing.T) {
	units := make([]compilePlanningUnit, 132)
	for index := range units {
		packageTarget := fmt.Sprintf("./internal/parallel/package%03d", index)
		workload, err := NewGoTestWorkload(GateIDBackendTestGuardWithRace, packageTarget, fmt.Sprintf("TestParallel%03d", index), 10_000)
		if err != nil {
			t.Fatal(err)
		}
		group := CompileGroup{
			GroupID: fmt.Sprintf("group-%03d", index), PackageTarget: packageTarget,
			SemanticKey: CompileGroupSemanticGoTestRace, ResourceEstimateSource: "bootstrap",
			EstimatedCPUMilliCores: 500, EstimatedPeakRSSBytes: 1 << 30,
			BatchPlan: []CompileGroupBatch{{BatchID: "batch-000", SelectorIDs: []GateID{GateID(workload.ID)}}},
		}
		cost := int64(10_000)
		if index < 18 {
			cost = 500_000
		}
		planned := PlannedWorkload{Workload: workload, EstimatedDurationMS: cost, ResourceCPU: 4, ResourceMemoryGiB: 8}
		units[index] = compilePlanningUnit{workloads: []PlannedWorkload{planned}, group: &group, costMS: cost, affinityKey: packageTarget, sortID: group.GroupID, resourceClassID: "calibration", estimatedCPUMilliCores: 500, estimatedPeakRSSBytes: 1 << 30}
	}
	shards, err := provenCompileUnitPacking(units, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 17 {
		t.Fatalf("parallel race shard count = %d, want 17 for 132 low-resource groups: %#v", len(shards), shards)
	}
	for _, shard := range shards {
		if shard.EstimatedCPUMilliCores > 4_000 || shard.EstimatedPeakRSSBytes > 8<<30 || len(shard.CompileGroupIDs) > 8 {
			t.Fatalf("parallel race shard exceeds 4C/8GiB resource proof: %#v", shard)
		}
	}
}
