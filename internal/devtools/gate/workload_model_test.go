package gate

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const testWorkloadDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestValidateDurationEnvironmentRejectsPaddedIdentity(t *testing.T) {
	tests := []struct {
		name                        string
		platform, runner, toolchain string
	}{
		{name: "platform", platform: " linux/amd64 ", runner: "runner", toolchain: "toolchain"},
		{name: "runner", platform: "linux/amd64", runner: " runner ", toolchain: "toolchain"},
		{name: "toolchain", platform: "linux/amd64", runner: "runner", toolchain: " toolchain "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateDurationEnvironment(test.platform, test.runner, test.toolchain); err == nil {
				t.Fatalf("padded %s identity unexpectedly accepted", test.name)
			}
		})
	}
}

func TestDurationCalibrationBindsEnvironmentAndCatalogs(t *testing.T) {
	calibration := &DurationCalibration{
		SchemaVersion:              DurationCalibrationSchemaVersion,
		Commit:                     strings.Repeat("1", 40),
		Tree:                       strings.Repeat("2", 40),
		Platform:                   "linux/amd64",
		Runner:                     "sha256:" + strings.Repeat("3", 64),
		Toolchain:                  "sha256:" + strings.Repeat("4", 64),
		CommitEntrypoint:           CIEntrypointGitPreCommit,
		PushEntrypoint:             CIEntrypointGitPrePush,
		ReleaseEntrypoint:          CIEntrypointRelease,
		CommitCatalogDigest:        "sha256:" + strings.Repeat("5", 64),
		PushCatalogDigest:          "sha256:" + strings.Repeat("6", 64),
		ReleaseCatalogDigest:       "sha256:" + strings.Repeat("7", 64),
		CalibrationResourceClassID: "calibration", CalibrationResourceCPU: 4, CalibrationResourceMemoryGiB: 8,
		WorkloadCount:      10,
		RacePackageCount:   2,
		AcceptedSnapshotID: "snapshot-test",
		CompletedAt:        time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC),
	}
	ledger := NewDurationLedger()
	ledger.Calibration = calibration
	if err := ValidateDurationLedger(ledger); err != nil {
		t.Fatalf("ValidateDurationLedger() error = %v", err)
	}
	calibration.CompletedAt = calibration.CompletedAt.In(time.FixedZone("local", 3600))
	if err := ValidateDurationLedger(ledger); err == nil {
		t.Fatal("ValidateDurationLedger() accepted non-UTC calibration")
	}
}

func TestDurationLedgerJSONFieldCoverage(t *testing.T) {
	registrations := []struct {
		name     string
		producer reflect.Type
		fields   []string
	}{
		{
			name:     "ledger",
			producer: reflect.TypeFor[DurationLedger](),
			fields:   []string{"calibration", "samples", "shard_overhead", "version"},
		},
		{
			name:     "calibration",
			producer: reflect.TypeFor[DurationCalibration](),
			fields: []string{
				"accepted_snapshot_id", "commit", "commit_catalog_digest", "commit_entrypoint", "completed_at", "platform",
				"calibration_resource_class_id", "calibration_resource_cpu", "calibration_resource_memory_gib",
				"push_catalog_digest", "race_package_count", "runner",
				"push_entrypoint", "release_catalog_digest", "release_entrypoint",
				"schema_version", "toolchain", "tree", "workload_count",
			},
		},
		{
			name:     "sample",
			producer: reflect.TypeFor[DurationSample](),
			fields: []string{
				"bucket", "duration_ms", "parent_command_digest", "parent_workload_id",
				"succeeded", "target_kind", "target_name", "target_status",
			},
		},
		{
			name:     "bucket",
			producer: reflect.TypeFor[DurationBucket](),
			fields: []string{
				"command_digest", "execution_mode", "input_digest", "platform", "resource_class_id", "resource_cpu", "resource_memory_gib", "runner", "toolchain", "workload_id",
			},
		},
		{
			name:     "planning context",
			producer: reflect.TypeFor[PlanningContext](),
			fields:   []string{"accepted_snapshot_id", "calibration", "calibration_resource_class_id", "calibration_resource_cpu", "calibration_resource_memory_gib", "platform", "runner", "shard_overhead_p95_ms", "shard_overhead_provenance_digest", "shard_overhead_sample_count", "target_duration_ms", "toolchain"},
		},
	}
	for _, registration := range registrations {
		t.Run(registration.name, func(t *testing.T) {
			producer, err := JSONFieldNames(registration.producer)
			if err != nil {
				t.Fatalf("JSONFieldNames() error = %v", err)
			}
			missing, stale := FieldCoverageDiff(producer, registration.fields)
			if len(missing) != 0 || len(stale) != 0 {
				t.Fatalf("duration JSON field coverage missing=%v stale=%v", missing, stale)
			}
			failFirstMissing, failFirstStale := FieldCoverageDiff(
				producer,
				append(append([]string(nil), registration.fields[1:]...), "stale_field"),
			)
			if len(failFirstMissing) != 1 || failFirstMissing[0] != registration.fields[0] ||
				len(failFirstStale) != 1 || failFirstStale[0] != "stale_field" {
				t.Fatalf(
					"fail-first coverage missing=%v stale=%v, want missing=%q stale=stale_field",
					failFirstMissing,
					failFirstStale,
					registration.fields[0],
				)
			}
		})
	}
}

func TestDurationLedgerValidatesStructuredGoTestTargetSamples(t *testing.T) {
	parentID := "backend:test_with_guard::go-package::fixture"
	parentDigest := strings.Repeat("a", 64)
	name := "TestOne/subcase"
	sample := DurationSample{
		Bucket: DurationBucket{
			WorkloadID:      GoTestDurationWorkloadID(parentID, name),
			CommandDigest:   GoTestDurationCommandDigest(parentDigest, name),
			InputDigest:     "sha256:" + strings.Repeat("0", 64),
			Platform:        "linux/amd64",
			Runner:          "runner-v1",
			Toolchain:       "toolchain-v1",
			ExecutionMode:   DurationExecutionModeNormal,
			ResourceClassID: "small", ResourceCPU: 2, ResourceMemoryGiB: 4,
		},
		Succeeded:           true,
		DurationMS:          125,
		TargetKind:          WorkloadKindGoTest,
		ParentWorkloadID:    parentID,
		ParentCommandDigest: parentDigest,
		TargetName:          name,
		TargetStatus:        GoTestStatusPass,
	}
	ledger := DurationLedger{Version: durationLedgerVersion, Samples: []DurationSample{sample}}
	if err := ValidateDurationLedger(ledger); err != nil {
		t.Fatalf("ValidateDurationLedger() error = %v", err)
	}
	ledger.Samples[0].TargetName = "TestOther"
	if err := ValidateDurationLedger(ledger); err == nil {
		t.Fatal("ValidateDurationLedger() accepted a target name that does not match its bucket")
	}
}

func TestLoadWorkloadCatalogRejectsMissingRequiredFieldsAndUnknownJSON(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{name: "unknown kind", json: `{"version":1,"workloads":[{"id":"unit","kind":"shell","command_digest":"` + testWorkloadDigest + `","bootstrap_estimate_ms":1}]}`},
		{name: "zero bootstrap estimate", json: `{"version":1,"workloads":[{"id":"unit","kind":"go_test","command_digest":"` + testWorkloadDigest + `","bootstrap_estimate_ms":0}]}`},
		{name: "unknown field", json: `{"version":1,"workloads":[{"id":"unit","kind":"go_test","command_digest":"` + testWorkloadDigest + `","bootstrap_estimate_ms":1,"slots":2}]}`},
		{name: "missing shardable", json: `{"version":1,"workloads":[{"id":"unit","kind":"go_test","command_digest":"` + testWorkloadDigest + `","bootstrap_estimate_ms":1}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadWorkloadCatalog(strings.NewReader(test.json)); err == nil {
				t.Fatal("LoadWorkloadCatalog() error = nil, want fail-fast error")
			}
		})
	}
}

func TestPlanCurrentWorkloadsUsesOnlyComparableSuccessfulSamples(t *testing.T) {
	catalog := testWorkloadCatalog(
		Workload{ID: "alpha", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 100},
		Workload{ID: "beta", Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 200},
		Workload{ID: "gamma", Kind: WorkloadKindNodeTest, CommandDigest: strings.Repeat("b", 64), BootstrapEstimateMS: 300},
	)
	ledger := testPlanningLedger(testPlanningContext(), []DurationSample{
		testDurationSample("alpha", testWorkloadDigest, true, 900),
		testDurationSample("alpha", testWorkloadDigest, false, 1),
		testDurationSample("beta", strings.Repeat("a", 64), true, 800),
		{Bucket: DurationBucket{WorkloadID: "gamma", CommandDigest: strings.Repeat("b", 64), InputDigest: "sha256:" + strings.Repeat("0", 64), Platform: "darwin", Runner: "other", Toolchain: "go1.25", ExecutionMode: DurationExecutionModeNormal, ResourceClassID: "small", ResourceCPU: 2, ResourceMemoryGiB: 4}, Succeeded: true, DurationMS: 9},
	})

	shards, err := planCurrentWorkloads(catalog, ledger, testPlanningContext())
	if err != nil {
		t.Fatalf("planCurrentWorkloads() error = %v", err)
	}
	if len(shards) != 1 {
		t.Fatalf("len(shards) = %d, want one non-empty shard below the 100 second target", len(shards))
	}
	if got := shards[0].Workloads[0]; got.Workload.ID != "alpha" || got.EstimatedDurationMS != 900 {
		t.Fatalf("shard 0 first workload = %#v, want alpha estimated from successes only", got)
	}
	if got := shards[0].Workloads[1]; got.Workload.ID != "beta" || got.EstimatedDurationMS != 800 {
		t.Fatalf("shard 0 second workload = %#v, want beta", got)
	}
	if got := shards[0].Workloads[2]; got.Workload.ID != "gamma" || got.EstimatedDurationMS != 300 {
		t.Fatalf("shard 0 third workload = %#v, want bootstrap estimate", got)
	}
}

func TestHasComparableSuccessfulDurationSampleRequiresSuccessAndExactEnvironment(t *testing.T) {
	workload := Workload{
		ID: "alpha", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest,
		BootstrapEstimateMS: 100, Shardable: true,
	}
	context := testCalibrationPlanningContext()
	context.AcceptedSnapshotID = "snapshot-test"
	comparable := func(samples []DurationSample) bool {
		t.Helper()
		index, err := BuildDurationSampleIndex(DurationLedger{Version: durationLedgerVersion, Samples: samples}, context)
		if err != nil {
			t.Fatalf("BuildDurationSampleIndex() error = %v", err)
		}
		return index.HasComparableSuccessfulDurationSample(workload)
	}
	failed := testCalibrationDurationSample(workload.ID, workload.CommandDigest, false, 900)
	if comparable([]DurationSample{failed}) {
		t.Fatal("failed duration sample was accepted as comparable success")
	}
	succeeded := failed
	succeeded.Succeeded = true
	succeeded.Bucket.Runner = "other-runner"
	if comparable([]DurationSample{succeeded}) {
		t.Fatal("different runner duration sample was accepted as comparable success")
	}
	succeeded.Bucket.Runner = context.Runner
	if !comparable([]DurationSample{succeeded}) {
		t.Fatal("exact successful duration sample was not recognized")
	}
}

func TestPlanCurrentWorkloadsUsesOneShardBelow100SecondTarget(t *testing.T) {
	catalog := testWorkloadCatalog(
		Workload{ID: "charlie", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 50},
		Workload{ID: "alpha", Kind: WorkloadKindGoTest, CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 50},
		Workload{ID: "bravo", Kind: WorkloadKindGoTest, CommandDigest: strings.Repeat("b", 64), BootstrapEstimateMS: 50},
	)
	shards, err := planCurrentWorkloads(catalog, testPlanningLedger(testPlanningContext(), nil), testPlanningContext())
	if err != nil {
		t.Fatalf("planCurrentWorkloads() error = %v", err)
	}
	if len(shards) != 1 {
		t.Fatalf("len(shards) = %d, want one shard below the 100 second target", len(shards))
	}
	for index, wantID := range []string{"alpha", "bravo", "charlie"} {
		if got := shards[0].Workloads[index].Workload.ID; got != wantID {
			t.Fatalf("shards[0].workloads[%d] = %q, want stable ID order %q", index, got, wantID)
		}
	}
}

func TestPlanCurrentWorkloadsIsolatesNormalResourceTiersBeforeBalancing(t *testing.T) {
	catalog := testWorkloadCatalog(
		Workload{ID: "fast", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 5_000},
		Workload{ID: "medium", Kind: WorkloadKindGoTest, CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 5_001},
		Workload{ID: "slow", Kind: WorkloadKindGoTest, CommandDigest: strings.Repeat("b", 64), BootstrapEstimateMS: 70_001},
	)
	shards, err := planCurrentWorkloads(catalog, testPlanningLedger(testPlanningContext(), nil), testPlanningContext())
	if err != nil {
		t.Fatalf("planCurrentWorkloads() error = %v", err)
	}
	if len(shards) != 1 || shards[0].Index != 0 || len(shards[0].Workloads) != 3 {
		t.Fatalf("shards = %#v, want one bootstrap shard with all three workloads", shards)
	}
	for index, wantID := range []string{"slow", "medium", "fast"} {
		planned := shards[0].Workloads[index]
		if planned.Workload.ID != wantID || planned.ResourceCPU != 2 || planned.ResourceMemoryGiB != 4 {
			t.Fatalf("planned workload %d = %#v, want %q persisted at 2C/4GiB", index, planned, wantID)
		}
	}
}

func TestPlanCurrentWorkloadsCalibrationPacksAcrossNormalResourceTiers(t *testing.T) {
	catalog := testWorkloadCatalog(
		Workload{ID: "fast", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 1},
		Workload{ID: "medium", Kind: WorkloadKindGoTest, CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 1},
		Workload{ID: "slow", Kind: WorkloadKindGoTest, CommandDigest: strings.Repeat("b", 64), BootstrapEstimateMS: 1},
	)
	ledger := testPlanningLedger(testCalibrationPlanningContext(), []DurationSample{
		testCalibrationDurationSample("fast", testWorkloadDigest, true, 5_000),
		testCalibrationDurationSample("medium", strings.Repeat("a", 64), true, 5_001),
		testCalibrationDurationSample("slow", strings.Repeat("b", 64), true, 70_001),
	})
	shards, err := planCurrentWorkloads(catalog, ledger, testCalibrationPlanningContext())
	if err != nil {
		t.Fatalf("planCurrentWorkloads() calibration error = %v", err)
	}
	if len(shards) != 1 {
		t.Fatalf("len(shards) = %d, want one cross-tier calibration shard", len(shards))
	}
	if got, want := shards[0].EstimatedDurationMS, int64(80_002); got != want {
		t.Fatalf("calibration shard estimate = %dms, want %dms ledger total", got, want)
	}
	for index, wantID := range []string{"slow", "medium", "fast"} {
		if got := shards[0].Workloads[index].Workload.ID; got != wantID {
			t.Fatalf("shards[0].workloads[%d] = %q, want stable LPT order %q", index, got, wantID)
		}
	}
}

func TestPlanCurrentWorkloadsAutomaticallyAddsShardsToMeet100SecondSLA(t *testing.T) {
	catalog := testWorkloadCatalog(
		Workload{ID: "alpha", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 60_000},
		Workload{ID: "beta", Kind: WorkloadKindGoTest, CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 60_000},
		Workload{ID: "gamma", Kind: WorkloadKindGoTest, CommandDigest: strings.Repeat("b", 64), BootstrapEstimateMS: 60_000},
	)
	shards, err := planCurrentWorkloads(catalog, testPlanningLedger(testPlanningContext(), nil), testPlanningContext())
	if err != nil {
		t.Fatalf("planCurrentWorkloads() error = %v", err)
	}
	if len(shards) != 3 {
		t.Fatalf("len(shards) = %d, want 3 to keep every shard within 100 seconds", len(shards))
	}
}

func TestPlanCurrentWorkloadsKeepsIndivisibleWorkloadOver100SecondsRunnable(t *testing.T) {
	catalog := testWorkloadCatalog(
		Workload{ID: "too-slow", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 100_001},
	)
	shards, err := planCurrentWorkloads(catalog, testPlanningLedger(testPlanningContext(), nil), testPlanningContext())
	if err != nil {
		t.Fatalf("planCurrentWorkloads() rejected an over-target workload: %v", err)
	}
	if len(shards) != 1 || len(shards[0].Workloads) != 1 ||
		shards[0].Workloads[0].Workload.ID != "too-slow" ||
		shards[0].Workloads[0].EstimatedDurationMS != 100_001 {
		t.Fatalf("planCurrentWorkloads() shards = %#v, want runnable over-target workload", shards)
	}
}

func TestPlanCurrentWorkloadsUsesAtomicEstimateAfterSuccessfulPreparationOverrun(t *testing.T) {
	workload, err := NewGoTestWorkload(
		GateIDBackendTestWithGuard,
		"./internal/archtest",
		"TestDeterministicTimeGuard",
		10_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	ledger := testPlanningLedger(testPlanningContext(), []DurationSample{
		testDurationSample(workload.ID, workload.CommandDigest, true, FullCITargetDurationMS+50),
	})
	ledger.Samples[0].Bucket.ResourceClassID = "medium"
	ledger.Samples[0].Bucket.ResourceCPU = 4
	ledger.Samples[0].Bucket.ResourceMemoryGiB = 8
	shards, err := planCurrentWorkloads(testWorkloadCatalog(workload), ledger, testPlanningContext())
	if err != nil {
		t.Fatalf("planCurrentWorkloads() rejected a passed atomic test with deadline-external preparation: %v", err)
	}
	if got := shards[0].Workloads[0].EstimatedDurationMS; got != workload.BootstrapEstimateMS {
		t.Fatalf("atomic test estimate = %dms, want %dms", got, workload.BootstrapEstimateMS)
	}
}

func TestPlanCurrentWorkloadsKeepsMeasuredPackageOver100SecondsRunnable(t *testing.T) {
	workload, err := NewGoPackageWorkload(GateIDBackendTestWithGuard, "./internal/archtest", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	ledger := testPlanningLedger(testPlanningContext(), []DurationSample{
		testDurationSample(workload.ID, workload.CommandDigest, true, FullCITargetDurationMS+50),
	})
	// A fast-tier observation is required before the fixed-point planner can
	// carry the measured duration into the slow tier.  A slow-tier sample then
	// closes the chain without treating the bootstrap estimate as a tier hint.
	slowSample := ledger.Samples[0]
	slowSample.Bucket.ResourceClassID = "large"
	slowSample.Bucket.ResourceCPU = 8
	slowSample.Bucket.ResourceMemoryGiB = 16
	ledger.Samples = append(ledger.Samples, slowSample)
	shards, err := planCurrentWorkloads(testWorkloadCatalog(workload), ledger, testPlanningContext())
	if err != nil {
		t.Fatalf("planCurrentWorkloads() rejected a measured over-target package: %v", err)
	}
	planned := shards[0].Workloads[0]
	if planned.EstimatedDurationMS != FullCITargetDurationMS+50 || planned.ResourceCPU != 8 || planned.ResourceMemoryGiB != 16 {
		t.Fatalf("planned package = %#v, want measured over-target duration at 8C/16GiB", planned)
	}
}

func TestPlanCurrentWorkloadsExcludesOwnerOnlyWorkloads(t *testing.T) {
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Workloads: []Workload{
		{ID: "unit", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 50},
		{ID: "aggregate", Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 1, Shardable: false},
	}}
	catalog.Workloads[0].Shardable = true
	shards, err := planCurrentWorkloads(catalog, testPlanningLedger(testPlanningContext(), nil), testPlanningContext())
	if err != nil {
		t.Fatalf("planCurrentWorkloads() error = %v", err)
	}
	if len(shards) != 1 || len(shards[0].Workloads) != 1 || shards[0].Workloads[0].Workload.ID != "unit" {
		t.Fatalf("shards = %#v, want only shardable unit workload", shards)
	}
}

func TestPlanCurrentWorkloadsRejectsCatalogWithoutShardableWorkload(t *testing.T) {
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Workloads: []Workload{{
		ID: "aggregate", Kind: WorkloadKindGuard, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 1,
	}}}
	if _, err := planCurrentWorkloads(catalog, testPlanningLedger(testPlanningContext(), nil), testPlanningContext()); err == nil {
		t.Fatal("planCurrentWorkloads() error = nil for catalog without shardable workload")
	}
}

func TestPlanCurrentWorkloadsRejectsInvalidContextAndLedger(t *testing.T) {
	catalog := testWorkloadCatalog(Workload{ID: "unit", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 1})
	if _, err := planCurrentWorkloads(catalog, DurationLedger{Version: durationLedgerVersion}, PlanningContext{}); err == nil {
		t.Fatal("planCurrentWorkloads() error = nil for incomplete bucket context")
	}
	invalidLedger := DurationLedger{Version: durationLedgerVersion, Samples: []DurationSample{testDurationSample("unit", testWorkloadDigest, true, 0)}}
	if _, err := planCurrentWorkloads(catalog, invalidLedger, testPlanningContext()); err == nil {
		t.Fatal("planCurrentWorkloads() error = nil for zero duration sample")
	}
}

func TestBuildWorkloadExecutionPlanForWorkloadsBindsCatalogLedgerAndPlanner(t *testing.T) {
	gatePlan := mustBuildPlan(t, ProfileRelease)
	catalog, err := BuildExpandedWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{
		GoPackages: []string{"./internal/alpha", "./internal/beta"},
	})
	if err != nil {
		t.Fatalf("BuildExpandedWorkloadCatalog() error = %v", err)
	}
	snapshot := DurationLedgerSnapshot{
		Generation: 7,
		Ledger:     fastDurationLedger(catalog),
	}
	context := testLinuxPlanningContext()
	plan, err := BuildWorkloadExecutionPlanForWorkloads(gatePlan, catalog, snapshot, context, allShardableWorkloadIDs(catalog))
	if err != nil {
		t.Fatalf("BuildWorkloadExecutionPlanForWorkloads() error = %v", err)
	}
	if plan.LedgerGeneration != 7 || len(plan.Shards) != 1 || !isPrefixedSHA256Digest(plan.CatalogDigest) {
		t.Fatalf("execution plan identity = %#v", plan)
	}
	if err := plan.Validate(gatePlan, snapshot); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, shard := range plan.Shards {
		for _, workload := range shard.Workloads {
			if workload.Workload.ID == string(GateIDReleaseLayeredCheck) {
				t.Fatal("release layered attestation leaked into worker shard")
			}
		}
	}
}

func TestPlanningRejectsExpansionOnlyNilnessDescriptor(t *testing.T) {
	gatePlan := mustBuildPlan(t, ProfileRelease)
	catalog, err := BuildWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy())
	if err != nil {
		t.Fatalf("BuildWorkloadCatalog() error = %v", err)
	}
	if _, err := planCurrentWorkloads(catalog, fastDurationLedger(catalog), testLinuxPlanningContext()); err == nil || !strings.Contains(err.Error(), "expansion descriptor") {
		t.Fatalf("planCurrentWorkloads() error = %v, want expansion descriptor rejection", err)
	}
	if _, err := BuildWorkloadExecutionPlanForWorkloads(gatePlan, catalog, DurationLedgerSnapshot{Generation: 1, Ledger: fastDurationLedger(catalog)}, testLinuxPlanningContext(), allShardableWorkloadIDs(catalog)); err == nil || !strings.Contains(err.Error(), "expansion descriptor") {
		t.Fatalf("BuildWorkloadExecutionPlanForWorkloads() error = %v, want expansion descriptor rejection", err)
	}
}

func TestPlanningAcceptsExpandedNilnessPackageWorkloads(t *testing.T) {
	gatePlan := mustBuildPlan(t, ProfileRelease)
	catalog, err := BuildExpandedWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{
		GoPackages: []string{"./internal/alpha", "./internal/beta"},
	})
	if err != nil {
		t.Fatalf("BuildExpandedWorkloadCatalog() error = %v", err)
	}
	assertExpandedCatalogHasNoNilnessDescriptor(t, catalog)
	snapshot := DurationLedgerSnapshot{Generation: 1, Ledger: fastDurationLedger(catalog)}
	plan, err := BuildWorkloadExecutionPlanForWorkloads(gatePlan, catalog, snapshot, testLinuxPlanningContext(), allShardableWorkloadIDs(catalog))
	if err != nil {
		t.Fatalf("BuildWorkloadExecutionPlanForWorkloads() error = %v", err)
	}
	assertExpandedNilnessPackageWorkloadPlanned(t, plan)
}

// assertExpandedCatalogHasNoNilnessDescriptor 确认 nilness 已展开为 package workload。
func assertExpandedCatalogHasNoNilnessDescriptor(t *testing.T, catalog WorkloadCatalog) {
	t.Helper()
	for _, workload := range catalog.Workloads {
		if workload.ID == string(GateIDBackendNilness) {
			t.Fatal("expanded catalog retained nilness descriptor")
		}
	}
}

// assertExpandedNilnessPackageWorkloadPlanned 确认展开后的 nilness package 进入执行计划。
func assertExpandedNilnessPackageWorkloadPlanned(t *testing.T, plan WorkloadExecutionPlan) {
	t.Helper()
	found := false
	for _, shard := range plan.Shards {
		for _, workload := range shard.Workloads {
			parent, kind, _, targeted, parseErr := ParseWorkloadID(workload.Workload.ID)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			if parent == GateIDBackendNilness {
				if !targeted || kind != WorkloadTargetGoPackage {
					t.Fatalf("planned nilness workload = %#v", workload.Workload)
				}
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expanded nilness package workload did not enter execution plan")
	}
}

func TestWorkloadExecutionPlanRejectsTamperingAndLedgerDrift(t *testing.T) {
	gatePlan := mustBuildPlan(t, ProfileLocalFast)
	catalog, err := BuildWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy())
	if err != nil {
		t.Fatalf("BuildWorkloadCatalog() error = %v", err)
	}
	snapshot := DurationLedgerSnapshot{Generation: 3, Ledger: fastDurationLedger(catalog)}
	context := testLinuxPlanningContext()
	plan, err := BuildWorkloadExecutionPlanForWorkloads(gatePlan, catalog, snapshot, context, allShardableWorkloadIDs(catalog))
	if err != nil {
		t.Fatalf("BuildWorkloadExecutionPlanForWorkloads() error = %v", err)
	}

	tamperedEstimate := plan
	tamperedEstimate.Shards = cloneShardPlans(plan.Shards)
	tamperedEstimate.Shards[0].Workloads[0].EstimatedDurationMS++
	if err := tamperedEstimate.ValidateStored(gatePlan); err == nil {
		t.Fatal("ValidateStored() accepted tampered shard estimate")
	}

	tamperedTarget := plan
	tamperedTarget.Context.TargetDurationMS--
	if err := tamperedTarget.ValidateStored(gatePlan); err == nil {
		t.Fatal("ValidateStored() accepted a non-canonical CI target duration")
	}

	tamperedOwnerDuration := plan
	tamperedOwnerDuration.OwnerEstimatedDurationMS = FullCITargetDurationMS
	if err := tamperedOwnerDuration.ValidateStored(gatePlan); err == nil {
		t.Fatal("ValidateStored() accepted owner work that consumes the complete CI deadline")
	}

	tamperedCatalog := plan
	tamperedCatalog.Catalog.Workloads = append([]Workload(nil), plan.Catalog.Workloads...)
	tamperedCatalog.Catalog.Workloads[0].CommandDigest = strings.Repeat("f", 64)
	if err := tamperedCatalog.ValidateStored(gatePlan); err == nil {
		t.Fatal("ValidateStored() accepted tampered catalog")
	}

	driftedSnapshot := snapshot
	driftedSnapshot.Generation++
	if err := plan.Validate(gatePlan, driftedSnapshot); err == nil {
		t.Fatal("Validate() accepted a different ledger generation")
	}
}

func TestBuildWorkloadExecutionPlanForWorkloadsRejectsAbsentLedgerGeneration(t *testing.T) {
	gatePlan := mustBuildPlan(t, ProfileLocalFast)
	catalog, err := BuildWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy())
	if err != nil {
		t.Fatalf("BuildWorkloadCatalog() error = %v", err)
	}
	context := testLinuxPlanningContext()
	if _, err := BuildWorkloadExecutionPlanForWorkloads(
		gatePlan,
		catalog,
		DurationLedgerSnapshot{Ledger: DurationLedger{Version: durationLedgerVersion}},
		context,
		allShardableWorkloadIDs(catalog),
	); err == nil {
		t.Fatal("BuildWorkloadExecutionPlanForWorkloads() error = nil for generation zero")
	}
}

// planCurrentWorkloads exercises the canonical index-backed planner without the
// removed ledger-to-plan compatibility wrapper.
func planCurrentWorkloads(catalog WorkloadCatalog, ledger DurationLedger, context PlanningContext) ([]ShardPlan, error) {
	index, err := BuildDurationSampleIndex(ledger, context)
	if err != nil {
		return nil, err
	}
	return planLPTWithIndex(catalog, index)
}

func testWorkloadCatalog(workloads ...Workload) WorkloadCatalog {
	for index := range workloads {
		workloads[index].Shardable = true
		if workloads[index].InputDigest == "" {
			workloads[index].InputDigest = "sha256:" + strings.Repeat("0", 64)
		}
	}
	return WorkloadCatalog{Version: durationLedgerVersion, Workloads: workloads}
}

func cloneShardPlans(shards []ShardPlan) []ShardPlan {
	cloned := make([]ShardPlan, len(shards))
	for index, shard := range shards {
		cloned[index] = shard
		cloned[index].Workloads = append([]PlannedWorkload(nil), shard.Workloads...)
	}
	return cloned
}

func testPlanningContext() PlanningContext {
	return PlanningContext{
		Platform: "darwin", Runner: "local", Toolchain: "go1.25",
		TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-test",
	}
}

func testCalibrationPlanningContext() PlanningContext {
	context := testPlanningContext()
	context.Calibration = true
	context.CalibrationResourceClassID = "calibration"
	context.CalibrationResourceCPU = 4
	context.CalibrationResourceMemoryGiB = 8
	return context
}

func testLinuxPlanningContext() PlanningContext {
	return PlanningContext{
		Platform: "linux/amd64", Runner: "runner-digest", Toolchain: "go1.26-node22",
		TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-test-linux",
	}
}

func fastDurationLedger(catalog WorkloadCatalog) DurationLedger {
	context := testLinuxPlanningContext()
	ledger := DurationLedger{Version: durationLedgerVersion, ShardOverhead: testPlanningOverhead(context)}
	for _, workload := range catalog.Workloads {
		for _, resource := range []struct {
			classID string
			cpu     float64
			memory  float64
		}{{"small", 2, 4}, {"medium", 4, 8}, {"large", 8, 16}} {
			ledger.Samples = append(ledger.Samples, DurationSample{
				Bucket: DurationBucket{
					WorkloadID: workload.ID, CommandDigest: workload.CommandDigest,
					InputDigest: "sha256:" + strings.Repeat("0", 64),
					Platform:    "linux/amd64", Runner: "runner-digest", Toolchain: "go1.26-node22",
					ExecutionMode: DurationExecutionModeNormal, ResourceClassID: resource.classID, ResourceCPU: resource.cpu, ResourceMemoryGiB: resource.memory,
				},
				Succeeded: true, DurationMS: 1_000,
			})
		}
	}
	return ledger
}

func testPlanningLedger(context PlanningContext, samples []DurationSample) DurationLedger {
	return DurationLedger{Version: durationLedgerVersion, ShardOverhead: testPlanningOverhead(context), Samples: samples}
}

func testPlanningOverhead(context PlanningContext) *ShardOrchestrationOverhead {
	return &ShardOrchestrationOverhead{
		SchemaVersion: ShardOrchestrationOverheadSchemaVersion, PolicyVersion: ShardOverheadPolicyVersion,
		Platform: context.Platform, Runner: context.Runner, Toolchain: context.Toolchain,
		CalibrationResourceClassID: "calibration", CalibrationResourceCPU: 4, CalibrationResourceMemoryGiB: 8,
		P95MS: 0, SampleCount: 1, ProvenanceDigest: "sha256:" + strings.Repeat("d", 64),
		AcceptedGeneration: 1, AcceptedSnapshotID: context.AcceptedSnapshotID,
	}
}

func testDurationSample(workloadID, digest string, succeeded bool, durationMS int64) DurationSample {
	return DurationSample{
		Bucket: DurationBucket{
			WorkloadID: workloadID, CommandDigest: digest, InputDigest: "sha256:" + strings.Repeat("0", 64),
			Platform: "darwin", Runner: "local", Toolchain: "go1.25",
			ExecutionMode: DurationExecutionModeNormal, ResourceClassID: "small", ResourceCPU: 2, ResourceMemoryGiB: 4,
		},
		Succeeded:  succeeded,
		DurationMS: durationMS,
	}
}

func testCalibrationDurationSample(workloadID, digest string, succeeded bool, durationMS int64) DurationSample {
	sample := testDurationSample(workloadID, digest, succeeded, durationMS)
	sample.Bucket.ExecutionMode = DurationExecutionModeCalibration
	sample.Bucket.ResourceClassID = "calibration"
	sample.Bucket.ResourceCPU = 4
	sample.Bucket.ResourceMemoryGiB = 8
	return sample
}
