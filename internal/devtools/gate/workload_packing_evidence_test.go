package gate

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestDeriveWorkloadPackingEvidenceUsesCompileCriticalPath(t *testing.T) {
	context := testLinuxPlanningContext()
	context.ShardOverheadSampleCount = 1
	context.ShardOverheadProvenanceDigest = "sha256:" + strings.Repeat("0", 64)
	workload := PlannedWorkload{Workload: Workload{ID: "group-workload"}, EstimatedDurationMS: 60001, ResourceCPU: 4, ResourceMemoryGiB: 8}
	shards := []ShardPlan{{Index: 0, EstimatedDurationMS: 60001, Workloads: []PlannedWorkload{workload}}}
	group := CompileGroup{
		GroupID: "group-1", PackageTarget: AtomicGatePackageTarget, SemanticKey: CompileGroupSemanticGoTestNormal,
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
	shards, groups, units := mixedEvidenceFixture()
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
		t.Fatalf("evidence = %#v, want solver-proven 5 packable/20 isolated exact evidence", evidence)
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

func TestFinalizePackingEvidenceRejectsPlannedBelowSoundLowerBound(t *testing.T) {
	_, err := finalizePackingEvidence(map[cicontract.WorkloadResourceTier]*workloadPackingEvidenceStats{
		cicontract.WorkloadResourceTierMedium: {planned: 1, packable: 1, ordinaryTotal: 11},
	}, 10)
	if err == nil {
		t.Fatal("finalizePackingEvidence accepted a plan below its sound lower bound")
	}
}

func mixedEvidenceFixture() ([]ShardPlan, []CompileGroup, []compilePlanningUnit) {
	shards := make([]ShardPlan, 0, 25)
	groups := make([]CompileGroup, 0, 25)
	units := make([]compilePlanningUnit, 0, 25)
	for index := range 25 {
		id := GateID(fmt.Sprintf("evidence-%d", index))
		workload := PlannedWorkload{Workload: Workload{ID: string(id)}, EstimatedDurationMS: 1000, ResourceCPU: 4, ResourceMemoryGiB: 8}
		shards = append(shards, ShardPlan{Index: index, EstimatedDurationMS: 1000, Workloads: []PlannedWorkload{workload}})
		packageTarget := "non-eligible"
		if index < 5 {
			packageTarget = AtomicGatePackageTarget
		}
		group := CompileGroup{GroupID: fmt.Sprintf("evidence-group-%d", index), PackageTarget: packageTarget, SemanticKey: CompileGroupSemanticGoTestNormal, WorkloadIDs: []GateID{id}, CompileEstimateMS: 1000, BodyEstimateMS: 1000, EstimatedDurationMS: 2000}
		groups = append(groups, group)
		units = append(units, compilePlanningUnit{workloads: []PlannedWorkload{workload}, group: &group, costMS: 1000, affinityKey: string(id), sortID: string(id), tier: cicontract.WorkloadResourceTierMedium})
	}
	packable := ShardPlan{Index: 0, EstimatedDurationMS: 10_000}
	for index := range 5 {
		packable.Workloads = append(packable.Workloads, shards[index].Workloads...)
	}
	merged := make([]ShardPlan, 0, len(shards)-4)
	merged = append(merged, packable)
	merged = append(merged, shards[5:]...)
	for index := range merged {
		merged[index].Index = index
	}
	shards = merged
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
