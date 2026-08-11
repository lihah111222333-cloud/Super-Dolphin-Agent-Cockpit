package gate

import (
	"fmt"
	"slices"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestBuildWorkloadExecutionPlanWithCompileInputsUsesProductionHeuristic(t *testing.T) {
	gatePlan, catalog, context, snapshot, inputs, executionIDs := productionCompileHeuristicFixture(t)
	first, err := BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs(gatePlan, catalog, snapshot, context, executionIDs, inputs)
	if err != nil {
		t.Fatalf("production compile heuristic plan = %v", err)
	}
	second, err := BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs(gatePlan, catalog, snapshot, context, executionIDs, inputs)
	if err != nil {
		t.Fatalf("repeat production compile heuristic plan = %v", err)
	}
	if first.PlanDigest != second.PlanDigest || len(first.CompileGroups) != len(catalog.Workloads) {
		t.Fatalf("production heuristic determinism/groups: first=%s second=%s groups=%d want=%d", first.PlanDigest, second.PlanDigest, len(first.CompileGroups), len(catalog.Workloads))
	}
	assertProductionCompileHeuristicCoverage(t, first, catalog, context)
	if err := first.ValidateStored(gatePlan); err != nil {
		t.Fatalf("production heuristic stored validation = %v", err)
	}
	assertProductionCompileHeuristicReferenceGuards(t, first, catalog)
}

// productionCompileHeuristicFixture 构造真实 BuildWorkloadExecutionPlan 的 20 组输入。
func productionCompileHeuristicFixture(t *testing.T) (GatePlan, WorkloadCatalog, PlanningContext, DurationLedgerSnapshot, map[GateID]CompileGroupInput, []GateID) {
	t.Helper()
	gatePlan := mustBuildPlan(t, ProfileLocalFast)
	targets := make([]GoTestTarget, 20)
	for index := range targets {
		targets[index] = GoTestTarget{Package: AtomicGatePackageTarget, Name: fmt.Sprintf("TestCompileHeuristicProduction%02d", index)}
	}
	catalog, err := BuildSelectedTestWorkloadCatalog(gatePlan, WorkloadInventory{GoTests: targets})
	if err != nil {
		t.Fatal(err)
	}
	context := testPlanningContext()
	inputs := make(map[GateID]CompileGroupInput, len(catalog.Workloads))
	samples := make([]DurationSample, 0, len(catalog.Workloads)*3)
	parent := compileParentWorkload(t, AtomicGatePackageTarget, 60_000)
	for index := range catalog.Workloads {
		inputDigest := fmt.Sprintf("sha256:%064x", index+1)
		catalog.Workloads[index].InputDigest = inputDigest
		inputs[GateID(catalog.Workloads[index].ID)] = compileTestInput(AtomicGatePackageTarget, inputDigest)
		samples = append(samples, compileParentAndSelectorSamples(t, parent, []Workload{catalog.Workloads[index]}, inputDigest, 60_000, int64(5_000+index%3_000))...)
	}
	snapshot := DurationLedgerSnapshot{Generation: 1, Ledger: testPlanningLedger(context, samples)}
	return gatePlan, catalog, context, snapshot, inputs, allShardableWorkloadIDsForTest(catalog)
}

// assertProductionCompileHeuristicCoverage 校验 group 覆盖、critical cost 和 evidence 算术。
func assertProductionCompileHeuristicCoverage(t *testing.T, plan WorkloadExecutionPlan, catalog WorkloadCatalog, context PlanningContext) {
	t.Helper()
	seen, groups := productionCompileGroupMaps(plan)
	assertProductionGroupCounts(t, seen, groups, len(catalog.Workloads))
	assertProductionCriticalCosts(t, plan, groups)
	assertProductionHeuristicEvidence(t, plan, context)
}

// productionCompileGroupMaps 收集 shard 中的 group 引用和定义。
func productionCompileGroupMaps(plan WorkloadExecutionPlan) (map[string]int, map[string]CompileGroup) {
	seen := make(map[string]int, len(plan.CompileGroups))
	for _, shard := range plan.Shards {
		for _, groupID := range shard.CompileGroupIDs {
			seen[groupID]++
		}
	}
	groups := make(map[string]CompileGroup, len(plan.CompileGroups))
	for _, group := range plan.CompileGroups {
		groups[group.GroupID] = group
	}
	return seen, groups
}

// assertProductionGroupCounts 校验所有 compile group 恰好被一个 shard 引用。
func assertProductionGroupCounts(t *testing.T, seen map[string]int, groups map[string]CompileGroup, want int) {
	t.Helper()
	if len(seen) != len(groups) || len(groups) != want {
		t.Fatalf("production heuristic group coverage = %d/%d, want %d", len(seen), len(groups), want)
	}
	for groupID, count := range seen {
		if count != 1 {
			t.Fatalf("compile group %q appears %d times, want exactly once", groupID, count)
		}
	}
}

// assertProductionCriticalCosts 校验 compile once 加每个 wave 的正文关键路径。
func assertProductionCriticalCosts(t *testing.T, plan WorkloadExecutionPlan, groups map[string]CompileGroup) {
	t.Helper()
	for _, shard := range plan.Shards {
		var want int64
		for _, groupID := range shard.CompileGroupIDs {
			want += compileGroupCriticalDurationMS(groups[groupID])
		}
		if shard.EstimatedDurationMS != want {
			t.Fatalf("compile-once critical cost shard %d = %d, want %d", shard.Index, shard.EstimatedDurationMS, want)
		}
	}
}

// assertProductionHeuristicEvidence 校验 heuristic mode、gap 算术和零 setup proxy。
func assertProductionHeuristicEvidence(t *testing.T, plan WorkloadExecutionPlan, context PlanningContext) {
	t.Helper()
	if len(plan.PackingEvidence) != 1 || plan.PackingEvidence[0].SolverMode != cicontract.WorkloadPlanningHeuristicSolverModeID || plan.PackingEvidence[0].PlannedShards-plan.PackingEvidence[0].LowerBoundShards != plan.PackingEvidence[0].HeuristicGapShards {
		t.Fatalf("production heuristic evidence = %#v", plan.PackingEvidence)
	}
	score, err := compilePackingScoreForShards(plan.Shards, context.TargetDurationMS)
	if err != nil {
		t.Fatal(err)
	}
	if score.setupProxyMS != 0 {
		t.Fatalf("production heuristic setup proxy = %d, want zero", score.setupProxyMS)
	}
}

// assertProductionCompileHeuristicReferenceGuards 确认 stored validator 拒绝重复与缺失 group 引用。
func assertProductionCompileHeuristicReferenceGuards(t *testing.T, plan WorkloadExecutionPlan, catalog WorkloadCatalog) {
	t.Helper()
	shardIndex := -1
	for index, shard := range plan.Shards {
		if len(shard.CompileGroupIDs) > 0 {
			shardIndex = index
			break
		}
	}
	if shardIndex < 0 {
		t.Fatal("production heuristic plan has no compile-group shard")
	}
	duplicate := plan
	duplicate.Shards = slices.Clone(plan.Shards)
	duplicate.Shards[shardIndex].CompileGroupIDs = append(slices.Clone(plan.Shards[shardIndex].CompileGroupIDs), plan.Shards[shardIndex].CompileGroupIDs[0])
	if _, _, err := validateStoredCompileGroups(duplicate, catalog); err == nil {
		t.Fatal("stored validator accepted duplicate compile-group reference")
	}
	missing := plan
	missing.Shards = slices.Clone(plan.Shards)
	missing.Shards[shardIndex].CompileGroupIDs = slices.Clone(plan.Shards[shardIndex].CompileGroupIDs[1:])
	if _, _, err := validateStoredCompileGroups(missing, catalog); err == nil {
		t.Fatal("stored validator accepted missing compile-group reference")
	}
}
