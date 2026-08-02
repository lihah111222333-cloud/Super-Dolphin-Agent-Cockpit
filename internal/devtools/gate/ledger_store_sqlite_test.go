package gate

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func assertSQLitePlanningEstimate(
	t *testing.T,
	store *DurationLedgerStore,
	planning PlanningContext,
	wantGeneration uint64,
	wantEstimate int64,
) {
	t.Helper()
	snapshot, err := store.LoadPlanning(planning)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Generation != wantGeneration || len(snapshot.Ledger.Samples) != 0 ||
		snapshot.SampleIndex == nil {
		t.Fatalf("planning snapshot = %#v", snapshot)
	}
	estimate, err := snapshot.SampleIndex.EstimateWorkloadDurationMS(Workload{
		ID:            "unit",
		CommandDigest: testWorkloadDigest,
	})
	if err != nil || estimate != wantEstimate {
		t.Fatalf("planning estimate = %d, err = %v", estimate, err)
	}
}

func assertSQLiteAuthorityPath(t *testing.T, store *DurationLedgerStore) {
	t.Helper()
	if filepath.Ext(store.AuthorityPath()) != ".sqlite" {
		t.Fatalf("authority path = %q", store.AuthorityPath())
	}
	if _, err := os.Stat(store.AuthorityPath()); err != nil {
		t.Fatal(err)
	}
}

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
		"idx_ci_workload_fingerprint_lookup",
		"idx_ci_workload_fingerprint_observation_tree",
		"idx_ci_workload_fingerprint_observation_latest",
		"idx_ci_workload_pass_lookup",
		"idx_ci_workload_pass_compatible",
		"idx_ci_runs_tree_status",
		"idx_ci_runs_catalog_status",
		"idx_ci_run_requesters_lookup",
		"idx_ci_run_workloads_lookup",
		"idx_ci_shards_container",
		"idx_ci_shard_workloads_lookup",
		"idx_ci_gate_executions_lookup",
		"idx_ci_run_phase_timings_hotspots",
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
	generation, err := store.AppendSamplesFast(samples)
	if err != nil {
		t.Fatal(err)
	}
	if generation != 2 {
		t.Fatalf("generation = %d, want 2", generation)
	}
}

func TestDurationLedgerSQLitePassAndRunProjectionRoundTrip(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	store.nowFunc = func() time.Time { return now }
	proof := assertWorkloadPassProofProjectionRoundTrip(t, store, now)
	assertWorkloadFingerprintProjectionRoundTrip(t, store, proof, now)
	assertRemoteCIRunProjectionRoundTrip(t, store, now)
	assertCIQueryRevision(t, store, "9", now)
}

func assertCIQueryRevision(t *testing.T, store *DurationLedgerStore, wantRevision string, wantUpdatedAt time.Time) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var revision string
	var updatedAtUnixMS int64
	if err := database.QueryRow(`
		SELECT revision, updated_at_unix_ms
		FROM ci_query_meta WHERE singleton = 1
	`).Scan(&revision, &updatedAtUnixMS); err != nil {
		t.Fatal(err)
	}
	if revision != wantRevision || updatedAtUnixMS != wantUpdatedAt.UTC().UnixMilli() {
		t.Fatalf("CI query revision = (%q, %d), want (%q, %d)", revision, updatedAtUnixMS, wantRevision, wantUpdatedAt.UTC().UnixMilli())
	}
}

func TestDurationLedgerSQLiteCompatiblePassCandidatesAreRecentAndBoundToTrees(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	proofs := make([]WorkloadPassProof, 3)
	fingerprints := make([]WorkloadFingerprintRecord, 3)
	for index := range proofs {
		digit := fmt.Sprintf("%x", index+1)
		proofs[index] = WorkloadPassProof{
			IdentityDigest:    "sha256:" + strings.Repeat(digit, 64),
			WorkloadID:        "unit",
			ExecutionDigest:   strings.Repeat("a", 64),
			InputDigest:       "sha256:" + strings.Repeat(digit, 64),
			EnvironmentDigest: "sha256:" + strings.Repeat("b", 64),
			ObjectKey:         "passes/" + digit + ".pass",
			ObservedAt:        now.Add(time.Duration(index) * time.Second),
		}
		fingerprints[index] = WorkloadFingerprintRecord{
			IdentityDigest:    proofs[index].IdentityDigest,
			WorkloadID:        proofs[index].WorkloadID,
			ExecutionDigest:   proofs[index].ExecutionDigest,
			InputDigest:       proofs[index].InputDigest,
			EnvironmentDigest: proofs[index].EnvironmentDigest,
			SourceTreeSHA:     strings.Repeat(digit, 40),
			ObservedAt:        proofs[index].ObservedAt,
		}
	}
	if err := store.RecordWorkloadFingerprints(fingerprints); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadPassProofs(proofs); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.LookupCompatibleWorkloadPassCandidates(
		[]WorkloadPassCandidateQuery{{
			WorkloadID:        "unit",
			ExecutionDigest:   proofs[0].ExecutionDigest,
			EnvironmentDigest: proofs[0].EnvironmentDigest,
		}},
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertCompatiblePassCandidates(t, candidates["unit"], proofs, fingerprints)
}

func assertCompatiblePassCandidates(
	t *testing.T,
	got []WorkloadPassCandidate,
	proofs []WorkloadPassProof,
	fingerprints []WorkloadFingerprintRecord,
) {
	t.Helper()
	if len(got) != 2 {
		t.Fatalf("compatible PASS candidates = %d, want 2", len(got))
	}
	if got[0].Proof.IdentityDigest != proofs[2].IdentityDigest ||
		got[0].SourceTreeSHA != fingerprints[2].SourceTreeSHA ||
		got[1].Proof.IdentityDigest != proofs[1].IdentityDigest ||
		got[1].SourceTreeSHA != fingerprints[1].SourceTreeSHA {
		t.Fatalf("compatible PASS candidates = %#v", got)
	}
}

func TestDurationLedgerSQLiteCatalogProjectionPreservesOrderAndObservations(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
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
		SourceTreeSHA: strings.Repeat("3", 40),
		Entrypoint:    CIEntrypointGitPreCommit,
		Profile:       ProfileLocalFast,
		ObservedAt:    time.Now().UTC().Truncate(time.Millisecond),
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

func TestDurationLedgerSQLiteConcurrentProjectionWritersShareOneAuthority(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	authorityPath := store.AuthorityPath()
	const writerCount = 12
	now := time.Now().UTC().Truncate(time.Millisecond)
	digests := make([]string, writerCount)
	for index := range writerCount {
		digests[index] = fmt.Sprintf("sha256:%064x", index+1)
	}
	runDurationLedgerProjectionWriters(t, authorityPath, digests, now)
	readerStore, err := NewDurationLedgerStore(authorityPath)
	if err != nil {
		t.Fatal(err)
	}
	proofs, err := readerStore.LookupWorkloadPassProofs(digests)
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != writerCount {
		t.Fatalf("PASS proof count = %d, want %d", len(proofs), writerCount)
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
		Profile: ProfileLocalFast, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	err = store.RecordRemoteCIRun(RemoteCIRunRecord{
		JobID: "uncovered", Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast,
		PlanDigest: "sha256:plan", CatalogDigest: digest, SourceTreeSHA: strings.Repeat("b", 40),
		CandidateCLIManifestSHA256:              strings.Repeat("c", 64),
		CandidateTestBinaryReceiptBindingDigest: "sha256:" + strings.Repeat("d", 64),
		RunnerImage:                             "ubuntu:22.04", Status: ResultStatusPassed, Authoritative: true,
		StartedAt: now, CompletedAt: now,
	})
	if err == nil || !strings.Contains(err.Error(), "does not cover") {
		t.Fatalf("passed uncovered catalog error = %v", err)
	}
}

func TestRecordRemoteCIRunRejectsUnknownStatusAndFailedCatalogDrift(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
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
		Profile: ProfileLocalFast, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	record := RemoteCIRunRecord{
		JobID: "failed-catalog-drift", Entrypoint: CIEntrypointManualCLI, Profile: ProfileLocalFast,
		PlanDigest: "sha256:plan", CatalogDigest: digest, SourceTreeSHA: strings.Repeat("b", 40),
		RunnerImage: "ubuntu:22.04", Status: ResultStatusFailed, StartedAt: now, CompletedAt: now,
		ReusedWorkloads: []GateID{GateIDAIMaintenanceSelfTest},
	}
	if err := store.RecordRemoteCIRun(record); err == nil || !strings.Contains(err.Error(), "absent from its catalog") {
		t.Fatalf("failed run workload catalog error = %v", err)
	}
	record.ReusedWorkloads = nil
	record.Shards = []RemoteCIShardRecord{{
		ShardIdentity: "sha256:" + strings.Repeat("6", 64), ContainerGroup: "eci-123", ContainerStatus: "Failed",
		Workloads:             []GateID{GateIDAIMaintenanceSelfTest},
		MaterializationTiming: ShardMaterializationTiming{Measurement: MaterializationMeasurementMeasured, ShardIdentity: "sha256:" + strings.Repeat("6", 64)},
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
		Profile: ProfileLocalFast, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	record := RemoteCIRunRecord{
		JobID: "manual-selection", Entrypoint: CIEntrypointManualCLI, Profile: ProfileLocalFast,
		PlanDigest: "sha256:plan", CatalogDigest: digest, SourceTreeSHA: strings.Repeat("d", 40),
		CandidateCLIManifestSHA256: strings.Repeat("e", 64),
		RunnerImage:                "ubuntu:22.04", Status: ResultStatusPassed, Authoritative: false,
		StartedAt: now, CompletedAt: now, CleanupComplete: true,
		ReusedWorkloads: []GateID{GateIDWhitespaceCheck},
	}
	if err := store.RecordRemoteCIRun(record); err != nil {
		t.Fatalf("record passed manual selection: %v", err)
	}
	record.JobID = "authoritative-selection"
	record.Entrypoint = CIEntrypointGitPreCommit
	record.Authoritative = true
	record.CandidateTestBinaryReceiptBindingDigest = "sha256:" + strings.Repeat("f", 64)
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

func TestRecordRemoteCIRunRejectsPassedShardDispositionDrift(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
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
		SourceTreeSHA: strings.Repeat("2", 40),
		Entrypoint:    CIEntrypointGitPreCommit,
		Profile:       ProfileLocalFast,
		ObservedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	record := RemoteCIRunRecord{
		JobID: "job-shard-drift", Entrypoint: CIEntrypointGitPreCommit,
		Profile: ProfileLocalFast, PlanDigest: "sha256:plan",
		CatalogDigest: digest, SourceTreeSHA: strings.Repeat("2", 40),
		RunnerImage: "ubuntu:22.04", Status: ResultStatusPassed,
		Authoritative: true, StartedAt: now, CompletedAt: now.Add(time.Second),
		CandidateTestBinaryReceiptBindingDigest: "sha256:" + strings.Repeat("3", 64),
		CleanupComplete:                         true, CacheMisses: []GateID{GateIDWhitespaceCheck},
	}
	if err := store.RecordRemoteCIRun(record); err == nil {
		t.Fatal("passed cache miss without a shard was accepted")
	}
	record.CacheMisses = nil
	record.ReusedWorkloads = []GateID{GateIDWhitespaceCheck}
	record.Shards = []RemoteCIShardRecord{{
		ShardIdentity: "shard-reused", ContainerGroup: "eci-reused",
		ContainerStatus: "Succeeded", Workloads: []GateID{GateIDWhitespaceCheck},
	}}
	if err := store.RecordRemoteCIRun(record); err == nil {
		t.Fatal("passed reused workload executed in a shard was accepted")
	}
	record.ReusedWorkloads = []GateID{"catalog-external"}
	record.Shards = nil
	if err := store.RecordRemoteCIRun(record); err == nil {
		t.Fatal("passed catalog-external workload was accepted")
	}
}

func calibrationAtMillisecond(calibration *DurationCalibration) *DurationCalibration {
	copy := *calibration
	copy.CompletedAt = copy.CompletedAt.UTC().Truncate(time.Millisecond)
	return &copy
}

func testDurationPlanningContext() PlanningContext {
	return PlanningContext{
		Platform:         "darwin",
		Runner:           "local",
		Toolchain:        "go1.25",
		MaxShards:        8,
		TargetDurationMS: FullCITargetDurationMS,
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
