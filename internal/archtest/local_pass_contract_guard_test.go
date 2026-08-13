package archtest

import (
	"go/ast"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestLocalPassContractBoundary freezes the local PASS companion as an
// explicitly non-ECI authority.  These checks intentionally inspect source
// and the local contract file instead of importing local production symbols:
// a partially wired local path must fail this guard, not break the archtest
// package's shared compile.
func TestLocalPassContractBoundary(t *testing.T) {
	root := findRepoRoot(t)
	assertLocalPassContractValues(t, root)
	assertLocalPassIdentityAndNamespace(t, root)
	assertLocalRemoteAuthoritySeparation(t, root)
	assertLocalExecutorProviderBoundary(t, root)
	assertLocalSchedulerOrderingAndNoFallback(t, root)
	assertLocalSQLiteAdditiveMigrationAndFailFast(t, root)
	assertLocalProductionAuthorityInitialization(t, root)
	assertRemoteCIExecutionScopeSchemaV16(t, root)
	assertLocalCLIWorkloadCompatibility(t, root)
}

// TestLocalRunnerSemanticClosureGuard keeps local PASS reuse bound to the
// dynamic exact-tree closure rooted at the production local CLI, rather than a
// hand-maintained subset of source directories.
func TestLocalRunnerSemanticClosureGuard(t *testing.T) {
	root := findRepoRoot(t)
	gitSource := readRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/local_executor_receipt_git.go")
	for _, marker := range []string{
		`localRunnerSemanticEntryPackage = "cmd/super-dolphin-gate"`,
		"newLocalRunnerSourceClosure(modulePath)",
		"localRunnerSourceImports(file.path, file.content)",
		"gitTreeLocalRunnerEmbedPaths",
		"appendLocalRunnerClosurePath",
		"content collision",
		"localRunnerEmbedPatterns",
		"parseLocalRunnerEmbedPattern",
		"gitTreeLocalRunnerSourceFile",
		"is not a regular blob",
	} {
		if !strings.Contains(gitSource, marker) {
			t.Errorf("local runner closure is missing dynamic exact-tree guard %q", marker)
		}
	}
	closureFile := parseRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/local_executor_receipt_git.go")
	closure := remoteCIFunctionByName(closureFile, "gitTreeLocalRunnerSourcePaths")
	if closure == nil || !localPassFunctionHasIdentifier(closure, []string{"gitTreeModulePath", "newLocalRunnerSourceClosure", "visitNextPackage", "gitTreeLocalRunnerEmbedPaths", "appendLocalRunnerClosurePath"}) {
		t.Fatal("local runner source closure must derive packages and reachable embed assets from the exact-tree import graph with content-checked path ownership")
	}
	receiptFile := parseRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/local_executor_receipt.go")
	sourcePayload := remoteCIStructTypeByName(receiptFile, "localExecutorSourcePayload")
	if sourcePayload == nil {
		t.Fatal("local runner source payload is missing")
	}
	for _, field := range localPassJSONFields(sourcePayload) {
		if strings.Contains(field, "path") || strings.Contains(field, "tree") || strings.Contains(field, "commit") {
			t.Fatalf("local runner semantic payload must not include provenance field %q", field)
		}
	}
}

// TestLocalRunnerSelfSemanticProofFieldGuard prevents ProjectMap's receipt-bound
// gate binary from becoming an audit-only receipt field again. The proof is
// path-free and must be projected through the per-program runner digest.
func TestLocalRunnerSelfSemanticProofFieldGuard(t *testing.T) {
	root := findRepoRoot(t)
	receipt := parseRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/local_executor_receipt.go")
	verify := parseRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/local_executor_receipt_verify.go")
	payload := requireLocalSelfSemanticPayload(t, receipt)
	semantic := requireLocalSelfSemanticBinding(t, verify)
	assertLocalSelfSemanticPayloadMappings(t, payload, semantic)
	assertLocalSelfSemanticPayloadIsPathFree(t, payload)
}

func requireLocalSelfSemanticPayload(t *testing.T, receipt *ast.File) *ast.StructType {
	t.Helper()
	payload := remoteCIStructTypeByName(receipt, "localExecutorSelfPayload")
	if payload == nil {
		t.Fatal("trusted self semantic payload is missing")
	}
	return payload
}

func requireLocalSelfSemanticBinding(t *testing.T, verify *ast.File) *ast.FuncDecl {
	t.Helper()
	semantic := remoteCIFunctionByName(verify, "localReceiptRunnerSemanticDigest")
	environments := remoteCIFunctionByName(verify, "localReceiptEnvironments")
	if semantic == nil || environments == nil {
		t.Fatal("receipt self semantic functions are missing")
	}
	if !localPassFunctionHasSelector(semantic, "LocalRunnerSelfSemanticDomain") {
		t.Fatal("receipt self proof must use the versioned runner-semantic domain")
	}
	if !localPassSequenceContains(localPassFunctionCallSequence(environments), "localReceiptRunnerSemanticDigest") {
		t.Fatal("receipt self proof must be derived into per-program runner semantics")
	}
	return semantic
}

func assertLocalSelfSemanticPayloadMappings(t *testing.T, payload *ast.StructType, semantic *ast.FuncDecl) {
	t.Helper()
	literal, found := localPassDigestPayloadLiteral(semantic, "localExecutorSelfPayload")
	if !found {
		t.Fatal("self semantic runner digest must construct a canonical self payload")
	}
	values, problems := localPassPayloadLiteralValues(literal)
	for _, problem := range problems {
		t.Error(problem)
	}
	for _, field := range localPassGoJSONFields(payload) {
		assertLocalSelfSemanticPayloadField(t, field, values[field], values[field] != nil)
	}
}

func assertLocalSelfSemanticPayloadField(t *testing.T, field string, value ast.Expr, mapped bool) {
	t.Helper()
	if !mapped {
		t.Errorf("trusted self semantic payload field %s is not mapped", field)
		return
	}
	switch field {
	case "Name":
		assertLocalSelfSemanticLogicalName(t, value)
	case "Digest", "Version":
		assertLocalSelfSemanticDirectField(t, field, value)
	default:
		t.Errorf("trusted self semantic payload has unregistered field %s", field)
	}
}

func assertLocalSelfSemanticLogicalName(t *testing.T, value ast.Expr) {
	t.Helper()
	identifier, ok := value.(*ast.Ident)
	if !ok || identifier.Name != "trustedSelfBinaryLogicalName" {
		t.Error("trusted self semantic name must use the logical name constant")
	}
}

func assertLocalSelfSemanticDirectField(t *testing.T, field string, value ast.Expr) {
	t.Helper()
	selector, ok := value.(*ast.SelectorExpr)
	if !ok {
		t.Errorf("trusted self semantic payload field %s must map directly from self.%s", field, strings.ToLower(field))
		return
	}
	owner, owned := selector.X.(*ast.Ident)
	if !owned || owner.Name != "self" || !strings.EqualFold(selector.Sel.Name, field) {
		t.Errorf("trusted self semantic payload field %s must map directly from self.%s", field, strings.ToLower(field))
	}
}

func assertLocalSelfSemanticPayloadIsPathFree(t *testing.T, payload *ast.StructType) {
	t.Helper()
	for _, field := range localPassGoJSONFields(payload) {
		if strings.Contains(strings.ToLower(field), "path") {
			t.Fatalf("trusted self semantic payload must exclude absolute path field %s", field)
		}
	}
}

func assertLocalPassContractValues(t *testing.T, root string) {
	t.Helper()
	contractPath := root + "/internal/devtools/cicontract/local_pass_contract.go"
	source := readRemoteCIContractGuardFile(t, contractPath)
	for _, marker := range []string{
		`LocalWorkloadPassEnvironmentSchemaVersion = "local-workload-pass-environment/v1"`,
		`LocalWorkloadPassEnvironmentDomain        = "local-canonical-runner/v1"`,
		`LocalWorkloadPassHostContextDomain        = "local-host-context/v1"`,
		`LocalWorkloadRunnerSemanticPolicy         = "local-canonical-runner-semantic-policy/v1"`,
		`LocalRunnerSourceClosureDomain            = "local-runner-source-closure/v4"`,
		`LocalWorkloadPassNamespace                = "local"`,
		`RemoteWorkloadPassNamespace               = "remote"`,
		`LocalAutoMissCountLimit        int64 = 64`,
		`LocalAutoDurationLimitMS       int64 = 10 * 60 * 1000`,
		`LocalAutoSingleDurationLimitMS int64 = 5 * 60 * 1000`,
		`LocalHostCPUWindowMS           int64 = 30 * 1000`,
		`LocalHostCPUBusyLimitPercent         = 70.0`,
	} {
		if !strings.Contains(source, marker) {
			t.Errorf("local contract owner is missing frozen marker %q", marker)
		}
	}
	remoteContract := readRemoteCIContractGuardFile(t, root+"/internal/devtools/cicontract/contract.go")
	if !strings.Contains(remoteContract, "BaselineStateSchemaVersion uint32 = 13") {
		t.Fatal("remote accepted baseline JSON schema owner drifted from v13")
	}
	if cicontract.DurationLedgerSQLiteSchemaVersion != 17 {
		t.Fatalf("cicontract duration-ledger SQLite physical schema = %d, want v17", cicontract.DurationLedgerSQLiteSchemaVersion)
	}
	ledger := readRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/ledger_store_sqlite.go")
	if !strings.Contains(ledger, "durationLedgerSQLiteSchemaVersion = cicontract.DurationLedgerSQLiteSchemaVersion") {
		t.Fatal("duration-ledger SQLite schema version must consume the cicontract owner")
	}
	migration := readRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/ledger_store_sqlite_namespace_migration.go")
	for _, marker := range []string{"legacyDurationLedgerSQLiteSchemaVersion = 13", "localDurationLedgerSQLiteSchemaVersion = 14", "retainedProofDurationLedgerSQLiteSchemaVersion = 16", "migrateDurationLedgerSQLiteSchema13To14", "migrateDurationLedgerSQLiteSchema14To15", "migrateDurationLedgerSQLiteSchema15To16", "migrateDurationLedgerSQLiteSchema16To17", "strictLocalWorkloadPassSQLiteSchema", "durationLedgerRemoteCIExecutionScopeSchemaStatements", "durationLedgerRetainedWorkloadPassProofSchemaStatements", "durationLedgerSourceReplayIndexSchemaStatements", "backfillRetainedWorkloadPassProofs"} {
		if !strings.Contains(migration, marker) {
			t.Errorf("local migration is missing additive marker %q", marker)
		}
	}
}

func assertLocalPassIdentityAndNamespace(t *testing.T, root string) {
	t.Helper()
	assertLocalPassWorkloadPassKey(t, root)
	assertLocalPassRemoteIdentity(t, root)
	assertLocalPassEnvironment(t, root)
}

func assertLocalPassWorkloadPassKey(t *testing.T, root string) {
	t.Helper()
	localPath := root + "/internal/devtools/gate/local_pass_contract.go"
	keyFile := parseRemoteCIContractGuardFile(t, localPath)
	if !localPassTypeHasField(keyFile, "WorkloadPassKey", "Namespace") || !localPassTypeHasField(keyFile, "WorkloadPassKey", "IdentityDigest") {
		t.Fatal("WorkloadPassKey must carry an explicit namespace and identity digest")
	}
	for _, function := range []string{"Validate", "String", "ParseWorkloadPassKey"} {
		if remoteCIFunctionByName(keyFile, function) == nil {
			t.Fatalf("local namespace key contract is missing %s", function)
		}
	}
	if !strings.Contains(readRemoteCIContractGuardFile(t, localPath), `strings.Cut(value, ":")`) {
		t.Fatal("WorkloadPassKey parsing must require an explicit namespace prefix")
	}
}

func assertLocalPassRemoteIdentity(t *testing.T, root string) {
	t.Helper()
	identityPath := root + "/internal/devtools/gate/workload_pass_evidence.go"
	identityFile := parseRemoteCIContractGuardFile(t, identityPath)
	if localPassTypeHasField(identityFile, "WorkloadPassIdentity", "Namespace") {
		t.Fatal("remote WorkloadPassIdentity must not absorb the local namespace")
	}
	identityPayload := remoteCIStructTypeByName(identityFile, "workloadPassIdentityPayload")
	if identityPayload == nil {
		t.Fatal("canonical workload PASS identity payload is missing")
	}
	for _, field := range localPassJSONFields(identityPayload) {
		if localPassIdentityAuditField(field) {
			t.Errorf("remote PASS identity payload includes audit/namespace field %q", field)
		}
	}
	identityFunction := remoteCIFunctionByName(identityFile, "WorkloadPassIdentitySHA256")
	if identityFunction == nil || !remoteCIPlanFunctionHasSelector(identityFunction, "WorkloadPassIdentityDomain") {
		t.Fatal("remote PASS identity hash must use the canonical pass-identity domain")
	}
	if localPassFunctionHasIdentifier(identityFunction, []string{"Namespace", "SourceTreeSHA", "Path", "Commit", "Worktree"}) {
		t.Fatal("remote PASS identity hash must exclude namespace, tree, path, commit and worktree provenance")
	}
}

func assertLocalPassEnvironment(t *testing.T, root string) {
	t.Helper()
	localPath := root + "/internal/devtools/gate/local_pass_contract.go"
	localFile := parseRemoteCIContractGuardFile(t, localPath)
	localEnvironment := remoteCIStructTypeByName(localFile, "LocalWorkloadPassEnvironment")
	if localEnvironment == nil {
		t.Fatal("local PASS environment producer is missing")
	}
	for _, field := range localPassGoJSONFields(localEnvironment) {
		if localPassIdentityAuditField(field) {
			t.Errorf("local PASS environment includes provenance field %q", field)
		}
	}
	localDigest := remoteCIFunctionByName(localFile, "LocalWorkloadPassEnvironmentDigest")
	if localDigest == nil || !localPassFunctionHasSelector(localDigest, "LocalWorkloadPassEnvironmentSchemaVersion") || !localPassFunctionHasSelector(localDigest, "LocalWorkloadPassEnvironmentDomain") {
		t.Fatal("local PASS environment digest must use its independent schema and domain")
	}
	payload := remoteCIStructTypeByName(localFile, "localWorkloadPassEnvironmentPayload")
	for _, problem := range localPassEnvironmentDigestParityErrors(localEnvironment, payload, localDigest) {
		t.Error(problem)
	}
}

func assertLocalRemoteAuthoritySeparation(t *testing.T, root string) {
	t.Helper()
	assertRemoteEvidenceDoesNotUseLocalAuthority(t, root)
	assertRemoteAuthorityPathsDoNotUseLocalAuthority(t, root)
	assertLocalProductionFilesDoNotUseRemoteAuthority(t, root)
	assertRemoteCoordinatorSourcesDoNotUseLocalAuthority(t, root)
	assertRemoteCLISourcesDoNotUseLocalAuthority(t, root)
}

func assertRemoteEvidenceDoesNotUseLocalAuthority(t *testing.T, root string) {
	t.Helper()
	const forbiddenLocalMarker = "ci_local_"
	remoteEvidencePath := root + "/internal/devtools/gate/workload_pass_evidence.go"
	remoteEvidence := parseRemoteCIContractGuardFile(t, remoteEvidencePath)
	for _, function := range []string{"LookupWorkloadPassEvidence", "lookupWorkloadPassEvidenceTransaction", "loadSQLiteReusableWorkloadEvidence"} {
		body := remoteCIFunctionByName(remoteEvidence, function)
		if body != nil && localPassFunctionHasIdentifier(body, []string{"LookupLocalWorkloadPassEvidence", "RecordLocalWorkloadPassBatch"}) {
			t.Errorf("remote PASS lookup function %s calls the local PASS authority", function)
		}
	}
	if strings.Contains(readRemoteCIContractGuardFile(t, remoteEvidencePath), forbiddenLocalMarker) {
		t.Fatal("remote PASS evidence source references a local SQLite table")
	}
}

func assertRemoteAuthorityPathsDoNotUseLocalAuthority(t *testing.T, root string) {
	t.Helper()
	const forbiddenLocalMarker = "ci_local_"
	for _, relative := range []string{
		"internal/devtools/gate/remote_ci_authority_finalize.go",
		"internal/devtools/gate/ci_query_store_remote_run_write.go",
		"internal/devtools/gate/ledger_store_sqlite_write.go",
	} {
		if strings.Contains(readRemoteCIContractGuardFile(t, root+"/"+relative), forbiddenLocalMarker) {
			t.Errorf("remote receipt/authority path %s references a local SQLite table", relative)
		}
	}
}

func assertLocalProductionFilesDoNotUseRemoteAuthority(t *testing.T, root string) {
	t.Helper()
	for _, relative := range localPassProductionFiles() {
		if relative == "internal/devtools/gate/ledger_store_sqlite_schema_workload_reuse.go" {
			// This file owns both the frozen remote schema and the additive
			// local schema; the local literal is checked separately below.
			continue
		}
		if relative == "internal/devtools/gate/ledger_store_sqlite_namespace_migration.go" {
			assertLocalVersionedMigrationBoundary(t, parseRemoteCIContractGuardFile(t, root+"/"+relative))
			continue
		}
		source := readRemoteCIContractGuardFile(t, root+"/"+relative)
		for _, marker := range []string{"ci_runs", "ci_check_receipts", "ci_remote_baseline_state", "FinalizeRemoteCIRunAuthority", "promoteSQLiteRemoteCI", "CheckReceiptRecord"} {
			if strings.Contains(source, marker) {
				t.Errorf("local PASS source %s can become a remote receipt authority through %q", relative, marker)
			}
		}
	}
}

func assertRemoteCoordinatorSourcesDoNotUseLocalAuthority(t *testing.T, root string) {
	t.Helper()
	const forbiddenLocalMarker = "ci_local_"
	for _, relative := range productionFilesUnder(t, root, "internal/devtools/remoteci") {
		file := parseRemoteCIContractGuardFile(t, root+"/"+relative)
		if strings.Contains(readRemoteCIContractGuardFile(t, root+"/"+relative), forbiddenLocalMarker) || localPassASTHasAnyIdentifier(file, localPassRemoteAuthorityIdentifiers()) {
			t.Errorf("remote coordinator source %s references a local SQLite table", relative)
		}
	}
}

func assertRemoteCLISourcesDoNotUseLocalAuthority(t *testing.T, root string) {
	t.Helper()
	for _, relative := range productionFilesUnder(t, root, "cmd/super-dolphin-gate") {
		if relative == "cmd/super-dolphin-gate/local_test_cli.go" {
			continue
		}
		file := parseRemoteCIContractGuardFile(t, root+"/"+relative)
		if localPassASTHasAnyIdentifier(file, localPassRemoteAuthorityIdentifiers()) {
			t.Errorf("remote CLI source %s references a local PASS authority", relative)
		}
	}
}

func assertLocalExecutorProviderBoundary(t *testing.T, root string) {
	t.Helper()
	for _, relative := range localPassProductionFiles() {
		path := root + "/" + relative
		file := parseRemoteCIContractGuardFile(t, path)
		if relative == "cmd/super-dolphin-gate/local_test_cli.go" {
			assertLocalCLIFrozenRemoteSubsetBoundary(t, file)
			continue
		}
		for _, importPath := range localPassImportPaths(file) {
			if strings.Contains(importPath, "/alicloud/") || strings.Contains(importPath, "/remoteci") {
				t.Errorf("local PASS source %s imports remote provider package %q", relative, importPath)
			}
		}
		for _, identifier := range []string{"AgentToken", "AgentTokenDigest", "CreateContainerGroup", "ImageCache", "ImageCacheSnapshot", "OSS", "ECI", "NewECIClient", "RemoteCIAgentToken"} {
			if localPassASTHasIdentifier(file, identifier) {
				t.Errorf("local PASS source %s touches forbidden remote capability %q", relative, identifier)
			}
		}
	}
}

// assertLocalVersionedMigrationBoundary permits the frozen v16 retained-proof
// backfill to read its explicit remote inputs, but rejects every unregistered
// ci_* object rather than exempting the shared migration file.
func assertLocalVersionedMigrationBoundary(t *testing.T, file *ast.File) {
	t.Helper()
	for _, name := range []string{"backfillRetainedWorkloadPassProofs", "validateRetainedWorkloadPassProofBackfillSources"} {
		function := remoteCIFunctionByName(file, name)
		if function == nil {
			t.Errorf("versioned SQLite migration is missing %s", name)
			continue
		}
		if !localPassVersionedMigrationTablesAllowed(function) {
			t.Errorf("versioned SQLite migration %s touches an unregistered ci_* object", name)
		}
	}
}

// assertLocalCLIFrozenRemoteSubsetBoundary permits the one frozen remote
// adapter only after a non-empty Remote selection; ordinary local CLI
// functions remain unable to name remote capabilities or token material.
func assertLocalCLIFrozenRemoteSubsetBoundary(t *testing.T, file *ast.File) {
	t.Helper()
	invoke := remoteCIFunctionByName(file, "runLocalTestInvocation")
	subset := remoteCIFunctionByName(file, "runLocalRemoteSubset")
	if invoke == nil || subset == nil ||
		!localPassSequenceBefore(localPassFunctionCallSequence(invoke), "len", "runLocalRemoteSubset") ||
		!localPassSequenceContains(localPassFunctionCallSequence(subset), "RequireRemoteToken") ||
		!localPassSequenceContains(localPassFunctionCallSequence(subset), "RunSelectedRemoteWorkloads") {
		t.Error("local CLI must reach frozen remote subset only after a non-empty Remote selection and token handshake")
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name == "runLocalRemoteSubset" {
			continue
		}
		if localPassFunctionTouchesForbiddenRemoteCapability(function) {
			t.Errorf("local CLI function %s touches remote provider or token material outside frozen subset adapter", function.Name.Name)
		}
	}
}

func assertLocalSchedulerOrderingAndNoFallback(t *testing.T, root string) {
	t.Helper()
	schedulerPath := root + "/internal/devtools/gate/local_workload_scheduler.go"
	scheduler := parseRemoteCIContractGuardFile(t, schedulerPath)
	assertLocalSchedulerLookupOrder(t, scheduler)
	assertLocalSchedulerMissesDoNotFallback(t, scheduler)
	assertLocalSchedulerAutoScale(t, scheduler)
	assertLocalHostAdmissionSelectors(t, root)
	assertLocalSchedulerRejectsRemoteFallback(t, schedulerPath)
}

func assertLocalSchedulerLookupOrder(t *testing.T, scheduler *ast.File) {
	t.Helper()
	if !localPassExplicitLookupPrecedesHostObserve(scheduler) {
		t.Fatal("local scheduler must call lookupSelectedLocalWorkloads before the miss-admission path observes the host")
	}
}

func assertLocalSchedulerMissesDoNotFallback(t *testing.T, scheduler *ast.File) {
	t.Helper()
	runMisses := remoteCIFunctionByName(scheduler, "RunLocalWorkloadMisses")
	if runMisses == nil {
		t.Fatal("local MISS execution entrypoint is missing")
	}
	if localPassFunctionHasIdentifier(runMisses, []string{"RunSelectedRemoteWorkloads", "executeSelectedRemoteWorkloads", "RemoteExecute"}) {
		t.Fatal("local MISS execution must not fall back to remote execution")
	}
}

func assertLocalSchedulerAutoScale(t *testing.T, scheduler *ast.File) {
	t.Helper()
	auto := remoteCIFunctionByName(scheduler, "localSchedulerAutoScaleAdmitted")
	for _, selector := range []string{"LocalAutoMissCountLimit", "LocalAutoDurationLimitMS", "LocalAutoSingleDurationLimitMS"} {
		if auto == nil || !localPassFunctionHasSelector(auto, selector) {
			t.Errorf("auto scale is missing frozen contract selector %s", selector)
		}
	}
}

func assertLocalHostAdmissionSelectors(t *testing.T, root string) {
	t.Helper()
	host := parseRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/local_host_admission.go")
	for _, function := range []string{"BuildLocalHostAdmissionFromSamples", "validateLocalHostWindow"} {
		body := remoteCIFunctionByName(host, function)
		if body == nil || !localPassFunctionHasSelector(body, "LocalHostCPUWindowMS") {
			t.Errorf("local host admission function %s is not bound to the 30 second window owner", function)
		}
	}
	if !localPassFunctionHasSelector(remoteCIFunctionByName(host, "BuildLocalHostAdmissionFromSamples"), "LocalHostCPUBusyLimitPercent") {
		t.Fatal("local host admission must use the <=70%% CPU busy owner")
	}
}

func assertLocalSchedulerRejectsRemoteFallback(t *testing.T, schedulerPath string) {
	t.Helper()
	if !strings.Contains(readRemoteCIContractGuardFile(t, schedulerPath), "remote fallback is forbidden") {
		t.Fatal("explicit local target must fail instead of silently falling back to remote")
	}
}

func assertLocalAuthorityStateFailFast(t *testing.T, root string) {
	t.Helper()
	statePath := root + "/internal/devtools/gate/local_authority_state.go"
	state := readRemoteCIContractGuardFile(t, statePath)
	for _, marker := range []string{"InitializeLocalAuthority", "openSQLiteAuthority(true)", "initializeLocalAuthorityStateOnConnection", "currentLocalAuthorityGeneration", "validateLocalAuthorityStateProjection", "DisallowUnknownFields", "sql.ErrNoRows", "local authority state is missing"} {
		if !strings.Contains(state, marker) {
			t.Errorf("local authority state is missing fail-fast marker %q", marker)
		}
	}
}

func assertLocalProductionAuthorityInitialization(t *testing.T, root string) {
	t.Helper()
	source := readRemoteCIContractGuardFile(t, root+"/cmd/super-dolphin-gate/local_test_cli_production.go")
	if !strings.Contains(source, "store.InitializeLocalAuthority()") {
		t.Fatal("production local plan must explicitly initialize the local SQLite authority")
	}
	for _, marker := range []string{"validateWorkloadAuthorityTarget(options.Target)", "scheduleTarget: gatecontract.LocalWorkloadScheduleTarget(options.Target)", "Target: inputs.scheduleTarget"} {
		if !strings.Contains(source, marker) {
			t.Errorf("production local plan target projection is missing %q", marker)
		}
	}
	for _, forbidden := range []string{"LoadRemoteBaselineState", "baselineLedger", "ImageCache", "AgentToken"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("production local authority initializer must not consume remote state %q", forbidden)
		}
	}
}

func assertLocalAuthorityLookupInitializesAndValidates(t *testing.T, root string) {
	t.Helper()
	lookup := readRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/local_pass_authority.go")
	if !strings.Contains(lookup, "openSQLiteAuthority(true)") || !strings.Contains(lookup, "currentLocalAuthorityGeneration(transaction)") {
		t.Fatal("local lookup must clean-initialize a missing DB and then validate local authority state")
	}
}

func assertLocalSQLiteSchemaGuardFailFast(t *testing.T, root string) {
	t.Helper()
	strict := readRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/ledger_store_sqlite_schema_strict.go")
	for _, marker := range []string{"schemaVersion != durationLedgerSQLiteSchemaVersion", "refuse migration", "strictLocalWorkloadPassSQLiteSchema"} {
		if !strings.Contains(strict, marker) {
			t.Errorf("SQLite schema guard is missing corrupt/legacy fail-fast marker %q", marker)
		}
	}
}

func assertLocalCLIWorkloadCompatibility(t *testing.T, root string) {
	t.Helper()
	options := readRemoteCIContractGuardFile(t, root+"/cmd/super-dolphin-gate/remote_run_options.go")
	for _, marker := range []string{`flags.StringVar(&options.WorkloadID, "workload"`, `flags.Var(&gateWorkloads, "gate-workload"`, `flags.StringVar(&options.Target, "target", defaultTarget`} {
		if !strings.Contains(options, marker) {
			t.Errorf("CLI workload compatibility marker %q is missing", marker)
		}
	}
	cliFile := parseRemoteCIContractGuardFile(t, root+"/cmd/super-dolphin-gate/test_cli.go")
	for _, function := range []string{"parseAutoTestRunOptions", "bindOrValidateAutoTestSelectors", "bindMcpLSPWorkloadSelectors"} {
		if remoteCIFunctionByName(cliFile, function) == nil {
			t.Fatalf("legacy --workload CLI semantic function %s is missing", function)
		}
	}
	if !strings.Contains(readRemoteCIContractGuardFile(t, root+"/cmd/super-dolphin-gate/test_cli.go"), `options.WorkloadID`) {
		t.Fatal("test CLI no longer preserves catalog --workload binding")
	}
	localCLI := readRemoteCIContractGuardFile(t, root+"/cmd/super-dolphin-gate/local_test_cli.go")
	for _, marker := range []string{"validateWorkloadAuthorityTarget", `case "local", "remote", "auto", "hybrid"`, "validateLocalWorkloadSelection"} {
		if !strings.Contains(localCLI, marker) {
			t.Errorf("local CLI authority boundary is missing marker %q", marker)
		}
	}
}

func localPassProductionFiles() []string {
	return []string{
		"internal/devtools/cicontract/local_pass_contract.go",
		"internal/devtools/gate/local_authority_state.go",
		"internal/devtools/gate/local_executor.go",
		"internal/devtools/gate/local_executor_session.go",
		"internal/devtools/gate/local_host_admission.go",
		"internal/devtools/gate/local_pass_authority.go",
		"internal/devtools/gate/local_pass_contract.go",
		"internal/devtools/gate/local_workload_scheduler.go",
		"internal/devtools/gate/ledger_store_sqlite_namespace_migration.go",
		"internal/devtools/gate/ledger_store_sqlite_schema_workload_reuse.go",
		"cmd/super-dolphin-gate/local_test_cli.go",
	}
}

func localPassAdditiveSQLiteTable(name string) bool {
	switch name {
	case "ci_local_authority_state", "ci_local_workload_origins", "ci_local_workload_executions", "ci_local_workload_pass_evidence":
		return true
	default:
		return false
	}
}

func productionFilesUnder(t *testing.T, root, relativeRoot string) []string {
	t.Helper()
	var files []string
	err := walkProductionFiles(root+"/"+relativeRoot, func(path string) {
		relative := strings.TrimPrefix(strings.TrimPrefix(path, root), "/")
		files = append(files, relative)
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func walkProductionFiles(root string, visit func(string)) error {
	return walkProductionFilesWithFS(root, visit)
}

func walkProductionFilesWithFS(root string, visit func(string)) error {
	entries, err := readDirRecursive(root)
	if err != nil {
		return err
	}
	for _, path := range entries {
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			visit(path)
		}
	}
	return nil
}

func readDirRecursive(root string) ([]string, error) {
	var files []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		path := root + "/" + entry.Name()
		if entry.IsDir() {
			children, err := readDirRecursive(path)
			if err != nil {
				return nil, err
			}
			files = append(files, children...)
			continue
		}
		files = append(files, path)
	}
	return files, nil
}

func localPassTypeHasField(file *ast.File, typeName, fieldName string) bool {
	structure := remoteCIStructTypeByName(file, typeName)
	if structure == nil {
		return false
	}
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if name.Name == fieldName {
				return true
			}
		}
	}
	return false
}

func localPassJSONFields(structure *ast.StructType) []string {
	if structure == nil {
		return nil
	}
	return enumerateStructJSONFields(structure)
}

func localPassGoJSONFields(structure *ast.StructType) []string {
	if structure == nil {
		return nil
	}
	var fields []string
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			fields = append(fields, name.Name)
		}
	}
	return fields
}

func localPassIdentityAuditField(field string) bool {
	lower := strings.ToLower(field)
	return strings.Contains(lower, "namespace") || strings.Contains(lower, "tree") || strings.Contains(lower, "path") || strings.Contains(lower, "commit") || strings.Contains(lower, "worktree")
}

func localPassFunctionHasIdentifier(function *ast.FuncDecl, forbidden []string) bool {
	if function == nil {
		return false
	}
	for _, name := range forbidden {
		found := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok && identifier.Name == name {
				found = true
				return false
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

func localPassFunctionHasSelector(function *ast.FuncDecl, selectorName string) bool {
	if function == nil {
		return false
	}
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == selectorName {
			found = true
			return false
		}
		return !found
	})
	return found
}

func localPassASTHasIdentifier(file *ast.File, name string) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Name == name {
			found = true
			return false
		}
		return !found
	})
	return found
}

func localPassASTHasAnyIdentifier(file *ast.File, names []string) bool {
	for _, name := range names {
		if localPassASTHasIdentifier(file, name) {
			return true
		}
	}
	return false
}

func localPassRemoteAuthorityIdentifiers() []string {
	return []string{
		"ExecuteLocalGateWorkload",
		"PrepareLocalWorkloadSchedule",
		"RunLocalWorkloadMisses",
		"LookupLocalWorkloadPassEvidence",
		"RecordLocalWorkloadPassBatch",
		"LocalWorkloadPassBatch",
		"ci_local_authority_state",
		"ci_local_workload_origins",
		"ci_local_workload_executions",
		"ci_local_workload_pass_evidence",
	}
}

func localPassImportPaths(file *ast.File) []string {
	var paths []string
	for _, importSpec := range file.Imports {
		if importSpec.Path == nil {
			continue
		}
		value, err := strconv.Unquote(importSpec.Path.Value)
		if err == nil {
			paths = append(paths, value)
		}
	}
	return paths
}

func localPassFunctionCallSequence(function *ast.FuncDecl) []string {
	if function == nil {
		return nil
	}
	var sequence []string
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok {
			sequence = append(sequence, remoteCICallName(call))
		}
		return true
	})
	return sequence
}

// localPassExplicitLookupPrecedesHostObserve verifies the finite scheduler path:
// PrepareLocalWorkloadSchedule -> prepareLocalSchedulerLocalItems ->
// lookupSelectedLocalWorkloads -> admitLocalSchedulerMisses ->
// observeLocalSchedulerHost. Host sampling before PASS lookup is forbidden.
func localPassExplicitLookupPrecedesHostObserve(scheduler *ast.File) bool {
	prepare := remoteCIFunctionByName(scheduler, "PrepareLocalWorkloadSchedule")
	prepareLocalItems := remoteCIFunctionByName(scheduler, "prepareLocalSchedulerLocalItems")
	admitMisses := remoteCIFunctionByName(scheduler, "admitLocalSchedulerMisses")
	if prepare == nil || prepareLocalItems == nil || admitMisses == nil {
		return false
	}
	prepareSequence := localPassFunctionCallSequence(prepare)
	if localPassSequenceContains(prepareSequence, "observeLocalSchedulerHost") ||
		!localPassSequenceContains(prepareSequence, "prepareLocalSchedulerLocalItems") {
		return false
	}
	prepareLocalItemsSequence := localPassFunctionCallSequence(prepareLocalItems)
	if localPassSequenceContains(prepareLocalItemsSequence, "observeLocalSchedulerHost") {
		return false
	}
	if !localPassSequenceBefore(
		prepareLocalItemsSequence,
		"lookupSelectedLocalWorkloads",
		"admitLocalSchedulerMisses",
	) {
		return false
	}
	return localPassSequenceContains(localPassFunctionCallSequence(admitMisses), "observeLocalSchedulerHost")
}

func localPassSequenceContains(sequence []string, want string) bool {
	return slices.Contains(sequence, want)
}

func localPassSequenceBefore(sequence []string, first, second string) bool {
	firstIndex, secondIndex := -1, -1
	for index, name := range sequence {
		if name == first && firstIndex < 0 {
			firstIndex = index
		}
		if name == second && secondIndex < 0 {
			secondIndex = index
		}
	}
	return firstIndex >= 0 && secondIndex >= 0 && firstIndex < secondIndex
}

func localPassConstString(file *ast.File, name string) (string, bool) {
	for _, declaration := range file.Decls {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok || gen.Tok.String() != "const" {
			continue
		}
		value, matched, valid := localPassConstStringFromSpecs(gen.Specs, name)
		if matched {
			return value, valid
		}
	}
	return "", false
}

func localPassConstStringFromSpecs(specs []ast.Spec, name string) (string, bool, bool) {
	for _, spec := range specs {
		values, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		value, matched, valid := localPassConstStringFromValues(values, name)
		if matched {
			return value, true, valid
		}
	}
	return "", false, false
}

func localPassConstStringFromValues(values *ast.ValueSpec, name string) (string, bool, bool) {
	for index, identifier := range values.Names {
		if identifier.Name != name || index >= len(values.Values) {
			continue
		}
		literal, ok := values.Values[index].(*ast.BasicLit)
		if !ok || literal.Kind.String() != "STRING" {
			return "", true, false
		}
		value, err := strconv.Unquote(literal.Value)
		return value, true, err == nil
	}
	return "", false, false
}
