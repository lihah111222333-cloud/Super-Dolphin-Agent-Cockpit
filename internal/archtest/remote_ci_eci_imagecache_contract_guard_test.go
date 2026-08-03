package archtest

import (
	"go/ast"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestRemoteCIECIImageCacheContractDocumentationAnchors keeps the accepted
// contract discoverable from both repository-level navigation entrypoints.
func TestRemoteCIECIImageCacheContractDocumentationAnchors(t *testing.T) {
	root := findRepoRoot(t)
	for _, file := range []string{"AGENTS.md", "docs/契约/README.md"} {
		contents := readRemoteCIContractGuardFile(t, filepath.Join(root, file))
		if !strings.Contains(contents, "remote-ci-eci-imagecache-contract.md") {
			t.Errorf("%s must link the accepted remote CI ImageCache contract", file)
		}
	}
}

// TestRemoteCIContractHasOneCodeOwner 将 accepted 文档绑定到唯一 Go owner，
// 并拒绝生产消费者在本地重声明平台、耗时或刷新状态协议。
func TestRemoteCIContractHasOneCodeOwner(t *testing.T) {
	root := findRepoRoot(t)
	assertRemoteCIContractDocumentHasCanonicalOwner(t, root)
	assertRemoteCIContractOwnerHasSourceAndTests(t, root)
	assertRemoteCIContractConsumersImportOwner(t, root)
}

// assertRemoteCIContractDocumentHasCanonicalOwner 验证文档身份和规范映射仍由唯一代码 owner 提供。
func assertRemoteCIContractDocumentHasCanonicalOwner(t *testing.T, root string) {
	t.Helper()
	if err := cicontract.Validate(); err != nil {
		t.Fatalf("validate remote CI code contract: %v", err)
	}
	contract := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(cicontract.DocumentPath)))
	if !strings.Contains(contract, "internal/devtools/cicontract") {
		t.Error("accepted remote CI contract must name internal/devtools/cicontract as its code owner")
	}
	for _, identity := range []string{cicontract.ID, cicontract.ExecutionPathID, cicontract.GenerationOneReceiptImportPathID, cicontract.SQLAuthorityID} {
		if !strings.Contains(contract, identity) {
			t.Errorf("accepted remote CI document is missing code contract identity %q", identity)
		}
	}
	if got := remoteCICanonicalContractBlock(t, contract); got != cicontract.CanonicalMarkdown() {
		t.Error("accepted remote CI document and internal/devtools/cicontract are not 1:1")
	}
}

// assertRemoteCIContractOwnerHasSourceAndTests 验证 owner 同时保有生产 API 与聚焦测试。
func assertRemoteCIContractOwnerHasSourceAndTests(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "internal", "devtools", "cicontract"))
	if err != nil {
		t.Fatalf("remote CI contract code owner is unavailable: %v", err)
	}
	hasProductionSource, hasTest := false, false
	for _, entry := range entries {
		isGo := !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go")
		hasTest = hasTest || (isGo && strings.HasSuffix(entry.Name(), "_test.go"))
		hasProductionSource = hasProductionSource || (isGo && !strings.HasSuffix(entry.Name(), "_test.go"))
	}
	if !hasProductionSource || !hasTest {
		t.Fatalf("remote CI contract owner must provide production API and focused tests: source=%t test=%t", hasProductionSource, hasTest)
	}
}

// assertRemoteCIContractConsumersImportOwner 拒绝消费者复制契约值后绕过唯一 owner。
func assertRemoteCIContractConsumersImportOwner(t *testing.T, root string) {
	t.Helper()
	for _, file := range remoteCIContractConsumerFiles(t, root) {
		parsed := parseRemoteCIContractGuardFile(t, file)
		if remoteCIRepeatsContractValue(parsed) && !remoteCIImportsContractOwner(parsed) {
			t.Errorf("%s repeats a remote CI contract value without importing internal/devtools/cicontract", relativeRemoteCIContractPath(t, root, file))
		}
	}
}

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
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "ledger_store_sqlite_schema") || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		schema.WriteString(readRemoteCIContractGuardFile(t, filepath.Join(schemaDirectory, entry.Name())))
	}
	for _, binding := range cicontract.SQLAuthorityBindings() {
		statement := "CREATE TABLE IF NOT EXISTS " + binding.Table
		if !strings.Contains(schema.String(), statement) {
			t.Errorf("gate SQLite schema does not create cicontract authority table %q for domain %q", binding.Table, binding.Domain)
		}
	}
}

// TestRemoteCIDurationLedgerSchemaForbidsCompatibilityDDL keeps startup from
// reconstructing any pre-current physical schema. Existing non-current files
// must be rejected by exact preflight before production executes DDL.
func TestRemoteCIDurationLedgerSchemaForbidsCompatibilityDDL(t *testing.T) {
	root := findRepoRoot(t)
	schemaDirectory := filepath.Join(root, "internal", "devtools", "gate")
	entries, err := os.ReadDir(schemaDirectory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "ledger_store_sqlite_schema") ||
			!strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(schemaDirectory, entry.Name())
		for _, violation := range remoteCIDurationLedgerCompatibilityDDLViolations(readRemoteCIContractGuardFile(t, path)) {
			t.Errorf("%s contains retired schema compatibility path %q", relativeRemoteCIContractPath(t, root, path), violation)
		}
	}

	if violations := remoteCIDurationLedgerCompatibilityDDLViolations(`package fixture
const current = "CREATE TABLE ci_runs (job_id TEXT PRIMARY KEY)"
`); len(violations) != 0 {
		t.Fatalf("current schema fixture was rejected: %v", violations)
	}
	legacy := `package fixture
const alter = "ALTER TABLE ci_runs ADD COLUMN candidate_state TEXT"
const copy = "INSERT INTO ci_check_receipts_next SELECT * FROM ci_check_receipts"
const image = "successor_image"
`
	if violations := remoteCIDurationLedgerCompatibilityDDLViolations(legacy); len(violations) < 4 {
		t.Fatalf("legacy schema compatibility fixture violations = %v, want ALTER/copy/next/retired-column rejection", violations)
	}
}

func remoteCIDurationLedgerCompatibilityDDLViolations(source string) []string {
	normalized := strings.ToLower(source)
	violations := map[string]bool{}
	for _, forbidden := range []string{
		"alter table",
		"candidate_state",
		"successor_image",
		"replacelegacycheckreceipttable",
		"migratecheckreceiptreusesqliteschema",
	} {
		if strings.Contains(normalized, forbidden) {
			violations[forbidden] = true
		}
	}
	if strings.Contains(normalized, "_next") {
		violations["shadow _next table"] = true
	}
	if strings.Contains(normalized, "insert into") && strings.Contains(normalized, "select") {
		violations["INSERT INTO ... SELECT compatibility copy"] = true
	}
	result := make([]string, 0, len(violations))
	for violation := range violations {
		result = append(result, violation)
	}
	slices.Sort(result)
	return result
}

// TestRemoteCIAuthorityDoesNotReintroduceJSONStateFilesOrSecondWriters keeps
// baseline and ledger persistence SQLite-only. It excludes protocol request,
// receipt, and state_json payload encoding: those are transport/column values,
// not filesystem truth sources.
func TestRemoteCIAuthorityDoesNotReintroduceJSONStateFilesOrSecondWriters(t *testing.T) {
	root := findRepoRoot(t)
	for _, relative := range []string{
		"cmd/super-dolphin-gate/remote_baseline_state_store.go",
		"internal/devtools/gate/remote_baseline_state_store.go",
		"internal/devtools/gate/ledger_store_sqlite_schema.go",
	} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if remoteCIUsesFilesystemStateStore(parseRemoteCIContractGuardFile(t, path)) {
			t.Errorf("%s must not read or write a baseline/ledger filesystem state store", relative)
		}
	}

	compareAndSwapCalls, promotionCalls := 0, 0
	for _, file := range remoteCIContractConsumerFiles(t, root) {
		parsed := parseRemoteCIContractGuardFile(t, file)
		compareAndSwapCalls += remoteCIFunctionCallCount(parsed, "CompareAndSwapRemoteBaselineState")
		promotionCalls += remoteCIFunctionCallCount(parsed, "PromoteRemoteBaselineStateWithRefreshLease")
	}
	if compareAndSwapCalls != 0 {
		t.Fatalf("remote baseline generic CAS production calls = %d, want 0", compareAndSwapCalls)
	}
	if promotionCalls != 0 {
		t.Fatalf("repository successor promotion calls = %d, want 0", promotionCalls)
	}
}

// TestRemoteCIRunAuthorityHasOneSQLiteFinalizer 拒绝绕过写入回执、新鲜 PASS 证据或运行权威事务的公开捷径。
// 暂定投影刻意不具备写入能力。
func TestRemoteCIRunAuthorityHasOneSQLiteFinalizer(t *testing.T) {
	root := findRepoRoot(t)
	gateDirectory := filepath.Join(root, "internal", "devtools", "gate")
	if finalizers := remoteCIGateAuthorityFinalizers(t, gateDirectory); finalizers != 1 {
		t.Fatalf("remote CI authority finalizer declarations = %d, want 1", finalizers)
	}
	projection := readRemoteCIContractGuardFile(t, filepath.Join(gateDirectory, "ci_query_store_remote_run_write.go"))
	if !strings.Contains(projection, "\t\t0, record.StartedAt.UTC().UnixMilli()") || strings.Contains(projection, "authoritative = excluded.authoritative") {
		t.Fatal("provisional remote CI projection must persist authoritative = 0 and never update it")
	}
}

// remoteCIGateAuthorityFinalizers 统计唯一终结器并逐文件拒绝历史写入捷径。
func remoteCIGateAuthorityFinalizers(t *testing.T, gateDirectory string) int {
	t.Helper()
	entries, err := os.ReadDir(gateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	finalizers := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			finalizers += remoteCIGateFileAuthorityFinalizers(t, gateDirectory, entry.Name())
		}
	}
	return finalizers
}

func remoteCIGateFileAuthorityFinalizers(t *testing.T, gateDirectory, name string) int {
	t.Helper()
	parsed := parseRemoteCIContractGuardFile(t, filepath.Join(gateDirectory, name))
	for _, forbidden := range []string{"AppendCheckReceipts", "PromoteRemoteCIWorkloadPassEvidence", "FinalizeRemoteCIRunAuthority"} {
		if remoteCIFunctionExists(parsed, forbidden) {
			t.Errorf("%s retains forbidden remote CI authority writer %s", name, forbidden)
		}
	}
	if remoteCIFunctionExists(parsed, "FinalizeRemoteCIRunAuthorityWithSamples") {
		return 1
	}
	return 0
}

// TestRemoteCIBaselineStateMutatorsRejectRepositoryPromotion keeps accepted
// state initialization at the external generation-one receipt boundary.
func TestRemoteCIBaselineStateMutatorsRejectRepositoryPromotion(t *testing.T) {
	root := findRepoRoot(t)
	for _, file := range remoteCIProductionFiles(t, root) {
		parsed := parseRemoteCIContractGuardFile(t, file)
		for _, forbidden := range []string{
			"CompareAndSwapRemoteBaselineState",
			"loadRemoteBaselineStateForRefresh",
			"nextRemoteBaselineGeneration",
			"remoteBaselineDatabasePath",
		} {
			if remoteCIFunctionExists(parsed, forbidden) {
				t.Errorf("%s retains forbidden baseline state helper %s", relativeRemoteCIContractPath(t, root, file), forbidden)
			}
		}
	}
	statePath := filepath.Join(root, "internal", "devtools", "remoteci", "baseline_state.go")
	if remoteCIFunctionExists(parseRemoteCIContractGuardFile(t, statePath), "Renew") {
		t.Error("internal/devtools/remoteci/baseline_state.go retains retired BaselineState.Renew API")
	}
}

func remoteCICanonicalContractBlock(t *testing.T, document string) string {
	t.Helper()
	const begin, end = "<!-- cicontract:begin -->", "<!-- cicontract:end -->"
	start := strings.Index(document, begin)
	if start < 0 {
		t.Fatal("accepted remote CI document is missing the cicontract begin marker")
	}
	relativeEnd := strings.Index(document[start:], end)
	if relativeEnd < 0 {
		t.Fatal("accepted remote CI document is missing the cicontract end marker")
	}
	finish := start + relativeEnd + len(end)
	if strings.Contains(document[finish:], begin) || strings.Contains(document[finish:], end) {
		t.Fatal("accepted remote CI document contains multiple cicontract blocks")
	}
	return document[start:finish]
}

// TestRemoteCIProductionPathRejectsLegacyExecutionPaths parses only executable
// remote-CI Go sources and rejects every retired execution/cache path.
func TestRemoteCIProductionPathRejectsLegacyExecutionPaths(t *testing.T) {
	root := findRepoRoot(t)
	for _, file := range remoteCIProductionFiles(t, root) {
		parsed := parseRemoteCIContractGuardFile(t, file)
		for _, violation := range remoteCIConcurrencyCapViolations(parsed) {
			t.Errorf("%s retains forbidden remote CI concurrency cap %s", relativeRemoteCIContractPath(t, root, file), violation)
		}
		for _, violation := range remoteCIWorkloadPassReuseViolations(parsed) {
			t.Errorf("%s retains forbidden legacy workload PASS reuse path %s", relativeRemoteCIContractPath(t, root, file), violation)
		}
		for _, violation := range remoteCILegacySQLiteSchemaViolations(parsed) {
			t.Errorf("%s retains forbidden legacy SQLite schema path %s", relativeRemoteCIContractPath(t, root, file), violation)
		}
	}
}

// TestRemoteCIWorkloadProjectionRejectsOnlyLegacyReusePaths 允许严格 SQLite 证据路径，
// 同时阻止已退役缓存或 schema 名称成为第二权威。
func TestRemoteCIWorkloadProjectionRejectsOnlyLegacyReusePaths(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, "internal", "devtools", "remoteci", "workload_projection.go")
	parsed := parseRemoteCIContractGuardFile(t, path)
	for _, violation := range remoteCIWorkloadPassReuseViolations(parsed) {
		t.Errorf("workload projection retains forbidden legacy reuse path %s", violation)
	}
}

func TestRemoteCIConcurrencyCapGuardCounterexamples(t *testing.T) {
	safe := remoteCIParseGuardFixture(t, `package fixture
func heartbeat() {
	const MaxBytes = 1024
	const MaxInt64 = 1
	const MaxControlPlaneRetryDuration = 1
	errCh := make(chan error, 1)
	_ = MaxBytes
	_ = MaxInt64
	_ = MaxControlPlaneRetryDuration
	_ = errCh
}`)
	if got := remoteCIConcurrencyCapViolations(safe); len(got) != 0 {
		t.Fatalf("safe size/retry/error-channel fixture has false concurrency caps: %v", got)
	}
	gitIndexBoundary := remoteCIParseGuardFixture(t, `package fixture
func gitIndexConsistencyBoundary() {
	_ = "index.lock"
}
`)
	if got := remoteCIConcurrencyCapViolations(gitIndexBoundary); len(got) != 0 {
		t.Fatalf("Git index consistency boundary has false CI admission cap: %v", got)
	}
	capped := remoteCIParseGuardFixture(t, `package fixture
func shards() {
	const MaxShards = 4
	slots := make(chan struct{}, 4)
	semaphore.NewWeighted(4)
	_ = MaxShards
	_ = slots
}`)
	if got := remoteCIConcurrencyCapViolations(capped); len(got) == 0 {
		t.Fatal("shard cap fixture must be rejected")
	}
	serialized := remoteCIParseGuardFixture(t, `package fixture
func run() {
	_ = GlobalHookLock{}
	_ = ActiveJobLock{}
	_ = SharedRawToken{}
}
`)
	if got := remoteCIConcurrencyCapViolations(serialized); len(got) == 0 {
		t.Fatal("global hook, active-job, and shared raw-token serialization fixture must be rejected")
	}
}
func TestRemoteCILegacyGuardCounterexamples(t *testing.T) {
	safe := remoteCIParseGuardFixture(t, `package fixture
func calibration(passedWorkloads map[string]struct{}) {
	_ = passedWorkloads
}
func strictReuse(proofSHA256 string) {
	_ = proofSHA256
}
func source(TargetSourceClosure string) {
	_ = TargetSourceClosure
}
func freshWorkerCopy(BootstrapVolume string) {
	_ = BootstrapVolume
}`)
	if got := remoteCIWorkloadPassReuseViolations(safe); len(got) != 0 {
		t.Fatalf("current-run calibration PASS evidence or strict proof was mistaken for legacy result reuse: %v", got)
	}

	legacyReuse := remoteCIParseGuardFixture(t, `package fixture
func run() {
	_ = WorkloadPassCache{}
	_ = ReusedWorkloadResult{}
	_ = CIWorkloadFingerprints{}
}`)
	if got := remoteCIWorkloadPassReuseViolations(legacyReuse); len(got) == 0 {
		t.Fatal("legacy workload PASS result cache/reuse schema fixture must be rejected")
	}
	legacySQLite := remoteCIParseGuardFixture(t, `package fixture
func schema() { _ = "ci_run_workloads" }`)
	if got := remoteCILegacySQLiteSchemaViolations(legacySQLite); len(got) == 0 {
		t.Fatal("legacy SQLite workload schema fixture must be rejected")
	}
}

func remoteCILegacySQLiteSchemaViolations(file *ast.File) []string {
	violations := map[string]bool{}
	for _, literal := range remoteCIStringLiterals(file) {
		if strings.Contains(literal, "ci_run_workloads") ||
			strings.Contains(literal, "ci_workload_pass_proofs") ||
			strings.Contains(literal, "ci_workload_fingerprints") ||
			strings.Contains(literal, "ci_workload_identity_aliases") ||
			strings.Contains(literal, "ci_workload_fingerprint_observations") ||
			strings.Contains(literal, "legacy_source_sha256") {
			violations["literal "+literal] = true
		}
	}
	return remoteCIViolationList(violations)
}

// TestRemoteCIHundredSecondTargetCannotBecomeTermination rejects the precise
// target-duration spellings when they are wired to timeout/cancel/kill paths;
// the only allowed 100-second behavior is cicontract's warn-and-continue rule.
func TestRemoteCIHundredSecondTargetCannotBecomeTermination(t *testing.T) {
	root := findRepoRoot(t)
	for _, file := range remoteCIProductionFiles(t, root) {
		parsed := parseRemoteCIContractGuardFile(t, file)
		for _, violation := range remoteCIHundredSecondTerminationViolations(parsed) {
			t.Errorf("%s turns the 100-second target into terminating call %s", relativeRemoteCIContractPath(t, root, file), violation)
		}
	}
}

// TestRemoteCITestCommandHasOnlyTheRemoteECIPath prevents the test command
// from recreating a host executor or treating a coordinator cache probe as an
// authoritative result. Cache reuse remains an internal coordinator concern.
func TestRemoteCITestCommandHasOnlyTheRemoteECIPath(t *testing.T) {
	root := findRepoRoot(t)
	for _, relative := range []string{
		"cmd/super-dolphin-gate/test_local_exec.go",
		"internal/devtools/remoteci/local_test_policy.go",
		"internal/devtools/remoteci/local_test_policy_test.go",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); !os.IsNotExist(err) {
			t.Errorf("remote CI must not retain local test executor artifact %s", relative)
		}
	}

	path := filepath.Join(root, "cmd", "super-dolphin-gate", "test_cli.go")
	parsed := parseRemoteCIContractGuardFile(t, path)
	for _, identifier := range []string{
		"autoTestBackend",
		"selectAutoTestBackend",
		"runLockedLocalLightTests",
		"executeLocalLightTests",
		"localSourceMatchesTree",
	} {
		if remoteCIForbiddenIdentifiers(parsed)[identifier] {
			t.Errorf("cmd/super-dolphin-gate/test_cli.go retains local test routing %s", identifier)
		}
	}
	for _, literal := range remoteCIStringLiterals(parsed) {
		if strings.EqualFold(literal, "local-light") || strings.EqualFold(literal, "remote-cache") {
			t.Errorf("cmd/super-dolphin-gate/test_cli.go retains non-ECI backend %q", literal)
		}
	}
	if calls := remoteCIFunctionCallCount(parsed, "executeRemoteRun"); calls != 1 {
		t.Errorf("test command executeRemoteRun calls = %d, want 1", calls)
	}
	if calls := remoteCIFunctionCallCount(parsed, "emitRemoteRunResult"); calls != 1 {
		t.Errorf("test command emitRemoteRunResult calls = %d, want 1", calls)
	}
}

func TestRemoteCIHooksRejectConcurrencyCaps(t *testing.T) {
	root := findRepoRoot(t)
	for _, hook := range []string{"pre-commit", "pre-push"} {
		contents := readRemoteCIContractGuardFile(t, filepath.Join(root, ".githooks", hook))
		for _, forbidden := range []string{"max_shards", "max-shards", "max_concurrency", "max-concurrency", "flock", "global_hook_lock", "global-hook-lock", "active_job_lock", "active-job-lock", "shared_raw_token", "shared-raw-token"} {
			if strings.Contains(strings.ToLower(contents), forbidden) {
				t.Errorf(".githooks/%s retains forbidden remote CI concurrency cap %q", hook, forbidden)
			}
		}
	}
}

// TestRemoteCIECIRequestsBindAcceptedSnapshot proves every executable remote
// ECI CreateRequest binds the accepted ImageCacheSnapshotID, never a cache ID
// lookup or automatic cache selection.
func TestRemoteCIECIRequestsBindAcceptedSnapshot(t *testing.T) {
	root := findRepoRoot(t)
	found := false
	for _, file := range remoteCIProductionFiles(t, root) {
		parsed := parseRemoteCIContractGuardFile(t, file)
		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || !isECICreateRequest(literal.Type) {
				return true
			}
			found = true
			if !remoteCICreateRequestHasSnapshot(literal) {
				t.Errorf("%s creates an ECI container group without ImageCacheSnapshotID", relativeRemoteCIContractPath(t, root, file))
			}
			return true
		})
	}
	if !found {
		t.Fatal("remote CI production path has no ECI CreateRequest to bind to the accepted snapshot")
	}
}

// TestRemoteCILegacyRefreshWritersAreAbsent prevents a direct ImageCache
// promotion or baseline-state write from bypassing the refresh lease CAS.
func TestRemoteCILegacyRefreshWritersAreAbsent(t *testing.T) {
	root := findRepoRoot(t)
	for _, file := range remoteCIProductionFiles(t, root) {
		parsed := parseRemoteCIContractGuardFile(t, file)
		for _, legacy := range []string{
			"promoteRemoteBaselineImageCache",
			"writeRemoteBaselineState",
			"promoteRemoteBaselineState",
			"renewRemoteBaselineState",
		} {
			if remoteCIFunctionExists(parsed, legacy) {
				t.Errorf("%s retains forbidden legacy refresh writer %s", relativeRemoteCIContractPath(t, root, file), legacy)
			}
		}
	}
}

// TestRemoteCIProductionHasOneAuthorityWriter rejects filesystem JSON/SQLite
// authorities. SQL table spelling is not itself a second authority: the
// cicontract domains are enforced by the schema and promotion-path guards.
func TestRemoteCIProductionHasOneAuthorityWriter(t *testing.T) {
	root := findRepoRoot(t)
	for _, file := range remoteCIProductionFiles(t, root) {
		parsed := parseRemoteCIContractGuardFile(t, file)
		relative := relativeRemoteCIContractPath(t, root, file)
		for _, violation := range remoteCIAuthorityWriterViolations(parsed) {
			t.Errorf("%s retains second filesystem JSON/SQLite authority writer %s", relative, violation)
		}
	}
}

// TestRemoteCIExecutionUsesFrozenWorkloadLPTShards prevents the normal remote
// execution path from returning to static gate groups or a fixed shard count.
func TestRemoteCIExecutionUsesFrozenWorkloadLPTShards(t *testing.T) {
	root := findRepoRoot(t)
	coordinator := parseRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "remoteci", "coordinator.go"))
	if !remoteCIFunctionCalls(coordinator, "buildRemoteExecutionShardSetForWorkloads", "BuildContainerShardSetFromWorkloadPlan") {
		t.Fatal("remote workload execution must project the frozen workload LPT plan")
	}
	for _, forbidden := range []string{"BuildContainerShardSet", "BuildContainerShardSetWithCount", "legacyCanonicalContainerShardGroups"} {
		if remoteCIFunctionContainsIdentifier(coordinator, "buildRemoteExecutionShardSetForWorkloads", forbidden) {
			t.Errorf("remote workload execution retains forbidden static shard builder %s", forbidden)
		}
	}
}

// TestRemoteCIExecutorGoBuildCacheSeedsRequireGenerations rejects the retired
// fixed seed directory before it can re-enter the remote executor path.
func TestRemoteCIExecutorGoBuildCacheSeedsRequireGenerations(t *testing.T) {
	root := findRepoRoot(t)
	paths := map[string]string{
		"executor_workspace.go": filepath.Join(root, "internal", "devtools", "gate", "executor_workspace.go"),
		"executor_plan.go":      filepath.Join(root, "internal", "devtools", "gate", "executor_plan.go"),
		"executor.go":           filepath.Join(root, "internal", "devtools", "gate", "executor.go"),
	}
	workspace := readRemoteCIContractGuardFile(t, paths["executor_workspace.go"])
	plan := readRemoteCIContractGuardFile(t, paths["executor_plan.go"])
	for name, path := range paths {
		identifiers := remoteCIForbiddenIdentifiers(parseRemoteCIContractGuardFile(t, path))
		for _, forbidden := range []string{"ExecutorGoBuildCacheSeedRoot", "legacySeedRoot"} {
			if identifiers[forbidden] {
				t.Errorf("%s retains retired Go build-cache seed identifier %q", name, forbidden)
			}
		}
		if strings.Contains(readRemoteCIContractGuardFile(t, path), "cache-seed/go-build") {
			t.Errorf("%s retains retired Go build-cache seed path", name)
		}
	}
	if !strings.Contains(workspace, "Go build cache seed generations root must be a real directory") {
		t.Fatal("Go build-cache seed discovery must reject a missing or invalid generation root")
	}
	if !strings.Contains(plan, "ExecutorGoBuildCacheSeedsRoot") {
		t.Fatal("executor plan must use the generation-scoped Go build-cache seeds root")
	}
}

// TestGenerationOneProvisionReceiptFieldGuard binds the provision-only check
// catalogue, receipt JSON field, and command importer to one strict boundary.
func TestGenerationOneProvisionReceiptFieldGuard(t *testing.T) {
	root := findRepoRoot(t)
	receiptPath := filepath.Join(root, "internal", "devtools", "cicontract", "generation_one.go")
	contractPath := filepath.Join(root, "internal", "devtools", "cicontract", "contract.go")
	importerPath := filepath.Join(root, "cmd", "super-dolphin-gate", "remote_provision_generation_one.go")
	if !remoteCITypeHasJSONField(parseRemoteCIContractGuardFile(t, receiptPath), "GenerationOneProvisionReceipt", "ProvisionChecks", "provision_checks") {
		t.Fatal("generation-one provision receipt must declare ProvisionChecks with JSON field provision_checks")
	}
	contractSource := readRemoteCIContractGuardFile(t, contractPath)
	for _, required := range []string{"type ProvisionCheck string", "type ProvisionCheckObservation struct"} {
		if !strings.Contains(contractSource, required) {
			t.Fatalf("generation-one provision content contract is missing %q", required)
		}
	}
	if strings.Contains(contractSource, "RefreshCheck") {
		t.Fatal("generation-one provision content contract must not retain refresh-named checks")
	}
	importer := parseRemoteCIContractGuardFile(t, importerPath)
	if !remoteCIFunctionHasSelector(importer, "loadRemoteGenerationOneProvisionInput", "cicontract", "ValidateGenerationOneProvisionChecks") {
		t.Fatal("generation-one provision importer must validate provision checks")
	}
	missingField := remoteCIParseGuardFixture(t, "package fixture\ntype GenerationOneProvisionReceipt struct { ReceiptSHA256 string `json:\"receipt_sha256\"` }\n")
	if remoteCITypeHasJSONField(missingField, "GenerationOneProvisionReceipt", "ProvisionChecks", "provision_checks") {
		t.Fatal("generation-one provision receipt field guard accepted a fixture without provision_checks")
	}
}

func remoteCITypeHasJSONField(file *ast.File, typeName, fieldName, jsonName string) bool {
	structure := remoteCIStructTypeByName(file, typeName)
	if structure == nil {
		return false
	}
	return slices.ContainsFunc(structure.Fields.List, func(field *ast.Field) bool {
		return len(field.Names) == 1 && field.Names[0].Name == fieldName && field.Tag != nil && strings.Contains(field.Tag.Value, `json:"`+jsonName+`"`)
	})
}

func remoteCIStructTypeByName(file *ast.File, typeName string) *ast.StructType {
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok.String() != "type" {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if ok && typeSpec.Name.Name == typeName {
				structure, _ := typeSpec.Type.(*ast.StructType)
				return structure
			}
		}
	}
	return nil
}
