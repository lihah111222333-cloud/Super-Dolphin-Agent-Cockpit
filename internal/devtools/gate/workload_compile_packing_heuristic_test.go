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
	if !slices.ContainsFunc(first.Shards, func(shard ShardPlan) bool { return len(shard.CompileGroupIDs) > 1 }) {
		t.Fatal("production heuristic plan has no legal multi-group shard")
	}
	containerShards, err := BuildContainerShardSetFromWorkloadPlan(gatePlan, first, shardTestDigest('a'), shardTestDigest('b'))
	if err != nil {
		t.Fatalf("production heuristic container shards = %v", err)
	}
	assertWorkloadContainerShardSetIdentity(t, containerShards, first)
	assertProductionCompileHeuristicReferenceGuards(t, first, catalog)
}

// TestHeuristicCompilePackingSeparatesMixedMissDomains reproduces the >12 mixed
// MISS shape that contains the taskdag/sqlite selectors from the failed remote run.
func TestHeuristicCompilePackingSeparatesMixedMissDomains(t *testing.T) {
	units, groups, grouped := mixedMissCompilePlanningUnits(t)
	shards, err := provenCompileUnitPacking(units, FullCITargetDurationMS)
	if err != nil {
		t.Fatalf("mixed MISS compile packing = %v", err)
	}
	assertCompilePackingDomainsSeparated(t, shards, groups, grouped)
}

func TestExactCompilePackingSeparatesMixedMissDomains(t *testing.T) {
	units, groups, grouped := mixedMissCompilePlanningUnits(t)
	exactUnits := append([]compilePlanningUnit(nil), units[:6]...)
	exactUnits = append(exactUnits, units[13:17]...)
	shards, err := provenCompileUnitPacking(exactUnits, FullCITargetDurationMS)
	if err != nil {
		t.Fatalf("mixed MISS exact compile packing = %v", err)
	}
	assertCompilePackingDomainsSeparated(t, shards, compileGroupsInShards(groups, shards), grouped)
}

func TestValidateStoredAndContainerRejectTamperedSerialGroupLayout(t *testing.T) {
	gatePlan, catalog, context, snapshot, inputs, executionIDs := productionCompileHeuristicFixture(t)
	plan, err := BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs(gatePlan, catalog, snapshot, context, executionIDs, inputs)
	if err != nil {
		t.Fatal(err)
	}
	tampered := duplicateSerialGroupReference(t, plan)
	if err := tampered.ValidateStored(gatePlan); err == nil {
		t.Fatal("ValidateStored accepted duplicate serial-group reference")
	}
	if _, err := BuildContainerShardSetFromWorkloadPlan(gatePlan, tampered, shardTestDigest('a'), shardTestDigest('b')); err == nil {
		t.Fatal("container projection accepted duplicate serial-group reference")
	}
}

func duplicateSerialGroupReference(t *testing.T, plan WorkloadExecutionPlan) WorkloadExecutionPlan {
	t.Helper()
	tampered := plan
	tampered.Shards = slices.Clone(plan.Shards)
	for index := range tampered.Shards {
		if len(tampered.Shards[index].CompileGroupIDs) == 0 {
			continue
		}
		groupID := tampered.Shards[index].CompileGroupIDs[0]
		tampered.Shards[index].CompileGroupIDs = append(slices.Clone(tampered.Shards[index].CompileGroupIDs), groupID)
		digest, err := tampered.digest()
		if err != nil {
			t.Fatal(err)
		}
		tampered.PlanDigest = digest
		return tampered
	}
	t.Fatal("production compile fixture has no compile-group shard")
	return WorkloadExecutionPlan{}
}

func TestValidateStoredRejectsNonCanonicalHeuristicCompileSplit(t *testing.T) {
	gatePlan, catalog, context, snapshot, inputs, executionIDs := productionCompileHeuristicFixture(t)
	plan, err := BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs(gatePlan, catalog, snapshot, context, executionIDs, inputs)
	if err != nil {
		t.Fatal(err)
	}
	tampered := splitEligibleCompileGroup(t, plan)
	if err := tampered.ValidateStored(gatePlan); err == nil {
		t.Fatal("ValidateStored accepted non-canonical heuristic compile split")
	}
}

func TestValidateStoredRejectsReorderedHeuristicCompileLayout(t *testing.T) {
	gatePlan, catalog, context, snapshot, inputs, executionIDs := productionCompileHeuristicFixture(t)
	plan, err := BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs(gatePlan, catalog, snapshot, context, executionIDs, inputs)
	if err != nil {
		t.Fatal(err)
	}
	tampered := reorderHeuristicShards(t, plan)
	if err := tampered.ValidateStored(gatePlan); err == nil {
		t.Fatal("ValidateStored accepted reordered heuristic compile layout")
	}
}

func reorderHeuristicShards(t *testing.T, plan WorkloadExecutionPlan) WorkloadExecutionPlan {
	t.Helper()
	tampered := plan
	tampered.Shards = slices.Clone(plan.Shards)
	for left := range tampered.Shards {
		for right := left + 1; right < len(tampered.Shards); right++ {
			if compilePackingShardKey(tampered.Shards[left]) == compilePackingShardKey(tampered.Shards[right]) {
				continue
			}
			tampered.Shards[left], tampered.Shards[right] = tampered.Shards[right], tampered.Shards[left]
			for index := range tampered.Shards {
				tampered.Shards[index].Index = index
			}
			digest, err := tampered.digest()
			if err != nil {
				t.Fatal(err)
			}
			tampered.PlanDigest = digest
			return tampered
		}
	}
	t.Fatal("heuristic fixture has no distinct shards to reorder")
	return WorkloadExecutionPlan{}
}

func splitEligibleCompileGroup(t *testing.T, plan WorkloadExecutionPlan) WorkloadExecutionPlan {
	t.Helper()
	groups := make(map[string]CompileGroup, len(plan.CompileGroups))
	for _, group := range plan.CompileGroups {
		groups[group.GroupID] = group
	}
	tampered := plan
	tampered.Shards = slices.Clone(plan.Shards)
	for index := range tampered.Shards {
		if splitEligibleCompileShard(&tampered, index, groups) {
			return finalizeCompileTamper(t, plan, tampered)
		}
	}
	t.Fatal("production compile fixture has no multi-group eligible shard")
	return WorkloadExecutionPlan{}
}

func splitEligibleCompileShard(plan *WorkloadExecutionPlan, index int, groups map[string]CompileGroup) bool {
	shard := &plan.Shards[index]
	if len(shard.CompileGroupIDs) < 2 {
		return false
	}
	groupID := shard.CompileGroupIDs[len(shard.CompileGroupIDs)-1]
	group, ok := groups[groupID]
	if !ok || !CompileGroupSerialPackingEligible(group) {
		return false
	}
	moved, remaining := splitGroupWorkloads(shard.Workloads, group)
	cost := compileGroupCriticalDurationMS(group)
	if len(moved) == 0 || len(remaining) == 0 || shard.EstimatedDurationMS < cost {
		return false
	}
	shard.Workloads = remaining
	shard.CompileGroupIDs = slices.DeleteFunc(slices.Clone(shard.CompileGroupIDs), func(id string) bool { return id == groupID })
	shard.EstimatedDurationMS -= cost
	plan.Shards = append(plan.Shards, ShardPlan{Index: len(plan.Shards), EstimatedDurationMS: cost, CompileGroupIDs: []string{groupID}, Workloads: moved})
	return true
}

func finalizeCompileTamper(t *testing.T, original, tampered WorkloadExecutionPlan) WorkloadExecutionPlan {
	for shardIndex := range tampered.Shards {
		tampered.Shards[shardIndex].Index = shardIndex
	}
	if len(tampered.PackingEvidence) != 1 {
		t.Fatal("production compile fixture has unexpected evidence tiers")
	}
	tampered.PackingEvidence = slices.Clone(original.PackingEvidence)
	tampered.PackingEvidence[0].PlannedShards++
	tampered.PackingEvidence[0].HeuristicGapShards++
	digest, err := tampered.digest()
	if err != nil {
		t.Fatal(err)
	}
	tampered.PlanDigest = digest
	return tampered
}

func splitGroupWorkloads(workloads []PlannedWorkload, group CompileGroup) ([]PlannedWorkload, []PlannedWorkload) {
	moved := make([]PlannedWorkload, 0, len(group.WorkloadIDs))
	remaining := make([]PlannedWorkload, 0, len(workloads))
	for _, workload := range workloads {
		if slices.Contains(group.WorkloadIDs, GateID(workload.Workload.ID)) {
			moved = append(moved, workload)
			continue
		}
		remaining = append(remaining, workload)
	}
	return moved, remaining
}

func mixedMissCompilePlanningUnits(t *testing.T) ([]compilePlanningUnit, map[string]CompileGroup, map[GateID]struct{}) {
	t.Helper()
	units := make([]compilePlanningUnit, 0, 30)
	groups := make(map[string]CompileGroup, 13)
	grouped := make(map[GateID]struct{}, 13)
	for index := range 13 {
		packageTarget, name := AtomicTaskDAGPackageTarget, fmt.Sprintf("TestTaskDAGMixedMiss%02d", index)
		if index == 0 {
			name = "TestClaimedWakeupLeaseContextRejectsNilReceiver"
		}
		if index == 1 {
			packageTarget, name = AtomicSQLitePackageTarget, "TestCopyBranchLocalMigrationsBefore120KeepsBoundaryAt119"
		}
		workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, packageTarget, name, 1_000)
		if err != nil {
			t.Fatal(err)
		}
		input := compileTestInput(packageTarget, fmt.Sprintf("sha256:%064x", index+1))
		groupID := fmt.Sprintf("mixed-miss-group-%02d", index)
		group := CompileGroup{
			GroupID: groupID, PackageTarget: input.PackageTarget, SemanticKey: input.SemanticKey,
			SharedInputDigest: input.SharedInputDigest, ProfileDigest: input.ProfileDigest,
			ResourceClassID: "medium", WorkloadIDs: []GateID{GateID(workload.ID)},
		}
		groups[groupID] = group
		grouped[GateID(workload.ID)] = struct{}{}
		units = append(units, compilePlanningUnit{
			workloads: []PlannedWorkload{{Workload: workload, EstimatedDurationMS: 1_000, ResourceCPU: 4, ResourceMemoryGiB: 8}},
			group:     &group, costMS: 1_000, affinityKey: "mixed-group-" + groupID, sortID: groupID,
			tier: cicontract.WorkloadResourceTierMedium,
		})
	}
	for index := range 17 {
		workload, err := NewGoPackageWorkload(GateIDBackendTestWithGuard, fmt.Sprintf("./cmd/mcp-lsp/ordinary-miss-%02d", index), 1_000)
		if err != nil {
			t.Fatal(err)
		}
		units = append(units, compilePlanningUnit{
			workloads: []PlannedWorkload{{Workload: workload, EstimatedDurationMS: 1_000, ResourceCPU: 4, ResourceMemoryGiB: 8}},
			costMS:    1_000, affinityKey: "mixed-ordinary-" + workload.ID, sortID: workload.ID,
			tier: cicontract.WorkloadResourceTierMedium,
		})
	}
	return units, groups, grouped
}

func assertCompilePackingDomainsSeparated(t *testing.T, shards []ShardPlan, groups map[string]CompileGroup, grouped map[GateID]struct{}) {
	t.Helper()
	if len(shards) == 0 {
		t.Fatal("mixed MISS packing returned no shards")
	}
	seen := make(map[string]int, len(groups))
	for _, shard := range shards {
		assertCompileShardDomain(t, shard, groups, grouped)
		for _, groupID := range shard.CompileGroupIDs {
			seen[groupID]++
		}
	}
	for groupID := range groups {
		if seen[groupID] != 1 {
			t.Fatalf("compile group %q appears %d times, want exactly once", groupID, seen[groupID])
		}
	}
}

func compileGroupsInShards(groups map[string]CompileGroup, shards []ShardPlan) map[string]CompileGroup {
	selected := make(map[string]CompileGroup)
	for _, shard := range shards {
		for _, groupID := range shard.CompileGroupIDs {
			selected[groupID] = groups[groupID]
		}
	}
	return selected
}

func assertCompileShardDomain(t *testing.T, shard ShardPlan, groups map[string]CompileGroup, grouped map[GateID]struct{}) {
	t.Helper()
	if len(shard.CompileGroupIDs) == 0 {
		for _, workload := range shard.Workloads {
			if _, isGrouped := grouped[GateID(workload.Workload.ID)]; isGrouped {
				t.Fatalf("ordinary shard %d contains grouped workload %q", shard.Index, workload.Workload.ID)
			}
		}
		return
	}
	if err := compileGroupAffinityFromShardIDs(groups, shard); err != nil {
		t.Fatalf("compile shard %d domain invalid: %v", shard.Index, err)
	}
	if !compileGroupsCoverShardWorkloads(groups, shard) {
		extra, missing := compileGroupShardCoverageMismatch(groups, shard)
		t.Fatalf("compile shard %d coverage drift: extra=%v missing=%v", shard.Index, extra, missing)
	}
	for _, workload := range shard.Workloads {
		if _, isGrouped := grouped[GateID(workload.Workload.ID)]; !isGrouped {
			t.Fatalf("compile shard %d contains ordinary workload %q", shard.Index, workload.Workload.ID)
		}
	}
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
	seen, groups := productionCompileGroupMaps(t, plan)
	assertProductionGroupCounts(t, seen, groups, len(catalog.Workloads))
	assertProductionCriticalCosts(t, plan, groups)
	assertProductionHeuristicEvidence(t, plan, context)
}

// productionCompileGroupMaps 收集 shard 中的 group 引用和定义。
func productionCompileGroupMaps(t *testing.T, plan WorkloadExecutionPlan) (map[string]int, map[string]CompileGroup) {
	t.Helper()
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
