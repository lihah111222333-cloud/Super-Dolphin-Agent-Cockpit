package gate

import "testing"

func TestOrdinaryCompilePlanningUnitCalibrationUsesFixedResourceClass(t *testing.T) {
	context := testCalibrationPlanningContext()
	unit, err := ordinaryCompilePlanningUnit(PlannedWorkload{
		Workload:            Workload{ID: "frontend:lint"},
		EstimatedDurationMS: 1_000,
		ResourceCPU:         context.CalibrationResourceCPU,
		ResourceMemoryGiB:   context.CalibrationResourceMemoryGiB,
	}, context)
	if err != nil {
		t.Fatal(err)
	}
	if unit.resourceClassID != context.CalibrationResourceClassID {
		t.Fatalf("resource class = %q, want fixed calibration class %q", unit.resourceClassID, context.CalibrationResourceClassID)
	}
}

func TestCompileShardsMeetTargetAllowsSingleIndivisibleCompileGroup(t *testing.T) {
	workloads := []PlannedWorkload{
		{Workload: Workload{ID: "go-test::pkg::TestA"}, EstimatedDurationMS: 80},
		{Workload: Workload{ID: "go-test::pkg::TestB"}, EstimatedDurationMS: 80},
	}
	group := CompileGroup{GroupID: "isolated-group"}
	unit := compilePlanningUnit{workloads: workloads, costMS: 160, group: &group}
	shard := ShardPlan{Workloads: workloads, CompileGroupIDs: []string{group.GroupID}, EstimatedDurationMS: unit.costMS}
	if !compileShardsMeetTarget([]ShardPlan{shard}, 100, []compilePlanningUnit{unit}) {
		t.Fatal("single indivisible compile group was rejected above the target")
	}
	otherGroup := CompileGroup{GroupID: "other-group"}
	otherUnit := compilePlanningUnit{workloads: []PlannedWorkload{{Workload: Workload{ID: "go-test::pkg::TestC"}, EstimatedDurationMS: 160}}, costMS: 160, group: &otherGroup}
	mixed := ShardPlan{Workloads: append(append([]PlannedWorkload(nil), workloads...), otherUnit.workloads...), CompileGroupIDs: []string{group.GroupID, otherGroup.GroupID}, EstimatedDurationMS: 320}
	if compileShardsMeetTarget([]ShardPlan{mixed}, 100, []compilePlanningUnit{unit, otherUnit}) {
		t.Fatal("multiple indivisible compile groups exceeded the target without rejection")
	}
}
