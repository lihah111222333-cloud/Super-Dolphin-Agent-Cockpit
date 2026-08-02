package gate

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestNewDurationLedgerStoreRequiresAbsoluteCanonicalParent(t *testing.T) {
	if _, err := NewDurationLedgerStore("duration-ledger.sqlite"); err == nil {
		t.Fatal("relative duration ledger path was accepted")
	}
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "ledger-parent")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	store, err := NewDurationLedgerStore(filepath.Join(alias, "duration-ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalRoot, "duration-ledger.sqlite")
	if store.AuthorityPath() != want {
		t.Fatalf("authority path = %q, want %q", store.AuthorityPath(), want)
	}
}

func TestDurationLedgerSQLiteCreatesRequiredQueryIndexes(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, name := range []string{
		"idx_duration_samples_planning",
		"idx_duration_samples_retention",
		"idx_duration_samples_target",
		"idx_duration_calibrations_environment",
		"idx_ci_catalog_observations_tree_entrypoint",
		"idx_ci_catalog_workloads_order",
		"idx_ci_catalog_workloads_identity",
		"idx_ci_catalog_workloads_target",
		"idx_ci_runs_tree_status",
		"idx_ci_runs_catalog_status",
		"idx_ci_run_requesters_lookup",
		"idx_ci_shards_container",
		"idx_ci_shard_workloads_lookup",
		"idx_ci_gate_executions_lookup",
	} {
		var count int
		if err := database.QueryRow(`
			SELECT COUNT(*)
			FROM sqlite_master
			WHERE type = 'index' AND name = ?
		`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("SQLite index %q count = %d", name, count)
		}
	}
}

func TestDurationLedgerSQLiteCompactsLargeRetentionScopeWithoutVariableOverflow(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, testDurationLedger(1)); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	const sampleCount = 7_000
	samples := make([]DurationSample, sampleCount)
	for index := range samples {
		samples[index] = testDurationSample(
			fmt.Sprintf("workload-%05d", index),
			testWorkloadDigest,
			true,
			int64(index+1),
		)
	}
	generation, err := store.AppendSamplesFast(1, samples)
	if err != nil {
		t.Fatal(err)
	}
	if generation != 2 {
		t.Fatalf("generation = %d, want 2", generation)
	}
}

func TestDurationLedgerSQLiteCatalogProjectionPreservesOrderAndObservations(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	catalog := WorkloadCatalog{
		Version:       durationLedgerVersion,
		Authoritative: true,
		Workloads: []Workload{
			{
				ID: string(GateIDAIMaintenanceSelfTest), Kind: WorkloadKindGuard,
				CommandDigest: strings.Repeat("1", 64), BootstrapEstimateMS: 2_000,
				Shardable: true,
			},
			{
				ID: string(GateIDWhitespaceCheck), Kind: WorkloadKindGuard,
				CommandDigest: strings.Repeat("2", 64), BootstrapEstimateMS: 1_000,
				Shardable: true,
			},
		},
	}
	first := WorkloadCatalogObservation{
		SourceTreeSHA:      strings.Repeat("3", 40),
		Entrypoint:         CIEntrypointGitPreCommit,
		Profile:            ProfileLocalFast,
		AcceptedGeneration: 1,
		ObservedAt:         time.Now().UTC().Truncate(time.Millisecond),
	}
	second := first
	second.SourceTreeSHA = strings.Repeat("4", 40)
	second.ObservedAt = first.ObservedAt.Add(time.Second)
	if err := store.RecordWorkloadCatalog(catalog, first); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadCatalog(catalog, second); err != nil {
		t.Fatal(err)
	}
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.LoadWorkloadCatalogRecord(digest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(record.Catalog, catalog) {
		t.Fatalf("catalog = %#v, want %#v", record.Catalog, catalog)
	}
	if !reflect.DeepEqual(record.Observations, []WorkloadCatalogObservation{second, first}) {
		t.Fatalf("observations = %#v", record.Observations)
	}
}

func TestCompareAndSwapCalibrationPreservesSQLiteSamples(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	first, err := store.CompareAndSwap(0, testDurationLedger(101))
	if err != nil {
		t.Fatal(err)
	}
	calibration := testSQLiteDurationCalibration()
	updated, err := store.CompareAndSwapCalibration(first.Generation, &calibration)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Generation != first.Generation+1 {
		t.Fatalf("generation = %d", updated.Generation)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Ledger.Samples) != 1 ||
		loaded.Ledger.Samples[0].DurationMS != 101 ||
		!reflect.DeepEqual(loaded.Ledger.Calibration, calibrationAtMillisecond(&calibration)) {
		t.Fatalf("loaded ledger = %#v", loaded.Ledger)
	}
}

func TestRecordRemoteCIRunRejectsUncoveredPassedCatalogWorkload(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	now := time.Now().UTC().Truncate(time.Millisecond)
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Authoritative: true, Workloads: []Workload{{
		ID: string(GateIDAIMaintenanceSelfTest), Kind: WorkloadKindGuard,
		CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 1_000, Shardable: true,
	}}}
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{
		SourceTreeSHA: strings.Repeat("b", 40), Entrypoint: CIEntrypointGitPreCommit,
		Profile: ProfileLocalFast, AcceptedGeneration: 1, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	err = store.RecordRemoteCIRun(RemoteCIRunRecord{
		JobID: "uncovered", Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast,
		AcceptedGeneration: 1,
		PlanDigest:         "sha256:plan", CatalogDigest: digest, SourceTreeSHA: strings.Repeat("b", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("1", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("2", 64),
		RunnerImage: "ubuntu:22.04", Status: ResultStatusPassed, Authoritative: true,
		StartedAt: now, CompletedAt: now,
		TimingObservations: []TimingObservation{authoritativeRunTimingObservation("uncovered")},
	})
	if err == nil || !strings.Contains(err.Error(), "does not cover") {
		t.Fatalf("passed uncovered catalog error = %v", err)
	}
}

// TestRecordRemoteCIRunRejectsAuthoritativeRunWithoutTimingObservations keeps the
// authority boundary at the direct SQLite store entrypoint.
func TestRecordRemoteCIRunRejectsAuthoritativeRunWithoutTimingObservations(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	err := store.RecordRemoteCIRun(RemoteCIRunRecord{
		JobID: "authoritative-missing-timing", Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast,
		AcceptedGeneration: 1,
		PlanDigest:         "sha256:plan", CatalogDigest: "sha256:" + strings.Repeat("a", 64), SourceTreeSHA: strings.Repeat("a", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("b", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("c", 64),
		RunnerImage: "ubuntu:22.04", Status: ResultStatusFailed, Authoritative: true, StartedAt: now, CompletedAt: now,
	})
	if err == nil || !strings.Contains(err.Error(), "requires complete timing observations") {
		t.Fatalf("authoritative empty timing observations error = %v", err)
	}
}

func TestRecordRemoteCIRunRejectsUnknownStatusAndFailedCatalogDrift(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	now := time.Now().UTC().Truncate(time.Millisecond)
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Workloads: []Workload{{
		ID: string(GateIDWhitespaceCheck), Kind: WorkloadKindGuard,
		CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 1_000, Shardable: true,
	}}}
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{
		SourceTreeSHA: strings.Repeat("b", 40), Entrypoint: CIEntrypointManualCLI,
		Profile: ProfileLocalFast, AcceptedGeneration: 1, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	record := RemoteCIRunRecord{
		JobID: "failed-catalog-drift", Entrypoint: CIEntrypointManualCLI, Profile: ProfileLocalFast,
		AcceptedGeneration: 1,
		PlanDigest:         "sha256:plan", CatalogDigest: digest, SourceTreeSHA: strings.Repeat("b", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("3", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("4", 64),
		RunnerImage: "ubuntu:22.04", Status: ResultStatusFailed, StartedAt: now, CompletedAt: now,
		Shards: []RemoteCIShardRecord{{
			ShardIdentity: "sha256:" + strings.Repeat("5", 64), ContainerGroup: "eci-failed", ContainerStatus: "Failed",
			Workloads:             []GateID{GateIDAIMaintenanceSelfTest},
			MaterializationTiming: measuredShardMaterializationTiming("sha256:" + strings.Repeat("5", 64)),
		}},
	}
	if err := store.RecordRemoteCIRun(record); err == nil || !strings.Contains(err.Error(), "absent from its catalog") {
		t.Fatalf("failed run workload catalog error = %v", err)
	}
	record.Shards = []RemoteCIShardRecord{{
		ShardIdentity: "sha256:" + strings.Repeat("6", 64), ContainerGroup: "eci-123", ContainerStatus: "Failed",
		Workloads:             []GateID{GateIDAIMaintenanceSelfTest},
		MaterializationTiming: measuredShardMaterializationTiming("sha256:" + strings.Repeat("6", 64)),
	}}
	if err := store.RecordRemoteCIRun(record); err == nil || !strings.Contains(err.Error(), "absent from its catalog") {
		t.Fatalf("failed run shard workload catalog error = %v", err)
	}
	record.Shards = nil
	record.Status = ResultStatus("unknown")
	if err := store.RecordRemoteCIRun(record); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unknown remote CI run status error = %v", err)
	}
}

func TestRecordRemoteCIRunAcceptsPassedManualSelectionCatalog(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	now := time.Now().UTC().Truncate(time.Millisecond)
	catalog := WorkloadCatalog{
		Version: durationLedgerVersion, Authoritative: false,
		Workloads: []Workload{{
			ID: string(GateIDWhitespaceCheck), Kind: WorkloadKindGuard,
			CommandDigest: strings.Repeat("c", 64), BootstrapEstimateMS: 1_000,
			Shardable: true,
		}},
	}
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{
		SourceTreeSHA: strings.Repeat("d", 40), Entrypoint: CIEntrypointManualCLI,
		Profile: ProfileLocalFast, AcceptedGeneration: 1, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	record := RemoteCIRunRecord{
		JobID: "manual-selection", Entrypoint: CIEntrypointManualCLI, Profile: ProfileLocalFast,
		AcceptedGeneration: 1,
		PlanDigest:         "sha256:plan", CatalogDigest: digest, SourceTreeSHA: strings.Repeat("d", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("5", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("6", 64),
		RunnerImage: "ubuntu:22.04", Status: ResultStatusPassed, Authoritative: false,
		StartedAt: now, CompletedAt: now, CleanupComplete: true,
		TimingObservations: []TimingObservation{authoritativeRunTimingObservation("manual-selection")},
		Shards: []RemoteCIShardRecord{{
			ShardIdentity: "sha256:" + strings.Repeat("7", 64), ContainerGroup: "eci-manual", ContainerStatus: "Succeeded",
			Workloads:             []GateID{GateIDWhitespaceCheck},
			MaterializationTiming: measuredShardMaterializationTiming("sha256:" + strings.Repeat("7", 64)),
		}},
	}
	if err := store.RecordRemoteCIRun(record); err != nil {
		t.Fatalf("record passed manual selection: %v", err)
	}
	execution := PlanGateExecution{ShardIdentity: record.Shards[0].ShardIdentity, GateID: GateIDWhitespaceCheck, StartedAt: now, CompletedAt: now.Add(time.Millisecond), ExecutionProfile: ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: CacheObservationMiss, CacheMeasurement: "measured"}}
	record.WorkloadExecutions = []PlanGateExecution{execution}
	record.TimingObservations = authoritativeTimingObservationsForTest(record.JobID, execution)
	record.JobID = "authoritative-selection"
	record.Entrypoint = CIEntrypointGitPreCommit
	record.Authoritative = true
	for index := range record.TimingObservations {
		record.TimingObservations[index].JobID = record.JobID
	}
	if err := store.RecordRemoteCIRun(record); err == nil ||
		!strings.Contains(err.Error(), "requires an authoritative workload catalog") {
		t.Fatalf("authoritative run with selection catalog error = %v", err)
	}
	record.JobID = "manual-authority-mismatch"
	record.Entrypoint = CIEntrypointManualCLI
	if err := store.RecordRemoteCIRun(record); err == nil ||
		!strings.Contains(err.Error(), "does not match entrypoint") {
		t.Fatalf("manual run authority mismatch error = %v", err)
	}
}

func TestRecordRemoteCIRunRejectsPassedShardCoverageDrift(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	catalog := WorkloadCatalog{
		Version: durationLedgerVersion, Authoritative: true,
		Workloads: []Workload{{
			ID: string(GateIDWhitespaceCheck), Kind: WorkloadKindGuard,
			CommandDigest: strings.Repeat("1", 64), BootstrapEstimateMS: 1_000,
			Shardable: true,
		}},
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{
		SourceTreeSHA:      strings.Repeat("2", 40),
		Entrypoint:         CIEntrypointGitPreCommit,
		Profile:            ProfileLocalFast,
		AcceptedGeneration: 1,
		ObservedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	record := RemoteCIRunRecord{
		JobID: "job-shard-drift", Entrypoint: CIEntrypointGitPreCommit,
		AcceptedGeneration: 1,
		Profile:            ProfileLocalFast, PlanDigest: "sha256:plan",
		CatalogDigest: digest, SourceTreeSHA: strings.Repeat("2", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("7", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("8", 64),
		RunnerImage: "ubuntu:22.04", Status: ResultStatusPassed,
		Authoritative: true, StartedAt: now, CompletedAt: now.Add(time.Second),
		CleanupComplete: true,
	}
	if err := store.RecordRemoteCIRun(record); err == nil {
		t.Fatal("passed workload without a shard was accepted")
	}
	record.Shards = []RemoteCIShardRecord{{
		ShardIdentity: "sha256:" + strings.Repeat("9", 64), ContainerGroup: "eci-executed",
		ContainerStatus: "Succeeded", Workloads: []GateID{GateIDWhitespaceCheck},
		MaterializationTiming: measuredShardMaterializationTiming("sha256:" + strings.Repeat("9", 64)),
	}}
	execution := PlanGateExecution{ShardIdentity: record.Shards[0].ShardIdentity, GateID: GateIDWhitespaceCheck, StartedAt: now, CompletedAt: now.Add(time.Millisecond), ExecutionProfile: ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: CacheObservationMiss, CacheMeasurement: "measured"}}
	record.WorkloadExecutions = []PlanGateExecution{execution}
	record.TimingObservations = authoritativeTimingObservationsForTest(record.JobID, execution)
	if err := store.RecordRemoteCIRun(record); err != nil {
		t.Fatalf("passed executed workload rejected: %v", err)
	}
	record.JobID = "job-catalog-external"
	record.Shards[0].Workloads = []GateID{"catalog-external"}
	if err := store.RecordRemoteCIRun(record); err == nil {
		t.Fatal("passed catalog-external workload was accepted")
	}
}

func authoritativeRunTimingObservation(jobID string) TimingObservation {
	startedAt := time.UnixMilli(100)
	return TimingObservation{JobID: jobID, Scope: cicontract.TimingScopeRun, Phase: cicontract.TimingTotal, StartedAt: startedAt, CompletedAt: startedAt.Add(time.Millisecond), DurationMS: 1, Measurement: cicontract.ObservationMeasured, Aggregation: cicontract.TimingAggregationCriticalPath, CacheEvidence: NewNotApplicableCacheEvidence("run_has_no_workload_cache")}
}

func calibrationAtMillisecond(calibration *DurationCalibration) *DurationCalibration {
	copy := *calibration
	copy.CompletedAt = copy.CompletedAt.UTC().Truncate(time.Millisecond)
	return &copy
}

func measuredShardMaterializationTiming(shardIdentity string) ShardMaterializationTiming {
	return ShardMaterializationTiming{
		Measurement:   MaterializationMeasurementMeasured,
		ShardIdentity: shardIdentity,
		Source: MaterializationPhaseTiming{
			DownloadMS: 5, VerifyMS: 3, InstallMS: 2, MaterializeMS: 10,
		},
		Baseline: MaterializationPhaseTiming{
			DownloadMS: 7, VerifyMS: 4, InstallMS: 3, MaterializeMS: 14,
		},
		CandidateCompile: MaterializationPhaseTiming{
			DownloadMS: 11, VerifyMS: 6, InstallMS: 4, MaterializeMS: 21,
		},
		CandidateTestBinaries: MaterializationPhaseTiming{
			DownloadMS: 13, VerifyMS: 7, InstallMS: 5, MaterializeMS: 25,
		},
	}
}

func testSQLiteDurationCalibration() DurationCalibration {
	return DurationCalibration{
		SchemaVersion:        DurationCalibrationSchemaVersion,
		Commit:               strings.Repeat("1", 40),
		Tree:                 strings.Repeat("2", 40),
		Platform:             "linux/amd64",
		Runner:               "runner-v1",
		Toolchain:            RequiredGoToolchain,
		CommitEntrypoint:     CIEntrypointGitPreCommit,
		PushEntrypoint:       CIEntrypointGitPrePush,
		ReleaseEntrypoint:    CIEntrypointRelease,
		CommitCatalogDigest:  "sha256:" + strings.Repeat("3", 64),
		PushCatalogDigest:    "sha256:" + strings.Repeat("4", 64),
		ReleaseCatalogDigest: "sha256:" + strings.Repeat("5", 64),
		WorkloadCount:        1,
		RacePackageCount:     1,
		CompletedAt:          time.Now().UTC(),
	}
}
