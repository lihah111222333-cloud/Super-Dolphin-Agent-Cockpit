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
