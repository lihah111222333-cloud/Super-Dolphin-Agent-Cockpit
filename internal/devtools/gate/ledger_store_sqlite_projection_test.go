package gate

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

// TestRemoteCIRunRecordFieldRegistry 防止 run 主投影字段在 schema guard 前静默漂移。
func TestRemoteCIRunRecordFieldRegistry(t *testing.T) {
	want := []string{"JobID", "AgentTokenDigest", "Force", "Entrypoint", "Profile", "PlanDigest", "CatalogDigest", "AcceptedGeneration", "Scope", "ImageCacheSnapshotID", "SourceTreeSHA", "CandidateGateSourceSHA256", "CandidateGateToolchainSHA256", "RunnerImage", "Status", "Authoritative", "StartedAt", "CompletedAt", "CleanupComplete", "ErrorText", "Shards", "Executions", "WorkloadExecutions", "WorkloadResults", "Warnings", "TimingWarnings", "TimingObservations", "CompileTimingObservations", "DurationSamples"}
	typeOf := reflect.TypeFor[RemoteCIRunRecord]()
	got := make([]string, 0, typeOf.NumField())
	for field := range typeOf.Fields() {
		got = append(got, field.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RemoteCIRunRecord fields = %v, want %v", got, want)
	}
}

func TestRemoteCIExecutionScopeSQLiteProjectionRoundTripAndFailFast(t *testing.T) {
	fixture := newRemoteCIExecutionScopeSQLiteProjectionFixture(t)
	insertRemoteCIExecutionScopeRun(t, fixture.database, fixture.record.JobID)
	assertRemoteCIExecutionScopeCollision(t, fixture.database, fixture.record, fixture.conflicting)
	assertRemoteCIExecutionScopeRoundTrip(t, fixture.database, fixture.record)
	assertRemoteCIExecutionScopeTamperFails(t, fixture.database, fixture.record)
}

func TestRemoteCIExecutionScopeSQLiteFullAndLegacyDoNotWriteRows(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	catalog := testWorkloadCatalog(Workload{ID: "scope-full", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 1})
	full, err := NewRemoteCIFullExecutionScope(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []RemoteCIRunRecord{{JobID: "scope-full", AcceptedGeneration: 1, Scope: &full}, {JobID: "scope-legacy", AcceptedGeneration: 1}} {
		insertRemoteCIExecutionScopeRun(t, database, record.JobID)
		transaction, err := database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := insertSQLiteRemoteCIExecutionScope(transaction, record); err != nil {
			_ = transaction.Rollback()
			t.Fatal(err)
		}
		if err := transaction.Commit(); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM ci_remote_run_execution_scopes WHERE job_id = ?`, record.JobID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s scope rows = %d, want 0", record.JobID, count)
		}
	}
}

func TestRemoteCIExecutionScopeSubsetCoverageIsExactAndHasNoOwner(t *testing.T) {
	fixture := newRemoteCIExecutionScopeSubsetCoverageFixture(t)
	assertRemoteCIExecutionScopeSubsetAccepted(t, fixture)
	assertRemoteCIExecutionScopeRejectsMissingCoverage(t, fixture)
	assertRemoteCIExecutionScopeRejectsExtraWorkload(t, fixture)
	assertRemoteCIExecutionScopeRejectsOwnerAttestation(t, fixture)
}

func insertRemoteCIExecutionScopeRun(t *testing.T, database *sql.DB, jobID string) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO ci_runs (
			job_id, entrypoint, profile, plan_digest, catalog_digest, accepted_generation,
			image_cache_snapshot_id, source_tree_sha, runner_image, status, authoritative,
			started_at_unix_ms, completed_at_unix_ms, cleanup_complete
		) VALUES (?, 'git-pre-commit', 'local-fast', 'sha256:plan', 'sha256:catalog', '1', 'snapshot', 'tree', 'image', 'passed', 0, 1, 2, 1)
	`, jobID)
	if err != nil {
		t.Fatal(err)
	}
}

type remoteCIExecutionScopeSQLiteProjectionFixture struct {
	database    *sql.DB
	record      RemoteCIRunRecord
	conflicting RemoteCIRunRecord
}

func newRemoteCIExecutionScopeSQLiteProjectionFixture(t *testing.T) remoteCIExecutionScopeSQLiteProjectionFixture {
	t.Helper()
	store := newTestDurationLedgerStore(t)
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	catalog := testWorkloadCatalog(
		Workload{ID: "scope-alpha", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 1},
		Workload{ID: "scope-beta", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 1},
		Workload{ID: "scope-gamma", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 1},
	)
	return remoteCIExecutionScopeSQLiteProjectionFixture{
		database:    database,
		record:      newRemoteCIExecutionScopeRunRecord(t, catalog, "scope-round-trip", "scope-alpha"),
		conflicting: newRemoteCIExecutionScopeRunRecord(t, catalog, "scope-round-trip", "scope-beta"),
	}
}

func newRemoteCIExecutionScopeRunRecord(t *testing.T, catalog WorkloadCatalog, jobID string, gateID GateID) RemoteCIRunRecord {
	t.Helper()
	scope, err := NewRemoteCISubsetExecutionScope(catalog, []GateID{gateID})
	if err != nil {
		t.Fatal(err)
	}
	return RemoteCIRunRecord{JobID: jobID, AcceptedGeneration: 1, Scope: &scope}
}

func assertRemoteCIExecutionScopeCollision(t *testing.T, database *sql.DB, record, conflicting RemoteCIRunRecord) {
	t.Helper()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	insertRemoteCIExecutionScope(t, transaction, record)
	insertRemoteCIExecutionScope(t, transaction, record)
	if err := insertSQLiteRemoteCIExecutionScope(transaction, conflicting); err == nil || !strings.Contains(err.Error(), "conflicting") {
		_ = transaction.Rollback()
		t.Fatalf("conflicting scope collision error = %v, want fail-fast", err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}

func insertRemoteCIExecutionScope(t *testing.T, transaction *sql.Tx, record RemoteCIRunRecord) {
	t.Helper()
	if err := insertSQLiteRemoteCIExecutionScope(transaction, record); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
}

func assertRemoteCIExecutionScopeRoundTrip(t *testing.T, database *sql.DB, record RemoteCIRunRecord) {
	t.Helper()
	stored, err := loadRemoteCIExecutionScope(database, record.JobID, record.AcceptedGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if !remoteCIExecutionScopesEqual(record.Scope, stored) {
		t.Fatalf("stored scope = %#v, want %#v", stored, record.Scope)
	}
}

func assertRemoteCIExecutionScopeTamperFails(t *testing.T, database *sql.DB, record RemoteCIRunRecord) {
	t.Helper()
	if _, err := database.Exec(`UPDATE ci_remote_run_execution_scopes SET scope_count = 2 WHERE job_id = ?`, record.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRemoteCIExecutionScope(database, record.JobID, record.AcceptedGeneration); err == nil || !strings.Contains(err.Error(), "count does not match") {
		t.Fatalf("tampered scope count error = %v, want fail-fast", err)
	}
}

type remoteCIExecutionScopeSubsetCoverageFixture struct {
	catalog  WorkloadCatalog
	scope    RemoteCIExecutionScope
	index    remoteCIRunCatalogIndex
	selected RemoteCIRunRecord
}

func newRemoteCIExecutionScopeSubsetCoverageFixture(t *testing.T) remoteCIExecutionScopeSubsetCoverageFixture {
	t.Helper()
	catalog := testWorkloadCatalog(
		Workload{ID: "scope-alpha", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 1},
		Workload{ID: "scope-beta", Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: 1},
	)
	scope, err := NewRemoteCISubsetExecutionScope(catalog, []GateID{"scope-alpha"})
	if err != nil {
		t.Fatal(err)
	}
	index, err := newRemoteCIRunCatalogIndex(catalog)
	if err != nil {
		t.Fatal(err)
	}
	return remoteCIExecutionScopeSubsetCoverageFixture{
		catalog: catalog,
		scope:   scope,
		index:   index,
		selected: RemoteCIRunRecord{
			Status: ResultStatusPassed,
			WorkloadResults: []RemoteCIWorkloadResult{{
				Identity:    WorkloadPassIdentity{WorkloadID: "scope-alpha"},
				Disposition: WorkloadDispositionReused,
			}},
		},
	}
}

func assertRemoteCIExecutionScopeSubsetAccepted(t *testing.T, fixture remoteCIExecutionScopeSubsetCoverageFixture) {
	t.Helper()
	if err := validateRemoteCIRunScopeRecords(fixture.selected, fixture.catalog, fixture.scope); err != nil {
		t.Fatal(err)
	}
	if err := fixture.index.validatePassed(fixture.selected, fixture.scope); err != nil {
		t.Fatal(err)
	}
}

func assertRemoteCIExecutionScopeRejectsMissingCoverage(t *testing.T, fixture remoteCIExecutionScopeSubsetCoverageFixture) {
	t.Helper()
	if err := fixture.index.validatePassed(RemoteCIRunRecord{Status: ResultStatusPassed}, fixture.scope); err == nil || !strings.Contains(err.Error(), "does not cover") {
		t.Fatalf("missing selected coverage error = %v, want fail-fast", err)
	}
}

func assertRemoteCIExecutionScopeRejectsExtraWorkload(t *testing.T, fixture remoteCIExecutionScopeSubsetCoverageFixture) {
	t.Helper()
	extra := fixture.selected
	extra.WorkloadResults = append(extra.WorkloadResults, RemoteCIWorkloadResult{Identity: WorkloadPassIdentity{WorkloadID: "scope-beta"}, Disposition: WorkloadDispositionReused})
	if err := validateRemoteCIRunScopeRecords(extra, fixture.catalog, fixture.scope); err == nil || !strings.Contains(err.Error(), "extra workload result") {
		t.Fatalf("extra subset result error = %v, want fail-fast", err)
	}
}

func assertRemoteCIExecutionScopeRejectsOwnerAttestation(t *testing.T, fixture remoteCIExecutionScopeSubsetCoverageFixture) {
	t.Helper()
	owner := RemoteCIRunRecord{Executions: []PlanGateExecution{{GateID: GateIDReleaseLayeredCheck}}}
	if err := validateRemoteCIRunScopeRecords(owner, fixture.catalog, fixture.scope); err == nil || !strings.Contains(err.Error(), "owner attestation") {
		t.Fatalf("subset owner attestation error = %v, want fail-fast", err)
	}
}
