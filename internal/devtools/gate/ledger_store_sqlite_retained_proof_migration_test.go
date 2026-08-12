package gate

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
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

// TestDurationLedgerSQLiteV15ToV16RetainsLegacyProofAsNaturalMiss 锁定旧无域
// identity 只按原摘要回填，不能阻断迁移或被当前域查询命中。
func TestDurationLedgerSQLiteV15ToV16RetainsLegacyProofAsNaturalMiss(t *testing.T) {
	legacyEvidence, currentIdentity := legacyRetainedProofEvidenceForTest(t)
	path := migrateLegacyRetainedProofFixtureForTest(t, legacyEvidence)
	assertLegacyRetainedProofNotRewrittenForTest(t, path, legacyEvidence.Identity.IdentityDigest, currentIdentity.IdentityDigest)
}

func legacyRetainedProofEvidenceForTest(t *testing.T) (WorkloadPassEvidence, WorkloadPassIdentity) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	run, currentIdentity, receipts := recordWorkloadPassRun(t, store, "legacy-proof-origin", 1, "legacy-proof")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(run), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	evidence := lookupSingleWorkloadPassEvidence(t, store, currentIdentity)
	legacyDigest, err := legacyWorkloadPassIdentitySHA256(evidence.Identity)
	if err != nil {
		t.Fatal(err)
	}
	evidence.Identity.IdentityDigest = legacyDigest
	evidence.OriginExecution.ExecutionProfile.GoFlags = ""
	evidence.EvidenceSHA256, err = WorkloadPassEvidenceSHA256(evidence)
	if err != nil {
		t.Fatal(err)
	}
	return evidence, currentIdentity
}

func migrateLegacyRetainedProofFixtureForTest(t *testing.T, evidence WorkloadPassEvidence) string {
	t.Helper()
	path := t.TempDir() + "/retained-proof-legacy.sqlite"
	database := openStrictSchemaDatabaseAtPath(t, path)
	createDurationLedgerSQLiteV14Fixture(t, database)
	if err := migrateDurationLedgerSQLiteSchema14To15(database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA user_version = 15`); err != nil {
		t.Fatal(err)
	}
	insertValidV15RetainedProofBackfillFixture(t, database, []WorkloadPassEvidence{evidence}, []string{"legacy-consumer-one", "legacy-consumer-two", "legacy-consumer-three"})
	if err := migrateDurationLedgerSQLiteSchema15To16(database); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertLegacyRetainedProofNotRewrittenForTest(t *testing.T, path, legacyDigest, currentDigest string) {
	t.Helper()
	database := openStrictSchemaDatabaseAtPath(t, path)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close migrated retained proof database: %v", err)
		}
	})
	var retainedLegacy, rewrittenCurrent, currentEvidence int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_retained_workload_pass_proofs WHERE identity_digest = ?`, legacyDigest).Scan(&retainedLegacy); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_retained_workload_pass_proofs WHERE identity_digest = ?`, currentDigest).Scan(&rewrittenCurrent); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_workload_pass_evidence WHERE identity_digest = ?`, currentDigest).Scan(&currentEvidence); err != nil {
		t.Fatal(err)
	}
	if retainedLegacy != 3 || rewrittenCurrent != 0 || currentEvidence != 0 {
		t.Fatalf("retained legacy proofs = %d, rewritten current proofs = %d, current evidence = %d, want 3, 0 and 0", retainedLegacy, rewrittenCurrent, currentEvidence)
	}
}

// TestMigratedLegacyRetainedProofMissesEveryRuntimeReplayPath 从完整 v15
// authority 迁移后覆盖 exact/source/environment、compaction 与 run reload。
func TestMigratedLegacyRetainedProofMissesEveryRuntimeReplayPath(t *testing.T) {
	store, origin, consumer, legacyIdentity, canonicalIdentity := migratedLegacyReplayStoreForTest(t, "runtime")
	assertWorkloadPassLookupMiss(t, store, legacyIdentity)
	assertMigratedLegacyReplayMisses(t, store, legacyIdentity)
	assertCanonicalReplayStillHits(t, store, canonicalIdentity)
	seedAcceptedGenerationForTest(t, store, 3)
	_, _, _ = recordWorkloadPassRun(t, store, "migrated-legacy-compaction", 3, "migrated-legacy-compaction")
	seedAcceptedGenerationForTest(t, store, 4)
	_, _, _ = recordWorkloadPassRun(t, store, "migrated-legacy-compaction-evict", 4, "migrated-legacy-compaction-evict")
	if _, err := store.LoadRemoteCIRun(origin.JobID); err == nil {
		t.Fatal("legacy direct origin survived retention compaction")
	}
	if _, err := store.LoadRemoteCIRun(consumer.JobID); err != nil {
		t.Fatalf("reload migrated legacy consumer after compaction: %v", err)
	}
	assertMigratedLegacyReplayMisses(t, store, legacyIdentity)
}

// TestMigratedLegacyRetainedProofReplayRejectsDrift 锁定 legacy skip 不能吞掉
// 未知 identity 摘要、损坏 JSON 或被伪装成 current-domain 的 proof 漂移。
func TestMigratedLegacyRetainedProofReplayRejectsDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		want   string
		mutate func(*testing.T, *DurationLedgerStore, WorkloadPassIdentity)
	}{
		{name: "unknown identity digest", want: "identity digest does not match content", mutate: mutateMigratedProofUnknownDigest},
		{name: "damaged execution JSON", want: "malformed JSON", mutate: mutateMigratedProofDamagedJSON},
		{name: "nonlegacy evidence drift", want: "evidence SHA-256 does not match content", mutate: mutateMigratedProofNonLegacyDrift},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, _, legacyIdentity, _ := migratedLegacyReplayStoreForTest(t, tc.name)
			tc.mutate(t, store, legacyIdentity)
			target := workloadPassReplayTargetForTest(t, legacyIdentity, "drift-"+tc.name, false)
			if _, err := store.LookupWorkloadPassSourceReplayCandidates([]WorkloadPassIdentity{target}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("source replay migrated proof drift error = %v, want %q", err, tc.want)
			}
		})
	}
}

func migratedLegacyReplayStoreForTest(t *testing.T, label string) (*DurationLedgerStore, RemoteCIRunRecord, RemoteCIRunRecord, WorkloadPassIdentity, WorkloadPassIdentity) {
	t.Helper()
	store, origin, consumer, legacyIdentity, evidence := retentionIdentityMigrationFixture(t, "migrated-"+label)
	legacyDigest := legacyWorkloadPassIdentityDigestForTest(t, legacyIdentity)
	rewriteRetentionIdentityProof(t, store, origin, consumer, evidence, legacyDigest)
	downgradeRetainedProofStoreToV15ForTest(t, store)
	migrated, err := NewDurationLedgerStore(store.AuthorityPath())
	if err != nil {
		t.Fatal(err)
	}
	return migrated, origin, consumer, legacyIdentity, consumer.WorkloadResults[1].Identity
}

func downgradeRetainedProofStoreToV15ForTest(t *testing.T, store *DurationLedgerStore) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`DROP INDEX idx_ci_run_workload_results_retention`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`DROP TABLE ci_retained_workload_pass_proofs`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA user_version = 15`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertMigratedLegacyReplayMisses(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity) {
	t.Helper()
	sourceTarget := workloadPassReplayTargetForTest(t, identity, "legacy-source", false)
	sources, err := store.LookupWorkloadPassSourceReplayCandidates([]WorkloadPassIdentity{sourceTarget})
	if err != nil {
		t.Fatalf("legacy source replay should miss: %v", err)
	}
	environmentTarget := workloadPassReplayTargetForTest(t, identity, "legacy-environment", true)
	hints, err := store.LookupWorkloadPassEnvironmentReplayHints([]WorkloadPassIdentity{environmentTarget})
	if err != nil {
		t.Fatalf("legacy environment replay should miss: %v", err)
	}
	if len(sources[sourceTarget.WorkloadID]) != 0 || len(hints[environmentTarget.WorkloadID]) != 0 {
		t.Fatalf("legacy replay candidates = source:%d environment:%d, want 0/0", len(sources[sourceTarget.WorkloadID]), len(hints[environmentTarget.WorkloadID]))
	}
}

func assertCanonicalReplayStillHits(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity) {
	t.Helper()
	hits, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	if err != nil || len(hits) != 1 {
		t.Fatalf("canonical exact lookup = %d, %v, want one hit", len(hits), err)
	}
	sourceTarget := workloadPassReplayTargetForTest(t, identity, "canonical-source", false)
	sources, err := store.LookupWorkloadPassSourceReplayCandidates([]WorkloadPassIdentity{sourceTarget})
	if err != nil || len(sources[sourceTarget.WorkloadID]) != 1 {
		t.Fatalf("canonical source replay = %d, %v, want one hit", len(sources[sourceTarget.WorkloadID]), err)
	}
	environmentTarget := workloadPassReplayTargetForTest(t, identity, "canonical-environment", true)
	hints, err := store.LookupWorkloadPassEnvironmentReplayHints([]WorkloadPassIdentity{environmentTarget})
	if err != nil || len(hints[environmentTarget.WorkloadID]) != 1 {
		t.Fatalf("canonical environment replay = %d, %v, want one hit", len(hints[environmentTarget.WorkloadID]), err)
	}
	if _, err := store.ValidateWorkloadPassEnvironmentReplayHint(hints[environmentTarget.WorkloadID][0]); err != nil {
		t.Fatalf("validate canonical environment replay: %v", err)
	}
}

func workloadPassReplayTargetForTest(t *testing.T, identity WorkloadPassIdentity, label string, environment bool) WorkloadPassIdentity {
	t.Helper()
	if environment {
		identity.EnvironmentDigest = digestForWorkloadPass(label)
	} else {
		identity.InputDigest = digestForWorkloadPass(label)
	}
	identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
	return identity
}

func mutateMigratedProofUnknownDigest(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity) {
	updateMigratedLegacyProofForTest(t, store, identity, `identity_digest = ?`, digestForWorkloadPass("unknown-identity-domain"))
}

func mutateMigratedProofDamagedJSON(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity) {
	updateMigratedLegacyProofForTest(t, store, identity, `origin_execution_json = ?`, "{")
}

func mutateMigratedProofNonLegacyDrift(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity) {
	updateMigratedLegacyProofForTest(t, store, identity, `identity_digest = ?`, workloadPassIdentityDigest(t, identity))
}

func updateMigratedLegacyProofForTest(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity, assignment string, value any) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	query := `UPDATE ci_retained_workload_pass_proofs SET ` + assignment + ` WHERE identity_digest = ?`
	if _, err := database.Exec(query, value, legacyWorkloadPassIdentityDigestForTest(t, identity)); err != nil {
		t.Fatal(err)
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
