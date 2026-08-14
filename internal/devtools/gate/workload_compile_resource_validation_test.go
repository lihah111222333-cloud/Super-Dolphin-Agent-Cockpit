package gate

import (
	"slices"
	"strings"
	"testing"
)

func TestStoredCompilePlanRejectsEligibleSerialMultiGroupShard(t *testing.T) {
	plan, catalog := storedEligibleMultiGroupFixture(t)
	plan = mergeStoredEligibleSerialGroupShards(t, plan)
	if _, _, err := validateStoredCompileGroups(plan, catalog); err == nil || !strings.Contains(err.Error(), "must reference exactly one compile group") {
		t.Fatalf("eligible serial multi-group shard validation error = %v", err)
	}
}

func TestStoredCompilePlanRejectsCompileGroupResourceTampering(t *testing.T) {
	plan, catalog := storedEligibleMultiGroupFixture(t)
	plan.CompileGroups[0].ResourceClassID = "maximum"
	rebindStoredCompileGroupID(t, &plan, 0)
	if _, _, err := validateStoredCompileGroups(plan, catalog); err == nil || !strings.Contains(err.Error(), "resource class") {
		t.Fatalf("tampered compile group class error = %v", err)
	}
	plan, catalog = storedEligibleMultiGroupFixture(t)
	plan.Shards[0].Workloads[0].ResourceCPU = 8
	plan.Shards[0].Workloads[0].ResourceMemoryGiB = 16
	if _, _, err := validateStoredCompileGroups(plan, catalog); err == nil || !strings.Contains(err.Error(), "resource") {
		t.Fatalf("tampered member resource error = %v", err)
	}
}

func rebindStoredCompileGroupID(t *testing.T, plan *WorkloadExecutionPlan, groupIndex int) {
	t.Helper()
	group := &plan.CompileGroups[groupIndex]
	oldID := group.GroupID
	batchDigest, err := CompileGroupBatchPlanDigest(*group)
	if err != nil {
		t.Fatal(err)
	}
	group.BatchPlanDigest = batchDigest
	newID, err := CompileGroupID(*group)
	if err != nil {
		t.Fatal(err)
	}
	group.GroupID = newID
	for shardIndex := range plan.Shards {
		for idIndex := range plan.Shards[shardIndex].CompileGroupIDs {
			if plan.Shards[shardIndex].CompileGroupIDs[idIndex] == oldID {
				plan.Shards[shardIndex].CompileGroupIDs[idIndex] = newID
			}
		}
	}
}

func storedEligibleMultiGroupFixture(t *testing.T) (WorkloadExecutionPlan, WorkloadCatalog) {
	t.Helper()
	firstInput := compileTestInput(AtomicGatePackageTarget, "sha256:"+strings.Repeat("4", 64))
	secondInput := compileTestInput(AtomicRemoteCIPackageTarget, "sha256:"+strings.Repeat("5", 64))
	first, firstInputs := compileTestWorkloads(t, firstInput.PackageTarget, []string{"TestStoredEligibleFirst"}, 1_000, firstInput)
	second, secondInputs := compileTestWorkloads(t, secondInput.PackageTarget, []string{"TestStoredEligibleSecond"}, 1_000, secondInput)
	workloads := append(first, second...)
	catalog := testWorkloadCatalog(workloads...)
	inputs := mergeCompileInputs(firstInputs, secondInputs)
	context := testPlanningContext()
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	_, groups, err := planLPTWithCompileInputs(catalog, index, inputs)
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(groups, func(left, right CompileGroup) int { return strings.Compare(left.GroupID, right.GroupID) })
	planned := make([]PlannedWorkload, 0, len(workloads))
	for _, workload := range workloads {
		planned = append(planned, PlannedWorkload{Workload: workload, EstimatedDurationMS: 1_000, ResourceCPU: 2, ResourceMemoryGiB: 4})
	}
	shards := make([]ShardPlan, len(groups))
	for index, group := range groups {
		var groupWorkload PlannedWorkload
		for _, candidate := range planned {
			if candidate.Workload.ID == string(group.WorkloadIDs[0]) {
				groupWorkload = candidate
				break
			}
		}
		if groupWorkload.Workload.ID == "" {
			t.Fatal("eligible serial fixture cannot bind group workload")
		}
		shards[index] = ShardPlan{Index: index, Workloads: []PlannedWorkload{groupWorkload}, CompileGroupIDs: []string{group.GroupID}, EstimatedDurationMS: compileGroupCriticalDurationMS(group)}
	}
	return WorkloadExecutionPlan{ExecutionWorkloadIDs: []GateID{GateID(workloads[0].ID), GateID(workloads[1].ID)}, CompileGroups: groups, Shards: shards}, catalog
}

func mergeStoredEligibleSerialGroupShards(t *testing.T, plan WorkloadExecutionPlan) WorkloadExecutionPlan {
	t.Helper()
	if len(plan.Shards) != 2 {
		t.Fatal("eligible serial fixture requires two shards")
	}
	merged := plan.Shards[0]
	merged.Workloads = append(slices.Clone(merged.Workloads), plan.Shards[1].Workloads...)
	merged.CompileGroupIDs = append(slices.Clone(merged.CompileGroupIDs), plan.Shards[1].CompileGroupIDs...)
	merged.EstimatedDurationMS += plan.Shards[1].EstimatedDurationMS
	plan.Shards = []ShardPlan{merged}
	return plan
}
