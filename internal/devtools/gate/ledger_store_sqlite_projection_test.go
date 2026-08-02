package gate

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

func TestRemoteCIRunRecordFieldRegistry(t *testing.T) {
	want := []string{"JobID", "RequesterFingerprint", "Entrypoint", "Profile", "PlanDigest", "CatalogDigest", "SourceTreeSHA", "CandidateCLIManifestSHA256", "CandidateTestBinaryReceiptBindingDigest", "RunnerImage", "Status", "Authoritative", "StartedAt", "CompletedAt", "CleanupComplete", "ErrorText", "Shards", "Executions", "WorkloadExecutions", "ReusedWorkloads", "CacheMisses", "Warnings", "PhaseTimings", "CandidateTestBinaryBuilds"}
	typeOfRecord := reflect.TypeFor[RemoteCIRunRecord]()
	got := make([]string, typeOfRecord.NumField())
	for index := range got {
		got[index] = typeOfRecord.Field(index).Name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RemoteCIRunRecord fields = %v, want %v", got, want)
	}
}

func assertWorkloadPassProofProjectionRoundTrip(
	t *testing.T,
	store *DurationLedgerStore,
	now time.Time,
) WorkloadPassProof {
	t.Helper()
	proof := WorkloadPassProof{
		IdentityDigest:    "sha256:" + strings.Repeat("1", 64),
		WorkloadID:        "unit",
		ExecutionDigest:   strings.Repeat("2", 64),
		InputDigest:       "sha256:" + strings.Repeat("3", 64),
		EnvironmentDigest: "sha256:" + strings.Repeat("4", 64),
		ObjectKey:         "passes/unit.pass",
		ObservedAt:        now,
	}
	if err := store.RecordWorkloadPassProofs([]WorkloadPassProof{proof}); err != nil {
		t.Fatal(err)
	}
	assertStoredWorkloadPassProof(t, store, proof)
	newerProof := proof
	newerProof.ObjectKey = "passes/unit-new.pass"
	newerProof.ObservedAt = proof.ObservedAt.Add(time.Second)
	if err := store.RecordWorkloadPassProofs([]WorkloadPassProof{newerProof}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadPassProofs([]WorkloadPassProof{proof}); err == nil ||
		!strings.Contains(err.Error(), "older than") {
		t.Fatalf("stale PASS proof error = %v", err)
	}
	assertStoredWorkloadPassProof(t, store, newerProof)
	aliasProof := newerProof
	aliasProof.WorkloadID = "unit-release"
	aliasProof.ObservedAt = newerProof.ObservedAt.Add(time.Second)
	if err := store.RecordWorkloadPassProofs([]WorkloadPassProof{aliasProof}); err != nil {
		t.Fatalf("equivalent PASS proof alias: %v", err)
	}
	storedAliasProof := aliasProof
	storedAliasProof.WorkloadID = proof.WorkloadID
	assertStoredWorkloadPassProof(t, store, storedAliasProof)
	assertStoredWorkloadIdentityAliases(t, store, proof.IdentityDigest, "unit", "unit-release")
	conflictingProof := proof
	conflictingProof.ExecutionDigest = strings.Repeat("9", 64)
	if err := store.RecordWorkloadPassProofs([]WorkloadPassProof{conflictingProof}); err == nil {
		t.Fatal("conflicting PASS proof identity was accepted")
	}
	return proof
}

func assertStoredWorkloadPassProof(
	t *testing.T,
	store *DurationLedgerStore,
	want WorkloadPassProof,
) {
	t.Helper()
	proofs, err := store.LookupWorkloadPassProofs([]string{want.IdentityDigest})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(proofs[want.IdentityDigest], want) {
		t.Fatalf("PASS proof = %#v, want %#v", proofs[want.IdentityDigest], want)
	}
}

func assertWorkloadFingerprintProjectionRoundTrip(
	t *testing.T,
	store *DurationLedgerStore,
	proof WorkloadPassProof,
	now time.Time,
) {
	t.Helper()
	fingerprint := WorkloadFingerprintRecord{
		IdentityDigest:    proof.IdentityDigest,
		WorkloadID:        proof.WorkloadID,
		ExecutionDigest:   proof.ExecutionDigest,
		InputDigest:       proof.InputDigest,
		EnvironmentDigest: proof.EnvironmentDigest,
		SourceTreeSHA:     strings.Repeat("5", 40),
		ObservedAt:        now,
	}
	if err := store.RecordWorkloadFingerprints([]WorkloadFingerprintRecord{fingerprint}); err != nil {
		t.Fatal(err)
	}
	newerFingerprint := fingerprint
	newerFingerprint.SourceTreeSHA = strings.Repeat("6", 40)
	newerFingerprint.ObservedAt = fingerprint.ObservedAt.Add(time.Second)
	if err := store.RecordWorkloadFingerprints([]WorkloadFingerprintRecord{newerFingerprint}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadFingerprints([]WorkloadFingerprintRecord{fingerprint}); err != nil {
		t.Fatalf("out-of-order immutable fingerprint observation: %v", err)
	}
	aliasFingerprint := newerFingerprint
	aliasFingerprint.WorkloadID = "unit-release"
	aliasFingerprint.SourceTreeSHA = strings.Repeat("7", 40)
	aliasFingerprint.ObservedAt = newerFingerprint.ObservedAt.Add(time.Second)
	if err := store.RecordWorkloadFingerprints([]WorkloadFingerprintRecord{aliasFingerprint}); err != nil {
		t.Fatalf("equivalent workload fingerprint alias: %v", err)
	}
	assertStoredWorkloadFingerprintObservations(t, store, fingerprint, newerFingerprint, aliasFingerprint)
	assertStoredWorkloadIdentityAliases(t, store, proof.IdentityDigest, "unit", "unit-release")
	conflictingFingerprint := fingerprint
	conflictingFingerprint.InputDigest = "sha256:" + strings.Repeat("8", 64)
	if err := store.RecordWorkloadFingerprints(
		[]WorkloadFingerprintRecord{conflictingFingerprint},
	); err == nil {
		t.Fatal("conflicting workload fingerprint identity was accepted")
	}
}

func assertStoredWorkloadIdentityAliases(
	t *testing.T,
	store *DurationLedgerStore,
	identityDigest string,
	want ...string,
) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rows, err := database.Query(`
		SELECT workload_id FROM ci_workload_identity_aliases
		WHERE identity_digest = ? ORDER BY workload_id
	`, identityDigest)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var workloadID string
		if err := rows.Scan(&workloadID); err != nil {
			t.Fatal(err)
		}
		got = append(got, workloadID)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workload identity aliases = %v, want %v", got, want)
	}
}

func assertStoredWorkloadFingerprintObservations(
	t *testing.T,
	store *DurationLedgerStore,
	want ...WorkloadFingerprintRecord,
) {
	t.Helper()
	if len(want) == 0 {
		t.Fatal("workload fingerprint observations are required")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rows, err := database.Query(`
		SELECT source_tree_sha, observed_at_unix_ms
		FROM ci_workload_fingerprint_observations
		WHERE identity_digest = ?
		ORDER BY source_tree_sha
	`, want[0].IdentityDigest)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := make(map[string]int64, len(want))
	for rows.Next() {
		var sourceTreeSHA string
		var observedAtMS int64
		if err := rows.Scan(&sourceTreeSHA, &observedAtMS); err != nil {
			t.Fatal(err)
		}
		got[sourceTreeSHA] = observedAtMS
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("workload fingerprint observation count = %d, want %d", len(got), len(want))
	}
	for _, record := range want {
		if got[record.SourceTreeSHA] != record.ObservedAt.UTC().UnixMilli() {
			t.Fatalf(
				"workload fingerprint observation %q = %d, want %d",
				record.SourceTreeSHA,
				got[record.SourceTreeSHA],
				record.ObservedAt.UTC().UnixMilli(),
			)
		}
	}
}

func assertRemoteCIRunProjectionRoundTrip(t *testing.T, store *DurationLedgerStore, now time.Time) {
	t.Helper()
	run := sqliteProjectionRemoteCIRun(now)
	catalog := sqliteProjectionWorkloadCatalog()
	catalogDigest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	run.CatalogDigest = catalogDigest
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{SourceTreeSHA: run.SourceTreeSHA, Entrypoint: run.Entrypoint, Profile: run.Profile, ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRemoteCIRun(run); err != nil {
		t.Fatalf("RecordRemoteCIRun() error = %v", err)
	}
	assertStoredRemoteCIRun(t, store, run)
	assertRemoteCIRunConflictsAreRejected(t, store, run)
}

func sqliteProjectionRemoteCIRun(now time.Time) RemoteCIRunRecord {
	shardIdentity := "sha256:" + strings.Repeat("9", 64)
	return RemoteCIRunRecord{
		JobID: "job-sqlite-round-trip", RequesterFingerprint: RequesterFingerprint("sha256:" + strings.Repeat("a", 64)),
		Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast, PlanDigest: "sha256:plan", SourceTreeSHA: strings.Repeat("5", 40),
		CandidateCLIManifestSHA256:              strings.Repeat("c", 64),
		CandidateTestBinaryReceiptBindingDigest: "sha256:" + strings.Repeat("d", 64),
		RunnerImage:                             "ubuntu:22.04", Status: ResultStatusPassed, Authoritative: true, StartedAt: now, CompletedAt: now.Add(time.Second), CleanupComplete: true,
		Shards:             []RemoteCIShardRecord{{ShardIdentity: shardIdentity, ContainerGroup: "eci-123", ContainerStatus: "Succeeded", Workloads: []GateID{GateIDAIMaintenanceSelfTest}, MaterializationTiming: ShardMaterializationTiming{Measurement: MaterializationMeasurementMeasured, ShardIdentity: shardIdentity}}},
		Executions:         []PlanGateExecution{{GateID: GateIDAIMaintenanceSelfTest, Status: ResultStatusPassed, StartedAt: now, CompletedAt: now.Add(time.Second), ArgvDigest: "sha256:argv", LogDigest: "sha256:log", TestTimings: []GoTestTiming{{Name: "TestGate", Status: GoTestStatusPass, DurationMS: 1000}}, ExecutionProfile: ExecutionProfile{CacheSource: "none", CacheStatus: "not_applicable", CacheMeasurement: "not_measured", TestBodyMS: 1000, TotalMS: 1000}}},
		WorkloadExecutions: []PlanGateExecution{{GateID: GateIDAIMaintenanceSelfTest, Status: ResultStatusPassed, StartedAt: now, CompletedAt: now.Add(time.Second), ArgvDigest: "sha256:argv", LogDigest: "sha256:log", TestTimings: []GoTestTiming{{Name: "TestWorkload", Status: GoTestStatusPass, DurationMS: 1000}}, ExecutionProfile: ExecutionProfile{CacheSource: "none", CacheStatus: "not_applicable", CacheMeasurement: "not_measured", TestBodyMS: 1000, TotalMS: 1000}}},
		ReusedWorkloads:    []GateID{GateIDWhitespaceCheck}, CacheMisses: []GateID{GateIDAIMaintenanceSelfTest}, Warnings: []string{"unit exceeded the planning target"},
		PhaseTimings: []RemoteCIPhaseTiming{{
			Phase: "cache.parent_prepare", StartedAt: now, DurationMillis: 17,
			Outcome: RemoteCIPhaseOutcomeSucceeded, WorkloadCount: 2,
			CacheHitCount: 1, CacheMissCount: 1,
		}},
	}
}

func TestLoadRemoteCIRunRejectsNonStrictMaterializationTimingJSON(t *testing.T) {
	store := newInitializedDurationLedgerProjectionStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	run := sqliteProjectionRemoteCIRun(now)
	run.Shards[0].ShardIdentity = "sha256:" + strings.Repeat("a", 64)
	run.Shards[0].MaterializationTiming.ShardIdentity = run.Shards[0].ShardIdentity
	catalog := sqliteProjectionWorkloadCatalog()
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	run.CatalogDigest = digest
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{SourceTreeSHA: run.SourceTreeSHA, Entrypoint: run.Entrypoint, Profile: run.Profile, ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRemoteCIRun(run); err != nil {
		t.Fatal(err)
	}
	validTiming := `{"measurement":"measured","shard_identity":"` + run.Shards[0].ShardIdentity + `","source":{"download_ms":0,"verify_ms":0,"install_ms":0,"materialize_ms":0},"baseline":{"download_ms":0,"verify_ms":0,"install_ms":0,"materialize_ms":0},"candidate_cli":{"download_ms":0,"verify_ms":0,"install_ms":0,"materialize_ms":0},"candidate_test_binaries":{"download_ms":0,"verify_ms":0,"install_ms":0,"materialize_ms":0}}`
	for name, timingJSON := range map[string]string{
		"missing measurement": strings.Replace(validTiming, `"measurement":"measured",`, "", 1),
		"unknown field":       validTiming[:len(validTiming)-1] + `,"forged_metric":1}`,
		"trailing object":     validTiming + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			database, openErr := store.openSQLiteAuthority(true)
			if openErr != nil {
				t.Fatal(openErr)
			}
			_, updateErr := database.Exec(`UPDATE ci_shards SET materialization_timing_json = ? WHERE job_id = ?`, timingJSON, run.JobID)
			closeErr := database.Close()
			if updateErr != nil || closeErr != nil {
				t.Fatalf("write malformed timing JSON: update=%v close=%v", updateErr, closeErr)
			}
			if _, loadErr := store.LoadRemoteCIRun(run.JobID); loadErr == nil || !strings.Contains(loadErr.Error(), "stored remote CI shard materialization timing is invalid") {
				t.Fatalf("LoadRemoteCIRun() error = %v, want strict timing decode rejection", loadErr)
			}
		})
	}
}

func TestDecodeStoredRemoteCIExecutionTestTimingsRejectsNonStrictJSON(t *testing.T) {
	for name, encoded := range map[string]string{
		"missing status":   `[{"name":"TestStrict","duration_ms":1}]`,
		"unknown field":    `[{"name":"TestStrict","status":"pass","duration_ms":1,"forged":true}]`,
		"trailing payload": `[{"name":"TestStrict","status":"pass","duration_ms":1}][]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeStoredRemoteCIExecutionTestTimings(encoded); err == nil || !strings.Contains(err.Error(), "stored remote CI execution test timings are invalid") {
				t.Fatalf("decodeStoredRemoteCIExecutionTestTimings() error = %v, want strict rejection", err)
			}
		})
	}
}

func TestRecordRemoteCIRunRejectsTerminalShardWithoutMeasuredTiming(t *testing.T) {
	store := newInitializedDurationLedgerProjectionStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	run := sqliteProjectionRemoteCIRun(now)
	run.Shards[0].MaterializationTiming = ShardMaterializationTiming{Measurement: MaterializationMeasurementUnavailable}
	catalog := sqliteProjectionWorkloadCatalog()
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	run.CatalogDigest = digest
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{SourceTreeSHA: run.SourceTreeSHA, Entrypoint: run.Entrypoint, Profile: run.Profile, ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRemoteCIRun(run); err == nil || !strings.Contains(err.Error(), "materialization timing") {
		t.Fatalf("RecordRemoteCIRun() error = %v, want terminal shard timing rejection", err)
	}
}

func TestValidateRemoteCIRunRejectsAuthoritativePreBindingBuildRows(t *testing.T) {
	run := sqliteProjectionRemoteCIRun(time.Now().UTC().Truncate(time.Millisecond))
	digest, err := WorkloadCatalogDigest(sqliteProjectionWorkloadCatalog())
	if err != nil {
		t.Fatal(err)
	}
	run.CatalogDigest = digest
	run.CandidateTestBinaryBuilds = []CandidateTestBinaryBuildRecord{{}}
	if validationErr := validateRemoteCIRunRecord(run); validationErr == nil || !strings.Contains(validationErr.Error(), "pre-binding audit rows") {
		t.Fatalf("validateRemoteCIRunRecord() error = %v, want pre-binding rejection", validationErr)
	}
}

func TestLoadRemoteCIRunMarksLegacyMissingTimingUnavailable(t *testing.T) {
	store := newInitializedDurationLedgerProjectionStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	run := sqliteProjectionRemoteCIRun(now)
	catalog := sqliteProjectionWorkloadCatalog()
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	run.CatalogDigest = digest
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{SourceTreeSHA: run.SourceTreeSHA, Entrypoint: run.Entrypoint, Profile: run.Profile, ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRemoteCIRun(run); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		t.Fatal(err)
	}
	_, updateErr := database.Exec(`UPDATE ci_shards SET materialization_timing_json = '' WHERE job_id = ?`, run.JobID)
	closeErr := database.Close()
	if updateErr != nil || closeErr != nil {
		t.Fatalf("write legacy timing: update=%v close=%v", updateErr, closeErr)
	}
	loaded, err := store.LoadRemoteCIRun(run.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Shards[0].MaterializationTiming.Measurement != MaterializationMeasurementUnavailable {
		t.Fatalf("legacy timing measurement = %q, want unavailable", loaded.Shards[0].MaterializationTiming.Measurement)
	}
}

func newInitializedDurationLedgerProjectionStore(t *testing.T) *DurationLedgerStore {
	t.Helper()
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	return store
}

func sqliteProjectionWorkloadCatalog() WorkloadCatalog {
	return WorkloadCatalog{Version: durationLedgerVersion, Authoritative: true, Workloads: []Workload{
		{ID: string(GateIDAIMaintenanceSelfTest), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("7", 64), BootstrapEstimateMS: 1_000, Shardable: true},
		{ID: string(GateIDWhitespaceCheck), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("8", 64), BootstrapEstimateMS: 1_000, Shardable: true},
	}}
}

func assertStoredRemoteCIRun(t *testing.T, store *DurationLedgerStore, want RemoteCIRunRecord) {
	t.Helper()
	loaded, err := store.LoadRemoteCIRun(want.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.PhaseTimings) <= len(want.PhaseTimings) {
		t.Fatalf(
			"remote CI phase timing count = %d, want caller timings plus SQLite projection timings",
			len(loaded.PhaseTimings),
		)
	}
	if !reflect.DeepEqual(loaded.PhaseTimings[:len(want.PhaseTimings)], want.PhaseTimings) {
		t.Fatalf(
			"remote CI caller phase timings = %#v, want %#v",
			loaded.PhaseTimings[:len(want.PhaseTimings)],
			want.PhaseTimings,
		)
	}
	want.PhaseTimings = loaded.PhaseTimings
	if !reflect.DeepEqual(loaded, want) {
		t.Fatalf("remote CI run = %#v, want %#v", loaded, want)
	}
	jobIDs, err := store.ListRemoteCIRunIDsByRequester(want.RequesterFingerprint, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(jobIDs, []string{want.JobID}) {
		t.Fatalf("requester job IDs = %v, want %q", jobIDs, want.JobID)
	}
}

func assertRemoteCIRunConflictsAreRejected(t *testing.T, store *DurationLedgerStore, run RemoteCIRunRecord) {
	t.Helper()
	conflictingRun := run
	conflictingRun.PlanDigest = "sha256:other-plan"
	if err := store.RecordRemoteCIRun(conflictingRun); err == nil {
		t.Fatal("conflicting remote CI job identity was accepted")
	}
	conflictingRequester := run
	conflictingRequester.RequesterFingerprint = RequesterFingerprint("sha256:" + strings.Repeat("b", 64))
	if err := store.RecordRemoteCIRun(conflictingRequester); err == nil || !strings.Contains(err.Error(), "immutable requester identity") {
		t.Fatalf("conflicting requester identity error = %v", err)
	}
}

func runDurationLedgerProjectionWriters(t *testing.T, authorityPath string, digests []string, now time.Time) {
	t.Helper()
	var writers errgroup.Group
	for index, digest := range digests {
		writers.Go(sqliteProjectionWriter(authorityPath, digest, index, now))
	}
	if err := writers.Wait(); err != nil {
		t.Fatal(err)
	}
}

func sqliteProjectionWriter(authorityPath, digest string, writer int, now time.Time) func() error {
	return func() error {
		store, err := NewDurationLedgerStore(authorityPath)
		if err != nil {
			return err
		}
		return store.RecordWorkloadPassProofs([]WorkloadPassProof{{
			IdentityDigest: digest, WorkloadID: "unit-" + string(rune('a'+writer)), ExecutionDigest: fmt.Sprintf("%064x", writer+1),
			InputDigest: fmt.Sprintf("sha256:%064x", writer+101), EnvironmentDigest: "sha256:" + strings.Repeat("f", 64),
			ObjectKey: "passes/" + string(rune('a'+writer)) + ".pass", ObservedAt: now.Add(time.Duration(writer) * time.Millisecond),
		}})
	}
}
