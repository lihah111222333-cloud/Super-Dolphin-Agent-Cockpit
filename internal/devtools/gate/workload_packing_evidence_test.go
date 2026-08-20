package gate

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestDeriveWorkloadPackingEvidenceUsesCompileCriticalPath(t *testing.T) {
	context := testLinuxPlanningContext()
	context.ShardOverheadSampleCount = 1
	context.ShardOverheadProvenanceDigest = "sha256:" + strings.Repeat("0", 64)
	workload := PlannedWorkload{Workload: Workload{ID: "group-workload"}, EstimatedDurationMS: 60001, ResourceCPU: 4, ResourceMemoryGiB: 8}
	shards := []ShardPlan{{Index: 0, EstimatedDurationMS: 60001, CompileGroupIDs: []string{"group-1"}, Workloads: []PlannedWorkload{workload}}}
	group := CompileGroup{
		GroupID: "group-1", PackageTarget: AtomicGatePackageTarget, SemanticKey: CompileGroupSemanticGoTestNormal,
		SharedInputDigest: "sha256:" + strings.Repeat("1", 64), ProfileDigest: "sha256:" + strings.Repeat("f", 64), ResourceClassID: "medium",
		WorkloadIDs: []GateID{"group-workload"}, CompileEstimateMS: 1,
		BatchPlan: []CompileGroupBatch{{Wave: 0, EstimatedBodyMS: 60000}, {Wave: 0, EstimatedBodyMS: 60000}},
	}
	evidence, err := deriveWorkloadPackingEvidence(shards, []CompileGroup{group}, context)
	if err != nil {
		t.Fatalf("deriveWorkloadPackingEvidence() error = %v", err)
	}
	if len(evidence) != 1 || evidence[0].LowerBoundShards != 1 || evidence[0].HeuristicGapShards != 0 {
		t.Fatalf("evidence = %#v, want critical-path lower bound 1 and gap 0", evidence)
	}
}

func TestDeriveWorkloadPackingEvidenceExactModeWithIsolatedUnits(t *testing.T) {
	context := testLinuxPlanningContext()
	context.ShardOverheadSampleCount = 1
	context.ShardOverheadProvenanceDigest = "sha256:" + strings.Repeat("0", 64)
	_, groups, units := mixedEvidenceFixture(t)
	target, err := context.EffectiveTargetDurationForResourceTier(cicontract.WorkloadResourceTierMedium)
	if err != nil {
		t.Fatal(err)
	}
	shards, err := provenCompileUnitPacking(units, target)
	if err != nil {
		t.Fatal(err)
	}
	evidence := mustDerivePackingEvidence(t, shards, groups, context)
	assertExactPackingEvidence(t, evidence)
	assertMixedCompilePartition(t, units)
}

// mustDerivePackingEvidence 在测试中执行证据生产并将失败转换为明确断言。
func mustDerivePackingEvidence(t *testing.T, shards []ShardPlan, groups []CompileGroup, context PlanningContext) []WorkloadPackingEvidence {
	t.Helper()
	evidence, err := deriveWorkloadPackingEvidence(shards, groups, context)
	if err != nil {
		t.Fatalf("deriveWorkloadPackingEvidence() error = %v", err)
	}
	return evidence
}

// assertExactPackingEvidence 校验 exact solver 证明的 packable/isolated 计数与 gap。
func assertExactPackingEvidence(t *testing.T, evidence []WorkloadPackingEvidence) {
	t.Helper()
	if len(evidence) != 1 || evidence[0].PackableUnitCount != 5 || evidence[0].IsolatedUnitCount != 20 || evidence[0].SolverMode != cicontract.WorkloadPlanningExactSolverModeID || evidence[0].LowerBoundShards != 21 || evidence[0].PlannedShards != 21 || evidence[0].HeuristicGapShards != 0 {
		t.Fatalf("evidence = %#v, want solver-proven 5 co-packable groups/20 isolated exact evidence", evidence)
	}
}

// assertMixedCompilePartition 校验 mixed fixture 的 ordinary、serial 与 isolated 分区。
func assertMixedCompilePartition(t *testing.T, units []compilePlanningUnit) {
	t.Helper()
	ordinary, serial, isolated := partitionCompilePlanningUnits(units)
	if len(ordinary) != 0 || len(serial) != 5 || len(isolated) != 20 {
		t.Fatalf("compile partition = %d ordinary/%d serial/%d isolated, want 0/5/20", len(ordinary), len(serial), len(isolated))
	}
}

func TestFinalizePackingEvidenceRejectsMissingProofUnits(t *testing.T) {
	_, err := finalizePackingEvidence(map[cicontract.WorkloadResourceTier]*workloadPackingEvidenceStats{
		cicontract.WorkloadResourceTierMedium: {planned: 1, packable: 1},
	}, testLinuxPlanningContext(), false)
	if err == nil {
		t.Fatal("finalizePackingEvidence accepted evidence without rebuilt proof units")
	}
}

func mixedEvidenceFixture(t *testing.T) ([]ShardPlan, []CompileGroup, []compilePlanningUnit) {
	t.Helper()
	shards := make([]ShardPlan, 0, 21)
	groups := make([]CompileGroup, 0, 25)
	units := make([]compilePlanningUnit, 0, 25)
	for index := range 25 {
		id := GateID(fmt.Sprintf("evidence-%d", index))
		workload := PlannedWorkload{Workload: Workload{ID: string(id)}, EstimatedDurationMS: 1000, ResourceCPU: 4, ResourceMemoryGiB: 8}
		packageTarget := AtomicArchtestPackageTarget
		if index < 5 {
			packageTarget = AtomicGatePackageTarget
		}
		group := CompileGroup{GroupID: fmt.Sprintf("evidence-group-%d", index), PackageTarget: packageTarget, SemanticKey: CompileGroupSemanticGoTestNormal, SharedInputDigest: "sha256:" + strings.Repeat(fmt.Sprintf("%x", index+1), 64)[:64], ProfileDigest: "sha256:" + strings.Repeat("f", 64), ResourceClassID: "medium", WorkloadIDs: []GateID{id}, CompileEstimateMS: 1000, BodyEstimateMS: 1000, EstimatedDurationMS: 2000}
		affinity, err := CompileArtifactKey(group)
		if err != nil {
			t.Fatal(err)
		}
		reason, err := deriveCompileUnitHardIsolationReason([]PlannedWorkload{workload})
		if err != nil {
			t.Fatal(err)
		}
		groups = append(groups, group)
		units = append(units, compilePlanningUnit{workloads: []PlannedWorkload{workload}, group: &group, costMS: compileGroupCriticalDurationMS(group), affinityKey: affinity, sortID: group.GroupID, tier: cicontract.WorkloadResourceTierMedium, resourceClassID: group.ResourceClassID, hardIsolationReason: reason})
		if index == 0 {
			shards = append(shards, ShardPlan{Index: 0, EstimatedDurationMS: 10000, CompileGroupIDs: []string{"evidence-group-0", "evidence-group-1", "evidence-group-2", "evidence-group-3", "evidence-group-4"}, Workloads: make([]PlannedWorkload, 0, 5)})
		}
		if index < 5 {
			shards[0].Workloads = append(shards[0].Workloads, workload)
		} else {
			shards = append(shards, ShardPlan{Index: len(shards), EstimatedDurationMS: 2000, CompileGroupIDs: []string{string(group.GroupID)}, Workloads: []PlannedWorkload{workload}})
		}
	}
	return shards, groups, units
}

func TestBuildWorkloadExecutionPlanReportsLargeHeuristicGap(t *testing.T) {
	gatePlan, catalog, snapshot, context, selected := largeHeuristicPlanFixture(t)
	plan, err := BuildWorkloadExecutionPlanForWorkloads(gatePlan, catalog, snapshot, context, selected)
	if err != nil {
		t.Fatalf("BuildWorkloadExecutionPlanForWorkloads() error = %v", err)
	}
	if err := plan.Validate(gatePlan, snapshot); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if len(plan.PackingEvidence) != 1 {
		t.Fatalf("packing evidence = %#v, want one calibration tier", plan.PackingEvidence)
	}
	evidence := plan.PackingEvidence[0]
	if evidence.SolverMode != cicontract.WorkloadPlanningHeuristicSolverModeID || evidence.PackableUnitCount != 13 || evidence.IsolatedUnitCount != 0 {
		t.Fatalf("packing evidence = %#v, want 13-unit heuristic evidence", evidence)
	}
	if evidence.PlannedShards <= evidence.LowerBoundShards || evidence.HeuristicGapShards != evidence.PlannedShards-evidence.LowerBoundShards {
		t.Fatalf("packing evidence = %#v, want truthful positive gap", evidence)
	}
}

func TestBuildWorkloadExecutionPlanReportsExactOversizeMeasuredPacking(t *testing.T) {
	plan, gatePlan, snapshot, selected, durationByID, target := exactOversizeMeasuredPlanFixture(t)
	heuristicItems := make([]dcpapItem, 0, len(selected))
	for _, id := range selected {
		heuristicItems = append(heuristicItems, dcpapItem{id: string(id), durationMS: durationByID[id]})
	}
	heuristic, err := dcpapHeuristicPack(heuristicItems, target)
	if err != nil {
		t.Fatalf("dcpapHeuristicPack() error = %v", err)
	}
	if len(heuristic.bins) != 6 {
		t.Fatalf("13-unit heuristic counterexample shards = %d, want six (exact isolation must save one shard)", len(heuristic.bins))
	}
	if len(plan.Shards) != 5 || len(plan.Shards) >= len(selected) {
		t.Fatalf("planned shards = %d, want five exact shards (one oversize plus four regular shards)", len(plan.Shards))
	}
	wantLayout := exactOversizeCanonicalLayout(selected)
	if got := dcpapShardLayout(plan.Shards); got != wantLayout {
		t.Fatalf("canonical exact oversize layout = %q, want %q", got, wantLayout)
	}
	want := WorkloadPackingEvidence{
		ResourceTier:       cicontract.WorkloadResourceTierMedium,
		Resolution:         cicontract.WorkloadResourceTierReserveResolutionMeasured,
		SolverMode:         cicontract.WorkloadPlanningExactSolverModeID,
		PackableUnitCount:  12,
		IsolatedUnitCount:  1,
		LowerBoundShards:   5,
		PlannedShards:      5,
		HeuristicGapShards: 0,
	}
	if len(plan.PackingEvidence) != 1 || plan.PackingEvidence[0] != want {
		t.Fatalf("packing evidence = %#v, want exact oversize evidence %#v", plan.PackingEvidence, want)
	}
	if err := plan.Validate(gatePlan, snapshot); err != nil {
		t.Fatalf("Validate() after exact assertions = %v", err)
	}
}

func exactOversizeMeasuredPlanFixture(t *testing.T) (WorkloadExecutionPlan, GatePlan, DurationLedgerSnapshot, []GateID, map[GateID]int64, int64) {
	t.Helper()
	gatePlan := mustBuildPlan(t, ProfileRelease)
	packages := make([]string, 13)
	for index := range packages {
		packages[index] = fmt.Sprintf("./internal/dcpap-oversize-evidence%02d", index)
	}
	catalog, err := BuildExpandedWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{GoPackages: packages})
	if err != nil {
		t.Fatalf("BuildExpandedWorkloadCatalog() error = %v", err)
	}
	context := testLinuxPlanningContext()
	context.ResourceTierReserves[1].Measurement.P95MS = 35_000
	selected := packageEvidenceIDs(t, catalog, len(packages))
	durationByID := make(map[GateID]int64, len(selected))
	regularDurations := []int64{39_000, 32_500, 19_500, 13_000, 13_000, 13_000, 39_000, 32_500, 19_500, 13_000, 13_000, 13_000}
	for index, id := range selected {
		if index == 0 {
			durationByID[id] = 65_001
			continue
		}
		durationByID[id] = regularDurations[index-1]
	}
	ledger := DurationLedger{Version: durationLedgerVersion, ShardOverhead: testPlanningOverhead(context)}
	for _, workload := range catalog.Workloads {
		duration, ok := durationByID[GateID(workload.ID)]
		if !ok {
			continue
		}
		for _, resource := range []struct {
			classID string
			cpu     float64
			memory  float64
		}{{"small", 2, 4}, {"maximum", 8, 16}} {
			ledger.Samples = append(ledger.Samples, DurationSample{
				Bucket: DurationBucket{
					WorkloadID: workload.ID, CommandDigest: workload.CommandDigest,
					InputDigest: "sha256:" + strings.Repeat("0", 64),
					Platform:    context.Platform, Runner: context.Runner, Toolchain: context.Toolchain,
					ExecutionMode: DurationExecutionModeNormal, ResourceClassID: resource.classID,
					ResourceCPU: resource.cpu, ResourceMemoryGiB: resource.memory,
				},
				Succeeded: true, DurationMS: duration,
			})
		}
	}
	snapshot := DurationLedgerSnapshot{Generation: 11, Ledger: ledger}
	plan, err := BuildWorkloadExecutionPlanForWorkloads(gatePlan, catalog, snapshot, context, selected)
	if err != nil {
		t.Fatalf("BuildWorkloadExecutionPlanForWorkloads() error = %v", err)
	}
	target, err := context.EffectiveTargetDurationForResourceTier(cicontract.WorkloadResourceTierMedium)
	if err != nil {
		t.Fatalf("EffectiveTargetDurationForResourceTier() error = %v", err)
	}
	return plan, gatePlan, snapshot, selected, durationByID, target
}

func exactOversizeCanonicalLayout(selected []GateID) string {
	canonicalGroup := func(indices ...int) string {
		ids := make([]string, len(indices))
		for index, selectedIndex := range indices {
			ids[index] = string(selected[selectedIndex])
		}
		slices.Sort(ids)
		return strings.Join(ids, ",")
	}
	return strings.Join([]string{
		canonicalGroup(4, 5, 7), canonicalGroup(6, 8, 9), canonicalGroup(0), canonicalGroup(1, 10, 11), canonicalGroup(2, 3, 12),
	}, "|")
}

func TestValidateStoredRejectsTamperedLargeDCPAPLayout(t *testing.T) {
	gatePlan, catalog, snapshot, context, selected := largeHeuristicPlanFixture(t)
	plan, err := BuildWorkloadExecutionPlanForWorkloads(gatePlan, catalog, snapshot, context, selected)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ValidateStored(gatePlan); err != nil {
		t.Fatalf("large D-CPAP producer stored validation = %v", err)
	}
	tampered := splitOrdinaryWorkloadIntoShard(t, plan)
	if err := tampered.ValidateStored(gatePlan); err == nil {
		t.Fatal("ValidateStored accepted tampered large D-CPAP layout")
	}
}

func TestValidateStoredRejectsReorderedHeuristicDCPAPLayout(t *testing.T) {
	gatePlan, catalog, snapshot, context, selected := largeHeuristicPlanFixture(t)
	plan, err := BuildWorkloadExecutionPlanForWorkloads(gatePlan, catalog, snapshot, context, selected)
	if err != nil {
		t.Fatal(err)
	}
	tampered := reorderHeuristicShards(t, plan)
	if err := tampered.ValidateStored(gatePlan); err == nil {
		t.Fatal("ValidateStored accepted reordered heuristic D-CPAP layout")
	}
}

func splitOrdinaryWorkloadIntoShard(t *testing.T, plan WorkloadExecutionPlan) WorkloadExecutionPlan {
	t.Helper()
	tampered := plan
	tampered.Shards = slices.Clone(plan.Shards)
	target := plan.Context.TargetDurationMS
	for sourceIndex, source := range tampered.Shards {
		if len(source.Workloads) < 2 {
			continue
		}
		for workloadIndex, workload := range source.Workloads {
			if workload.EstimatedDurationMS > target {
				continue
			}
			tampered.Shards[sourceIndex].Workloads = append(slices.Clone(source.Workloads[:workloadIndex]), source.Workloads[workloadIndex+1:]...)
			tampered.Shards[sourceIndex].EstimatedDurationMS -= workload.EstimatedDurationMS
			tampered.Shards = append(tampered.Shards, ShardPlan{Index: len(tampered.Shards), EstimatedDurationMS: workload.EstimatedDurationMS, Workloads: []PlannedWorkload{workload}})
			for shardIndex := range tampered.Shards {
				tampered.Shards[shardIndex].Index = shardIndex
			}
			tampered.PackingEvidence = slices.Clone(plan.PackingEvidence)
			if len(tampered.PackingEvidence) != 1 {
				t.Fatal("large D-CPAP fixture has unexpected evidence tiers")
			}
			tampered.PackingEvidence[0].PlannedShards++
			tampered.PackingEvidence[0].HeuristicGapShards++
			digest, err := tampered.digest()
			if err != nil {
				t.Fatal(err)
			}
			tampered.PlanDigest = digest
			return tampered
		}
	}
	t.Fatal("large D-CPAP fixture has no movable ordinary workload")
	return WorkloadExecutionPlan{}
}

func largeHeuristicPlanFixture(t *testing.T) (GatePlan, WorkloadCatalog, DurationLedgerSnapshot, PlanningContext, []GateID) {
	t.Helper()
	gatePlan := mustBuildPlan(t, ProfileRelease)
	packages := make([]string, 13)
	for index := range packages {
		packages[index] = fmt.Sprintf("./internal/dcpap-evidence-fixture%02d", index)
	}
	catalog, err := BuildExpandedWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{GoPackages: packages})
	if err != nil {
		t.Fatalf("BuildExpandedWorkloadCatalog() error = %v", err)
	}
	context := testCalibrationPlanningContext()
	selected := packageEvidenceIDs(t, catalog, len(packages))
	durations := []int64{80_000, 70_000, 60_000, 50_000, 40_000, 100_000, 100_000, 100_000, 100_000, 100_000, 100_000, 100_000, 100_000}
	durationByID := make(map[GateID]int64, len(selected))
	for index, id := range selected {
		durationByID[id] = durations[index]
	}
	ledger := DurationLedger{Version: durationLedgerVersion, ShardOverhead: testPlanningOverhead(context)}
	for _, workload := range catalog.Workloads {
		duration := workload.BootstrapEstimateMS
		if planned, ok := durationByID[GateID(workload.ID)]; ok {
			duration = planned
		}
		ledger.Samples = append(ledger.Samples, testCalibrationDurationSample(workload.ID, workload.CommandDigest, true, duration))
	}
	return gatePlan, catalog, DurationLedgerSnapshot{Generation: 11, Ledger: ledger}, context, selected
}

func packageEvidenceIDs(t *testing.T, catalog WorkloadCatalog, want int) []GateID {
	t.Helper()
	selected := make([]GateID, 0, want)
	for _, workload := range catalog.Workloads {
		parent, kind, _, targeted, parseErr := ParseWorkloadID(workload.ID)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if targeted && kind == WorkloadTargetGoPackage && parent == GateIDBackendTestWithGuard {
			selected = append(selected, GateID(workload.ID))
		}
	}
	if len(selected) != want {
		t.Fatalf("selected package workloads = %d, want %d", len(selected), want)
	}
	return selected
}
