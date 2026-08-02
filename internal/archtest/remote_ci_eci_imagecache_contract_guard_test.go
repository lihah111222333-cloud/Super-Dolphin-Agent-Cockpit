package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
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

// TestRemoteCIContractHasOneCodeOwner binds the accepted document to one Go
// owner without copying its field catalogue into archtest. Production code may
// consume cicontract's API, but may not redeclare its platform, timing, or
// refresh-state protocol locally.
func TestRemoteCIContractHasOneCodeOwner(t *testing.T) {
	root := findRepoRoot(t)
	if err := cicontract.Validate(); err != nil {
		t.Fatalf("validate remote CI code contract: %v", err)
	}
	contract := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(cicontract.DocumentPath)))
	if !strings.Contains(contract, "internal/devtools/cicontract") {
		t.Error("accepted remote CI contract must name internal/devtools/cicontract as its code owner")
	}
	for _, identity := range []string{cicontract.ID, cicontract.ExecutionPathID, cicontract.RefreshPathID, cicontract.SQLAuthorityID} {
		if !strings.Contains(contract, identity) {
			t.Errorf("accepted remote CI document is missing code contract identity %q", identity)
		}
	}
	if got := remoteCICanonicalContractBlock(t, contract); got != cicontract.CanonicalMarkdown() {
		t.Error("accepted remote CI document and internal/devtools/cicontract are not 1:1")
	}
	ownerDirectory := filepath.Join(root, "internal", "devtools", "cicontract")
	entries, err := os.ReadDir(ownerDirectory)
	if err != nil {
		t.Fatalf("remote CI contract code owner is unavailable: %v", err)
	}
	hasProductionSource, hasTest := false, false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		if strings.HasSuffix(entry.Name(), "_test.go") {
			hasTest = true
			continue
		}
		hasProductionSource = true
	}
	if !hasProductionSource || !hasTest {
		t.Fatalf("remote CI contract owner must provide production API and focused tests: source=%t test=%t", hasProductionSource, hasTest)
	}

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
	schema := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "gate", "ledger_store_sqlite_schema.go"))
	for _, binding := range cicontract.SQLAuthorityBindings() {
		statement := "CREATE TABLE IF NOT EXISTS " + binding.Table
		if !strings.Contains(schema, statement) {
			t.Errorf("gate SQLite schema does not create cicontract authority table %q for domain %q", binding.Table, binding.Domain)
		}
	}
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
		t.Fatalf("remote baseline generic CAS production calls = %d, want 0; only lease promotion may write accepted state", compareAndSwapCalls)
	}
	if promotionCalls != 1 {
		t.Fatalf("remote baseline lease promotion calls = %d, want exactly 1", promotionCalls)
	}
}

// TestRemoteCIBaselineStateMutatorsHaveOnlyTheLeasePromotionPath rejects
// generic accepted-state writers and retired missing-state/generation helpers.
// Test fixtures may seed SQLite directly, but production must neither expose nor
// call another accepted-state mutation path.
func TestRemoteCIBaselineStateMutatorsHaveOnlyTheLeasePromotionPath(t *testing.T) {
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
// remote-CI Go sources. It rejects retired execution/cache paths but explicitly
// allows SourceSnapshotDelta, the accepted uncompressed delta protocol.
func TestRemoteCIProductionPathRejectsLegacyExecutionPaths(t *testing.T) {
	root := findRepoRoot(t)
	for _, file := range remoteCIProductionFiles(t, root) {
		parsed := parseRemoteCIContractGuardFile(t, file)
		for _, violation := range remoteCIConcurrencyCapViolations(parsed) {
			t.Errorf("%s retains forbidden remote CI concurrency cap %s", relativeRemoteCIContractPath(t, root, file), violation)
		}
		for _, violation := range remoteCIWorkloadPassReuseViolations(parsed) {
			t.Errorf("%s retains forbidden workload PASS result-cache/reused-return identifier %s", relativeRemoteCIContractPath(t, root, file), violation)
		}
	}

	for _, file := range remoteCIRefreshProductionFiles(t, root) {
		parsed := parseRemoteCIContractGuardFile(t, file)
		for _, violation := range remoteCIRefreshLegacyViolations(parsed) {
			t.Errorf("%s retains forbidden refresh implementation %s", relativeRemoteCIContractPath(t, root, file), violation)
		}
	}
}

// TestRemoteCIWorkloadProjectionAcceptsOnlyFreshResults prevents a result-cache
// map from returning to the final workload coverage boundary.
func TestRemoteCIWorkloadProjectionAcceptsOnlyFreshResults(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, "internal", "devtools", "remoteci", "workload_projection.go")
	parsed := parseRemoteCIContractGuardFile(t, path)
	if remoteCIFunctionExists(parsed, "mergeRemoteWorkloadExecutions") {
		t.Fatal("workload projection must not retain mergeRemoteWorkloadExecutions")
	}
	function := remoteCIFunctionByName(parsed, "collectFreshRemoteWorkloadExecutions")
	if function == nil || function.Type.Params == nil || len(function.Type.Params.List) != 2 {
		t.Fatal("workload projection must expose the two-argument fresh-only collector")
	}
	for _, parameter := range function.Type.Params.List {
		for _, name := range parameter.Names {
			if name.Name == "cached" {
				t.Fatal("fresh-only workload collector must not accept a cached result map")
			}
		}
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
}
func TestRemoteCILegacyGuardCounterexamples(t *testing.T) {
	safe := remoteCIParseGuardFixture(t, `package fixture
func calibration(passedWorkloads map[string]struct{}) {
	_ = passedWorkloads
}
func rejectLegacyWorkloadReuseSchema() {}
func source(TargetSourceClosure string) {
	_ = TargetSourceClosure
}
func freshWorkerCopy(BootstrapVolume string) {
	_ = BootstrapVolume
}`)
	if got := remoteCIWorkloadPassReuseViolations(safe); len(got) != 0 {
		t.Fatalf("current-run calibration PASS evidence or strict-reject schema name was mistaken for result reuse: %v", got)
	}
	if got := remoteCIRefreshLegacyViolations(safe); len(got) != 0 {
		t.Fatalf("target closure or fresh worker copy was mistaken for a legacy refresh path: %v", got)
	}

	reused := remoteCIParseGuardFixture(t, `package fixture
func run() {
	_ = WorkloadPassCache{}
	_ = ReusedWorkloadResult{}
}`)
	if got := remoteCIWorkloadPassReuseViolations(reused); len(got) == 0 {
		t.Fatal("workload PASS result cache/reuse fixture must be rejected")
	}
	legacyRefresh := remoteCIParseGuardFixture(t, `package fixture
func refresh() {
	_ = FullWorkspaceTar{}
	_ = FullContextBootstrap{}
}`)
	if got := remoteCIRefreshLegacyViolations(legacyRefresh); len(got) == 0 {
		t.Fatal("full workspace tar/bootstrap fixture must be rejected")
	}
}

// TestRemoteCIIncrementalRefreshAndRequiredChecksUseContractOwner prevents a
// second protocol vocabulary from restoring a full context fallback or omitting
// a required accepted-snapshot/delta validation at the production call site.
func TestRemoteCIIncrementalRefreshAndRequiredChecksUseContractOwner(t *testing.T) {
	root := findRepoRoot(t)
	requireContractCall := func(relative, validator string) {
		path := filepath.Join(root, filepath.FromSlash(relative))
		parsed := parseRemoteCIContractGuardFile(t, path)
		if !remoteCIImportsContractOwner(parsed) || !remoteCIHasContractCall(parsed, validator) {
			t.Errorf("%s must call cicontract.%s on its production path", relative, validator)
		}
	}
	requireContractCall("cmd/super-dolphin-gate/remote_refresh.go", "ValidateIncrementalRefreshTransfer")
	requireContractCall("cmd/super-dolphin-gate/remote_refresh_oci_builder.go", "ValidateIncrementalRefreshTransfer")
	requireContractCall("cmd/super-dolphin-gate/remote_oci_baseline_worker.go", "ValidateDeltaRebuild")
	requireContractCall("cmd/super-dolphin-gate/remote_run.go", "ValidateRequiredChecksObservedPass")
	requireContractCall("cmd/super-dolphin-gate/remote_run.go", "ValidateTimingWarningAction")
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

// TestRemoteCISourceSnapshotAndRefreshLogConsumersUseCanonicalContractValues
// keeps the accepted snapshot layout and refresh-only log vocabulary on the
// executable ECI path. Merely importing cicontract is insufficient: the
// worker must pass the canonical root to the delta applier, while the
// coordinator must mount and copy the same contract-owned layout.
func TestRemoteCISourceSnapshotAndRefreshLogConsumersUseCanonicalContractValues(t *testing.T) {
	root := findRepoRoot(t)
	worker := parseRemoteCIContractGuardFile(t, filepath.Join(root, "cmd", "super-dolphin-gate", "remote_oci_baseline_worker.go"))
	if !remoteCIImportsContractOwner(worker) ||
		!remoteCIFunctionCallHasSelectorArgument(worker, "executeRemoteOCIBuildKit", "cicontract", "ValidateSourceSnapshotLayout", 0, "cicontract", "SourceSnapshotRootPath") ||
		!remoteCIFunctionCallHasSelectorArgument(worker, "executeRemoteOCIBuildKit", "cicontract", "ValidateSourceSnapshotLayout", 1, "cicontract", "SourceSnapshotManifestPath") ||
		!remoteCIFunctionCallHasSelectorArgument(worker, "executeRemoteOCIBuildKit", "remoteci", "ApplySourceSnapshotDelta", 0, "cicontract", "SourceSnapshotRootPath") ||
		!remoteCIFunctionHasSelector(worker, "executeRemoteOCIBuildKit", "cicontract", "SourceSnapshotManifestPath") {
		t.Error("remote OCI worker must validate and apply the cicontract source snapshot root and manifest on its production path")
	}

	coordinator := parseRemoteCIContractGuardFile(t, filepath.Join(root, "cmd", "super-dolphin-gate", "remote_refresh_oci_coordinator.go"))
	if !remoteCIImportsContractOwner(coordinator) ||
		!remoteCIFunctionCalls(coordinator, "remoteOCIBuildCreateRequest", "remoteOCIInitScript") ||
		!remoteCIFunctionHasSelectorCall(coordinator, "remoteOCIBuildCreateRequest", "filepath", "Dir", "cicontract", "SourceSnapshotRootPath") ||
		!remoteCIFunctionCallHasSelectorArgument(coordinator, "remoteOCIInitScript", "fmt", "Sprintf", 1, "cicontract", "SourceSnapshotRootPath") ||
		!remoteCIFunctionCallHasSelectorArgument(coordinator, "remoteOCIInitScript", "fmt", "Sprintf", 2, "cicontract", "SourceSnapshotManifestPath") {
		t.Error("remote OCI ECI init/main mount and copy path must consume cicontract's canonical source snapshot layout")
	}

	closure := parseRemoteCIContractGuardFile(t, filepath.Join(root, "build", "gate", "closure", "closure.go"))
	if !remoteCIImportsContractOwner(closure) || !remoteCIFunctionHasSelector(closure, "renderDockerfile", "cicontract", "RefreshCheckLogPrefix") {
		t.Error("refresh receipt Dockerfile must consume cicontract.RefreshCheckLogPrefix rather than a private log vocabulary")
	}
}

// TestRemoteCISourceSnapshotConsumerGuardCounterexamples proves the guard is
// fail-first: a lookalike path, a disconnected mount, or a private refresh
// receipt marker cannot satisfy the structural consumer checks.
func TestRemoteCISourceSnapshotConsumerGuardCounterexamples(t *testing.T) {
	wrongDeltaRoot := remoteCIParseGuardFixture(t, `package fixture
func executeRemoteOCIBuildKit() {
	remoteci.ApplySourceSnapshotDelta("/tmp/source-snapshot/root", output, accepted, delta)
}`)
	if remoteCIFunctionCallHasSelectorArgument(wrongDeltaRoot, "executeRemoteOCIBuildKit", "remoteci", "ApplySourceSnapshotDelta", 0, "cicontract", "SourceSnapshotRootPath") {
		t.Fatal("lookalike source snapshot root must not satisfy canonical delta-apply guard")
	}

	wrongMount := remoteCIParseGuardFixture(t, `package fixture
func remoteOCIBuildCreateRequest() {
	_ = filepath.Dir("/tmp/source-snapshot/root")
}`)
	if remoteCIFunctionHasSelectorCall(wrongMount, "remoteOCIBuildCreateRequest", "filepath", "Dir", "cicontract", "SourceSnapshotRootPath") {
		t.Fatal("lookalike source snapshot mount must not satisfy canonical ECI mount guard")
	}

	privatePrefix := remoteCIParseGuardFixture(t, `package fixture
func renderDockerfile() {
	_ = "REMOTE_CI_CHECK_PASS="
}`)
	if remoteCIFunctionHasSelector(privatePrefix, "renderDockerfile", "cicontract", "RefreshCheckLogPrefix") {
		t.Fatal("private refresh receipt prefix must not satisfy cicontract log-prefix guard")
	}
}

func TestRemoteCIProductionImageInputsUseCanonicalNonACRHostGuard(t *testing.T) {
	root := findRepoRoot(t)
	for _, relative := range []string{
		"internal/devtools/remoteci/baseline_state.go",
		"internal/devtools/remoteci/oci_baseline_builder_protocol.go",
		"internal/devtools/remoteci/oci_build_context.go",
		"internal/devtools/alicloud/eci/client_validation.go",
		"internal/devtools/alicloud/eci/image_cache.go",
	} {
		contents := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(relative)))
		if !strings.Contains(contents, "cicontract.ValidateNonACRRegistryHost") {
			t.Errorf("%s must use cicontract.ValidateNonACRRegistryHost for production image inputs", relative)
		}
	}
}

// TestRemoteCIBaselineBuildConsumesSingleGoDistributionLock keeps the remote
// baseline image on the only Go distribution lock and rejects archive fetching.
func TestRemoteCIBaselineBuildConsumesSingleGoDistributionLock(t *testing.T) {
	root := findRepoRoot(t)
	lockPath := filepath.Join(root, "internal", "devtools", "remoteci", "oci_toolchain_lock.go")
	lockConsumer := parseRemoteCIContractGuardFile(t, lockPath)
	if !strings.Contains(readRemoteCIContractGuardFile(t, lockPath), "internal/devtools/godistribution") {
		t.Fatal("remote OCI toolchain lock must import the single Go distribution owner")
	}
	if !remoteCIFunctionCalls(lockConsumer, "validateRemoteGoDistributionLock", "ValidateRemoteCIAsset") || !remoteCIFunctionCalls(lockConsumer, "validateToolchainVersions", "validateRemoteGoDistributionLock") {
		t.Error("remote OCI baseline toolchain validation must consume ValidateRemoteCIAsset")
	}
}

// TestRemoteCINormalConfigDoesNotDependOnRefreshPublication keeps background
// refresh failure isolated from an accepted normal run while retaining strict
// validation at the detached refresh boundary.
func TestRemoteCINormalConfigDoesNotDependOnRefreshPublication(t *testing.T) {
	root := findRepoRoot(t)
	configFile := parseRemoteCIContractGuardFile(t, filepath.Join(root, "cmd", "super-dolphin-gate", "remote_run_config.go"))
	if remoteCIFunctionCalls(configFile, "Validate", "ValidateOCIRefresh") {
		t.Fatal("normal remote CI config must not depend on refresh publication config")
	}
	if !remoteCIFunctionCalls(configFile, "ValidateOCIRefresh", "ValidateOCIOutputRepository") ||
		!remoteCIFunctionCalls(configFile, "ValidateOCIRefresh", "ValidateOCIRefreshImage") {
		t.Fatal("refresh-only config validation must retain both generic OCI publication guards")
	}
	optionsFile := parseRemoteCIContractGuardFile(t, filepath.Join(root, "cmd", "super-dolphin-gate", "remote_run_options.go"))
	if !remoteCIFunctionCalls(optionsFile, "loadRemoteRefreshConfig", "ValidateOCIRefresh") {
		t.Fatal("detached refresh config must validate the OCI publication boundary")
	}
	refreshFile := parseRemoteCIContractGuardFile(t, filepath.Join(root, "cmd", "super-dolphin-gate", "remote_refresh.go"))
	if !remoteCIFunctionCalls(refreshFile, "runClaimedRemoteBaselineRefresh", "loadRemoteRefreshConfig") {
		t.Fatal("claimed refresh must load the strict refresh-only config before build or ECI work")
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
		for _, forbidden := range []string{"max_shards", "max-shards", "max_concurrency", "max-concurrency"} {
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

// TestRemoteCIImageCacheCreationHasOneRefreshChain requires the sole automatic
// cache-reuse option to remain inside CreateImageCache and a refresh caller to
// create the successor explicitly.
func TestRemoteCIImageCacheCreationHasOneRefreshChain(t *testing.T) {
	root := findRepoRoot(t)
	clientPath := filepath.Join(root, "internal", "devtools", "alicloud", "eci", "image_cache.go")
	client := parseRemoteCIContractGuardFile(t, clientPath)
	functions := remoteCIFunctionNamesContainingString(client, "--AutoMatchImageCache")
	if len(functions) != 1 || functions[0] != "CreateImageCache" {
		t.Fatalf("AutoMatchImageCache functions = %v, want [CreateImageCache]", functions)
	}

	createCalls := 0
	for _, file := range remoteCIRefreshProductionFiles(t, root) {
		parsed := parseRemoteCIContractGuardFile(t, file)
		if remoteCIFunctionCalls(parsed, "runClaimedRemoteBaselineRefresh", "CreateImageCache") {
			createCalls++
		}
		if remoteCIFunctionContainsString(parsed, "", "--AutoMatchImageCache") {
			t.Errorf("%s selects ImageCache automatically outside CreateImageCache", relativeRemoteCIContractPath(t, root, file))
		}
	}
	if createCalls != 1 {
		t.Fatalf("refresh CreateImageCache main chain count = %d, want 1", createCalls)
	}
}

// TestRemoteCIBaselineRefreshHasOneProductionEntry prevents a second command
// from bypassing the SQLite lease-backed baseline-refresh implementation.
func TestRemoteCIBaselineRefreshHasOneProductionEntry(t *testing.T) {
	root := findRepoRoot(t)
	main := parseRemoteCIContractGuardFile(t, filepath.Join(root, "cmd", "super-dolphin-gate", "main.go"))
	if calls := remoteCIFunctionCallCount(main, "runRemoteBaselineRefresh"); calls != 1 {
		t.Fatalf("baseline-refresh production entry calls = %d, want exactly 1", calls)
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

func remoteCIProductionFiles(t *testing.T, root string) []string {
	t.Helper()
	return remoteCICollectProductionFiles(t, root, []string{
		"cmd/super-dolphin-gate",
		"internal/devtools/remoteci",
		"internal/devtools/alicloud/eci",
		"internal/devtools/gate",
	}, func(relative string) bool {
		base := filepath.Base(relative)
		return !strings.HasPrefix(relative, "cmd/") || strings.HasPrefix(base, "remote_")
	})
}

func remoteCIRefreshProductionFiles(t *testing.T, root string) []string {
	t.Helper()
	return remoteCICollectProductionFiles(t, root, []string{"cmd/super-dolphin-gate", "internal/devtools/gate", "internal/devtools/remoteci"}, func(relative string) bool {
		return strings.Contains(filepath.Base(relative), "remote_refresh") || strings.Contains(filepath.Base(relative), "baseline_refresh")
	})
}

func remoteCIContractConsumerFiles(t *testing.T, root string) []string {
	t.Helper()
	return remoteCICollectProductionFiles(t, root, []string{
		"cmd/super-dolphin-gate",
		"internal/devtools/gate",
		"internal/devtools/remoteci",
	}, func(relative string) bool {
		base := filepath.Base(relative)
		return !strings.HasPrefix(relative, "cmd/") || strings.HasPrefix(base, "remote_")
	})
}

func remoteCICollectProductionFiles(t *testing.T, root string, directories []string, include func(string) bool) []string {
	t.Helper()
	var files []string
	for _, directory := range directories {
		absolute := filepath.Join(root, filepath.FromSlash(directory))
		if err := filepath.WalkDir(absolute, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			relative := relativeRemoteCIContractPath(t, root, path)
			if include(relative) {
				files = append(files, path)
			}
			return nil
		}); err != nil {
			t.Fatalf("walk remote CI production directory %s: %v", directory, err)
		}
	}
	return files
}

func parseRemoteCIContractGuardFile(t *testing.T, path string) *ast.File {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return parsed
}

func remoteCIForbiddenIdentifiers(file *ast.File) map[string]bool {
	found := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok {
			found[identifier.Name] = true
		}
		return true
	})
	return found
}

func remoteCIStringLiterals(file *ast.File) []string {
	var values []string
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if ok && literal.Kind == token.STRING {
			values = append(values, strings.Trim(literal.Value, "\"`"))
		}
		return true
	})
	return values
}

func remoteCIImportsContractOwner(file *ast.File) bool {
	for _, importSpec := range file.Imports {
		if strings.Trim(importSpec.Path.Value, "\"") == "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract" {
			return true
		}
	}
	return false
}

func remoteCIRepeatsContractValue(file *ast.File) bool {
	for _, literal := range remoteCIStringLiterals(file) {
		switch literal {
		case "linux/amd64", "claimed", "building", "cache_preparing", "ready_validated", "promoted", "retiring", "failed":
			return true
		}
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		if remoteCIContractDuration(node) {
			found = true
			return false
		}
		return true
	})
	return found
}

func remoteCIContractDuration(node ast.Node) bool {
	expression, ok := node.(*ast.BinaryExpr)
	if !ok || expression.Op != token.MUL {
		return false
	}
	literal, ok := expression.X.(*ast.BasicLit)
	if !ok || literal.Kind != token.INT || (literal.Value != "2" && literal.Value != "100") {
		return false
	}
	selector, ok := expression.Y.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == "time" && ((literal.Value == "2" && selector.Sel.Name == "Hour") || (literal.Value == "100" && selector.Sel.Name == "Second"))
}

func remoteCIUsesFilesystemStateStore(file *ast.File) bool {
	used := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !remoteCIFileStoreCall(call) {
			return true
		}
		used = true
		return false
	})
	return used
}

func remoteCIFileStoreCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || packageName.Name != "os" {
		return false
	}
	switch selector.Sel.Name {
	case "ReadFile", "WriteFile", "Open", "OpenFile", "Create":
		return true
	default:
		return false
	}
}

func isECICreateRequest(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "CreateRequest" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == "eci"
}

func remoteCICreateRequestHasSnapshot(literal *ast.CompositeLit) bool {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		if ok && key.Name == "ImageCacheSnapshotID" {
			return true
		}
	}
	return false
}

func remoteCIFunctionContainsString(file *ast.File, functionName, want string) bool {
	return len(remoteCIFunctionNamesContainingString(file, want, functionName)) != 0
}

func remoteCIFunctionNamesContainingString(file *ast.File, want string, names ...string) []string {
	functionName := ""
	if len(names) != 0 {
		functionName = names[0]
	}
	var found []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || (functionName != "" && function.Name.Name != functionName) {
			continue
		}
		if slices.Contains(remoteCIStringLiterals(&ast.File{Decls: []ast.Decl{function}}), want) {
			found = append(found, function.Name.Name)
		}
	}
	return found
}

func remoteCIFunctionCalls(file *ast.File, functionName, calledName string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != functionName {
			continue
		}
		return remoteCIFunctionCallCount(&ast.File{Decls: []ast.Decl{function}}, calledName) != 0
	}
	return false
}

func remoteCIFunctionHasSelector(file *ast.File, functionName, packageName, selectorName string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != functionName {
			continue
		}
		found := false
		ast.Inspect(function, func(node ast.Node) bool {
			expression, ok := node.(*ast.SelectorExpr)
			if ok && remoteCISelectorMatches(expression, packageName, selectorName) {
				found = true
				return false
			}
			return true
		})
		return found
	}
	return false
}

func remoteCIFunctionCallHasSelectorArgument(file *ast.File, functionName, calledPackage, calledName string, argumentIndex int, argumentPackage, argumentName string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != functionName {
			continue
		}
		found := false
		ast.Inspect(function, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || argumentIndex >= len(call.Args) {
				return true
			}
			callee, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !remoteCISelectorMatches(callee, calledPackage, calledName) {
				return true
			}
			argument, ok := call.Args[argumentIndex].(*ast.SelectorExpr)
			if ok && remoteCISelectorMatches(argument, argumentPackage, argumentName) {
				found = true
				return false
			}
			return true
		})
		return found
	}
	return false
}

func remoteCIFunctionHasSelectorCall(file *ast.File, functionName, calledPackage, calledName, argumentPackage, argumentName string) bool {
	return remoteCIFunctionCallHasSelectorArgument(file, functionName, calledPackage, calledName, 0, argumentPackage, argumentName)
}

func remoteCISelectorMatches(expression *ast.SelectorExpr, packageName, selectorName string) bool {
	identifier, ok := expression.X.(*ast.Ident)
	return ok && identifier.Name == packageName && expression.Sel.Name == selectorName
}

func remoteCIFunctionExists(file *ast.File, want string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == want {
			return true
		}
	}
	return false
}

func remoteCIFunctionByName(file *ast.File, want string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == want {
			return function
		}
	}
	return nil
}

func remoteCIFunctionCallCount(file *ast.File, calledName string) int {
	count := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if identifier, ok := call.Fun.(*ast.Ident); ok && identifier.Name == calledName {
			count++
			return true
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == calledName {
			count++
		}
		return true
	})
	return count
}

func readRemoteCIContractGuardFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func relativeRemoteCIContractPath(t *testing.T, root, path string) string {
	t.Helper()
	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(relative)
}

func remoteCIHasContractCall(file *ast.File, want string) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != want {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		found = ok && packageName.Name == "cicontract"
		return !found
	})
	return found
}

func remoteCIRefreshLegacyViolations(file *ast.File) []string {
	violations := map[string]bool{}
	for identifier := range remoteCIForbiddenIdentifiers(file) {
		normalized := strings.ToLower(identifier)
		if strings.Contains(normalized, "sourcesnapshotdelta") {
			continue
		}
		for _, forbidden := range []string{"datacache", "anchor", "zstd", "gzip", "docker", "buildx", "fallback"} {
			if strings.Contains(normalized, forbidden) {
				violations["identifier "+identifier] = true
			}
		}
		if identifier == "Delta" || strings.Contains(normalized, "legacydelta") || strings.Contains(normalized, "directcachedelta") || remoteCIFullContextArchiveName(normalized) || remoteCIFullContextBootstrapName(normalized) {
			violations["identifier "+identifier] = true
		}
	}
	for _, literal := range remoteCIStringLiterals(file) {
		normalized := strings.ToLower(literal)
		for _, forbidden := range []string{"datacache", "anchor", "direct cache", "zstd", "gzip", "docker_host", "docker", "buildx", "fallback", "acr."} {
			if strings.Contains(normalized, forbidden) {
				violations["literal "+literal] = true
			}
		}
		if remoteCIFullContextArchiveName(normalized) || remoteCIFullContextBootstrapName(normalized) {
			violations["literal "+literal] = true
		}
	}
	return remoteCIViolationList(violations)
}

func remoteCIFullContextArchiveName(normalized string) bool {
	hasFullContext := strings.Contains(normalized, "context") || strings.Contains(normalized, "workspace") || strings.Contains(normalized, "closure")
	return hasFullContext && (strings.Contains(normalized, ".tar") || strings.Contains(normalized, "tarball") || strings.Contains(normalized, "tararchive") || strings.HasSuffix(normalized, "tar"))
}

func remoteCIFullContextBootstrapName(normalized string) bool {
	return strings.Contains(normalized, "bootstrap") && (strings.Contains(normalized, "full") || strings.Contains(normalized, "context") || strings.Contains(normalized, "workspace") || strings.Contains(normalized, "closure"))
}

func remoteCIConcurrencyCapViolations(file *ast.File) []string {
	violations := map[string]bool{}
	for identifier := range remoteCIForbiddenIdentifiers(file) {
		if identifier == "SetLimit" || remoteCIAdmissionCapIdentifier(identifier) {
			violations["identifier "+identifier] = true
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
			if packageName, ok := selector.X.(*ast.Ident); ok && packageName.Name == "semaphore" && selector.Sel.Name == "NewWeighted" {
				violations["call semaphore.NewWeighted"] = true
			}
		}
		if identifier, ok := call.Fun.(*ast.Ident); ok && identifier.Name == "make" && len(call.Args) == 2 {
			if channel, ok := call.Args[0].(*ast.ChanType); ok && remoteCIAdmissionChannelType(channel) {
				violations["buffered admission channel type "+remoteCIExpressionName(channel.Value)] = true
			}
		}
		return true
	})
	return remoteCIViolationList(violations)
}

func remoteCIAdmissionCapIdentifier(identifier string) bool {
	normalized := strings.ToLower(identifier)
	for _, marker := range []string{"maxshard", "maxconcurrency", "shardbatch", "batchlimit", "coordinatorlimit", "admissioncap", "admissionlimit"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func remoteCIAdmissionChannelType(channel *ast.ChanType) bool {
	_, ok := channel.Value.(*ast.StructType)
	return ok
}

func remoteCIExpressionName(expression ast.Expr) string {
	if _, ok := expression.(*ast.StructType); ok {
		return "struct{}"
	}
	if identifier, ok := expression.(*ast.Ident); ok {
		return identifier.Name
	}
	return "unknown"
}

func remoteCIParseGuardFixture(t *testing.T, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "remote_ci_guard_fixture.go", source, 0)
	if err != nil {
		t.Fatalf("parse remote CI guard fixture: %v", err)
	}
	return file
}

func remoteCIWorkloadPassReuseViolations(file *ast.File) []string {
	violations := map[string]bool{}
	for identifier := range remoteCIForbiddenIdentifiers(file) {
		normalized := strings.ToLower(identifier)
		if remoteCIStrictRejectLegacyReuseSchemaName(normalized) {
			continue
		}
		if strings.Contains(normalized, "workload") && ((strings.Contains(normalized, "pass") && strings.Contains(normalized, "cache")) || strings.Contains(normalized, "reuse")) {
			violations["identifier "+identifier] = true
		}
	}
	return remoteCIViolationList(violations)
}

func remoteCIStrictRejectLegacyReuseSchemaName(normalized string) bool {
	return normalized == "rejectlegacyworkloadreuseschema"
}

func remoteCIAuthorityWriterViolations(file *ast.File) []string {
	violations := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		if !ok || packageName.Name != "os" || (selector.Sel.Name != "WriteFile" && selector.Sel.Name != "Create" && selector.Sel.Name != "OpenFile") {
			return true
		}
		for _, argument := range call.Args {
			literal, ok := argument.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			path := strings.ToLower(strings.Trim(literal.Value, "\""))
			if strings.Contains(path, ".json") || strings.Contains(path, ".sqlite") || strings.Contains(path, ".db") {
				violations["os."+selector.Sel.Name+" "+path] = true
			}
		}
		return true
	})
	return remoteCIViolationList(violations)
}

func remoteCIViolationList(violations map[string]bool) []string {
	result := make([]string, 0, len(violations))
	for violation := range violations {
		result = append(result, violation)
	}
	slices.Sort(result)
	return result
}

func remoteCIHundredSecondTerminationViolations(file *ast.File) []string {
	targets := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.ValueSpec:
			for index, value := range statement.Values {
				if index < len(statement.Names) && remoteCIIsHundredSecondDuration(value) {
					targets[statement.Names[index].Name] = true
				}
			}
		case *ast.AssignStmt:
			for index, value := range statement.Rhs {
				if index < len(statement.Lhs) && remoteCIIsHundredSecondDuration(value) {
					if identifier, ok := statement.Lhs[index].(*ast.Ident); ok {
						targets[identifier.Name] = true
					}
				}
			}
		}
		return true
	})
	violations := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !remoteCITerminatingCall(call) {
			return true
		}
		for _, argument := range call.Args {
			if remoteCIIsHundredSecondDuration(argument) {
				violations[remoteCICallName(call)] = true
			}
			if identifier, ok := argument.(*ast.Ident); ok && targets[identifier.Name] {
				violations[remoteCICallName(call)+" via "+identifier.Name] = true
			}
		}
		return true
	})
	return remoteCIViolationList(violations)
}

func remoteCIIsHundredSecondDuration(expression ast.Expr) bool {
	if selector, ok := expression.(*ast.SelectorExpr); ok {
		if packageName, ok := selector.X.(*ast.Ident); ok && packageName.Name == "cicontract" && selector.Sel.Name == "ShardTargetDuration" {
			return true
		}
	}
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok || binary.Op != token.MUL {
		return false
	}
	literal, ok := binary.Y.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, ok := literal.X.(*ast.Ident)
	if !ok || packageName.Name != "time" || (literal.Sel.Name != "Second" && literal.Sel.Name != "Millisecond") {
		return false
	}
	value := remoteCIIntegerLiteral(binary.X)
	return (literal.Sel.Name == "Second" && value == 100) || (literal.Sel.Name == "Millisecond" && value == 100000)
}

func remoteCIIntegerLiteral(expression ast.Expr) int64 {
	if literal, ok := expression.(*ast.BasicLit); ok && literal.Kind == token.INT {
		var value int64
		for _, digit := range literal.Value {
			if digit < '0' || digit > '9' {
				return -1
			}
			value = value*10 + int64(digit-'0')
		}
		return value
	}
	if call, ok := expression.(*ast.CallExpr); ok && len(call.Args) == 1 {
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
			if packageName, ok := selector.X.(*ast.Ident); ok && packageName.Name == "time" && selector.Sel.Name == "Duration" {
				return remoteCIIntegerLiteral(call.Args[0])
			}
		}
	}
	return -1
}

func remoteCITerminatingCall(call *ast.CallExpr) bool {
	name := strings.ToLower(remoteCICallName(call))
	for _, terminating := range []string{"timeout", "deadline", "cancel", "kill", "fail", "cleanup"} {
		if strings.Contains(name, terminating) {
			return true
		}
	}
	return false
}

func remoteCICallName(call *ast.CallExpr) string {
	if identifier, ok := call.Fun.(*ast.Ident); ok {
		return identifier.Name
	}
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		return selector.Sel.Name
	}
	return "call"
}
