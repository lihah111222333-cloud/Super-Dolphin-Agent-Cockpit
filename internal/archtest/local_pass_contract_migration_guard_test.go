package archtest

import (
	"go/ast"
	"strconv"
	"strings"
	"testing"
)

func assertLocalSQLiteAdditiveMigrationAndFailFast(t *testing.T, root string) {
	t.Helper()
	schemaPath := root + "/internal/devtools/gate/ledger_store_sqlite_schema_workload_reuse.go"
	schemaFile := parseRemoteCIContractGuardFile(t, schemaPath)
	localSchema, ok := localPassConstString(schemaFile, "strictLocalWorkloadPassSQLiteSchema")
	if !ok {
		t.Fatal("local SQLite schema constant is missing")
	}
	assertLocalSQLiteSchemaTables(t, localSchema)
	assertLocalAuthorityStateFailFast(t, root)
	assertLocalAuthorityLookupInitializesAndValidates(t, root)
	assertLocalSQLiteSchemaGuardFailFast(t, root)
}

// assertRemoteCIExecutionScopeSchemaV16 freezes the v14 -> v16 additive chain:
// scope and retained proof projections remain side tables, while ci_runs and
// remote receipts retain their physical v14 projection.
func assertRemoteCIExecutionScopeSchemaV16(t *testing.T, root string) {
	t.Helper()
	assertRemoteCIExecutionScopeSideTable(t, root)
	assertRemoteCIExecutionScopeCoreProjection(t, root)
	assertRemoteCIExecutionScopeDigest(t, root)
	assertRemoteCIExecutionScopeConsumer(t, root)
}

func assertRemoteCIExecutionScopeSideTable(t *testing.T, root string) {
	t.Helper()
	schema := readRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/ledger_store_sqlite_schema_execution_scope.go")
	for _, marker := range []string{
		"CREATE TABLE IF NOT EXISTS ci_remote_run_execution_scopes",
		"PRIMARY KEY (job_id, accepted_generation)",
		"FOREIGN KEY (job_id) REFERENCES ci_runs(job_id) ON DELETE CASCADE",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_ci_remote_run_execution_scopes_job",
		"CREATE INDEX IF NOT EXISTS idx_ci_remote_run_execution_scopes_generation",
	} {
		if !strings.Contains(schema, marker) {
			t.Errorf("execution-scope v15 side-table schema is missing %q", marker)
		}
	}
}

func assertRemoteCIExecutionScopeCoreProjection(t *testing.T, root string) {
	t.Helper()
	write := parseRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/ci_query_store_remote_run_write.go")
	core := remoteCIFunctionByName(write, "writeSQLiteRemoteCIRunCoreProjection")
	if core == nil || !localPassFunctionHasIdentifier(core, []string{"insertSQLiteRemoteCIExecutionScope"}) {
		t.Fatal("remote run core projection must retain the execution-scope side-table writer")
	}
	insert := remoteCIFunctionByName(write, "upsertSQLiteRemoteCIRun")
	if insert == nil {
		t.Fatal("remote run core projection writer is missing")
	}
	const coreProjection = "INSERT INTO ci_runs ( job_id, force, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, image_cache_snapshot_id, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete, error_text ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	normalized, bindingCount := remoteCIRunCoreProjectionSQL(insert)
	if normalized == "" {
		t.Fatal("remote run core projection no longer inserts ci_runs")
	}
	if !strings.Contains(normalized, coreProjection) || bindingCount != 18 {
		t.Fatalf("remote run core ci_runs projection drifted: sql=%q bindings=%d", normalized, bindingCount)
	}
	if strings.Contains(strings.ToLower(normalized), "scope") {
		t.Fatal("execution scope must not be added to ci_runs; it belongs only to the v15 side table")
	}
}

func remoteCIRunCoreProjectionSQL(insert *ast.FuncDecl) (string, int) {
	var bindingCount int
	var normalized string
	ast.Inspect(insert.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		literal, literalOK := call.Args[0].(*ast.BasicLit)
		if !ok || selector.Sel.Name != "Exec" || !literalOK {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil || !strings.Contains(value, "INSERT INTO ci_runs") {
			return true
		}
		normalized = strings.Join(strings.Fields(value), " ")
		bindingCount = len(call.Args) - 1
		return false
	})
	return normalized, bindingCount
}

func assertRemoteCIExecutionScopeDigest(t *testing.T, root string) {
	t.Helper()
	scopeFile := parseRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/remote_ci_execution_scope.go")
	digest := remoteCIFunctionByName(scopeFile, "Digest")
	if digest == nil {
		t.Fatal("remote execution scope digest is missing")
	}
	for _, forbidden := range []string{"SourceTreeSHA", "Path", "Commit", "Token"} {
		if localPassFunctionHasIdentifier(digest, []string{forbidden}) {
			t.Errorf("remote execution scope digest includes forbidden audit/credential selector %q", forbidden)
		}
	}
}

func assertRemoteCIExecutionScopeConsumer(t *testing.T, root string) {
	t.Helper()
	store := readRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/ci_query_store_remote_execution_scope.go")
	for _, marker := range []string{"record.Scope == nil || record.Scope.IsFull()", "record.Scope.IsSubset()", "scope_count", "loadRemoteCIExecutionScope"} {
		if !strings.Contains(store, marker) {
			t.Errorf("execution-scope side-table consumer is missing %q", marker)
		}
	}
}

func assertLocalSQLiteSchemaTables(t *testing.T, localSchema string) {
	t.Helper()
	for _, table := range []string{"ci_local_authority_state", "ci_local_workload_origins", "ci_local_workload_executions", "ci_local_workload_pass_evidence"} {
		if !strings.Contains(localSchema, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("local additive schema is missing table %s", table)
		}
	}
	for _, remoteTable := range []string{"ci_runs", "ci_check_receipts", "ci_remote_baseline_state", "ci_workload_pass_evidence"} {
		if strings.Contains(localSchema, remoteTable) {
			t.Errorf("local additive schema embeds remote authority table %s", remoteTable)
		}
	}
}

func localPassForbiddenImportFixture(file *ast.File) bool {
	for _, path := range localPassImportPaths(file) {
		if strings.Contains(path, "/alicloud/") || strings.Contains(path, "/remoteci") {
			return true
		}
	}
	return false
}

func localPassFunctionTouchesForbiddenRemoteCapability(function *ast.FuncDecl) bool {
	return localPassFunctionHasIdentifier(function, []string{
		"remoteci", "AgentToken", "AgentTokenDigest", "CreateContainerGroup",
		"ImageCache", "ImageCacheSnapshot", "OSS", "ECI", "NewECIClient",
		"RemoteCIAgentToken",
	})
}

func localPassVersionedMigrationTablesAllowed(function *ast.FuncDecl) bool {
	if function == nil {
		return false
	}
	allowed := map[string]bool{
		"ci_retained_workload_pass_proofs": true,
		"ci_run_workload_results":          true,
		"ci_runs":                          true,
		"ci_remote_baseline_state":         true,
		"ci_remote_run_execution_scopes":   true,
		"ci_workload_pass_evidence":        true,
	}
	for _, value := range localPassFunctionStringLiterals(function) {
		for token := range strings.FieldsSeq(value) {
			name := strings.Trim(token, "(),;")
			if strings.HasPrefix(name, "ci_") && !allowed[name] {
				return false
			}
		}
	}
	return true
}

func localPassFunctionStringLiterals(function *ast.FuncDecl) []string {
	if function == nil {
		return nil
	}
	var values []string
	ast.Inspect(function.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind.String() != "STRING" {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil {
			values = append(values, value)
		}
		return true
	})
	return values
}

// TestLocalPassContractGuardCounterexamples keeps the static boundary red on
// the exact classes of accidental cross-authority wiring it is meant to catch.
func TestLocalPassContractGuardCounterexamples(t *testing.T) {
	assertLocalPassEnvironmentDigestCounterexamples(t)
	forbidden := parseRemoteCIContractSource(t, `package fixture
import "example/internal/devtools/alicloud/eci"
func localPath() { _ = eci.CreateContainerGroup }
`)
	if !localPassForbiddenImportFixture(forbidden) || !localPassASTHasIdentifier(forbidden, "CreateContainerGroup") {
		t.Fatal("local provider boundary counterexample was not observable")
	}
	identity := parseRemoteCIContractSource(t, `package fixture
type workloadPassIdentityPayload struct { SourceTreeSHA string `+"`json:\"source_tree_sha\"`"+` }
`)
	if !localPassIdentityAuditField("source_tree_sha") || !localPassTypeHasField(identity, "workloadPassIdentityPayload", "SourceTreeSHA") {
		t.Fatal("tree-bearing local identity counterexample was not observable")
	}
	hostBeforeLookup := parseRemoteCIContractSource(t, `package fixture
func PrepareLocalWorkloadSchedule() { prepareLocalSchedulerLocalItems() }
func prepareLocalSchedulerLocalItems() {
	observeLocalSchedulerHost()
	lookupSelectedLocalWorkloads()
	admitLocalSchedulerMisses()
}
func admitLocalSchedulerMisses() { observeLocalSchedulerHost() }
func lookupSelectedLocalWorkloads() {}
func observeLocalSchedulerHost() {}
`)
	if localPassExplicitLookupPrecedesHostObserve(hostBeforeLookup) {
		t.Fatal("host-before-lookup scheduler counterexample was not rejected")
	}

	localRemoteCapability := parseRemoteCIContractSource(t, `package fixture
import "example/internal/devtools/remoteci"
func localPath() { _ = remoteci.RunInput{} }
`)
	if !localPassFunctionTouchesForbiddenRemoteCapability(remoteCIFunctionByName(localRemoteCapability, "localPath")) {
		t.Fatal("ordinary local function remote-provider counterexample was not rejected")
	}
	localToken := parseRemoteCIContractSource(t, `package fixture
func localPath() { _ = RemoteCIAgentToken }
`)
	if !localPassFunctionTouchesForbiddenRemoteCapability(remoteCIFunctionByName(localToken, "localPath")) {
		t.Fatal("ordinary local function token counterexample was not rejected")
	}
	unknownMigrationTable := parseRemoteCIContractSource(t, `package fixture
func backfillRetainedWorkloadPassProofs() { _ = "SELECT * FROM ci_unregistered_remote_table" }
`)
	if localPassVersionedMigrationTablesAllowed(remoteCIFunctionByName(unknownMigrationTable, "backfillRetainedWorkloadPassProofs")) {
		t.Fatal("versioned migration unknown table counterexample was not rejected")
	}
}

// assertLocalPassEnvironmentDigestCounterexamples proves the environment-to-
// payload guard rejects both producer omissions and stale payload mappings.
func assertLocalPassEnvironmentDigestCounterexamples(t *testing.T) {
	t.Helper()
	missing := parseRemoteCIContractSource(t, `package fixture
type LocalWorkloadPassEnvironment struct { Platform string; NewCorrectnessInput string }
type localWorkloadPassEnvironmentPayload struct { SchemaVersion string; Domain string; Platform string }
func LocalWorkloadPassEnvironmentDigest(environment LocalWorkloadPassEnvironment) {
	_ = localWorkloadPassEnvironmentPayload{SchemaVersion: cicontract.LocalWorkloadPassEnvironmentSchemaVersion, Domain: cicontract.LocalWorkloadPassEnvironmentDomain, Platform: environment.Platform}
}
`)
	if len(localPassEnvironmentDigestParityErrors(
		remoteCIStructTypeByName(missing, "LocalWorkloadPassEnvironment"),
		remoteCIStructTypeByName(missing, "localWorkloadPassEnvironmentPayload"),
		remoteCIFunctionByName(missing, "LocalWorkloadPassEnvironmentDigest"),
	)) == 0 {
		t.Fatal("new LocalWorkloadPassEnvironment field missing from digest payload was not rejected")
	}

	stale := parseRemoteCIContractSource(t, `package fixture
type LocalWorkloadPassEnvironment struct { Platform string }
type localWorkloadPassEnvironmentPayload struct { SchemaVersion string; Domain string; Platform string; Stale string }
func LocalWorkloadPassEnvironmentDigest(environment LocalWorkloadPassEnvironment) {
	_ = localWorkloadPassEnvironmentPayload{SchemaVersion: cicontract.LocalWorkloadPassEnvironmentSchemaVersion, Domain: cicontract.LocalWorkloadPassEnvironmentDomain, Platform: environment.Platform, Stale: environment.Stale}
}
`)
	if len(localPassEnvironmentDigestParityErrors(
		remoteCIStructTypeByName(stale, "LocalWorkloadPassEnvironment"),
		remoteCIStructTypeByName(stale, "localWorkloadPassEnvironmentPayload"),
		remoteCIFunctionByName(stale, "LocalWorkloadPassEnvironmentDigest"),
	)) == 0 {
		t.Fatal("stale digest payload mapping was not rejected")
	}

	derivation := parseRemoteCIContractSource(t, `package fixture
type LocalWorkloadPassEnvironment struct { RunnerSemanticPolicy string }
type localWorkloadPassEnvironmentPayload struct { SchemaVersion string; Domain string; RunnerSemanticPolicyDigest string }
func LocalWorkloadPassEnvironmentDigest(environment LocalWorkloadPassEnvironment) {
	policyDigest := sha256.Sum256([]byte(environment.RunnerSemanticPolicy))
	_ = localWorkloadPassEnvironmentPayload{SchemaVersion: cicontract.LocalWorkloadPassEnvironmentSchemaVersion, Domain: cicontract.LocalWorkloadPassEnvironmentDomain, RunnerSemanticPolicyDigest: fmt.Sprintf("sha256:%x", policyDigest)}
}
`)
	if got := localPassEnvironmentDigestParityErrors(
		remoteCIStructTypeByName(derivation, "LocalWorkloadPassEnvironment"),
		remoteCIStructTypeByName(derivation, "localWorkloadPassEnvironmentPayload"),
		remoteCIFunctionByName(derivation, "LocalWorkloadPassEnvironmentDigest"),
	); len(got) != 0 {
		t.Fatalf("registered policy derivation was rejected: %v", got)
	}
}
