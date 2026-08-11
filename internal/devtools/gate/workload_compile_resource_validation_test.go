package gate

import (
	"slices"
	"strings"
	"testing"
)

func TestStoredCompilePlanAllowsEligibleSerialMultiGroupShard(t *testing.T) {
	plan, catalog := storedEligibleMultiGroupFixture(t)
	if _, _, err := validateStoredCompileGroups(plan, catalog); err != nil {
		t.Fatalf("eligible serial multi-group shard rejected: %v", err)
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
	plan, catalog = storedEligibleMultiGroupFixture(t)
	plan.CompileGroups[1].ResourceClassID = "medium"
	rebindStoredCompileGroupID(t, &plan, 1)
	for index := range plan.Shards[0].Workloads {
		if plan.Shards[0].Workloads[index].Workload.ID == string(plan.CompileGroups[1].WorkloadIDs[0]) {
			plan.Shards[0].Workloads[index].ResourceCPU = 4
			plan.Shards[0].Workloads[index].ResourceMemoryGiB = 8
		}
	}
	if _, _, err := validateStoredCompileGroups(plan, catalog); err == nil || !strings.Contains(err.Error(), "mixes") {
		t.Fatalf("mixed shard resource tuple error = %v", err)
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
	shard := ShardPlan{Index: 0, Workloads: planned, CompileGroupIDs: []string{groups[0].GroupID, groups[1].GroupID}}
	shard.EstimatedDurationMS = compileGroupCriticalDurationMS(groups[0]) + compileGroupCriticalDurationMS(groups[1])
	return WorkloadExecutionPlan{ExecutionWorkloadIDs: []GateID{GateID(workloads[0].ID), GateID(workloads[1].ID)}, CompileGroups: groups, Shards: []ShardPlan{shard}}, catalog
}
