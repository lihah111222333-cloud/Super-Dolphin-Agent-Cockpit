package mcplspcatalogguard

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

type brokenGuardWriter struct{}

func (brokenGuardWriter) Write([]byte) (int, error) {
	return 0, errors.New("broken guard stdout")
}

func TestRunReturnsStdoutWriteFailure(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Clean(filepath.Join(workingDirectory, "..", "..", ".."))
	tree := snapshotGuardCandidateTree(t, repoRoot)
	err = Run([]string{"--repository", repoRoot, "--tree", tree}, brokenGuardWriter{})
	if err == nil || !strings.Contains(err.Error(), "broken guard stdout") {
		t.Fatalf("Run() error = %v, want stdout write failure", err)
	}
}

func TestValidateCatalogTestSelectorsRejectsRenamedTest(t *testing.T) {
	repoRoot := t.TempDir()
	packageDirectory := filepath.Join(repoRoot, "cmd", "mcp-lsp", "fake")
	if err := os.MkdirAll(packageDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	testSource := "package fake\n\nimport \"testing\"\nfunc TestExisting(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(packageDirectory, "fake_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	tree := commitGuardFixture(t, repoRoot)
	document := catalog.Catalog{Workloads: []catalog.Workload{{
		ID: "mcp-lsp-mutated", ImplementationStatus: "implemented",
		Platforms: []string{"linux"},
		Command:   []string{"go", "test", "./cmd/mcp-lsp/fake", "-run", "^TestRenamed$"},
	}}}
	err := validateCatalogTestSelectors(repoRoot, tree, document)
	if err == nil || !strings.Contains(err.Error(), "TestRenamed") || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("validateCatalogTestSelectors() error = %v, want missing renamed test", err)
	}
}

func TestValidateCatalogTestSelectorsRejectsCrossPackageAndInapplicableMatches(t *testing.T) {
	repoRoot := t.TempDir()
	for _, fixture := range []struct{ pkg, file, source string }{
		{pkg: "a", file: "a_test.go", source: "package a\n\nimport \"testing\"\nfunc TestShared(t *testing.T) {}\n"},
		{pkg: "b", file: "b_test.go", source: "package b\n\nimport \"testing\"\nfunc TestShared(t *testing.T) {}\n"},
		{pkg: "linux", file: "only_windows_test.go", source: "//go:build windows\n\npackage linux\n\nimport \"testing\"\nfunc TestLinuxRequired(t *testing.T) {}\n"},
	} {
		directory := filepath.Join(repoRoot, "cmd", "mcp-lsp", fixture.pkg)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, fixture.file), []byte(fixture.source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree := commitGuardFixture(t, repoRoot)

	duplicate := catalog.Catalog{Workloads: []catalog.Workload{{
		ID: "mcp-lsp-duplicate", ImplementationStatus: "implemented", Platforms: []string{"linux"},
		Command: []string{"go", "test", "./cmd/mcp-lsp/a", "./cmd/mcp-lsp/b", "-run", "^TestShared$"},
	}}}
	if err := validateCatalogTestSelectors(repoRoot, tree, duplicate); err == nil || !strings.Contains(err.Error(), "matches multiple packages") {
		t.Fatalf("duplicate package projection error = %v, want multiple-package rejection", err)
	}

	inapplicable := catalog.Catalog{Workloads: []catalog.Workload{{
		ID: "mcp-lsp-linux", ImplementationStatus: "implemented", Platforms: []string{"linux"},
		Command: []string{"go", "test", "./cmd/mcp-lsp/linux", "-run", "^TestLinuxRequired$"},
	}}}
	if err := validateCatalogTestSelectors(repoRoot, tree, inapplicable); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("build-inapplicable projection error = %v, want zero-match rejection", err)
	}
}

func TestValidateConsumersRequiresExactLiveRegistryCoverage(t *testing.T) {
	repoRoot := t.TempDir()
	writeConsumerFixture := func(relative, content string) {
		writeGuardConsumerFixture(t, repoRoot, relative, content)
	}
	writeConsumerFixture("Makefile", "mcp-lsp-workload-catalog-check:\n\t@test -n \"$(MCP_LSP_CANDIDATE_TREE)\" || (echo \"MCP_LSP_CANDIDATE_TREE is required\" >&2; exit 1)\n\t@test -n \"$(SUPER_DOLPHIN_GATE_SELF)\" || (echo \"SUPER_DOLPHIN_GATE_SELF is required\" >&2; exit 1)\n\tSUPER_DOLPHIN_GATE_SELF=\"$(SUPER_DOLPHIN_GATE_SELF)\" ./scripts/check_mcp_lsp_workload_catalog.sh --tree \"$(MCP_LSP_CANDIDATE_TREE)\"\n\t@echo \"LOCAL_NON_AUTHORITATIVE NOT_VERIFIED: direct Make target cannot satisfy release authority\" >&2; exit 1\ntarget:\n\t./scripts/run_mcp_lsp_workload.sh --tree \"$(MCP_LSP_CANDIDATE_TREE)\" --id mcp-lsp-idle-quick\n\t./scripts/run_mcp_lsp_workload.sh --tree \"$(MCP_LSP_CANDIDATE_TREE)\" --id mcp-lsp-native-process-tree\n\t./scripts/run_mcp_lsp_workload.sh --tree \"$(MCP_LSP_CANDIDATE_TREE)\" --id mcp-lsp-default-15m\n")
	runnerWrapper := readGuardCandidateBlob(t, "scripts/run_mcp_lsp_workload.sh")
	writeConsumerFixture("scripts/run_mcp_lsp_workload.sh", string(runnerWrapper))
	guardWrapper := readGuardCandidateBlob(t, "scripts/check_mcp_lsp_workload_catalog.sh")
	writeConsumerFixture("scripts/check_mcp_lsp_workload_catalog.sh", string(guardWrapper))
	writeConsumerFixture("scripts/mcp_lsp_workload_runner/main.go", "package main\nimport catalog \"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog\"\nfunc run(args []string) error { document, workload, _ := loadRunnerWorkload(repoRoot, options.tree, options.id); if err := validateRunnerWorkload(workload); err != nil { return err }; return executeRunnerWorkload(options, repoRoot, document, workload, workload, head) }\nfunc loadRunnerWorkload(repoRoot, tree, id string) (catalog.Catalog, catalog.Workload, error) { document, err := catalog.LoadAt(repoRoot, tree); workload, err := document.Find(id); execution := runnerPlatformWorkload(workload); if err := validateRunnerPlatformView(workload, execution); err != nil { return catalog.Catalog{}, catalog.Workload{}, err }; return document, execution, err }\nfunc executeRunnerWorkload(options opts, repoRoot string, document catalog.Catalog, canonicalWorkload, executionWorkload catalog.Workload, head string) error { hostCommand, err := catalog.LocalHostTestCommand(canonicalWorkload); return executeLocalHostCommand(repoRoot, canonicalWorkload.ID, hostCommand, head, options.tree) }\n")
	platformAdapter := "package main\nimport catalog \"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog\"\nfunc runnerPlatformWorkload(workload catalog.Workload) catalog.Workload { return cloneRunnerWorkload(workload) }\n"
	writeConsumerFixture("scripts/mcp_lsp_workload_runner/platform_other.go", platformAdapter)
	writeConsumerFixture("scripts/mcp_lsp_workload_runner/platform_windows.go", platformAdapter)
	writeConsumerFixture("internal/devtools/gate/executor_mapping.go", "package main\nfunc splitSourceGoGuardExecutorProgram(target string) {\n\tswitch target {\n\tcase GoGuardTargetMcpLSPWorkloadCatalog:\n\t\tprogram := commandProgram([]string{ExecutorSelfCommandName, \"worker\", \"mcp-lsp-catalog-guard\"})\n\t\treturn program, true\n\t}\n}\n")
	writeConsumerFixture("internal/devtools/gate/executor.go", "package main\nfunc resolveExecutorStepsWithCandidateTree(sourceCopy, candidateTree string) ([]step, error) { steps, err := resolve(); if err != nil { return nil, err }; for index := range steps { argv, err := injectMcpLSPCandidateTreeAt(steps[index].argv, candidateTree, sourceCopy); if err != nil { return nil, err }; steps[index].argv = argv }; return steps, nil }\n")
	writeConsumerFixture("internal/devtools/gate/executor_catalog_tree.go", "package main\nimport \"slices\"\nfunc injectMcpLSPCandidateTree(candidateTree string) []string { return injectMcpLSPGuardTree([]string{\"guard\"}, candidateTree) }\nfunc injectMcpLSPGuardTree(argv []string, candidateTree string) []string { return append(slices.Clone(argv), \"--tree\", candidateTree) }\nfunc injectMcpLSPMakeTree(argv []string, candidateTree string) []string { return append(slices.Clone(argv), \"MCP_LSP_CANDIDATE_TREE=\"+candidateTree) }\n")
	writeConsumerFixture("internal/devtools/gate/workload_catalog.go", "package main\nfunc splitGoGuardWorkloadSpecs() { _ = GoGuardTargetMcpLSPWorkloadCatalog }\n")
	writeConsumerFixture("internal/devtools/gate/workload_targets.go", "package main\nconst GoGuardTargetMcpLSPWorkloadCatalog = \"mcp-lsp-workload-catalog\"\n")
	writeConsumerFixture("internal/devtools/mcplspguardcore/guard.go", "package mcplspcatalogguard\nimport catalog \"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog\"\nfunc guard() { _, _ = catalog.LoadAt(repository, tree) }\n")
	writeConsumerFixture("internal/devtools/mcplspguardcore/consumer_ast_helpers.go", "package mcplspcatalogguard\nimport catalog \"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog\"\nfunc flow() { _, _ = catalog.LoadAt(repository, tree) }\n")
	writeConsumerFixture("internal/devtools/mcplspguardcore/consumer_validation.go", "package mcplspcatalogguard\nimport catalog \"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog\"\nfunc validation() { _, _ = catalog.LoadAt(repository, tree) }\n")
	writeConsumerFixture("internal/devtools/mcplspguardcore/executor_ast_helpers.go", "package mcplspcatalogguard\nimport catalog \"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog\"\nfunc executorFlow() { _, _ = catalog.LoadAt(repository, tree) }\n")
	writeConsumerFixture("internal/devtools/mcplspguardcore/receipt_ast_helpers.go", "package mcplspcatalogguard\nimport catalog \"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog\"\nfunc receiptFlow() { _, _ = catalog.LoadAt(repository, tree) }\n")
	writeConsumerFixture("internal/devtools/mcplspguardcore/scheduler_validation.go", "package mcplspcatalogguard\nimport catalog \"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog\"\nfunc schedulerFlow(document catalog.Catalog) { _ = catalog.WorkloadGateInputPaths(document) }\n")
	writeConsumerFixture("cmd/super-dolphin-gate/main.go", "package main\nimport mcplspcatalogguard \"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/mcplspguardcore\"\nfunc runMcpLSPCatalogGuardWorker(args []string, stdout, stderr writer) int { if err := mcplspcatalogguard.Run(args, stdout); err != nil { return workerBuiltinErrorExitCode(stderr, err) }; return int(gatecontract.ExitOK) }\n")
	writeConsumerFixture("scripts/ai_maintenance/owned_gate_execution.go", ownedGateFixtureSource("mcp-lsp-idle-quick"))
	for _, relative := range []string{
		"scripts/ai_maintenance/main.go", "scripts/ai_maintenance/release_gate_rules.go", "scripts/ai_maintenance/gate_execution.go",
	} {
		writeConsumerFixture(relative, string(readGuardCandidateBlob(t, relative)))
	}
	tree := commitGuardFixture(t, repoRoot)
	document := catalog.Catalog{Workloads: []catalog.Workload{
		{ID: "mcp-lsp-idle-quick"}, {ID: "mcp-lsp-native-process-tree"}, {ID: "mcp-lsp-default-15m"},
	}}
	if err := validateConsumers(repoRoot, tree, document); err != nil {
		t.Fatalf("validateConsumers() error = %v, want complete exact consumer coverage", err)
	}

	writeConsumerFixture("scripts/ai_maintenance/owned_gate_execution.go", ownedGateFixtureSource("mcp-lsp-renamed"))
	tree = commitGuardFixture(t, repoRoot)
	if err := validateConsumers(repoRoot, tree, document); err == nil || !strings.Contains(err.Error(), "unknown workload ID") {
		t.Fatalf("validateConsumers() error = %v, want unknown live consumer rejection", err)
	}
	writeConsumerFixture("scripts/ai_maintenance/owned_gate_execution.go", ownedGateFixtureSource("mcp-lsp-native-process-tree"))
	tree = commitGuardFixture(t, repoRoot)
	if err := validateConsumers(repoRoot, tree, document); err == nil || !strings.Contains(err.Error(), "idle-quick workload role binding") {
		t.Fatalf("validateConsumers() error = %v, want wrong known-ID role rejection", err)
	}
	writeConsumerFixture("scripts/ai_maintenance/owned_gate_execution.go", ownedGateFixtureSource("mcp-lsp-idle-quick"))
	writeConsumerFixture("internal/devtools/gate/new_catalog_consumer.go", "package gate\nimport catalog \"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog\"\nfunc omitted(repoRoot, tree string) { _, _ = catalog.LoadAt(repoRoot, tree) }\n")
	tree = commitGuardFixture(t, repoRoot)
	if err := validateConsumers(repoRoot, tree, document); err == nil || !strings.Contains(err.Error(), "omitted discovered path") {
		t.Fatalf("validateConsumers() error = %v, want omitted dynamic consumer rejection", err)
	}
}

func writeGuardConsumerFixture(t *testing.T, repoRoot, relative, content string) {
	t.Helper()
	path := filepath.Join(repoRoot, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readGuardCandidateBlob(t *testing.T, relative string) []byte {
	t.Helper()
	repoRoot := filepath.Clean(filepath.Join("..", "..", ".."))
	indexPath := filepath.Join(t.TempDir(), "candidate.index")
	environment := append(os.Environ(), "GIT_INDEX_FILE="+indexPath)
	for _, args := range [][]string{{"read-tree", "HEAD"}, {"add", "--", relative}} {
		command := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
		command.Env = environment
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("prepare candidate blob %q with git %v: %v\n%s", relative, args, err, output)
		}
	}
	writeTree := exec.Command("git", "-C", repoRoot, "write-tree")
	writeTree.Env = environment
	output, err := writeTree.Output()
	if err != nil {
		t.Fatal(err)
	}
	tree := strings.TrimSpace(string(output))
	show := exec.Command("git", "-C", repoRoot, "show", tree+":"+relative)
	raw, err := show.Output()
	if err != nil {
		t.Fatalf("read candidate blob %s:%s: %v", tree, relative, err)
	}
	return raw
}

func snapshotGuardCandidateTree(t *testing.T, repoRoot string) string {
	t.Helper()
	indexPath := filepath.Join(t.TempDir(), "candidate.index")
	environment := append(os.Environ(), "GIT_INDEX_FILE="+indexPath)
	for _, args := range [][]string{{"read-tree", "HEAD"}, {"add", "-A", "--", "."}} {
		command := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
		command.Env = environment
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("snapshot candidate git %v: %v\n%s", args, err, output)
		}
	}
	command := exec.Command("git", "-C", repoRoot, "write-tree")
	command.Env = environment
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

func ownedGateFixtureSource(id string) string {
	return fmt.Sprintf(`package main
import (
	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
	trustedlauncher "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/trustedlauncher"
)
func runMcpLSPCatalogGuard(plan gatePlan) error {
	result, err := trustedlauncher.Build(ctx, trustedlauncher.BuildOptions{RepositoryRoot: gateExecutionDir(plan, ""), Tree: plan.ReviewObject.CandidateTree, InstallRoot: installRoot})
	if err != nil { return err }
	return runCommandWithEnvironment(gateExecutionDir(plan, ""), result.BinaryPath, []string{"LC_ALL=C"}, "worker", "mcp-lsp-catalog-guard", "--repository", gateExecutionDir(plan, ""), "--tree", plan.ReviewObject.CandidateTree)
}
func runMcpLSPQuickReleaseAuthority(plan gatePlan) error {
	document, err := catalog.LoadAt(gateExecutionDir(plan, ""), plan.ReviewObject.CandidateTree)
	if err != nil { return err }
	workload, err := document.Find(%q)
	if err != nil { return err }
	return requireMcpLSPQuickReleaseAuthority(workload)
}
func requireMcpLSPQuickReleaseAuthority(workload catalog.Workload) error {
	if err := catalog.RequireIdleQuickReleaseRole(workload); err != nil { return err }
	return catalog.RequireRemoteCompletionAuthority(workload)
}
`, id)
}

func TestWorkloadConsumerRegistryIncludesExecutorTreeClosure(t *testing.T) {
	paths := catalog.WorkloadConsumerPaths()
	for _, want := range []string{
		"internal/devtools/gate/executor_mapping.go",
		"internal/devtools/gate/executor.go",
		"internal/devtools/gate/executor_catalog_tree.go",
	} {
		if !slices.Contains(paths, want) {
			t.Fatalf("WorkloadConsumerPaths() omitted production consumer %q", want)
		}
	}
}

func TestPlatformWorkloadReferencesScansMapAndIfLiterals(t *testing.T) {
	source := `package main
var platformWorkloads = map[string]string{"mcp-lsp-omitted": "windows"}
func workloadForPlatform(platform string) string {
	if platform == "mcp-lsp-conditional" { return platform }
	return ""
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "platform_windows.go", []byte(source), 0)
	if err != nil {
		t.Fatal(err)
	}
	references := platformWorkloadReferences(file)
	for _, want := range []string{"mcp-lsp-omitted", "mcp-lsp-conditional"} {
		if references[want] != 1 {
			t.Fatalf("platformWorkloadReferences()[%q] = %d, want one reference", want, references[want])
		}
	}
}

func TestValidateRunnerPlatformConsumerRejectsSemanticRewrite(t *testing.T) {
	for _, source := range []string{
		`package main; func runnerPlatformWorkload(workload Workload) Workload { workload.Command = []string{"go", "version"}; return workload }`,
		`package main; func runnerPlatformWorkload(workload Workload) Workload { if false { return cloneRunnerWorkload(workload) }; return workload }`,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "platform_windows.go", []byte(source), 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateRunnerPlatformConsumer("platform_windows.go", file); err == nil {
			t.Fatalf("validateRunnerPlatformConsumer accepted rewrite: %s", source)
		}
	}
}

func TestValidateMakefileConsumerRejectsInvocationIndirection(t *testing.T) {
	for _, recipe := range []string{
		"target:\n\t$(RUNNER) --tree \"$(MCP_LSP_CANDIDATE_TREE)\" --id mcp-lsp-idle-quick\n",
		"target:\n\t./scripts/run_mcp_lsp_workload.sh --tree \"$(MCP_LSP_CANDIDATE_TREE)\" \\\n\t  --id mcp-lsp-idle-quick\n",
	} {
		if err := validateMakefileConsumerExecution("Makefile", recipe); err == nil {
			t.Fatalf("validateMakefileConsumerExecution accepted indirection: %q", recipe)
		}
	}
}

func TestReceiptOnlyGuardSkipsStaticCatalogValidationAndRequiresPairedAbsoluteReceipt(t *testing.T) {
	receiptPath := filepath.Join(t.TempDir(), "receipt.json")
	request := assertReceiptOnlyRequestParsing(t, receiptPath)
	assertReceiptOnlyValidationOrder(t, request)
}

func TestGuardRequiresExplicitRepositoryAndCandidateTree(t *testing.T) {
	if _, err := parseGuardRequest([]string{}); err == nil || !strings.Contains(err.Error(), "--repository and --tree are required") {
		t.Fatalf("parseGuardRequest() error = %v, want explicit candidate identity requirement", err)
	}
}

func TestGuardRejectsDuplicateCandidateTree(t *testing.T) {
	args := []string{"--repository", "/tmp/repo", "--tree", strings.Repeat("a", 40), "--tree=" + strings.Repeat("b", 40)}
	if _, err := parseGuardRequest(args); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("parseGuardRequest() error = %v, want duplicate tree rejection", err)
	}
}

func TestGuardRejectsDuplicateRepositoryAndSingleDashAuthorityAliases(t *testing.T) {
	args := []string{"-repository", "/tmp/one", "--repository", "/tmp/two", "--tree", strings.Repeat("a", 40)}
	if _, err := parseGuardRequest(args); err == nil || !strings.Contains(err.Error(), "duplicate authority flag --repository") {
		t.Fatalf("parseGuardRequest() error = %v, want repository duplicate rejection", err)
	}
}

func TestResolveGuardTreeUsesExplicitCandidateAfterHeadDrift(t *testing.T) {
	root := t.TempDir()
	writeGuardTreeFile(t, root, "candidate.txt", "candidate-a\n")
	firstTree := commitGuardFixture(t, root)
	writeGuardTreeFile(t, root, "candidate.txt", "candidate-b\n")
	runGuardGit(t, root, "add", "candidate.txt")
	runGuardGit(t, root, "commit", "--quiet", "-m", "head drift")
	resolved, err := resolveGuardTree(root, firstTree)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != firstTree {
		t.Fatalf("resolveGuardTree() = %q, want explicit candidate %q", resolved, firstTree)
	}
}

func TestValidateRunnerShellExecutionRejectsCommentOnlyMarkers(t *testing.T) {
	markers := "# git rev-parse --verify HEAD^{tree}\n# exec go run ./scripts/mcp_lsp_workload_runner --tree candidate\n"
	if err := validateRunnerShellExecution("scripts/run_mcp_lsp_workload.sh", markers); err == nil {
		t.Fatal("validateRunnerShellExecution() accepted comment-only invocation markers")
	}
}

func TestCompiledGuardRejectsNoOpCandidateWrappers(t *testing.T) {
	for _, relative := range []string{"scripts/check_mcp_lsp_workload_catalog.sh", "scripts/run_mcp_lsp_workload.sh"} {
		if err := validateConsumerExecution(relative, []byte("#!/usr/bin/env bash\nexit 0\n")); err == nil {
			t.Fatalf("compiled guard accepted no-op candidate wrapper %q", relative)
		}
	}
}

func TestValidateRunnerShellExecutionRejectsDeadInvocation(t *testing.T) {
	dead := `--tree|-tree)
tree="$2"
runner_args+=("--tree" "$2")
if false; then
exec go run ./scripts/mcp_lsp_workload_runner "${runner_args[@]}"
fi
`
	if err := validateRunnerShellExecution("scripts/run_mcp_lsp_workload.sh", dead); err == nil {
		t.Fatal("validateRunnerShellExecution() accepted a dead runner invocation")
	}
	dynamicDead := strings.Replace(dead, "if false; then", `if [[ "$MODE" == "live" ]]; then`, 1)
	if err := validateRunnerShellExecution("scripts/run_mcp_lsp_workload.sh", dynamicDead); err == nil {
		t.Fatal("validateRunnerShellExecution() accepted a conditional runner invocation")
	}
}

func TestValidateRunnerShellExecutionAcceptsExplicitTreeForwarding(t *testing.T) {
	raw := readGuardCandidateBlob(t, "scripts/run_mcp_lsp_workload.sh")
	if err := validateRunnerShellExecution("scripts/run_mcp_lsp_workload.sh", string(raw)); err != nil {
		t.Fatalf("validateRunnerShellExecution() error = %v, want explicit tree parser/forwarder", err)
	}
}

func TestValidateRunnerShellExecutionRejectsExtraTopLevelCommand(t *testing.T) {
	runner := string(readGuardCandidateBlob(t, "scripts/run_mcp_lsp_workload.sh"))
	execLine := `exec go run ./scripts/mcp_lsp_workload_runner "${runner_args[@]}"`
	mutated := strings.Replace(runner, execLine, "touch /tmp/runner-side-effect\n"+execLine, 1)
	if mutated == runner {
		t.Fatal("runner exec mutation anchor not found")
	}
	if err := validateRunnerShellExecution("scripts/run_mcp_lsp_workload.sh", mutated); err == nil {
		t.Fatal("runner shell validation accepted an extra top-level command")
	}
}

func TestValidateShellConsumerRejectsNonLiveWrapperGrammar(t *testing.T) {
	runner := string(readGuardCandidateBlob(t, "scripts/run_mcp_lsp_workload.sh"))
	guard := string(readGuardCandidateBlob(t, "scripts/check_mcp_lsp_workload_catalog.sh"))
	guardExec := `exec "$gate_self" worker mcp-lsp-catalog-guard --repository "$root_dir" --tree "$tree" ${guard_args[@]+"${guard_args[@]}"}`
	mutateGuard := func(statement string) string { return strings.Replace(guard, guardExec, statement+"\n"+guardExec, 1) }
	for _, test := range []struct {
		name     string
		relative string
		source   string
		validate func(string, string) error
	}{
		{name: "runner unused function", relative: "scripts/run_mcp_lsp_workload.sh", source: "unused() {\n" + runner + "\n}\n", validate: validateRunnerShellExecution},
		{name: "runner heredoc", relative: "scripts/run_mcp_lsp_workload.sh", source: "cat <<'WRAPPER'\n" + runner + "\nWRAPPER\n", validate: validateRunnerShellExecution},
		{name: "runner early exit", relative: "scripts/run_mcp_lsp_workload.sh", source: "exit 0\n" + runner, validate: validateRunnerShellExecution},
		{name: "guard unused function", relative: "scripts/check_mcp_lsp_workload_catalog.sh", source: "unused() {\n" + guard + "\n}\n", validate: validateGuardShellExecution},
		{name: "guard heredoc", relative: "scripts/check_mcp_lsp_workload_catalog.sh", source: "cat <<'WRAPPER'\n" + guard + "\nWRAPPER\n", validate: validateGuardShellExecution},
		{name: "guard early return", relative: "scripts/check_mcp_lsp_workload_catalog.sh", source: "return 0\n" + guard, validate: validateGuardShellExecution},
		{name: "guard extra export", relative: "scripts/check_mcp_lsp_workload_catalog.sh", source: mutateGuard("export EVIL=1"), validate: validateGuardShellExecution},
		{name: "guard arbitrary command", relative: "scripts/check_mcp_lsp_workload_catalog.sh", source: mutateGuard("touch /tmp/evil"), validate: validateGuardShellExecution},
		{name: "guard tree reassignment", relative: "scripts/check_mcp_lsp_workload_catalog.sh", source: mutateGuard(`tree="$EVIL"`), validate: validateGuardShellExecution},
		{name: "guard args reassignment", relative: "scripts/check_mcp_lsp_workload_catalog.sh", source: mutateGuard(`guard_args=("$EVIL")`), validate: validateGuardShellExecution},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.validate(test.relative, test.source); err == nil {
				t.Fatal("shell consumer validation accepted non-live or unsupported wrapper grammar")
			}
		})
	}
}

func TestHasLiveGuardWorkerBindingRejectsDeadOrStaleCalls(t *testing.T) {
	for _, source := range []string{
		`package main
import mcplspcatalogguard "example.invalid/guard"
func runMcpLSPCatalogGuardWorker(args []string, stdout writer) int {
	if false { _ = mcplspcatalogguard.Run(args, stdout) }
	return 0
}`,
		`package main
import mcplspcatalogguard "example.invalid/guard"
func dead(args []string, stdout writer) { _ = mcplspcatalogguard.Run(args, stdout) }
func runMcpLSPCatalogGuardWorker(args []string, stdout writer) int { return 0 }`,
		`package main
import mcplspcatalogguard "example.invalid/guard"
func runMcpLSPCatalogGuardWorker(args []string, stdout writer) int {
	_ = mcplspcatalogguard.Run(args, stdout)
	return 0
}`,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "main.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		if hasLiveGuardWorkerBinding(file) {
			t.Fatal("guard worker binding accepted a dead helper, dead branch, or stale unchecked call")
		}
	}
}

func TestHasLiveGuardWorkerBindingBindsCanonicalImportAlias(t *testing.T) {
	canonical := `package main
import (
	guardalias "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/mcplspguardcore"
	gatecontract "example.invalid/gate"
)
func runMcpLSPCatalogGuardWorker(args []string, stdout, stderr writer) int {
	if err := guardalias.Run(args, stdout); err != nil { return workerBuiltinErrorExitCode(stderr, err) }
	return int(gatecontract.ExitOK)
}`
	for _, test := range []struct {
		name   string
		source string
		want   bool
	}{
		{name: "canonical alias", source: canonical, want: true},
		{name: "local shadow", source: strings.Replace(canonical, "\tif err :=", "\tguardalias := fakeGuard{}\n\tif err :=", 1)},
		{name: "fake package", source: strings.Replace(canonical, canonicalGuardImport, "example.invalid/guard", 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "main.go", test.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := hasLiveGuardWorkerBinding(file); got != test.want {
				t.Fatalf("hasLiveGuardWorkerBinding() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestValidateRunnerConsumerAcceptsLiveCatalogTreeFlow(t *testing.T) {
	raw := readGuardCandidateBlob(t, "scripts/mcp_lsp_workload_runner/main.go")
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLiveRunnerCatalogFlow(file) {
		t.Fatal("hasLiveRunnerCatalogFlow() rejected the production runner load path")
	}
}

func TestCatalogFlowsRejectLocalImportAliasShadow(t *testing.T) {
	for _, test := range []struct {
		path        string
		declaration string
		validate    func(*ast.File) bool
	}{
		{path: "scripts/mcp_lsp_workload_runner/main.go", declaration: "func loadRunnerWorkload(repoRoot, tree, id string) (catalog.Catalog, catalog.Workload, error) {", validate: hasLiveRunnerCatalogFlow},
	} {
		raw := readGuardCandidateBlob(t, test.path)
		mutated := strings.Replace(string(raw), test.declaration, test.declaration+"\n\tcatalog := fakeCatalog{}", 1)
		if mutated == string(raw) {
			t.Fatalf("fixture declaration %q not found", test.declaration)
		}
		file, err := parser.ParseFile(token.NewFileSet(), test.path, mutated, 0)
		if err != nil {
			t.Fatal(err)
		}
		if test.validate(file) {
			t.Fatalf("catalog flow %s accepted a local import alias shadow", test.path)
		}
	}
}

func TestSchedulerCatalogRouteRejectsDisconnectedMutations(t *testing.T) {
	for _, test := range []struct {
		path string
		old  string
		new  string
	}{
		{path: "scripts/ai_maintenance/main.go", old: `"internal/devtools/mcplspguardcore/", `, new: ""},
		{path: "scripts/ai_maintenance/release_gate_rules.go", old: `gates["mcp-lsp:idle-quick"] = true`, new: `gates["backend:archtest"] = true`},
		{path: "scripts/ai_maintenance/gate_execution.go", old: `maps.Copy(runners, ownedGateRunners(plan))`, new: `_ = ownedGateRunners(plan)`},
	} {
		raw := readGuardCandidateBlob(t, test.path)
		mutated := strings.Replace(string(raw), test.old, test.new, 1)
		if mutated == string(raw) {
			t.Fatalf("mutation anchor %q not found in %s", test.old, test.path)
		}
		file, err := parser.ParseFile(token.NewFileSet(), test.path, mutated, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateGoConsumerExecution(test.path, file); err == nil {
			t.Fatalf("scheduler consumer %s accepted disconnected route", test.path)
		}
	}
}

func TestMakeCatalogGuardTargetRejectsNoOpRecipe(t *testing.T) {
	raw := readGuardCandidateBlob(t, "Makefile")
	mutated := strings.Replace(string(raw), `SUPER_DOLPHIN_GATE_SELF="$(SUPER_DOLPHIN_GATE_SELF)" ./scripts/check_mcp_lsp_workload_catalog.sh --tree "$(MCP_LSP_CANDIDATE_TREE)"`, `@true`, 1)
	if mutated == string(raw) {
		t.Fatal("Make catalog guard recipe anchor not found")
	}
	if err := validateMakefileConsumerExecution("Makefile", mutated); err == nil {
		t.Fatal("Make catalog guard validation accepted a no-op target")
	}
}
