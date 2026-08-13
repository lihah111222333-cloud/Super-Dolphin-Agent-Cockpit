package gate

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"testing"
)

func TestDurationLedgerSQLiteV15ToV16RejectsIncompleteRetainedProofBackfill(t *testing.T) {
	for _, tc := range []struct {
		name           string
		disposition    string
		insertEvidence bool
		evidenceSHA    string
	}{
		{name: "missing source evidence", disposition: "executed", insertEvidence: false},
		{name: "non direct source", disposition: "reused", insertEvidence: true, evidenceSHA: "sha256:result"},
		{name: "canonical digest drift", disposition: "executed", insertEvidence: true, evidenceSHA: "sha256:drift"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := openStrictSchemaTestDatabase(t, "retained-proof-"+tc.name+".sqlite")
			defer database.Close()
			createDurationLedgerSQLiteV14Fixture(t, database)
			if err := migrateDurationLedgerSQLiteSchema14To15(database); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`PRAGMA user_version = 15`); err != nil {
				t.Fatal(err)
			}
			insertV15RetainedProofBackfillFixture(t, database, tc.disposition, tc.insertEvidence, tc.evidenceSHA)
			if err := migrateDurationLedgerSQLiteSchema15To16(database); err == nil {
				t.Fatal("v15 to v16 migration accepted incomplete retained proof")
			}
			if got := durationLedgerSQLiteUserVersionForTest(t, database); got != 15 {
				t.Fatalf("user_version after rollback = %d, want 15", got)
			}
			var proofs int
			if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='ci_retained_workload_pass_proofs'`).Scan(&proofs); err != nil {
				t.Fatal(err)
			}
			if proofs != 0 {
				t.Fatalf("retained proof table survived rollback: %d", proofs)
			}
		})
	}
}

func TestDurationLedgerSQLiteV15ToV16BackfillsEveryLiveReusedConsumer(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	first, firstIdentity, firstReceipts := recordWorkloadPassRun(t, store, "proof-count-origin-one", 1, "proof-count-one")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(first), firstReceipts, nil, true); err != nil {
		t.Fatal(err)
	}
	evidence := []WorkloadPassEvidence{lookupSingleWorkloadPassEvidence(t, store, firstIdentity)}
	database := openStrictSchemaTestDatabase(t, "retained-proof-count.sqlite")
	defer database.Close()
	createDurationLedgerSQLiteV14Fixture(t, database)
	if err := migrateDurationLedgerSQLiteSchema14To15(database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA user_version = 15`); err != nil {
		t.Fatal(err)
	}
	insertValidV15RetainedProofBackfillFixture(t, database, evidence, []string{"consumer-one", "consumer-two", "consumer-three"})
	if err := migrateDurationLedgerSQLiteSchema15To16(database); err != nil {
		t.Fatal(err)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != 16 {
		t.Fatalf("user_version = %d, want 16", got)
	}
	var proofs int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_retained_workload_pass_proofs`).Scan(&proofs); err != nil {
		t.Fatal(err)
	}
	if proofs != 3 {
		t.Fatalf("retained proof count = %d, want 3", proofs)
	}
}

// TestDurationLedgerSQLiteV16ToV17AddsOnlySourceReplayIndexes 验证索引升级
// 不改写 direct evidence 或 retained proof 行。
func TestDurationLedgerSQLiteV16ToV17AddsOnlySourceReplayIndexes(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, receipts := recordWorkloadPassRun(t, store, "source-index-origin", 1, "source-index")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	_ = lookupSingleWorkloadPassEvidence(t, store, identity)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	downgradeDurationLedgerSQLiteV18ToV16Fixture(t, database)
	beforeCount, beforeDigest := sqliteRowsFingerprint(t, database, `SELECT * FROM ci_workload_pass_evidence ORDER BY identity_digest, accepted_generation`)
	if err := migrateDurationLedgerSQLiteSchema16To17(database); err != nil {
		t.Fatal(err)
	}
	afterCount, afterDigest := sqliteRowsFingerprint(t, database, `SELECT * FROM ci_workload_pass_evidence ORDER BY identity_digest, accepted_generation`)
	if beforeCount != afterCount || beforeDigest != afterDigest {
		t.Fatalf("v16 to v17 rewrote evidence rows: before=%d/%s after=%d/%s", beforeCount, beforeDigest, afterCount, afterDigest)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != 17 {
		t.Fatalf("user_version = %d, want 17", got)
	}
	if err := preflightDurationLedgerSQLiteSchemaVersion(database, sourceReplayIndexDurationLedgerSQLiteSchemaVersion); err != nil {
		t.Fatalf("v17 schema preflight: %v", err)
	}
}

func downgradeDurationLedgerSQLiteV18ToV16Fixture(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, index := range []string{"idx_ci_workload_pass_evidence_source_replay", "idx_ci_retained_workload_pass_proofs_source_replay"} {
		if _, err := database.Exec("DROP INDEX " + index); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`DROP TABLE ci_workload_input_replay_cache`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA user_version = 16`); err != nil {
		t.Fatal(err)
	}
}

func TestDurationLedgerSQLiteV17ToV18AddsOnlyWorkloadInputReplayCache(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, receipts := recordWorkloadPassRun(t, store, "input-cache-origin", 1, "input-cache")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	_ = lookupSingleWorkloadPassEvidence(t, store, identity)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	if _, err := database.Exec(`DROP TABLE ci_workload_input_replay_cache`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA user_version = 17`); err != nil {
		t.Fatal(err)
	}
	beforeCount, beforeDigest := sqliteRowsFingerprint(t, database, `SELECT * FROM ci_workload_pass_evidence ORDER BY identity_digest, accepted_generation`)
	if err := migrateDurationLedgerSQLiteSchema17To18(database); err != nil {
		t.Fatal(err)
	}
	afterCount, afterDigest := sqliteRowsFingerprint(t, database, `SELECT * FROM ci_workload_pass_evidence ORDER BY identity_digest, accepted_generation`)
	if beforeCount != afterCount || beforeDigest != afterDigest {
		t.Fatalf("v17 to v18 rewrote evidence rows: before=%d/%s after=%d/%s", beforeCount, beforeDigest, afterCount, afterDigest)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("user_version = %d, want %d", got, durationLedgerSQLiteSchemaVersion)
	}
	if err := newDurationLedgerSQLiteSchemaValidator().preflight(database, durationLedgerSQLiteSchemaVersion); err != nil {
		t.Fatalf("v18 schema preflight: %v", err)
	}
}

func TestDurationLedgerSQLiteV15ToV16PreservesRetiredDomainHistoryWithoutProofProjection(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, receipts := recordWorkloadPassRun(t, store, "retired-proof-origin", 1, "retired-proof")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	evidence := lookupSingleWorkloadPassEvidence(t, store, identity)
	legacyDigest, err := legacyWorkloadPassIdentitySHA256(evidence.Identity)
	if err != nil {
		t.Fatal(err)
	}
	evidence.Identity.IdentityDigest = legacyDigest
	evidence.EvidenceSHA256, err = WorkloadPassEvidenceSHA256(evidence)
	if err != nil {
		t.Fatal(err)
	}
	database := openStrictSchemaTestDatabase(t, "retired-proof-domain.sqlite")
	defer database.Close()
	createDurationLedgerSQLiteV14Fixture(t, database)
	if err := migrateDurationLedgerSQLiteSchema14To15(database); err != nil {
		t.Fatal(err)
	}
	insertValidV15RetainedProofBackfillFixture(t, database, []WorkloadPassEvidence{evidence}, []string{"consumer-one", "consumer-two", "consumer-three"})
	if err := migrateDurationLedgerSQLiteSchema15To16(database); err != nil {
		t.Fatal(err)
	}
	var proofs, results int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_retained_workload_pass_proofs`).Scan(&proofs); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_run_workload_results`).Scan(&results); err != nil {
		t.Fatal(err)
	}
	if proofs != 0 || results != 4 {
		t.Fatalf("retired domain migration proofs/results = %d/%d, want 0/4", proofs, results)
	}
}

func insertValidV15RetainedProofBackfillFixture(t *testing.T, database *sql.DB, evidence []WorkloadPassEvidence, consumers []string) {
	t.Helper()
	if len(evidence) != 1 || len(consumers) != 3 {
		t.Fatal("retained proof count fixture inputs are invalid")
	}
	if _, err := database.Exec(`INSERT INTO ci_remote_baseline_state(singleton,schema_version,generation,state_json,state_sha256,updated_at_unix_ms) VALUES(1,3,'1','{}','sha256:baseline',1)`); err != nil {
		t.Fatal(err)
	}
	for _, item := range evidence {
		insertValidV15ProofOrigin(t, database, item)
	}
	for index, consumer := range consumers {
		item := evidence[index%len(evidence)]
		if _, err := database.Exec(`INSERT INTO ci_runs(job_id,force,entrypoint,profile,plan_digest,catalog_digest,accepted_generation,image_cache_snapshot_id,source_tree_sha,candidate_gate_source_sha256,candidate_gate_toolchain_sha256,runner_image,status,authoritative,started_at_unix_ms,completed_at_unix_ms,cleanup_complete,error_text) VALUES(?,0,'test','medium','plan','catalog','1','snapshot','tree','','','image','passed',1,1,2,1,'')`, consumer); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO ci_run_workload_results(job_id,workload_id,identity_digest,execution_digest,input_digest,environment_digest,disposition,origin_job_id,origin_accepted_generation,evidence_sha256) VALUES(?,?,?,?,?,?,'reused',?,?,?)`, consumer, item.Identity.WorkloadID, item.Identity.IdentityDigest, item.Identity.ExecutionDigest, item.Identity.InputDigest, item.Identity.EnvironmentDigest, item.OriginJobID, strconv.FormatUint(item.OriginAcceptedGeneration, 10), item.EvidenceSHA256); err != nil {
			t.Fatal(err)
		}
	}
}

func insertValidV15ProofOrigin(t *testing.T, database *sql.DB, item WorkloadPassEvidence) {
	t.Helper()
	executionJSON, err := json.Marshal(item.OriginExecution)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO ci_runs(job_id,force,entrypoint,profile,plan_digest,catalog_digest,accepted_generation,image_cache_snapshot_id,source_tree_sha,candidate_gate_source_sha256,candidate_gate_toolchain_sha256,runner_image,status,authoritative,started_at_unix_ms,completed_at_unix_ms,cleanup_complete,error_text) VALUES(?,0,'test','medium','plan','catalog',?,'snapshot',?,'','','image','passed',1,1,2,1,'')`, item.OriginJobID, strconv.FormatUint(item.OriginAcceptedGeneration, 10), item.OriginSourceTreeSHA); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO ci_run_workload_results(job_id,workload_id,identity_digest,execution_digest,input_digest,environment_digest,disposition,origin_job_id,origin_accepted_generation,evidence_sha256) VALUES(?,?,?,?,?,?,'executed',?,?,?)`, item.OriginJobID, item.Identity.WorkloadID, item.Identity.IdentityDigest, item.Identity.ExecutionDigest, item.Identity.InputDigest, item.Identity.EnvironmentDigest, item.OriginJobID, strconv.FormatUint(item.OriginAcceptedGeneration, 10), item.EvidenceSHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO ci_workload_pass_evidence(identity_digest,accepted_generation,workload_id,execution_digest,input_digest,environment_digest,origin_job_id,origin_source_tree_sha,origin_receipt_set_sha256,origin_execution_json,evidence_sha256) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, item.Identity.IdentityDigest, strconv.FormatUint(item.OriginAcceptedGeneration, 10), item.Identity.WorkloadID, item.Identity.ExecutionDigest, item.Identity.InputDigest, item.Identity.EnvironmentDigest, item.OriginJobID, item.OriginSourceTreeSHA, item.OriginReceiptSetSHA256, string(executionJSON), item.EvidenceSHA256); err != nil {
		t.Fatal(err)
	}
}

func insertV15RetainedProofBackfillFixture(t *testing.T, database *sql.DB, disposition string, insertEvidence bool, evidenceSHA string) {
	t.Helper()
	statements := []string{
		`INSERT INTO ci_remote_baseline_state(singleton,schema_version,generation,state_json,state_sha256,updated_at_unix_ms) VALUES(1,3,'1','{}','sha256:baseline',1)`,
		`INSERT INTO ci_runs(job_id,force,entrypoint,profile,plan_digest,catalog_digest,accepted_generation,image_cache_snapshot_id,source_tree_sha,candidate_gate_source_sha256,candidate_gate_toolchain_sha256,runner_image,status,authoritative,started_at_unix_ms,completed_at_unix_ms,cleanup_complete,error_text) VALUES ('origin',0,'test','medium','plan','catalog','1','snapshot','tree','','','image','passed',1,1,2,1,'')`,
		`INSERT INTO ci_runs(job_id,force,entrypoint,profile,plan_digest,catalog_digest,accepted_generation,image_cache_snapshot_id,source_tree_sha,candidate_gate_source_sha256,candidate_gate_toolchain_sha256,runner_image,status,authoritative,started_at_unix_ms,completed_at_unix_ms,cleanup_complete,error_text) VALUES ('consumer',0,'test','medium','plan','catalog','1','snapshot','tree','','','image','passed',1,1,2,1,'')`,
		`INSERT INTO ci_run_workload_results(job_id,workload_id,identity_digest,execution_digest,input_digest,environment_digest,disposition,origin_job_id,origin_accepted_generation,evidence_sha256) VALUES ('origin','backend:fixture','sha256:identity','sha256:execution','sha256:input','sha256:environment','` + disposition + `','origin','1','sha256:result')`,
		`INSERT INTO ci_run_workload_results(job_id,workload_id,identity_digest,execution_digest,input_digest,environment_digest,disposition,origin_job_id,origin_accepted_generation,evidence_sha256) VALUES ('consumer','backend:fixture','sha256:identity','sha256:execution','sha256:input','sha256:environment','reused','origin','1','sha256:result')`,
	}
	if insertEvidence {
		statements = append(statements, `INSERT INTO ci_workload_pass_evidence(identity_digest,accepted_generation,workload_id,execution_digest,input_digest,environment_digest,origin_job_id,origin_source_tree_sha,origin_receipt_set_sha256,origin_execution_json,evidence_sha256) VALUES ('sha256:identity','1','backend:fixture','sha256:execution','sha256:input','sha256:environment','origin','tree','sha256:receipt','{}','`+evidenceSHA+`')`)
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}
