package archtest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestRemoteCISQLAuthorityBindingsAreCreatedByGateSchema makes cicontract's
// binding list executable: every fact table it owns must be created by the one
// duration-ledger SQLite schema. The guard deliberately consumes the owner API
// instead of repeating table names.
func TestRemoteCISQLAuthorityBindingsAreCreatedByGateSchema(t *testing.T) {
	root := findRepoRoot(t)
	schemaDirectory := filepath.Join(root, "internal", "devtools", "gate")
	entries, err := os.ReadDir(schemaDirectory)
	if err != nil {
		t.Fatal(err)
	}
	var schema strings.Builder
	for _, entry := range remoteCISQLSchemaSourceEntries(entries) {
		schema.WriteString(readRemoteCIContractGuardFile(t, filepath.Join(schemaDirectory, entry.Name())))
	}
	for _, table := range cicontract.SQLAuthoritySchemaTables() {
		statement := "CREATE TABLE IF NOT EXISTS " + table
		if !strings.Contains(schema.String(), statement) {
			t.Errorf("gate SQLite schema does not create cicontract-registered table %q", table)
		}
	}
	for _, index := range cicontract.SQLAuthorityAdditiveSchemaIndexes() {
		statement := "INDEX IF NOT EXISTS " + index
		if !strings.Contains(schema.String(), statement) {
			t.Errorf("gate SQLite schema does not create cicontract-registered additive index %q", index)
		}
	}
	if unexpected := remoteCIUnexpectedSQLSchemaTables(remoteCIUnregisteredSQLSchemaTables(schema.String())); len(unexpected) != 0 {
		t.Fatalf("gate SQLite schema creates unregistered cicontract tables: %v", unexpected)
	}
}

func remoteCIUnexpectedSQLSchemaTables(tables []string) []string {
	// Local PASS tables are an explicit v14 additive namespace. They are
	// deliberately not remote cicontract authority bindings and are checked
	// by local_pass_contract_guard_test.go instead.
	unexpected := make([]string, 0, len(tables))
	for _, table := range tables {
		if !localPassAdditiveSQLiteTable(table) {
			unexpected = append(unexpected, table)
		}
	}
	return unexpected
}

// TestRemoteCIRetainedWorkloadPassProofV16AcceptedContract keeps v16's
// consumer-owned immutable proof projection auxiliary to the existing authority.
func TestRemoteCIRetainedWorkloadPassProofV16AcceptedContract(t *testing.T) {
	root := findRepoRoot(t)
	if !slices.Contains(cicontract.SQLAuthoritySchemaTables(), cicontract.RetainedWorkloadPassProofsTable) {
		t.Fatal("retained workload PASS proof table is not registered with the one SQLite authority")
	}
	schema := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_schema_execution_scope.go"))
	for _, marker := range []string{
		"CREATE TABLE IF NOT EXISTS " + cicontract.RetainedWorkloadPassProofsTable,
		"PRIMARY KEY (consumer_job_id, workload_id)",
		"REFERENCES ci_runs(job_id) ON DELETE CASCADE",
		"CREATE INDEX IF NOT EXISTS " + cicontract.RetainedWorkloadPassProofLookupIndex,
		"CREATE INDEX IF NOT EXISTS " + cicontract.RunWorkloadResultsRetentionIndex,
	} {
		if !strings.Contains(schema, marker) {
			t.Errorf("retained proof v%d schema is missing %q", cicontract.DurationLedgerSQLiteSchemaVersion, marker)
		}
	}
	if strings.Contains(strings.ToLower(schema), "alter table") {
		t.Fatal("retained proof v16 schema must not alter an existing authority projection")
	}
}

// TestRemoteCIExecutionScopeV15AcceptedContract keeps the v15 scope proof in
// the accepted SQLite authority and rejects alternate side tables or aliases.
func TestRemoteCIExecutionScopeV15AcceptedContract(t *testing.T) {
	root := findRepoRoot(t)
	if !slices.Contains(cicontract.SQLAuthoritySchemaTables(), cicontract.RemoteRunExecutionScopesTable) {
		t.Fatal("remote execution-scope side table is not registered with the one SQLite authority")
	}
	schema := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_schema_execution_scope.go"))
	if violations := remoteCIExecutionScopeSchemaViolations(schema); len(violations) != 0 {
		t.Fatalf("accepted v15 execution-scope schema violations: %v", violations)
	}
	if strings.Contains(strings.ToLower(readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/remote_ci_execution_scope.go"))), "ci_local_") {
		t.Fatal("remote execution scope must not alias the local PASS namespace")
	}
}

func TestRemoteCIRunExecutionIdentityV19AcceptedContract(t *testing.T) {
	root := findRepoRoot(t)
	if !slices.Contains(cicontract.SQLAuthoritySchemaTables(), cicontract.RemoteRunExecutionIdentitiesTable) {
		t.Fatal("remote run execution identity table is not registered with the one SQLite authority")
	}
	schema := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_schema_strict.go"))
	statement := remoteCIRunExecutionIdentityV19Statement(t, schema)
	assertRemoteCIRunExecutionIdentityV19Statement(t, schema, statement)
	assertRemoteCIRunExecutionIdentityV19Migration(t, root)
}

func remoteCIRunExecutionIdentityV19Statement(t *testing.T, schema string) string {
	t.Helper()
	statementName := "CREATE TABLE IF NOT EXISTS " + cicontract.RemoteRunExecutionIdentitiesTable
	statementStart := strings.Index(schema, statementName)
	if statementStart < 0 {
		t.Fatalf("run execution identity v19 schema is missing %q", statementName)
	}
	statementEnd := strings.Index(schema[statementStart:], ");")
	if statementEnd < 0 {
		t.Fatalf("run execution identity v19 schema is missing a closing %q", ");")
	}
	return schema[statementStart : statementStart+statementEnd+2]
}

func assertRemoteCIRunExecutionIdentityV19Statement(t *testing.T, schema, statement string) {
	t.Helper()
	for _, marker := range []string{
		"job_id TEXT PRIMARY KEY REFERENCES ci_runs(job_id) ON DELETE CASCADE",
		"platform TEXT NOT NULL",
		"runner_identity_digest TEXT NOT NULL",
		"toolchain_digest TEXT NOT NULL",
		"execution_mode TEXT NOT NULL",
	} {
		if !strings.Contains(statement, marker) {
			t.Errorf("run execution identity v19 schema is missing %q", marker)
		}
	}
	if !strings.Contains(schema, "cicontract.TargetPlatform") {
		t.Errorf("run execution identity v19 schema is missing %q", "cicontract.TargetPlatform")
	}
	if strings.Contains(statement, "DEFAULT") || strings.Contains(strings.ToLower(statement), "alter table") {
		t.Fatal("run execution identity v19 schema must not default or alter identity fields")
	}
}

func assertRemoteCIRunExecutionIdentityV19Migration(t *testing.T, root string) {
	t.Helper()
	migration := strings.ToLower(readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_namespace_migration.go")))
	for _, forbidden := range []string{"insert into ci_remote_run_execution_identities", "update ci_remote_run_execution_identities", "alter table ci_runs"} {
		if strings.Contains(migration, forbidden) {
			t.Errorf("v18 to v19 migration contains forbidden compatibility operation %q", forbidden)
		}
	}
}

func TestRemoteCIPlanningReceiptV20AcceptedContract(t *testing.T) {
	root := findRepoRoot(t)
	if !slices.Contains(cicontract.SQLAuthoritySchemaTables(), cicontract.RemoteRunPlanningReceiptsTable) || !slices.Contains(cicontract.SQLAuthorityAdditiveSchemaIndexes(), cicontract.RemoteRunPlanningReceiptsStatusIndex) {
		t.Fatal("planning receipt table/index is not registered with the one SQLite authority")
	}
	schema := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_schema_strict.go"))
	for _, marker := range []string{"CREATE TABLE IF NOT EXISTS " + cicontract.RemoteRunPlanningReceiptsTable, "job_id TEXT PRIMARY KEY REFERENCES ci_runs(job_id) ON DELETE CASCADE", "receipt_json TEXT NOT NULL", "receipt_digest TEXT NOT NULL", "mapping_root TEXT NOT NULL", "CREATE INDEX IF NOT EXISTS " + cicontract.RemoteRunPlanningReceiptsStatusIndex} {
		if !strings.Contains(schema, marker) {
			t.Errorf("planning receipt v20 schema is missing %q", marker)
		}
	}
	migration := strings.ToLower(readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_namespace_migration.go")))
	for _, forbidden := range []string{"alter table ci_runs", "insert into ci_remote_run_planning_receipts", "update ci_remote_run_planning_receipts"} {
		if strings.Contains(migration, forbidden) {
			t.Errorf("planning receipt v20 migration contains forbidden operation %q", forbidden)
		}
	}
}

// TestRemoteCITargetBindingV23AcceptedContract 冻结 additive target side table，且 runtime/finalizer 保持未接线。
func TestRemoteCITargetBindingV23AcceptedContract(t *testing.T) {
	root := findRepoRoot(t)
	if !slices.Contains(cicontract.SQLAuthoritySchemaTables(), cicontract.RemoteRunTargetBindingsTable) || !slices.Contains(cicontract.SQLAuthorityAdditiveSchemaIndexes(), cicontract.RemoteRunTargetBindingsProfileIndex) {
		t.Fatal("remote target binding table/index is not registered with the one SQLite authority")
	}
	assertRemoteCITargetBindingSchema(t, root)
	assertRemoteCITargetBindingMigration(t, root)
	assertRemoteCITargetBindingStoreBoundary(t, root)
}

func assertRemoteCITargetBindingSchema(t *testing.T, root string) {
	t.Helper()
	schema := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_schema_strict.go"))
	for _, marker := range []string{
		"CREATE TABLE IF NOT EXISTS %s",
		"job_id TEXT PRIMARY KEY REFERENCES ci_runs(job_id) ON DELETE CASCADE",
		"repository_id TEXT NOT NULL", "object_format TEXT NOT NULL", "commit_sha TEXT NOT NULL", "tree_sha TEXT NOT NULL", "mount_path TEXT NOT NULL",
		"manifest_path TEXT NOT NULL", "manifest_sha256 TEXT NOT NULL", "manifest_schema_version TEXT NOT NULL", "manifest_abi_digest TEXT NOT NULL", "manifest_digest TEXT NOT NULL",
		"profile TEXT NOT NULL", "profile_workload_set_digest TEXT NOT NULL", "UNIQUE (job_id, repository_id",
		"CREATE INDEX IF NOT EXISTS %s ON %s (profile, profile_workload_set_digest",
		"CREATE TRIGGER IF NOT EXISTS %s BEFORE UPDATE ON %s",
	} {
		if !strings.Contains(schema, marker) {
			t.Errorf("target binding v23 schema is missing %q", marker)
		}
	}
}

func assertRemoteCITargetBindingMigration(t *testing.T, root string) {
	t.Helper()
	migration := strings.ToLower(readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/ledger_store_sqlite_namespace_migration.go")))
	for _, forbidden := range []string{"insert into ci_remote_run_target_bindings", "update ci_remote_run_target_bindings", "alter table ci_runs"} {
		if strings.Contains(migration, forbidden) {
			t.Errorf("target binding v23 migration contains forbidden compatibility operation %q", forbidden)
		}
	}
}

func assertRemoteCITargetBindingStoreBoundary(t *testing.T, root string) {
	t.Helper()
	store := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/remote_ci_authority_store.go"))
	for _, marker := range []string{"RecordRemoteCITargetBinding", "LoadRemoteCITargetBinding", "remote CI target binding exact readback drifted", "本卡只提供独立 SQLite record/readback，不接入 Coordinator 或 finalizer", "WorkloadCatalogDigest(mapped selected profile catalog)", "不得以 manifest digest 或 declared command digest 替代"} {
		if !strings.Contains(store, marker) {
			t.Errorf("target binding v23 store is missing %q", marker)
		}
	}
	if strings.Contains(store, "remoteci.Coordinator") || strings.Contains(store, "FinalizeRemoteCIRunAuthority") {
		t.Fatal("target binding schema card wired Coordinator or authoritative finalizer")
	}
}

func TestRemoteCIExecutionScopeV15GuardCounterexamples(t *testing.T) {
	for name, source := range map[string]string{
		"second scope table":   `CREATE TABLE IF NOT EXISTS ci_remote_run_execution_scopes (job_id TEXT); CREATE TABLE IF NOT EXISTS ci_remote_run_execution_scope_alias (job_id TEXT);`,
		"ci runs scope column": `CREATE TABLE IF NOT EXISTS ci_runs (job_id TEXT, scope_json TEXT);`,
		"unknown scope table":  `CREATE TABLE IF NOT EXISTS ci_remote_scope_proofs (job_id TEXT);`,
		"remote local alias":   `CREATE TABLE IF NOT EXISTS ci_local_remote_run_execution_scopes (job_id TEXT);`,
	} {
		if violations := remoteCIExecutionScopeSchemaViolations(source); len(violations) == 0 {
			t.Fatalf("%s counterexample was not rejected", name)
		}
	}
}

func remoteCIExecutionScopeSchemaViolations(source string) []string {
	normalized := strings.ToLower(source)
	violations := map[string]bool{}
	for _, table := range []string{
		"ci_remote_run_execution_scope_alias",
		"ci_remote_scope_proofs",
		"ci_local_remote_run_execution_scopes",
	} {
		if strings.Contains(normalized, table) {
			violations["unregistered execution-scope table "+table] = true
		}
	}
	if strings.Count(normalized, "create table if not exists ci_remote_run_execution_scopes") != 1 {
		violations["execution-scope table must have exactly one schema declaration"] = true
	}
	if ciRuns := strings.Index(normalized, "create table if not exists ci_runs"); ciRuns >= 0 {
		if end := strings.Index(normalized[ciRuns:], ");"); end >= 0 && strings.Contains(normalized[ciRuns:ciRuns+end], "scope") {
			violations["ci_runs must not carry execution scope columns"] = true
		}
	}
	return remoteCIViolationList(violations)
}
