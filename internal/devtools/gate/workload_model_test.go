package gate

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const testWorkloadDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestDurationCalibrationBindsEnvironmentAndCatalogs(t *testing.T) {
	calibration := &DurationCalibration{
		SchemaVersion:        DurationCalibrationSchemaVersion,
		Commit:               strings.Repeat("1", 40),
		Tree:                 strings.Repeat("2", 40),
		Platform:             "linux/amd64",
		Runner:               "sha256:" + strings.Repeat("3", 64),
		Toolchain:            "sha256:" + strings.Repeat("4", 64),
		CommitEntrypoint:     CIEntrypointGitPreCommit,
		PushEntrypoint:       CIEntrypointGitPrePush,
		ReleaseEntrypoint:    CIEntrypointRelease,
		CommitCatalogDigest:  "sha256:" + strings.Repeat("5", 64),
		PushCatalogDigest:    "sha256:" + strings.Repeat("6", 64),
		ReleaseCatalogDigest: "sha256:" + strings.Repeat("7", 64),
		WorkloadCount:        10,
		RacePackageCount:     2,
		CompletedAt:          time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC),
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
			fields:   []string{"calibration", "samples", "version"},
		},
		{
			name:     "calibration",
			producer: reflect.TypeFor[DurationCalibration](),
			fields: []string{
				"commit", "commit_catalog_digest", "commit_entrypoint", "completed_at", "platform",
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
				"command_digest", "platform", "runner", "toolchain", "workload_id",
			},
		},
		{
			name:     "planning context",
			producer: reflect.TypeFor[PlanningContext](),
			fields:   []string{"platform", "runner", "target_duration_ms", "toolchain"},
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
			WorkloadID:    GoTestDurationWorkloadID(parentID, name),
			CommandDigest: GoTestDurationCommandDigest(parentDigest, name),
			Platform:      "linux/amd64",
			Runner:        "runner-v1",
			Toolchain:     "toolchain-v1",
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

func TestPlanLPTUsesOnlyComparableSuccessfulSamples(t *testing.T) {
	catalog := testWorkloadCatalog(
		Workload{ID: "alpha", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 100},
		Workload{ID: "beta", Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 200},
		Workload{ID: "gamma", Kind: WorkloadKindNodeTest, CommandDigest: strings.Repeat("b", 64), BootstrapEstimateMS: 300},
	)
	ledger := DurationLedger{Version: durationLedgerVersion, Samples: []DurationSample{
		testDurationSample("alpha", testWorkloadDigest, true, 900),
		testDurationSample("alpha", testWorkloadDigest, false, 1),
		testDurationSample("beta", strings.Repeat("a", 64), true, 800),
		{Bucket: DurationBucket{WorkloadID: "gamma", CommandDigest: strings.Repeat("b", 64), Platform: "darwin", Runner: "other", Toolchain: "go1.25"}, Succeeded: true, DurationMS: 9},
	}}

	shards, err := PlanLPT(catalog, ledger, testPlanningContext())
	if err != nil {
		t.Fatalf("PlanLPT() error = %v", err)
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
	context := testPlanningContext()
	failed := testDurationSample(workload.ID, workload.CommandDigest, false, 900)
	if HasComparableSuccessfulDurationSample(workload, DurationLedger{Samples: []DurationSample{failed}}, context) {
		t.Fatal("failed duration sample was accepted as comparable success")
	}
	succeeded := failed
	succeeded.Succeeded = true
	succeeded.Bucket.Runner = "other-runner"
	if HasComparableSuccessfulDurationSample(workload, DurationLedger{Samples: []DurationSample{succeeded}}, context) {
		t.Fatal("different runner duration sample was accepted as comparable success")
	}
	succeeded.Bucket.Runner = context.Runner
	if !HasComparableSuccessfulDurationSample(workload, DurationLedger{Samples: []DurationSample{succeeded}}, context) {
		t.Fatal("exact successful duration sample was not recognized")
	}
}

func TestPlanLPTUsesOneShardBelow100SecondTarget(t *testing.T) {
	catalog := testWorkloadCatalog(
		Workload{ID: "charlie", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 50},
		Workload{ID: "alpha", Kind: WorkloadKindGoTest, CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 50},
		Workload{ID: "bravo", Kind: WorkloadKindGoTest, CommandDigest: strings.Repeat("b", 64), BootstrapEstimateMS: 50},
	)
	shards, err := PlanLPT(catalog, DurationLedger{Version: durationLedgerVersion}, testPlanningContext())
	if err != nil {
		t.Fatalf("PlanLPT() error = %v", err)
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

func TestPlanLPTAutomaticallyAddsShardsToMeet100SecondSLA(t *testing.T) {
	catalog := testWorkloadCatalog(
		Workload{ID: "alpha", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 60_000},
		Workload{ID: "beta", Kind: WorkloadKindGoTest, CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 60_000},
		Workload{ID: "gamma", Kind: WorkloadKindGoTest, CommandDigest: strings.Repeat("b", 64), BootstrapEstimateMS: 60_000},
	)
	shards, err := PlanLPT(catalog, DurationLedger{Version: durationLedgerVersion}, testPlanningContext())
	if err != nil {
		t.Fatalf("PlanLPT() error = %v", err)
	}
	if len(shards) != 3 {
		t.Fatalf("len(shards) = %d, want 3 to keep every shard within 100 seconds", len(shards))
	}
}

func TestPlanLPTKeepsIndivisibleWorkloadOver100SecondsRunnable(t *testing.T) {
	catalog := testWorkloadCatalog(
		Workload{ID: "too-slow", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 100_001},
	)
	shards, err := PlanLPT(catalog, DurationLedger{Version: durationLedgerVersion}, testPlanningContext())
	if err != nil {
		t.Fatalf("PlanLPT() rejected an over-target workload: %v", err)
	}
	if len(shards) != 1 || len(shards[0].Workloads) != 1 ||
		shards[0].Workloads[0].Workload.ID != "too-slow" ||
		shards[0].Workloads[0].EstimatedDurationMS != 100_001 {
		t.Fatalf("PlanLPT() shards = %#v, want runnable over-target workload", shards)
	}
}

func TestPlanLPTUsesAtomicEstimateAfterSuccessfulPreparationOverrun(t *testing.T) {
	workload, err := NewGoTestWorkload(
		GateIDBackendTestWithGuard,
		"./internal/archtest",
		"TestDeterministicTimeGuard",
		10_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	ledger := DurationLedger{Version: durationLedgerVersion, Samples: []DurationSample{
		testDurationSample(workload.ID, workload.CommandDigest, true, FullCITargetDurationMS+50),
	}}
	shards, err := PlanLPT(testWorkloadCatalog(workload), ledger, testPlanningContext())
	if err != nil {
		t.Fatalf("PlanLPT() rejected a passed atomic test with deadline-external preparation: %v", err)
	}
	if got := shards[0].Workloads[0].EstimatedDurationMS; got != workload.BootstrapEstimateMS {
		t.Fatalf("atomic test estimate = %dms, want %dms", got, workload.BootstrapEstimateMS)
	}
}

func TestPlanLPTKeepsMeasuredPackageOver100SecondsRunnable(t *testing.T) {
	workload, err := NewGoPackageWorkload(GateIDBackendTestWithGuard, "./internal/archtest", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	ledger := DurationLedger{Version: durationLedgerVersion, Samples: []DurationSample{
		testDurationSample(workload.ID, workload.CommandDigest, true, FullCITargetDurationMS+50),
	}}
	shards, err := PlanLPT(testWorkloadCatalog(workload), ledger, testPlanningContext())
	if err != nil {
		t.Fatalf("PlanLPT() rejected a measured over-target package: %v", err)
	}
	if got := shards[0].Workloads[0].EstimatedDurationMS; got != FullCITargetDurationMS+50 {
		t.Fatalf("package estimate = %dms, want measured over-target duration", got)
	}
}

func TestPlanLPTExcludesOwnerOnlyWorkloads(t *testing.T) {
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Workloads: []Workload{
		{ID: "unit", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 50},
		{ID: "aggregate", Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 1, Shardable: false},
	}}
	catalog.Workloads[0].Shardable = true
	shards, err := PlanLPT(catalog, DurationLedger{Version: durationLedgerVersion}, testPlanningContext())
	if err != nil {
		t.Fatalf("PlanLPT() error = %v", err)
	}
	if len(shards) != 1 || len(shards[0].Workloads) != 1 || shards[0].Workloads[0].Workload.ID != "unit" {
		t.Fatalf("shards = %#v, want only shardable unit workload", shards)
	}
}

func TestPlanLPTRejectsCatalogWithoutShardableWorkload(t *testing.T) {
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Workloads: []Workload{{
		ID: "aggregate", Kind: WorkloadKindGuard, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 1,
	}}}
	if _, err := PlanLPT(catalog, DurationLedger{Version: durationLedgerVersion}, testPlanningContext()); err == nil {
		t.Fatal("PlanLPT() error = nil for catalog without shardable workload")
	}
}

func TestPlanLPTRejectsInvalidContextAndLedger(t *testing.T) {
	catalog := testWorkloadCatalog(Workload{ID: "unit", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 1})
	if _, err := PlanLPT(catalog, DurationLedger{Version: durationLedgerVersion}, PlanningContext{}); err == nil {
		t.Fatal("PlanLPT() error = nil for incomplete bucket context")
	}
	invalidLedger := DurationLedger{Version: durationLedgerVersion, Samples: []DurationSample{testDurationSample("unit", testWorkloadDigest, true, 0)}}
	if _, err := PlanLPT(catalog, invalidLedger, testPlanningContext()); err == nil {
		t.Fatal("PlanLPT() error = nil for zero duration sample")
	}
}

func TestBuildWorkloadExecutionPlanBindsCatalogLedgerAndLPT(t *testing.T) {
	gatePlan := mustBuildPlan(t, ProfileRelease)
	catalog, err := BuildWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy())
	if err != nil {
		t.Fatalf("BuildWorkloadCatalog() error = %v", err)
	}
	snapshot := DurationLedgerSnapshot{
		Generation: 7,
		Ledger:     fastDurationLedger(catalog),
	}
	context := testLinuxPlanningContext()
	plan, err := BuildWorkloadExecutionPlan(gatePlan, catalog, snapshot, context)
	if err != nil {
		t.Fatalf("BuildWorkloadExecutionPlan() error = %v", err)
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

func TestBuildWorkloadExecutionPlanWithReusePlansOnlyMisses(t *testing.T) {
	gatePlan := mustBuildPlan(t, ProfileRelease)
	catalog, err := BuildWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := DurationLedgerSnapshot{Generation: 7, Ledger: fastDurationLedger(catalog)}
	context := testLinuxPlanningContext()
	reused := []string{catalog.Workloads[0].ID, catalog.Workloads[1].ID}
	plan, err := BuildWorkloadExecutionPlanWithReuse(gatePlan, catalog, snapshot, context, reused)
	if err != nil {
		t.Fatalf("BuildWorkloadExecutionPlanWithReuse() error = %v", err)
	}
	assertReusedWorkloadsNotPlanned(t, plan, reused)
	if len(plan.Shards) != 1 {
		t.Fatalf("shard count = %d", len(plan.Shards))
	}
	if err := plan.Validate(gatePlan, snapshot); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	all := testShardableWorkloadIDList(catalog)
	cacheOnly, err := BuildWorkloadExecutionPlanWithReuse(gatePlan, catalog, snapshot, context, all)
	if err != nil {
		t.Fatalf("cache-only plan error = %v", err)
	}
	if len(cacheOnly.Shards) != 0 {
		t.Fatalf("cache-only plan created %d shards", len(cacheOnly.Shards))
	}
	if err := cacheOnly.Validate(gatePlan, snapshot); err != nil {
		t.Fatalf("cache-only Validate() error = %v", err)
	}
}

func assertReusedWorkloadsNotPlanned(t *testing.T, plan WorkloadExecutionPlan, reused []string) {
	t.Helper()
	if !reflect.DeepEqual(plan.ReusedWorkloads, reused) {
		t.Fatalf("ReusedWorkloads = %v, want %v", plan.ReusedWorkloads, reused)
	}
	seen := plannedWorkloadIDs(plan)
	for _, workloadID := range reused {
		if _, executed := seen[workloadID]; executed {
			t.Fatalf("reused workload %q was assigned to a shard", workloadID)
		}
	}
}

func plannedWorkloadIDs(plan WorkloadExecutionPlan) map[string]struct{} {
	seen := make(map[string]struct{})
	for _, shard := range plan.Shards {
		for _, workload := range shard.Workloads {
			seen[workload.Workload.ID] = struct{}{}
		}
	}
	return seen
}

func testShardableWorkloadIDList(catalog WorkloadCatalog) []string {
	var ids []string
	for _, workload := range catalog.Workloads {
		if workload.Shardable {
			ids = append(ids, workload.ID)
		}
	}
	return ids
}

func TestWorkloadExecutionPlanRejectsTamperingAndLedgerDrift(t *testing.T) {
	gatePlan := mustBuildPlan(t, ProfileLocalFast)
	catalog, err := BuildWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy())
	if err != nil {
		t.Fatalf("BuildWorkloadCatalog() error = %v", err)
	}
	snapshot := DurationLedgerSnapshot{Generation: 3, Ledger: fastDurationLedger(catalog)}
	context := testLinuxPlanningContext()
	plan, err := BuildWorkloadExecutionPlan(gatePlan, catalog, snapshot, context)
	if err != nil {
		t.Fatalf("BuildWorkloadExecutionPlan() error = %v", err)
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

func TestBuildWorkloadExecutionPlanRejectsAbsentLedgerGeneration(t *testing.T) {
	gatePlan := mustBuildPlan(t, ProfileLocalFast)
	catalog, err := BuildWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy())
	if err != nil {
		t.Fatalf("BuildWorkloadCatalog() error = %v", err)
	}
	context := testLinuxPlanningContext()
	if _, err := BuildWorkloadExecutionPlan(
		gatePlan,
		catalog,
		DurationLedgerSnapshot{Ledger: DurationLedger{Version: durationLedgerVersion}},
		context,
	); err == nil {
		t.Fatal("BuildWorkloadExecutionPlan() error = nil for generation zero")
	}
}

func testWorkloadCatalog(workloads ...Workload) WorkloadCatalog {
	for index := range workloads {
		workloads[index].Shardable = true
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
		TargetDurationMS: FullCITargetDurationMS,
	}
}

func testLinuxPlanningContext() PlanningContext {
	return PlanningContext{
		Platform: "linux/amd64", Runner: "runner-digest", Toolchain: "go1.26-node22",
		TargetDurationMS: FullCITargetDurationMS,
	}
}

func fastDurationLedger(catalog WorkloadCatalog) DurationLedger {
	ledger := DurationLedger{Version: durationLedgerVersion}
	for _, workload := range catalog.Workloads {
		ledger.Samples = append(ledger.Samples, DurationSample{
			Bucket: DurationBucket{
				WorkloadID: workload.ID, CommandDigest: workload.CommandDigest,
				Platform: "linux/amd64", Runner: "runner-digest", Toolchain: "go1.26-node22",
			},
			Succeeded: true, DurationMS: 1_000,
		})
	}
	return ledger
}

func testDurationSample(workloadID, digest string, succeeded bool, durationMS int64) DurationSample {
	return DurationSample{
		Bucket:     DurationBucket{WorkloadID: workloadID, CommandDigest: digest, Platform: "darwin", Runner: "local", Toolchain: "go1.25"},
		Succeeded:  succeeded,
		DurationMS: durationMS,
	}
}
