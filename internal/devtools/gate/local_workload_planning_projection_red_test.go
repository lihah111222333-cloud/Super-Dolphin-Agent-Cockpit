package gate

import (
	"reflect"
	"strings"
	"testing"
)

func TestProjectLocalWorkloadPlanningRetainsOwnerHighTierFastSample(t *testing.T) {
	workload := Workload{
		ID: "guard:local-projection-high-tier", Kind: WorkloadKindGuard,
		CommandDigest: strings.Repeat("a", 64), InputDigest: "sha256:" + strings.Repeat("b", 64),
		BootstrapEstimateMS: 1_000, Shardable: true,
	}
	context := PlanningContext{
		Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26",
		TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-v1",
	}
	ledger := testPlanningLedger(context, []DurationSample{
		testDurationIndexSample(workload, DurationExecutionModeNormal, "small", 2, 4, 80_000),
		testDurationIndexSample(workload, DurationExecutionModeNormal, "maximum", 8, 16, 1_000),
	})
	index, err := BuildDurationSampleIndex(ledger, context)
	if err != nil {
		t.Fatalf("BuildDurationSampleIndex() error = %v", err)
	}
	catalog := testWorkloadCatalog(workload)
	projection, err := ProjectLocalWorkloadPlanning(index, catalog, []GateID{GateID(workload.ID)})
	if err != nil {
		t.Fatalf("ProjectLocalWorkloadPlanning() error = %v", err)
	}
	if len(projection) != 1 {
		t.Fatalf("projection length = %d, want 1", len(projection))
	}
	got := projection[0]
	if got.WorkloadID != GateID(workload.ID) || got.PredictedDurationMS != 1_000 ||
		got.ResourceClassID != "maximum" || got.ResourceCPU != 8 || got.ResourceMemoryGiB != 16 {
		t.Fatalf("projection = %#v, want high-tier owner resource with 1000ms estimate", got)
	}
}

func TestProjectLocalWorkloadPlanningRetainsCatalogOrderAcrossSelectionPermutation(t *testing.T) {
	first := Workload{ID: "guard:z", Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("c", 64), InputDigest: "sha256:" + strings.Repeat("d", 64), BootstrapEstimateMS: 1_000, Shardable: true}
	second := Workload{ID: "guard:a", Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("e", 64), InputDigest: "sha256:" + strings.Repeat("f", 64), BootstrapEstimateMS: 2_000, Shardable: true}
	context := PlanningContext{
		Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26",
		TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-v1",
	}
	ledger := testPlanningLedger(context, []DurationSample{
		testDurationIndexSample(first, DurationExecutionModeNormal, "small", 2, 4, 1_000),
		testDurationIndexSample(second, DurationExecutionModeNormal, "small", 2, 4, 2_000),
	})
	index, err := BuildDurationSampleIndex(ledger, context)
	if err != nil {
		t.Fatalf("BuildDurationSampleIndex() error = %v", err)
	}
	catalog := testWorkloadCatalog(first, second)
	canonical, err := ProjectLocalWorkloadPlanning(index, catalog, []GateID{GateID(second.ID), GateID(first.ID)})
	if err != nil {
		t.Fatalf("canonical projection error = %v", err)
	}
	permuted, err := ProjectLocalWorkloadPlanning(index, catalog, []GateID{GateID(first.ID), GateID(second.ID)})
	if err != nil {
		t.Fatalf("permuted projection error = %v", err)
	}
	wantIDs := []GateID{GateID(first.ID), GateID(second.ID)}
	for index, want := range wantIDs {
		if got := canonical[index].WorkloadID; got != want {
			t.Fatalf("canonical projection[%d].WorkloadID = %q, want catalog order %q", index, got, want)
		}
	}
	if !reflect.DeepEqual(permuted, canonical) {
		t.Fatalf("permutation changed projection: canonical=%#v permuted=%#v", canonical, permuted)
	}
}

func TestProjectLocalWorkloadPlanningRejectsEmptyUnknownAndDuplicateIDs(t *testing.T) {
	workload := Workload{
		ID: "guard:local-projection-validation", Kind: WorkloadKindGuard,
		CommandDigest: strings.Repeat("1", 64), InputDigest: "sha256:" + strings.Repeat("2", 64),
		BootstrapEstimateMS: 1_000, Shardable: true,
	}
	context := PlanningContext{
		Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26",
		TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-v1",
	}
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, []DurationSample{
		testDurationIndexSample(workload, DurationExecutionModeNormal, "small", 2, 4, 1_000),
	}), context)
	if err != nil {
		t.Fatalf("BuildDurationSampleIndex() error = %v", err)
	}
	catalog := testWorkloadCatalog(workload)
	for name, ids := range map[string][]GateID{
		"empty selection": nil,
		"empty id":        {""},
		"unknown id":      {"guard:local-projection-unknown"},
		"duplicate id":    {GateID(workload.ID), GateID(workload.ID)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ProjectLocalWorkloadPlanning(index, catalog, ids); err == nil {
				t.Fatalf("ProjectLocalWorkloadPlanning(%v) unexpectedly succeeded", ids)
			}
		})
	}
}

func TestProjectLocalWorkloadPlanningMapsOwnerTupleWhenClassIsNotSampled(t *testing.T) {
	workload := Workload{
		ID: "guard:local-projection-carried-resource", Kind: WorkloadKindGuard,
		CommandDigest: strings.Repeat("3", 64), InputDigest: "sha256:" + strings.Repeat("4", 64),
		BootstrapEstimateMS: 1_000, Shardable: true,
	}
	context := PlanningContext{
		Platform: "linux/amd64", Runner: "runner-v1", Toolchain: "go1.26",
		TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-v1",
	}
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, []DurationSample{
		testDurationIndexSample(workload, DurationExecutionModeNormal, "small", 2, 4, 80_000),
	}), context)
	if err != nil {
		t.Fatalf("BuildDurationSampleIndex() error = %v", err)
	}
	projection, err := ProjectLocalWorkloadPlanning(index, testWorkloadCatalog(workload), []GateID{GateID(workload.ID)})
	if err != nil {
		t.Fatalf("ProjectLocalWorkloadPlanning() error = %v", err)
	}
	if got := projection[0]; got.ResourceClassID != "maximum" || got.ResourceCPU != 8 || got.ResourceMemoryGiB != 16 || got.PredictedDurationMS != 80_000 {
		t.Fatalf("projection = %#v, want carried maximum resource identity", got)
	}
}

func TestProjectLocalBootstrapWorkloadPlanningOwnsResourceFromImmutableCatalogEstimate(t *testing.T) {
	workload := Workload{
		ID: "guard:local-bootstrap-owner", Kind: WorkloadKindGuard,
		CommandDigest: strings.Repeat("5", 64), InputDigest: "sha256:" + strings.Repeat("6", 64),
		BootstrapEstimateMS: 80_001, Shardable: true,
	}
	projection, err := ProjectLocalBootstrapWorkloadPlanning(testWorkloadCatalog(workload), []GateID{GateID(workload.ID)})
	if err != nil {
		t.Fatalf("ProjectLocalBootstrapWorkloadPlanning() error = %v", err)
	}
	if len(projection) != 1 {
		t.Fatalf("projection length = %d, want 1", len(projection))
	}
	got := projection[0]
	if got.WorkloadID != GateID(workload.ID) || got.PredictedDurationMS != workload.BootstrapEstimateMS ||
		got.ResourceClassID != "maximum" || got.ResourceCPU != 8 || got.ResourceMemoryGiB != 16 {
		t.Fatalf("bootstrap projection = %#v, want catalog-derived maximum resource", got)
	}
}
