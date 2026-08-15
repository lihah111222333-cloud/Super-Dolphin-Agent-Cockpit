package gate

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

const lspDiagnosticsPackageTarget = "./scripts/lsp_diagnostics_gate"

func TestLSPDiagnosticsPackageRemainsOutsideAtomicGoPackageTargets(t *testing.T) {
	if slices.Contains(AtomicGoPackageTargets(), lspDiagnosticsPackageTarget) {
		t.Fatal("LSP diagnostics package must not be added to AtomicGoPackageTargets")
	}
}

func TestLSPDiagnosticsCompileUnitCarriesCanonicalHardIsolationReason(t *testing.T) {
	workload, err := NewGoPackageWorkload(GateIDBackendTestWithGuard, lspDiagnosticsPackageTarget, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	unit, err := ordinaryCompilePlanningUnit(PlannedWorkload{Workload: workload, EstimatedDurationMS: 1_000, ResourceCPU: 4, ResourceMemoryGiB: 8}, testPlanningContext())
	if err != nil {
		t.Fatal(err)
	}
	if unit.hardIsolationReason != compileUnitHardIsolationReasonLSPDiagnostics {
		t.Fatalf("LSP compile unit reason = %q, want %q", unit.hardIsolationReason, compileUnitHardIsolationReasonLSPDiagnostics)
	}
}

func TestBuildWorkloadExecutionPlanKeepsLSPDiagnosticsHardIsolatedWithoutCompileInputs(t *testing.T) {
	lsp := lspWorkloadForIsolationTest(t, 1_000)
	ordinary := ordinaryWorkloadForIsolationTest(t, "./internal/example", 1_000)
	catalog := testWorkloadCatalog(lsp, ordinary)
	gatePlan := mustBuildPlan(t, ProfileRelease)
	plan, err := BuildWorkloadExecutionPlanForWorkloads(
		gatePlan,
		catalog,
		DurationLedgerSnapshot{Generation: 1, Ledger: fastDurationLedger(catalog)},
		testLinuxPlanningContext(),
		[]GateID{GateID(lsp.ID), GateID(ordinary.ID)},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertLSPCompileShardIsSingleton(t, plan.Shards)
	var packable, isolated int
	for _, evidence := range plan.PackingEvidence {
		packable += evidence.PackableUnitCount
		isolated += evidence.IsolatedUnitCount
	}
	if packable != 1 || isolated != 1 {
		t.Fatalf("ordinary LSP packing evidence = packable:%d isolated:%d, want 1/1", packable, isolated)
	}
	if err := plan.ValidateStored(gatePlan); err != nil {
		t.Fatalf("ordinary LSP singleton stored validation = %v", err)
	}
}

func TestLSPDiagnosticsHardIsolationExactProducerRejectsBothPackingDirections(t *testing.T) {
	lsp := lspCompilePlanningUnit(t, 2)
	ordinary := ordinaryCompilePlanningUnitForTest(t, "./internal/example", 1)
	for _, units := range [][]compilePlanningUnit{{lsp, ordinary}, {ordinary, lsp}} {
		shards, err := provenCompileUnitPacking(units, 10)
		if err != nil {
			t.Fatal(err)
		}
		assertLSPCompileShardIsSingleton(t, shards)
	}
}

func TestLSPDiagnosticsHardIsolationReasonTamperFailsClosed(t *testing.T) {
	lsp := lspCompilePlanningUnit(t, 2)
	lsp.hardIsolationReason = ""
	if _, err := provenCompileUnitPacking([]compilePlanningUnit{lsp}, 10); err == nil {
		t.Fatal("LSP unit with empty reason was silently accepted")
	}
	lsp = lspCompilePlanningUnit(t, 2)
	lsp.hardIsolationReason = compileUnitHardIsolationReasonNone
	if compileUnitCanShareShard(ShardPlan{}, map[string]struct{}{}, map[string]struct{}{}, lsp) {
		t.Fatal("LSP unit with tampered none reason was accepted")
	}
	ordinary := ordinaryCompilePlanningUnitForTest(t, "./internal/example", 1)
	ordinary.hardIsolationReason = compileUnitHardIsolationReasonLSPDiagnostics
	if compileUnitCanShareShard(ShardPlan{}, map[string]struct{}{}, map[string]struct{}{}, ordinary) {
		t.Fatal("ordinary unit with tampered LSP reason was accepted")
	}
	for name, unit := range map[string]compilePlanningUnit{
		"unknown": func() compilePlanningUnit {
			copy := ordinary
			copy.hardIsolationReason = "unknown"
			return copy
		}(),
		"wrong-none": lsp,
		"wrong-lsp":  ordinary,
	} {
		if name == "wrong-none" {
			unit.hardIsolationReason = compileUnitHardIsolationReasonNone
		}
		if name == "wrong-lsp" {
			unit.hardIsolationReason = compileUnitHardIsolationReasonLSPDiagnostics
		}
		if _, err := provenCompileUnitPacking([]compilePlanningUnit{unit}, 10); err == nil {
			t.Fatalf("tampered %s reason was silently accepted", name)
		}
	}
}

func TestLSPDiagnosticsHardIsolationHeuristicProducerRejectsCoBoxing(t *testing.T) {
	units := make([]compilePlanningUnit, 0, 13)
	for index := range 12 {
		units = append(units, ordinaryCompilePlanningUnitForTest(t, fmt.Sprintf("./internal/example-%02d", index), int64(index+1)))
	}
	units = append(units, lspCompilePlanningUnit(t, 2))
	shards, err := provenCompileUnitPacking(units, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	assertLSPCompileShardIsSingleton(t, shards)
}

func TestValidateStoredRejectsTamperedLSPShardWithSecondWorkload(t *testing.T) {
	lsp := lspWorkloadForIsolationTest(t, 1_000)
	ordinary := ordinaryWorkloadForIsolationTest(t, "./internal/example", 1_000)
	ids := []GateID{GateID(lsp.ID), GateID(ordinary.ID)}
	executionDigest, err := workloadExecutionDigest(ids)
	if err != nil {
		t.Fatal(err)
	}
	plan := WorkloadExecutionPlan{
		Catalog:                 WorkloadCatalog{Version: 1, Workloads: []Workload{lsp, ordinary}},
		ExecutionWorkloadIDs:    ids,
		ExecutionWorkloadDigest: executionDigest,
		Context:                 PlanningContext{Calibration: true, TargetDurationMS: 10_000},
		Shards: []ShardPlan{{
			Index: 0,
			Workloads: []PlannedWorkload{
				{Workload: lsp, EstimatedDurationMS: 1_000, ResourceCPU: 4, ResourceMemoryGiB: 8},
				{Workload: ordinary, EstimatedDurationMS: 1_000, ResourceCPU: 4, ResourceMemoryGiB: 8},
			},
			EstimatedDurationMS: 2_000,
		}},
	}
	if err := validateStoredWorkloadShards(plan, false); err == nil || !strings.Contains(err.Error(), "hard-isolated") {
		t.Fatalf("stored LSP shard tamper error = %v, want hard-isolation rejection", err)
	}
}

func lspCompilePlanningUnit(t *testing.T, cost int64) compilePlanningUnit {
	t.Helper()
	workload := lspWorkloadForIsolationTest(t, cost)
	unit, err := ordinaryCompilePlanningUnit(PlannedWorkload{Workload: workload, EstimatedDurationMS: cost, ResourceCPU: 4, ResourceMemoryGiB: 8}, testPlanningContext())
	if err != nil {
		t.Fatal(err)
	}
	return unit
}

func ordinaryCompilePlanningUnitForTest(t *testing.T, packageTarget string, cost int64) compilePlanningUnit {
	t.Helper()
	workload := ordinaryWorkloadForIsolationTest(t, packageTarget, cost)
	unit, err := ordinaryCompilePlanningUnit(PlannedWorkload{Workload: workload, EstimatedDurationMS: cost, ResourceCPU: 4, ResourceMemoryGiB: 8}, testPlanningContext())
	if err != nil {
		t.Fatal(err)
	}
	return unit
}

func lspWorkloadForIsolationTest(t *testing.T, estimate int64) Workload {
	t.Helper()
	workload, err := NewGoPackageWorkload(GateIDBackendTestWithGuard, lspDiagnosticsPackageTarget, estimate)
	if err != nil {
		t.Fatal(err)
	}
	return workload
}

func ordinaryWorkloadForIsolationTest(t *testing.T, packageTarget string, estimate int64) Workload {
	t.Helper()
	workload, err := NewGoPackageWorkload(GateIDBackendTestWithGuard, packageTarget, estimate)
	if err != nil {
		t.Fatal(err)
	}
	return workload
}

func assertLSPCompileShardIsSingleton(t *testing.T, shards []ShardPlan) {
	t.Helper()
	for _, shard := range shards {
		for _, planned := range shard.Workloads {
			if strings.Contains(planned.Workload.ID, lspDiagnosticsPackageTarget) && len(shard.Workloads) != 1 {
				t.Fatalf("LSP workload was co-boxed in shard %#v", shard)
			}
		}
	}
}
