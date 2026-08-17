package remoteci

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func inventoryExpectedSelectors() ([]string, []gate.GoTestTarget, []gate.GoTestTarget) {
	packages := []string{"./build/gate", "./build/gate/closure", "./cmd/agent-runtime", "./cmd/agent-terminal", "./cmd/mcp-lsp", "./cmd/mcp-orch/store/taskdag", "./cmd/super-dolphin-updater", "./internal/alpha", "./internal/app", "./internal/archtest", "./internal/devtools/gate", "./internal/devtools/remoteci", "./internal/platform/db/sqlite", "./internal/provider/codexapp", "./new-root/tool"}
	tests := []gate.GoTestTarget{
		{Package: "./internal/archtest", Name: "TestCommon"}, {Package: "./internal/archtest", Name: "TestNormal"},
		{Package: "./internal/provider/codexapp", Name: "TestTransportCommon"}, {Package: "./internal/provider/codexapp", Name: "TestTransportNormal"},
		{Package: "./cmd/agent-runtime", Name: "TestAgentRuntimeMain"}, {Package: "./cmd/agent-runtime", Name: "TestAgentRuntimeRace"},
		{Package: "./cmd/agent-terminal", Name: "TestAgentTerminalMain"}, {Package: "./cmd/agent-terminal", Name: "TestAgentTerminalRecovery"},
		{Package: "./internal/app", Name: "TestAppModuleGraphIsClosed"}, {Package: "./internal/app", Name: "TestRecoveryRestoreAndGuardConvergeOneTokenBoundLaunch"}, {Package: "./internal/app", Name: "TestRunDesktopPreDrain"},
		{Package: "./cmd/super-dolphin-updater", Name: "TestUpdaterCandidateCleanup"}, {Package: "./cmd/super-dolphin-updater", Name: "TestUpdaterRollbackEntries"},
		{Package: "./cmd/mcp-orch/store/taskdag", Name: "TestTaskDAGStore"}, {Package: "./cmd/mcp-orch/store/taskdag", Name: "TestTaskDAGWakeup"},
		{Package: "./internal/platform/db/sqlite", Name: "TestSQLiteCommon"}, {Package: "./internal/platform/db/sqlite", Name: "TestSQLiteNormal"},
		{Package: "./internal/devtools/gate", Name: "TestGateAtomicCommon"}, {Package: "./internal/devtools/gate", Name: "TestGateAtomicNormal"},
		{Package: "./internal/devtools/remoteci", Name: "TestRemoteCIAtomicCommon"}, {Package: "./internal/devtools/remoteci", Name: "TestRemoteCIAtomicNormal"},
		{Package: "./cmd/mcp-lsp", Name: "TestMcpLSPCommon"}, {Package: "./cmd/mcp-lsp", Name: "TestMcpLSPProcess"},
	}
	raceTests := []gate.GoTestTarget{
		{Package: "./internal/archtest", Name: "TestCommon"}, {Package: "./internal/archtest", Name: "TestRace"},
		{Package: "./internal/provider/codexapp", Name: "TestTransportCommon"}, {Package: "./internal/provider/codexapp", Name: "TestTransportRace"},
		{Package: "./cmd/agent-runtime", Name: "TestAgentRuntimeMain"}, {Package: "./cmd/agent-runtime", Name: "TestAgentRuntimeRace"},
		{Package: "./cmd/agent-terminal", Name: "TestAgentTerminalMain"}, {Package: "./cmd/agent-terminal", Name: "TestAgentTerminalRecovery"},
		{Package: "./internal/app", Name: "TestAppModuleGraphIsClosed"}, {Package: "./internal/app", Name: "TestRecoveryRestoreAndGuardConvergeOneTokenBoundLaunch"}, {Package: "./internal/app", Name: "TestRunDesktopPreDrain"},
		{Package: "./cmd/super-dolphin-updater", Name: "TestUpdaterCandidateCleanup"}, {Package: "./cmd/super-dolphin-updater", Name: "TestUpdaterRollbackEntries"},
		{Package: "./cmd/mcp-orch/store/taskdag", Name: "TestTaskDAGStore"}, {Package: "./cmd/mcp-orch/store/taskdag", Name: "TestTaskDAGWakeup"},
		{Package: "./internal/platform/db/sqlite", Name: "TestSQLiteCommon"}, {Package: "./internal/platform/db/sqlite", Name: "TestSQLiteRace"},
		{Package: "./internal/devtools/gate", Name: "TestGateAtomicCommon"}, {Package: "./internal/devtools/gate", Name: "TestGateAtomicRace"},
		{Package: "./internal/devtools/remoteci", Name: "TestRemoteCIAtomicCommon"}, {Package: "./internal/devtools/remoteci", Name: "TestRemoteCIAtomicRace"},
		{Package: "./cmd/mcp-lsp", Name: "TestMcpLSPCommon"}, {Package: "./cmd/mcp-lsp", Name: "TestMcpLSPRace"},
	}
	return packages, tests, raceTests
}

func TestBuildWorkloadInventoryUsesExactCommitAndRange(t *testing.T) {
	repository := t.TempDir()
	runInventoryGit(t, repository, "init", "--quiet")
	runInventoryGit(t, repository, "config", "user.name", "CI Inventory")
	runInventoryGit(t, repository, "config", "user.email", "ci@example.invalid")
	writeInventoryFixture(t, repository)
	runInventoryGit(t, repository, "add", ".")
	runInventoryGit(t, repository, "commit", "--quiet", "-m", "基础")
	base := inventoryGitOutput(t, repository, "rev-parse", "HEAD")

	writeInventoryFile(t, repository, "frontend-app/src/widget.ts", "export const widget = 2\n")
	writeInventoryFile(t, repository, "frontend-app/scripts/chat-history-benchmark.test.mjs", "test('changed benchmark', () => {})\n")
	writeInventoryFile(t, repository, "internal/beta/beta_test.go", "package beta\n")
	runInventoryGit(t, repository, "add", ".")
	runInventoryGit(t, repository, "commit", "--quiet", "-m", "更新")
	commit := inventoryGitOutput(t, repository, "rev-parse", "HEAD")

	inventory, err := BuildWorkloadInventory(context.Background(), repository, commit, base, "linux/amd64")
	if err != nil {
		t.Fatalf("BuildWorkloadInventory() error = %v", err)
	}
	if !slices.Equal(inventory.GoPackages, []string{"./build/gate", "./build/gate/closure", "./cmd/agent-runtime", "./cmd/agent-terminal", "./cmd/mcp-lsp", "./cmd/mcp-orch/store/taskdag", "./cmd/super-dolphin-updater", "./internal/alpha", "./internal/app", "./internal/archtest", "./internal/beta", "./internal/devtools/gate", "./internal/devtools/remoteci", "./internal/platform/db/sqlite", "./internal/provider/codexapp", "./new-root/tool"}) ||
		!slices.Equal(inventory.NestedGoModules, []string{"build/gate/runtime-tools", "tools/custom-check"}) ||
		!slices.Equal(inventory.FrontendChangedTests, []string{"src/widget.test.ts"}) ||
		!slices.Equal(inventory.FrontendFullTests, []string{"scripts/runtime.test.mjs", "src/widget.test.ts"}) {
		t.Fatalf("inventory = %#v", inventory)
	}
	assertAtomicInventoryTestNames(t, inventory.GoTests, inventory.GoRaceTests)
	tree := inventoryGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	treeInventory, err := BuildWorkloadInventory(context.Background(), repository, tree, base, "linux/amd64")
	if err != nil {
		t.Fatalf("BuildWorkloadInventory(tree) error = %v", err)
	}
	if !reflect.DeepEqual(treeInventory, inventory) {
		t.Fatalf("tree inventory changed legacy behavior: tree=%#v commit=%#v", treeInventory, inventory)
	}
	assertLegacyInventoryRejectsRevisions(t, repository, base, commit)
}

// assertLegacyInventoryRejectsRevisions 验证旧入口只接受 canonical commit/tree OID。
func assertLegacyInventoryRejectsRevisions(t *testing.T, repository, base, commit string) {
	t.Helper()
	runInventoryGit(t, repository, "branch", "inventory-moving", commit)
	runInventoryGit(t, repository, "tag", "-a", "inventory-tag", "-m", "inventory tag", commit)
	tagOID := inventoryGitOutput(t, repository, "rev-parse", "inventory-tag^{tag}")
	for _, revision := range []struct {
		name  string
		value string
	}{
		{name: "HEAD", value: "HEAD"},
		{name: "branch", value: "inventory-moving"},
		{name: "tag", value: "inventory-tag"},
		{name: "ref", value: "refs/heads/inventory-moving"},
		{name: "tag-object", value: tagOID},
	} {
		t.Run(revision.name, func(t *testing.T) {
			if _, err := BuildWorkloadInventory(context.Background(), repository, revision.value, base, "linux/amd64"); err == nil {
				t.Fatalf("BuildWorkloadInventory() accepted revision %q", revision.value)
			}
		})
	}
}

func TestBuildWorkloadInventorySharesExactTreeSnapshot(t *testing.T) {
	repository := t.TempDir()
	runInventoryGit(t, repository, "init", "--quiet")
	runInventoryGit(t, repository, "config", "user.name", "CI Inventory")
	runInventoryGit(t, repository, "config", "user.email", "ci@example.invalid")
	writeInventoryFixture(t, repository)
	runInventoryGit(t, repository, "add", ".")
	runInventoryGit(t, repository, "commit", "--quiet", "-m", "基础")
	commit := inventoryGitOutput(t, repository, "rev-parse", "HEAD")
	tree := inventoryGitOutput(t, repository, "rev-parse", "HEAD^{tree}")

	assertInventorySnapshotRejectsCommit(t, repository, commit)
	inventory, inventorySnapshot, err := BuildWorkloadInventoryWithSnapshot(context.Background(), repository, tree, "", "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := inventorySnapshot.exactTree(repository, tree)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := inventorySnapshot.exactTree(repository, strings.Repeat("0", len(tree))); err == nil || got != nil {
		t.Fatal("inventory snapshot accepted a different exact tree")
	}
	if got, err := inventorySnapshot.exactTree(repository+"-drift", tree); err == nil || got != nil {
		t.Fatal("inventory snapshot accepted a different repository root")
	}
	expectedPackages, expectedTests, expectedRaceTests := inventoryExpectedSelectors()
	if !slices.Equal(inventory.GoPackages, expectedPackages) ||
		!slices.Equal(inventory.GoTests, expectedTests) ||
		!slices.Equal(inventory.GoRaceTests, expectedRaceTests) {
		t.Fatalf("inventory selectors changed: got=%#v wantPackages=%v wantTests=%v wantRaceTests=%v", inventory, expectedPackages, expectedTests, expectedRaceTests)
	}
	assertInventorySnapshotReuse(t, snapshot, expectedPackages, expectedTests, expectedRaceTests)
}

// assertInventorySnapshotRejectsCommit 验证 snapshot 入口只接受 exact tree OID。
func assertInventorySnapshotRejectsCommit(t *testing.T, repository, commit string) {
	t.Helper()
	if _, _, err := BuildWorkloadInventoryWithSnapshot(context.Background(), repository, commit, "", "linux/amd64"); err == nil {
		t.Fatal("BuildWorkloadInventoryWithSnapshot() accepted a commit object")
	}
}

func TestBuildWorkloadInventoryPrepareReusesInventorySnapshot(t *testing.T) {
	repository, base, baseTree, commit, tree := prepareInventorySnapshotReuseFixture(t)
	var listings atomic.Int32
	ctx := context.WithValue(context.Background(), remoteGitTreeListingObserverContextKey{}, func() {
		listings.Add(1)
	})
	inventory, inventorySnapshot, err := BuildWorkloadInventoryWithSnapshot(ctx, repository, tree, "", "linux/amd64")
	if err != nil {
		t.Fatalf("BuildWorkloadInventoryWithSnapshot() error = %v", err)
	}
	if got := listings.Load(); got != 1 {
		t.Fatalf("tree listings after inventory = %d, want 1", got)
	}
	input := prepareInventorySnapshotRunInput(t, repository, base, baseTree, commit, tree, inventory, inventorySnapshot)
	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	prepared, err := coordinator.Prepare(ctx, input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if got := listings.Load(); got != 1 {
		t.Fatalf("tree listings after Prepare = %d, want the same single listing", got)
	}
	if prepared.input.InventorySnapshot != nil {
		t.Fatal("Prepare() retained the inventory-bound snapshot after identity derivation")
	}
	assertPrepareSubsetReleasesInventorySnapshot(t, ctx, input)
	if got := listings.Load(); got != 1 {
		t.Fatalf("tree listings after PrepareSubset = %d, want the same single listing", got)
	}
	baselineInput := input
	baselineInput.InventorySnapshot = nil
	var baselineListings atomic.Int32
	baselineContext := context.WithValue(context.Background(), remoteGitTreeListingObserverContextKey{}, func() {
		baselineListings.Add(1)
	})
	baselineCoordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	baselinePrepared, err := baselineCoordinator.Prepare(baselineContext, baselineInput)
	if err != nil {
		t.Fatalf("baseline Prepare() error = %v", err)
	}
	if got := baselineListings.Load(); got != 1 {
		t.Fatalf("baseline Prepare tree listings = %d, want 1", got)
	}
	assertPreparedInventorySnapshotIdentity(t, prepared, baselinePrepared)
}

// assertPrepareSubsetReleasesInventorySnapshot 验证 subset freeze 不保留 inventory 全树对象。
func assertPrepareSubsetReleasesInventorySnapshot(t *testing.T, ctx context.Context, input RunInput) {
	t.Helper()
	catalog := mustCoordinatorCatalog(t, input)
	selected, _ := subsetFixtureIDs(t, catalog)
	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	prepared, err := coordinator.PrepareSubset(ctx, input, RemoteSubsetRequest{Selected: selected})
	if err != nil {
		t.Fatalf("PrepareSubset() error = %v", err)
	}
	if prepared.input.InventorySnapshot != nil {
		t.Fatal("PrepareSubset() retained the inventory-bound snapshot after identity derivation")
	}
}

// prepareInventorySnapshotReuseFixture 创建含远程门禁选择器策略的精确候选树。
func prepareInventorySnapshotReuseFixture(t *testing.T) (repository, base, baseTree, commit, tree string) {
	t.Helper()
	repository, base, baseTree = prepareRemoteRunFixtureBase(t)
	writeCoordinatorFixture(t, repository, "internal/devtools/gate/common_test.go", "package gate\n\nimport \"testing\"\n\nfunc TestGateAtomicCommon(t *testing.T) {}\n")
	writeCoordinatorFixture(t, repository, "internal/devtools/gate/normal_test.go", "//go:build !race\n\npackage gate\n\nimport \"testing\"\n\nfunc TestGateAtomicNormal(t *testing.T) {}\n")
	writeCoordinatorFixture(t, repository, "internal/devtools/remoteci/common_test.go", "package remoteci\n\nimport \"testing\"\n\nfunc TestRemoteCIAtomicCommon(t *testing.T) {}\n")
	writeCoordinatorFixture(t, repository, "internal/devtools/remoteci/normal_test.go", "//go:build !race\n\npackage remoteci\n\nimport \"testing\"\n\nfunc TestRemoteCIAtomicNormal(t *testing.T) {}\n")
	writeCoordinatorFixture(t, repository, "cmd/super-dolphin-gate/common_test.go", "package main\n\nimport \"testing\"\n\nfunc TestRemoteHookConcurrentProcessesKeepInheritedTokenAndDeliveryIsolated(t *testing.T) {}\n")
	writeCoordinatorFixture(t, repository, "cmd/super-dolphin-gate/race_test.go", "//go:build race\n\npackage main\n\nimport \"testing\"\n\nfunc TestRemoteCIAgentTokenActualValueContinuesPastHandshake(t *testing.T) {}\n")
	writeCoordinatorFixture(t, repository, ".github/actionlint.yaml", "self-hosted-runner:\n")
	writeCoordinatorFixture(t, repository, ".github/workflows/ci.yml", "name: ci\n")
	writeCoordinatorFixture(t, repository, inventoryVitestSuitePolicyPath, `{
  "schemaVersion": 1,
  "defaultExcludes": [
    "**/scripts/**/*benchmark.test.*",
    "scripts/remote-preflight-carriers/*.test.mjs",
    "scripts/remote-suite-carriers/*.test.mjs"
  ]
}`)
	runCoordinatorGit(t, repository, "add", ".")
	runCoordinatorGit(t, repository, "commit", "--quiet", "-m", "inventory policy")
	return repository, base, baseTree, coordinatorGitOutput(t, repository, "rev-parse", "HEAD"), coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
}

// prepareInventorySnapshotRunInput 构造复用 snapshot 与基线 Prepare 共用的运行输入。
func prepareInventorySnapshotRunInput(t *testing.T, repository, base, baseTree, commit, tree string, inventory gate.WorkloadInventory, snapshot *WorkloadInventorySnapshot) RunInput {
	t.Helper()
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	runnerImage := "registry.example/runner@" + digest
	ledgerStore, err := gate.NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.sqlite"))
	if err != nil {
		t.Fatalf("NewDurationLedgerStore() error = %v", err)
	}
	if _, err := ledgerStore.CompareAndSwap(0, gate.DurationLedger{Version: 1}); err != nil {
		t.Fatalf("initialize duration ledger: %v", err)
	}
	seedRemoteCITestAcceptedGeneration(t, ledgerStore, 1)
	ledgerSnapshot, err := ledgerStore.Load()
	if err != nil {
		t.Fatalf("load duration ledger: %v", err)
	}
	return RunInput{
		AcceptedGeneration: 1, AgentTokenDigest: digest, RepositoryRoot: repository, Commit: commit, Tree: tree, Base: base,
		RunnerBaseCommit: base, RunnerBaseTree: baseTree,
		Source: gate.SourceSpec{
			Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
			Commit: &gate.CommitSource{SHA: commit}, SourceTreeSHA: tree,
		},
		Profile: gate.ProfileLocalFast, Entrypoint: gate.CIEntrypointManualCLI, Platform: "linux/amd64",
		PolicyDigest: digest, ToolchainDigest: digest, LedgerStore: ledgerStore, LedgerSnapshot: ledgerSnapshot,
		RunnerImage: runnerImage, ImageCacheSnapshotID: "snap-accepted-baseline", RunnerIdentityDigest: digest,
		BaselineManifestDigest: digest, RunnerConfigDigest: digest, GateBinarySHA256: digest, RuntimeSeedSHA256: digest,
		Force: true, Inventory: inventory, InventorySnapshot: snapshot,
		OCIProjectCache: &BaselineOCIProjectCache{
			Image: runnerImage, ContentManifestSHA256: digest, MainTree: baseTree,
			ToolchainDigest: digest, Platform: "linux/amd64", CachePath: OCIProjectGoBuildCachePath,
		},
	}
}

// assertPreparedInventorySnapshotIdentity 验证复用与独立加载保持完整 Prepare 身份一致。
func assertPreparedInventorySnapshotIdentity(t *testing.T, prepared, baselinePrepared *PreparedRun) {
	t.Helper()
	if !maps.Equal(prepared.input.WorkloadInputDigests, baselinePrepared.input.WorkloadInputDigests) ||
		prepared.input.WorkerExecutionSemanticDigest != baselinePrepared.input.WorkerExecutionSemanticDigest ||
		prepared.catalogDigest != baselinePrepared.catalogDigest ||
		prepared.frozenDigest != baselinePrepared.frozenDigest {
		t.Fatalf("snapshot reuse changed Prepare identity: reused=(catalog=%s frozen=%s worker=%s inputs=%d) baseline=(catalog=%s frozen=%s worker=%s inputs=%d)",
			prepared.catalogDigest, prepared.frozenDigest, prepared.input.WorkerExecutionSemanticDigest, len(prepared.input.WorkloadInputDigests),
			baselinePrepared.catalogDigest, baselinePrepared.frozenDigest, baselinePrepared.input.WorkerExecutionSemanticDigest, len(baselinePrepared.input.WorkloadInputDigests))
	}
}

// assertInventorySnapshotReuse 验证原子测试发现复用已准备的精确树源码快照。
func assertInventorySnapshotReuse(t *testing.T, snapshot *remoteGitTreeSnapshot, expectedPackages []string, expectedTests, expectedRaceTests []gate.GoTestTarget) {
	t.Helper()
	packages, err := inventoryPlatformGoPackagesWithSnapshot(context.Background(), snapshot, "linux/amd64", expectedPackages)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.goSources["test-only/snapshot-sentinel.go"] = []byte("package sentinel\n")
	goTests, raceTests, err := inventoryAtomicGoTestsWithSnapshot(context.Background(), snapshot, "linux/amd64", packages)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(goTests, expectedTests) || !slices.Equal(raceTests, expectedRaceTests) {
		t.Fatalf("shared snapshot atomic selectors changed: normal=%v race=%v", goTests, raceTests)
	}
	if _, reused := snapshot.goSources["test-only/snapshot-sentinel.go"]; !reused {
		t.Fatal("atomic inventory replaced the prepared exact-tree source snapshot")
	}
}

func writeInventoryFixture(t *testing.T, repository string) {
	t.Helper()
	files := map[string]string{
		"go.mod":                     "module example.test/root\n\ngo 1.25\n",
		"build/gate/closure_test.go": "package gate_test\n",
		"build/gate/closure/runtime_deps_test.go":                                 "package gateclosure\n",
		"build/gate/runtime-tools/go.mod":                                         "module example.test/runtime-tools\n\ngo 1.25\n",
		"build/gate/runtime-tools/tools.go":                                       "package tools\n",
		"internal/alpha/alpha.go":                                                 "package alpha\n",
		"internal/ignored/tool.go":                                                "//go:build ignore\n\npackage main\n",
		"internal/archtest/common_test.go":                                        "package archtest\n\nimport \"testing\"\n\nfunc TestCommon(t *testing.T) {}\n",
		"internal/archtest/normal_test.go":                                        "//go:build !race\n\npackage archtest\n\nimport \"testing\"\n\nfunc TestNormal(t *testing.T) {}\n",
		"internal/archtest/race_test.go":                                          "//go:build race\n\npackage archtest\n\nimport \"testing\"\n\nfunc TestRace(t *testing.T) {}\n",
		"internal/provider/codexapp/common_test.go":                               "package codexapp\n\nimport \"testing\"\n\nfunc TestTransportCommon(t *testing.T) {}\n",
		"internal/provider/codexapp/normal_test.go":                               "//go:build !race\n\npackage codexapp\n\nimport \"testing\"\n\nfunc TestTransportNormal(t *testing.T) {}\n",
		"internal/provider/codexapp/race_test.go":                                 "//go:build race\n\npackage codexapp\n\nimport \"testing\"\n\nfunc TestTransportRace(t *testing.T) {}\n",
		"cmd/agent-runtime/main.go":                                               "package main\n",
		"cmd/agent-runtime/main_test.go":                                          "package main\n\nimport \"testing\"\n\nfunc TestAgentRuntimeMain(t *testing.T) {}\nfunc TestAgentRuntimeRace(t *testing.T) {}\n",
		"cmd/agent-terminal/main.go":                                              "package main\n",
		"cmd/agent-terminal/main_test.go":                                         "package main\n\nimport \"testing\"\n\nfunc TestAgentTerminalMain(t *testing.T) {}\nfunc TestAgentTerminalRecovery(t *testing.T) {}\n",
		"cmd/mcp-lsp/mcp.go":                                                      "package main\n",
		"cmd/mcp-lsp/common_test.go":                                              "package main\n\nimport \"testing\"\n\nfunc TestMcpLSPCommon(t *testing.T) {}\n",
		"cmd/mcp-lsp/normal_test.go":                                              "//go:build !race\n\npackage main\n\nimport \"testing\"\n\nfunc TestMcpLSPProcess(t *testing.T) {}\n",
		"cmd/mcp-lsp/race_test.go":                                                "//go:build race\n\npackage main\n\nimport \"testing\"\n\nfunc TestMcpLSPRace(t *testing.T) {}\n",
		"cmd/mcp-lsp/helper_test.go":                                              "package main\n\nimport \"testing\"\n\n// super-dolphin-ci: helper\nfunc TestMcpLSPChildProcess(t *testing.T) {}\n",
		"cmd/mcp-orch/store/taskdag/store.go":                                     "package taskdag\n",
		"cmd/mcp-orch/store/taskdag/store_test.go":                                "package taskdag\n\nimport \"testing\"\n\nfunc TestTaskDAGStore(t *testing.T) {}\nfunc TestTaskDAGWakeup(t *testing.T) {}\n\n// super-dolphin-ci: helper\nfunc TestTaskDAGChildProcess(t *testing.T) {}\n",
		"cmd/super-dolphin-updater/main.go":                                       "package main\n",
		"cmd/super-dolphin-updater/updater_test.go":                               "package main\n\nimport \"testing\"\n\nfunc TestUpdaterCandidateCleanup(t *testing.T) {}\nfunc TestUpdaterRollbackEntries(t *testing.T) {}\n\n// super-dolphin-ci: helper\nfunc TestUpdaterCandidateProcess(t *testing.T) {}\n",
		"internal/app/app.go":                                                     "package app\n",
		"internal/app/app_test.go":                                                "package app\n\nimport \"testing\"\n\nfunc TestAppModuleGraphIsClosed(t *testing.T) {}\nfunc TestRunDesktopPreDrain(t *testing.T) {}\nfunc TestRecoveryRestoreAndGuardConvergeOneTokenBoundLaunch(t *testing.T) {}\n",
		"internal/platform/db/sqlite/sqlite.go":                                   "package sqlite\n",
		"internal/platform/db/sqlite/common_test.go":                              "package sqlite\n\nimport \"testing\"\n\nfunc TestSQLiteCommon(t *testing.T) {}\n",
		"internal/platform/db/sqlite/normal_test.go":                              "//go:build !race\n\npackage sqlite\n\nimport \"testing\"\n\nfunc TestSQLiteNormal(t *testing.T) {}\n",
		"internal/platform/db/sqlite/race_test.go":                                "//go:build race\n\npackage sqlite\n\nimport \"testing\"\n\nfunc TestSQLiteRace(t *testing.T) {}\n",
		"internal/devtools/gate/gate.go":                                          "package gate\n",
		"internal/devtools/gate/common_test.go":                                   "package gate\n\nimport \"testing\"\n\nfunc TestGateAtomicCommon(t *testing.T) {}\n",
		"internal/devtools/gate/normal_test.go":                                   "//go:build !race\n\npackage gate\n\nimport \"testing\"\n\nfunc TestGateAtomicNormal(t *testing.T) {}\n",
		"internal/devtools/gate/race_test.go":                                     "//go:build race\n\npackage gate\n\nimport \"testing\"\n\nfunc TestGateAtomicRace(t *testing.T) {}\n",
		"internal/devtools/remoteci/remoteci.go":                                  "package remoteci\n",
		"internal/devtools/remoteci/common_test.go":                               "package remoteci\n\nimport \"testing\"\n\nfunc TestRemoteCIAtomicCommon(t *testing.T) {}\n",
		"internal/devtools/remoteci/normal_test.go":                               "//go:build !race\n\npackage remoteci\n\nimport \"testing\"\n\nfunc TestRemoteCIAtomicNormal(t *testing.T) {}\n",
		"internal/devtools/remoteci/race_test.go":                                 "//go:build race\n\npackage remoteci\n\nimport \"testing\"\n\nfunc TestRemoteCIAtomicRace(t *testing.T) {}\n",
		"new-root/tool/tool.go":                                                   "package tool\n",
		"tools/custom-check/go.mod":                                               "module example.test/custom-check\n\ngo 1.25\n",
		"tools/custom-check/check.go":                                             "package check\n",
		"frontend-app/src/widget.ts":                                              "export const widget = 1\n",
		"frontend-app/src/widget.test.ts":                                         "test('widget', () => {})\n",
		"frontend-app/scripts/runtime.test.mjs":                                   "test('runtime', () => {})\n",
		"frontend-app/scripts/remote-preflight-carriers/critical-guards.test.mjs": "// protocol carrier\n",
		"frontend-app/scripts/remote-suite-carriers/changed.test.mjs":             "// protocol carrier\n",
		inventoryVitestSuitePolicyPath: `{
  "schemaVersion": 1,
  "defaultExcludes": ["**/scripts/**/*benchmark.test.*", "**/scripts/remote-preflight-carriers/*.test.mjs", "**/scripts/remote-suite-carriers/*.test.mjs"]
}`,
		"frontend-app/scripts/chat-history-benchmark.test.mjs": "test('benchmark', () => {})\n",
	}
	for filePath, body := range files {
		writeInventoryFile(t, repository, filePath, body)
	}
}

func assertAtomicInventoryTestNames(t *testing.T, normal, race []gate.GoTestTarget) {
	t.Helper()
	toNames := func(targets []gate.GoTestTarget) []string {
		names := make([]string, len(targets))
		for index, target := range targets {
			names[index] = target.Package + "#" + target.Name
		}
		return names
	}
	normalNames, raceNames := toNames(normal), toNames(race)
	if !slices.Equal(normalNames, []string{
		"./internal/archtest#TestCommon",
		"./internal/archtest#TestNormal",
		"./internal/provider/codexapp#TestTransportCommon",
		"./internal/provider/codexapp#TestTransportNormal",
		"./cmd/agent-runtime#TestAgentRuntimeMain",
		"./cmd/agent-runtime#TestAgentRuntimeRace",
		"./cmd/agent-terminal#TestAgentTerminalMain",
		"./cmd/agent-terminal#TestAgentTerminalRecovery",
		"./internal/app#TestAppModuleGraphIsClosed",
		"./internal/app#TestRecoveryRestoreAndGuardConvergeOneTokenBoundLaunch",
		"./internal/app#TestRunDesktopPreDrain",
		"./cmd/super-dolphin-updater#TestUpdaterCandidateCleanup",
		"./cmd/super-dolphin-updater#TestUpdaterRollbackEntries",
		"./cmd/mcp-orch/store/taskdag#TestTaskDAGStore",
		"./cmd/mcp-orch/store/taskdag#TestTaskDAGWakeup",
		"./internal/platform/db/sqlite#TestSQLiteCommon",
		"./internal/platform/db/sqlite#TestSQLiteNormal",
		"./internal/devtools/gate#TestGateAtomicCommon",
		"./internal/devtools/gate#TestGateAtomicNormal",
		"./internal/devtools/remoteci#TestRemoteCIAtomicCommon",
		"./internal/devtools/remoteci#TestRemoteCIAtomicNormal",
		"./cmd/mcp-lsp#TestMcpLSPCommon",
		"./cmd/mcp-lsp#TestMcpLSPProcess",
	}) || !slices.Equal(raceNames, []string{
		"./internal/archtest#TestCommon",
		"./internal/archtest#TestRace",
		"./internal/provider/codexapp#TestTransportCommon",
		"./internal/provider/codexapp#TestTransportRace",
		"./cmd/agent-runtime#TestAgentRuntimeMain",
		"./cmd/agent-runtime#TestAgentRuntimeRace",
		"./cmd/agent-terminal#TestAgentTerminalMain",
		"./cmd/agent-terminal#TestAgentTerminalRecovery",
		"./internal/app#TestAppModuleGraphIsClosed",
		"./internal/app#TestRecoveryRestoreAndGuardConvergeOneTokenBoundLaunch",
		"./internal/app#TestRunDesktopPreDrain",
		"./cmd/super-dolphin-updater#TestUpdaterCandidateCleanup",
		"./cmd/super-dolphin-updater#TestUpdaterRollbackEntries",
		"./cmd/mcp-orch/store/taskdag#TestTaskDAGStore",
		"./cmd/mcp-orch/store/taskdag#TestTaskDAGWakeup",
		"./internal/platform/db/sqlite#TestSQLiteCommon",
		"./internal/platform/db/sqlite#TestSQLiteRace",
		"./internal/devtools/gate#TestGateAtomicCommon",
		"./internal/devtools/gate#TestGateAtomicRace",
		"./internal/devtools/remoteci#TestRemoteCIAtomicCommon",
		"./internal/devtools/remoteci#TestRemoteCIAtomicRace",
		"./cmd/mcp-lsp#TestMcpLSPCommon",
		"./cmd/mcp-lsp#TestMcpLSPRace",
	}) {
		t.Fatalf("atomic Go tests normal=%v race=%v", normalNames, raceNames)
	}
}

func TestLoadInventoryVitestSuitePolicyFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		policy string
	}{
		{name: "missing excludes", policy: `{"schemaVersion":1}`},
		{name: "unknown field", policy: `{"schemaVersion":1,"defaultExcludes":["scripts/*.test.mjs"],"extra":true}`},
		{name: "duplicate", policy: `{"schemaVersion":1,"defaultExcludes":["scripts/*.test.mjs","scripts/*.test.mjs"]}`},
		{name: "invalid globstar", policy: `{"schemaVersion":1,"defaultExcludes":["scripts/**bad/*.test.mjs"]}`},
		{name: "trailing value", policy: `{"schemaVersion":1,"defaultExcludes":["scripts/*.test.mjs"]} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := t.TempDir()
			runInventoryGit(t, repository, "init", "--quiet")
			runInventoryGit(t, repository, "config", "user.name", "CI Inventory")
			runInventoryGit(t, repository, "config", "user.email", "ci@example.invalid")
			writeInventoryFile(t, repository, inventoryVitestSuitePolicyPath, test.policy)
			runInventoryGit(t, repository, "add", ".")
			runInventoryGit(t, repository, "commit", "--quiet", "-m", "策略")
			commit := inventoryGitOutput(t, repository, "rev-parse", "HEAD")
			if _, err := loadInventoryVitestSuitePolicy(context.Background(), repository, commit); err == nil {
				t.Fatal("loadInventoryVitestSuitePolicy() error = nil")
			}
		})
	}
}

func TestInventoryVitestGlobMatches(t *testing.T) {
	for _, test := range []struct {
		pattern string
		target  string
		want    bool
	}{
		{pattern: "**/scripts/**/*benchmark.test.*", target: "scripts/chat-history-benchmark.test.mjs", want: true},
		{pattern: "**/scripts/**/*benchmark.test.*", target: "scripts/nested/stop-feedback-benchmark.test.mjs", want: true},
	} {
		if got := inventoryVitestGlobMatches(test.pattern, test.target); got != test.want {
			t.Errorf("inventoryVitestGlobMatches(%q, %q) = %t, want %t", test.pattern, test.target, got, test.want)
		}
	}
}

func runInventoryGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func inventoryGitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repository
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}

func writeInventoryFile(t *testing.T, repository string, relative string, contents string) {
	t.Helper()
	target := filepath.Join(repository, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
