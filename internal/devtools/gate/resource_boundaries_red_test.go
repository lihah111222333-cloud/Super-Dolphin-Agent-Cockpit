package gate

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestSQLitePlanningIndexRejectsFutureAcceptedGenerationRow(t *testing.T) {
	store, database := rawDurationStore(t)
	seedAcceptedGenerationForTest(t, store, 1)
	insertRawDurationSample(t, database, "2", "future-planning-row", "small", "linux/amd64", "runner-digest", "go1.26-node22")
	if _, err := loadSQLiteDurationSampleIndex(database, testLinuxPlanningContext(), 1); err == nil {
		t.Fatal("SQLite planning index accepted a row from an uncommitted future generation")
	}
}
func TestSQLiteFullReadRejectsOlderThanThreeGenerationRow(t *testing.T) {
	store, database := rawDurationStore(t)
	seedAcceptedGenerationForTest(t, store, 4)
	execLocalAuthorityRowsAffectedOne(t, database, "duration metadata", `UPDATE duration_ledger_meta SET generation = '4' WHERE singleton = 1`)
	insertRawDurationSample(t, database, "1", "old-full-row", "small", "linux/amd64", "runner-digest", "go1.26-node22")
	database.Close()
	if _, err := store.Load(); err == nil {
		t.Fatal("SQLite full read accepted a row older than the current plus two-generation window")
	}
}
func TestSQLiteFullReadUsesAcceptedBaselineGeneration(t *testing.T) {
	store, database := readAuthorityFixture(t, 1)
	insertRawDurationSample(t, database, "1", "baseline-full-row", "small", "linux/amd64", "runner-digest", "go1.26-node22")
	execLocalAuthorityRowsAffectedOne(t, database, "duration metadata advance", `UPDATE duration_ledger_meta SET generation = 4 WHERE singleton = 1`)
	database.Close()
	if _, err := store.Load(); err != nil {
		t.Fatalf("baseline generation 1 sample was rejected after duration metadata advance: %v", err)
	}
}
func TestSQLiteFullReadRequiresAcceptedBaselineAuthority(t *testing.T) {
	for _, statement := range []string{`DELETE FROM ci_remote_baseline_state`, `UPDATE ci_remote_baseline_state SET state_sha256 = 'invalid' WHERE singleton = 1`} {
		store, database := readAuthorityFixture(t, 1)
		execLocalAuthorityRowsAffectedOne(t, database, "baseline authority mutation", statement)
		database.Close()
		if _, err := store.Load(); err == nil {
			t.Fatalf("Load accepted invalid baseline authority after %s", statement)
		}
	}
}
func rawDurationStore(t *testing.T) (*DurationLedgerStore, *sql.DB) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	return store, database
}
func readAuthorityFixture(t *testing.T, baseline uint64) (*DurationLedgerStore, *sql.DB) {
	store, database := rawDurationStore(t)
	database.Close()
	seedAcceptedGenerationForTest(t, store, baseline)
	return store, openWorkloadPassDatabase(t, store)
}
func insertRawDurationSample(t *testing.T, database *sql.DB, generation, workloadID, classID, platform, runner, toolchain string) {
	if _, err := database.Exec(`INSERT INTO duration_samples (accepted_generation, workload_id, command_digest, input_digest, platform, runner, toolchain, execution_mode, resource_class_id, resource_cpu, resource_memory_gib, succeeded, duration_ms, target_kind, parent_workload_id, parent_command_digest, target_name, target_status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, generation, workloadID, strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64), platform, runner, toolchain, DurationExecutionModeNormal, classID, 2, 4, 1, 100, "", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
}
func TestRetainedConsumerMissingExecutionIdentityRejectsResourceEvidence(t *testing.T) {
	store, identity := retainedV16LookupFixture(t)
	deleteRetainedConsumerExecutionIdentity(t, store, identity)
	if _, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity}); err == nil {
		t.Fatal("resource-bearing retained consumer without execution identity was accepted")
	}
}

func TestRetainedConsumerMissingExecutionIdentityAllowsMixedResourceCorrectness(t *testing.T) {
	record := RemoteCIRunRecord{
		JobID:         "legacy-mixed-resource",
		Status:        ResultStatusPassed,
		Authoritative: true,
		WorkloadResults: []RemoteCIWorkloadResult{
			{Disposition: WorkloadDispositionReused},
			{Disposition: WorkloadDispositionExecuted},
		},
		Shards: []RemoteCIShardRecord{{Resources: RemoteCIShardResources{ClassID: "small", CPU: 2, MemoryGiB: 4}}},
	}
	if err := validateRetainedConsumerExecutionIdentityPresenceWithStates([]string{record.JobID}, map[string]RemoteCIRunRecord{record.JobID: record}, map[string]struct{}{}, map[string]remoteCIExecutionIdentityState{record.JobID: remoteCIExecutionIdentityStateMissing}); err != nil {
		t.Fatalf("mixed resource retained correctness with missing identity = %v, want accepted", err)
	}
}

func TestRetainedConsumerUnknownExecutionIdentityDoesNotBecomeLegacyMissing(t *testing.T) {
	record := RemoteCIRunRecord{JobID: "unknown-identity", Status: ResultStatusPassed, Authoritative: true}
	if err := validateRetainedConsumerExecutionIdentityPresence([]string{record.JobID}, map[string]RemoteCIRunRecord{record.JobID: record}, map[string]struct{}{}); err == nil {
		t.Fatal("assembled zero identity was treated as an SQL-confirmed legacy missing row")
	}
	record.Platform = "linux/amd64"
	if err := validateRetainedConsumerExecutionIdentityPresence([]string{record.JobID}, map[string]RemoteCIRunRecord{record.JobID: record}, map[string]struct{}{}); err == nil {
		t.Fatal("partial identity was treated as an SQL-confirmed legacy missing row")
	}
	record.Platform = ""
	if err := validateRetainedConsumerExecutionIdentityPresenceWithStates([]string{record.JobID}, map[string]RemoteCIRunRecord{record.JobID: record}, map[string]struct{}{}, map[string]remoteCIExecutionIdentityState{record.JobID: remoteCIExecutionIdentityStateMissing}); err != nil {
		t.Fatalf("explicit missing state with empty identity = %v, want accepted", err)
	}
	record.Platform = "linux/amd64"
	if err := validateRetainedConsumerExecutionIdentityPresenceWithStates([]string{record.JobID}, map[string]RemoteCIRunRecord{record.JobID: record}, map[string]struct{}{}, map[string]remoteCIExecutionIdentityState{record.JobID: remoteCIExecutionIdentityStateMissing}); err == nil {
		t.Fatal("explicit missing state with partial identity was accepted")
	}
}

func TestRetainedConsumerLegacyCorrectnessDoesNotRequireCompileIdentity(t *testing.T) {
	started := time.UnixMilli(1_000).UTC()
	record := RemoteCIRunRecord{
		JobID: "legacy-compile-timing", AgentTokenDigest: "sha256:" + strings.Repeat("a", 64), AcceptedGeneration: 1,
		Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast, PlanDigest: "sha256:" + strings.Repeat("b", 64),
		CatalogDigest: "sha256:" + strings.Repeat("c", 64), ImageCacheSnapshotID: "snapshot", SourceTreeSHA: strings.Repeat("d", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("e", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("f", 64),
		RunnerImage: "runner", Status: ResultStatusPassed, Authoritative: true, CleanupComplete: true,
		StartedAt: started, CompletedAt: started.Add(time.Second),
		CompileTimingObservations: []CompileTimingObservation{compileTimingTestObservation(started, 1)},
	}
	if err := validateRetainedConsumerCorrectnessRecord(record); err != nil {
		t.Fatalf("legacy correctness rejected compile timing identity that must be ineligible only: %v", err)
	}
}

func TestV18AllHitRetainedConsumerMissingExecutionIdentityKeepsCorrectnessOnly(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	origins, proofs := recordRetainedBatchOrigins(t, store, 1)
	seedAcceptedGenerationForTest(t, store, 2)
	finalizeRetainedBatchConsumers(t, store, origins, proofs)
	seedAcceptedGenerationForTest(t, store, 4)
	recordMixedRetentionTriggers(t, store, 3, 4)
	deleteRetainedConsumerExecutionIdentity(t, store, proofs[0].Identity)
	if hits, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{proofs[0].Identity}); err != nil || len(hits) != 1 {
		t.Fatalf("v18 all-hit retained correctness lookup = hits:%d err:%v, want one hit", len(hits), err)
	}
}
func TestSQLiteShardOverheadExcludesMissingExecutionIdentity(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	sample := durationLedgerSQLiteShardOverheadRoundTripSample()
	overhead := durationLedgerSQLiteShardOverheadRoundTripValue(sample.ProvenanceDigest)
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	insertRemoteRunForTest(t, database, sample.JobID)
	insertCalibrationExecutionIdentityForTest(t, database, sample.JobID)
	if _, err := database.Exec(`DELETE FROM ci_remote_run_execution_identities WHERE job_id = ?`, sample.JobID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwapShardOverhead(1, overhead, []ShardOrchestrationOverheadSample{sample}); err != nil {
		t.Fatal(err)
	}
	planning := PlanningContext{Platform: overhead.Platform, Runner: overhead.Runner, Toolchain: overhead.Toolchain, Calibration: true, CalibrationResourceClassID: overhead.CalibrationResourceClassID, CalibrationResourceCPU: overhead.CalibrationResourceCPU, CalibrationResourceMemoryGiB: overhead.CalibrationResourceMemoryGiB, TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: overhead.AcceptedSnapshotID}
	loaded, err := store.LoadPlanning(planning)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Ledger.ShardOverhead != nil {
		t.Fatalf("missing execution identity overhead = %#v, want excluded", loaded.Ledger.ShardOverhead)
	}
}

func TestSQLiteShardOverheadRequiresExactRunAndResourceBinding(t *testing.T) {
	cases := []struct {
		name      string
		mutateRun func(*sql.DB) error
	}{
		{name: "run generation", mutateRun: func(database *sql.DB) error {
			_, err := database.Exec(`UPDATE ci_runs SET accepted_generation = '2' WHERE job_id = ?`, "overhead-roundtrip-job")
			return err
		}},
		{name: "run snapshot", mutateRun: func(database *sql.DB) error {
			_, err := database.Exec(`UPDATE ci_runs SET image_cache_snapshot_id = 'other-snapshot' WHERE job_id = ?`, "overhead-roundtrip-job")
			return err
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store := newTestDurationLedgerStore(t)
			if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
				t.Fatal(err)
			}
			seedAcceptedGenerationForTest(t, store, 1)
			sample := durationLedgerSQLiteShardOverheadRoundTripSample()
			overhead := durationLedgerSQLiteShardOverheadRoundTripValue(sample.ProvenanceDigest)
			durationLedgerSQLiteRecordShardOverheadRoundTrip(t, store, sample, overhead)
			database, err := store.openSQLiteAuthority(false)
			if err != nil {
				t.Fatal(err)
			}
			if err := testCase.mutateRun(database); err != nil {
				database.Close()
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			planning := PlanningContext{Platform: overhead.Platform, Runner: overhead.Runner, Toolchain: overhead.Toolchain, Calibration: true, CalibrationResourceClassID: overhead.CalibrationResourceClassID, CalibrationResourceCPU: overhead.CalibrationResourceCPU, CalibrationResourceMemoryGiB: overhead.CalibrationResourceMemoryGiB, TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: overhead.AcceptedSnapshotID}
			loaded, err := store.LoadPlanning(planning)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Ledger.ShardOverhead != nil {
				t.Fatalf("drifted overhead = %#v, want excluded", loaded.Ledger.ShardOverhead)
			}
		})
	}
}

func TestSQLiteShardOverheadRejectsDriftedPlanningResource(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	sample := durationLedgerSQLiteShardOverheadRoundTripSample()
	overhead := durationLedgerSQLiteShardOverheadRoundTripValue(sample.ProvenanceDigest)
	durationLedgerSQLiteRecordShardOverheadRoundTrip(t, store, sample, overhead)
	planning := PlanningContext{
		Platform: overhead.Platform, Runner: overhead.Runner, Toolchain: overhead.Toolchain,
		Calibration: true, CalibrationResourceClassID: "other-class",
		CalibrationResourceCPU: overhead.CalibrationResourceCPU, CalibrationResourceMemoryGiB: overhead.CalibrationResourceMemoryGiB,
		TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: overhead.AcceptedSnapshotID,
	}
	if _, err := store.LoadPlanning(planning); err == nil {
		t.Fatal("drifted calibration planning resource was accepted")
	}
}

func deleteRetainedConsumerExecutionIdentity(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity) {
	database := openWorkloadPassDatabase(t, store)
	var consumerID string
	err := database.QueryRow(`SELECT consumer_job_id FROM ci_retained_workload_pass_proofs WHERE identity_digest = ?`, identity.IdentityDigest).Scan(&consumerID)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	execLocalAuthorityRowsAffectedOne(t, database, "retained execution identity delete", `DELETE FROM ci_remote_run_execution_identities WHERE job_id = ?`, consumerID)
	database.Close()
}
