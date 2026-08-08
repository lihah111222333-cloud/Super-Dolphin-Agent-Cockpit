package gate

import (
	"database/sql"
	"encoding/json"
	"testing"
)

// TestDurationLedgerSQLiteRepairsMissingPassReuseIndexes 验证旧 current schema
// 缺复用索引时只执行原子 CREATE INDEX，保留既有数据与版本。
func TestDurationLedgerSQLiteRepairsMissingPassReuseIndexes(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`INSERT INTO ci_schema_migrations(name, applied_at_unix_ms) VALUES ('before-reusable-pass-index', 1)`); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"idx_ci_runs_reusable_pass", "idx_ci_workload_pass_evidence_migration"} {
		if _, err := database.Exec("DROP INDEX " + index); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatalf("repair missing reusable PASS index on reopen: %v", err)
	}
	defer reopened.Close()
	for _, expectation := range []struct {
		table string
		name  string
	}{
		{table: "ci_runs", name: "idx_ci_runs_reusable_pass"},
		{table: "ci_workload_pass_evidence", name: "idx_ci_workload_pass_evidence_migration"},
	} {
		if _, ok := sqliteIndexListForTest(t, reopened, expectation.table)[expectation.name]; !ok {
			t.Fatalf("repaired schema omitted %s", expectation.name)
		}
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, reopened); got != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("repaired user_version = %d, want %d", got, durationLedgerSQLiteSchemaVersion)
	}
}

// TestDurationLedgerSQLiteV12AliasMigrationRetainsSourceAndIgnoresAlias verifies
// that opening a v12 authority creates the strict v13 relation without rewriting
// source evidence, while existing projected rows remain excluded from exact lookup.
func TestDurationLedgerSQLiteV12AliasMigrationRetainsSourceAndIgnoresAlias(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	source := prepareV12AliasMigrationFixture(t, store)
	reopened := openV13AliasMigrationAuthority(t, store)
	defer reopened.Close()
	assertV13AliasMigrationPreservesSource(t, reopened, source)
	projected := insertV13AliasFixture(t, reopened, source)
	assertAliasExactLookupMiss(t, store, projected, source)
}

func prepareV12AliasMigrationFixture(t *testing.T, store *DurationLedgerStore) WorkloadPassEvidence {
	t.Helper()
	record, sourceIdentity, _ := recordWorkloadPassRun(t, store, "migration-v12-alias", 1, "migration-v12-alias-workload")
	record.Status = ResultStatusFailed
	record.Authoritative = false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record v12 source run: %v", err)
	}
	source := lookupSingleWorkloadPassEvidence(t, store, sourceIdentity)
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`DROP TABLE ci_workload_pass_evidence_aliases`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA user_version = 12`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return source
}

func openV13AliasMigrationAuthority(t *testing.T, store *DurationLedgerStore) *sql.DB {
	t.Helper()
	reopened, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatalf("upgrade v12 authority: %v", err)
	}
	return reopened
}

func assertV13AliasMigrationPreservesSource(t *testing.T, database *sql.DB, source WorkloadPassEvidence) {
	t.Helper()
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("v12 migration user_version = %d, want %d", got, durationLedgerSQLiteSchemaVersion)
	}
	var sourceRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_workload_pass_evidence WHERE identity_digest = ?`, source.Identity.IdentityDigest).Scan(&sourceRows); err != nil {
		t.Fatal(err)
	}
	if sourceRows != 1 {
		t.Fatalf("v12 migration source rows = %d, want 1", sourceRows)
	}
	if _, err := database.Exec(`SELECT source_identity_digest FROM ci_workload_pass_evidence_aliases WHERE 1 = 0`); err != nil {
		t.Fatalf("v12 migration omitted alias relation: %v", err)
	}
}

func insertV13AliasFixture(t *testing.T, database *sql.DB, source WorkloadPassEvidence) WorkloadPassEvidence {
	t.Helper()
	projected := source
	projected.Identity.InputDigest = digestForWorkloadPass("migration-v12-alias-projected-input")
	projected.Identity.IdentityDigest = workloadPassIdentityDigest(t, projected.Identity)
	projected.EvidenceSHA256 = evidenceDigestForTest(t, projected)
	executionJSON, err := json.Marshal(projected.OriginExecution)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO ci_workload_pass_evidence (identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, projected.Identity.IdentityDigest, "1", projected.Identity.WorkloadID, projected.Identity.ExecutionDigest, projected.Identity.InputDigest, projected.Identity.EnvironmentDigest, projected.OriginJobID, projected.OriginSourceTreeSHA, projected.OriginReceiptSetSHA256, string(executionJSON), projected.EvidenceSHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO ci_workload_pass_evidence_aliases (alias_identity_digest, alias_accepted_generation, source_identity_digest, source_accepted_generation) VALUES (?, ?, ?, ?)`, projected.Identity.IdentityDigest, "1", source.Identity.IdentityDigest, "1"); err != nil {
		t.Fatal(err)
	}
	return projected
}

func assertAliasExactLookupMiss(t *testing.T, store *DurationLedgerStore, projected, source WorkloadPassEvidence) {
	t.Helper()
	got, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{projected.Identity})
	if err != nil {
		t.Fatalf("exact lookup of alias row: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("exact lookup reused %d alias rows, want miss", len(got))
	}
	if got := lookupSingleWorkloadPassEvidence(t, store, source.Identity); got.Identity != source.Identity {
		t.Fatalf("source lookup identity = %#v, want %#v", got.Identity, source.Identity)
	}
}

func evidenceDigestForTest(t *testing.T, evidence WorkloadPassEvidence) string {
	t.Helper()
	digest, err := WorkloadPassEvidenceSHA256(evidence)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
