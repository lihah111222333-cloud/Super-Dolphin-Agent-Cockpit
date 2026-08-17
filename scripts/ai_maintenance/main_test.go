package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sqlcconfig"
)

func TestBuildGatePlanRoutesFrontendBackendAndGeneratedFiles(t *testing.T) {
	plan := mustBuildGatePlan(t, []string{
		"frontend-app/src/App.jsx",
		"internal/app/modules.go",
		"docs/doc/codemap/project-map/AI_PROJECT_MAP.md",
	})

	assertStringSetContains(t, plan.RequiredGates,
		"backend:test_with_guard",
		"codemap:check",
		"diff:whitespace",
		"frontend:lint",
		"frontend:changed-tests",
		"lsp:changed-diagnostics",
		"project-map:check",
	)
	assertStringSetOmits(t, plan.RequiredGates, "frontend:build", "frontend:typecheck-contracts")
	assertStringSetContains(t, plan.DiagnosticFiles, "frontend-app/src/App.jsx", "internal/app/modules.go")
	assertStringSetContains(t, plan.GeneratedFiles, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md")
}

func TestWorktreeReviewObjectPreservesNulDelimitedUnicodePaths(t *testing.T) {
	root := t.TempDir()
	runAIMaintenanceGit(t, root, "init", "--quiet")
	runAIMaintenanceGit(t, root, "config", "user.name", "ai-maintenance-test")
	runAIMaintenanceGit(t, root, "config", "user.email", "ai-maintenance@example.invalid")
	tracked := filepath.Join(root, "  前导目录", "尾随文件  .txt")
	if err := os.MkdirAll(filepath.Dir(tracked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracked, []byte("候选\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runAIMaintenanceGit(t, root, "add", ".")
	runAIMaintenanceGit(t, root, "commit", "--quiet", "-m", "初始化")
	untracked := filepath.Join(root, "新增", "  中文 名  .md")
	if err := os.MkdirAll(filepath.Dir(untracked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(untracked, []byte("未跟踪\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracked, []byte("候选变更\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runAIMaintenanceGit(t, root, "add", "  前导目录/尾随文件  .txt")
	object, err := resolveReviewObject(root, reviewObjectOptions{Worktree: true})
	if err != nil {
		t.Fatalf("resolveReviewObject() error = %v", err)
	}
	files, err := changedFilesFromReviewObject(root, object)
	if err != nil {
		t.Fatalf("changedFilesFromReviewObject() error = %v", err)
	}
	if !slices.Contains(files, "  前导目录/尾随文件  .txt") || !slices.Contains(files, "新增/  中文 名  .md") {
		t.Fatalf("changedFilesFromReviewObject() = %v, want exact Unicode/whitespace paths", files)
	}
}

func runAIMaintenanceGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func TestBuildGatePlanRoutesAIMaintenanceHooksToSelfTest(t *testing.T) {
	plan := mustBuildGatePlan(t, []string{".githooks/pre-commit", ".githooks/pre-push"})

	assertStringSetContains(t, plan.RequiredGates, "ai-maintenance:self-test", "diff:whitespace")
}

func TestBuildGatePlanRoutesCriticalTypecheckSourcesAndInfrastructure(t *testing.T) {
	for _, file := range []string{
		"frontend-app/src/shared/ui/runUIAction.js",
		"frontend-app/tsconfig.contracts.json",
		"frontend-app/scripts/critical-typecheck-files.json",
		"frontend-app/scripts/critical-typecheck-guard.mjs",
	} {
		plan := mustBuildGatePlan(t, []string{file})
		assertStringSetContains(t, plan.RequiredGates, "frontend:typecheck-contracts")
	}
}

func TestBuildGatePlanRoutesProjectMapOverridesToCodemapChecks(t *testing.T) {
	plan := mustBuildGatePlan(t, []string{".ai-project-map.overrides.json"})

	assertStringSetContains(t, plan.RequiredGates, "codemap:check", "project-map:check", "diff:whitespace")
}

func TestBuildGatePlanFailsClosedWhenCapabilityRootsCannotBeParsed(t *testing.T) {
	repoRoot := t.TempDir()
	source := filepath.Join(repoRoot, "scripts", "capcontract", "main.go")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatalf("mkdir capcontract source dir: %v", err)
	}
	if err := os.WriteFile(source, []byte("package main\nvar defaultCapabilityRoots = []string{"), 0o644); err != nil {
		t.Fatalf("write malformed capcontract source: %v", err)
	}

	_, err := buildGatePlanForRepo(repoRoot, []string{"internal/provider/claudecli/session.go"}, newGatePlanPolicy())
	if err == nil || !strings.Contains(err.Error(), "parse default capability roots source") {
		t.Fatalf("buildGatePlanForRepo() error = %v, want parser failure", err)
	}
}

func TestExecuteGatePlanKeepsGeneratedDriftFailFast(t *testing.T) {
	binDir := t.TempDir()
	makePath := filepath.Join(binDir, "make")
	if err := os.WriteFile(makePath, []byte("#!/usr/bin/env bash\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake make: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	plan := exactCurrentReviewPlan(t, "codemap:check", "project-map:check")

	if err := executeGatePlan(plan); err == nil {
		t.Fatal("generated drift gate unexpectedly succeeded")
	}

	t.Setenv("SUPER_DOLPHIN_PRE_PUSH_SOFT_GENERATED_DRIFT", "1")
	if err := executeGatePlan(plan); err == nil {
		t.Fatal("legacy soft-generated environment variable bypassed a failing gate")
	}
}

func TestExecuteGatePlanRejectsRequiredGateWithoutRunner(t *testing.T) {
	plan := exactCurrentReviewPlan(t, "missing:runner")

	err := executeGatePlan(plan)
	if err == nil {
		t.Fatal("executeGatePlan should reject a required gate without a runner")
	}
	if !strings.Contains(err.Error(), "missing:runner") {
		t.Fatalf("error should name the missing runner, got %v", err)
	}
}

func TestLSPDiagnosticsRunnerAuditsAllDeleted(t *testing.T) {
	root := t.TempDir()
	plan := gatePlan{DiagnosticFiles: []string{
		filepath.Join(root, "deleted-a.go"),
		filepath.Join(root, "deleted-b.go"),
	}}
	runner := gateRunners(plan)["lsp:changed-diagnostics"]
	stderr := captureStderr(t, runner.run)
	want := "[ai-maintenance] lsp diagnostics skip: planned=2 existing=0 reason=all-deleted\n"
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

func TestExistingDiagnosticFilesRejectsNonRegularTarget(t *testing.T) {
	_, _, err := existingDiagnosticFiles([]string{t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "is not a regular file") {
		t.Fatalf("non-regular target error = %v", err)
	}
}

func TestFrontendChangedTestFilesResolveControlledCrossExtensions(t *testing.T) {
	repoRoot := t.TempDir()
	for _, file := range []string{
		"frontend-app/src/pages/chat/components/ChatActionFeedback.js",
		"frontend-app/src/pages/chat/components/ChatActionFeedback.test.jsx",
		"frontend-app/src/shared/model/same.ts",
		"frontend-app/src/shared/model/same.test.ts",
		"frontend-app/src/shared/ui/FromTSX.tsx",
		"frontend-app/src/shared/ui/FromTSX.test.js",
		"frontend-app/src/shared/ui/Multi.jsx",
		"frontend-app/src/shared/ui/Multi.test.js",
		"frontend-app/src/shared/ui/Multi.test.jsx",
		"frontend-app/src/shared/ui/Multi.test.ts",
		"frontend-app/src/shared/ui/Multi.test.tsx",
		"frontend-app/src/shared/ui/Unrelated.test.jsx",
	} {
		writeGateFixtureFile(t, repoRoot, file, "export {};\n")
	}
	got, err := frontendChangedTestFilesInRoot(repoRoot, []string{
		"frontend-app/src/shared/ui/Multi.jsx",
		"frontend-app/src/pages/chat/components/ChatActionFeedback.js",
		"frontend-app/src/shared/model/same.ts",
		"frontend-app/src/shared/ui/FromTSX.tsx",
		"frontend-app/src/shared/ui/Multi.jsx",
	})
	if err != nil {
		t.Fatalf("frontendChangedTestFilesInRoot() error = %v", err)
	}
	want := []string{
		"src/pages/chat/components/ChatActionFeedback.test.jsx",
		"src/shared/model/same.test.ts",
		"src/shared/ui/FromTSX.test.js",
		"src/shared/ui/Multi.test.js",
		"src/shared/ui/Multi.test.jsx",
		"src/shared/ui/Multi.test.ts",
		"src/shared/ui/Multi.test.tsx",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("frontendChangedTestFilesInRoot() = %v, want %v", got, want)
	}
}

func TestFrontendChangedTestFilesFailClosedWhenAnySourceHasNoTest(t *testing.T) {
	repoRoot := t.TempDir()
	writeGateFixtureFile(t, repoRoot, "frontend-app/src/covered.js", "export {};\n")
	writeGateFixtureFile(t, repoRoot, "frontend-app/src/covered.test.js", "export {};\n")
	writeGateFixtureFile(t, repoRoot, "frontend-app/src/missing.js", "export {};\n")

	_, err := frontendChangedTestFilesInRoot(repoRoot, []string{
		"frontend-app/src/covered.js",
		"frontend-app/src/missing.js",
	})
	if err == nil || !strings.Contains(err.Error(), `frontend changed source "frontend-app/src/missing.js" has no matching test files`) {
		t.Fatalf("missing frontend test error = %v", err)
	}
}

func TestFrontendChangedTestFilesRejectsNonRegularCandidates(t *testing.T) {
	tests := []struct {
		name   string
		create func(t *testing.T, repoRoot, candidate string)
	}{
		{
			name: "directory",
			create: func(t *testing.T, repoRoot, candidate string) {
				t.Helper()
				writeGateFixtureFile(t, repoRoot, candidate+"/Unrelated.test.jsx", "export {};\n")
			},
		},
		{
			name: "symlink",
			create: func(t *testing.T, repoRoot, candidate string) {
				t.Helper()
				target := "frontend-app/src/Symlink.actual.jsx"
				writeGateFixtureFile(t, repoRoot, target, "export {};\n")
				if err := os.Symlink("Symlink.actual.jsx", filepath.Join(repoRoot, filepath.FromSlash(candidate))); err != nil {
					t.Fatalf("symlink frontend test candidate: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			stem := strings.ToUpper(test.name[:1]) + test.name[1:]
			source := "frontend-app/src/" + stem + ".js"
			candidate := "frontend-app/src/" + stem + ".test.jsx"
			writeGateFixtureFile(t, repoRoot, source, "export {};\n")
			test.create(t, repoRoot, candidate)

			got, err := frontendChangedTestFilesInRoot(repoRoot, []string{source})
			wantError := `frontend test target "src/` + stem + `.test.jsx" is not a regular file`
			if err == nil || !strings.Contains(err.Error(), wantError) {
				t.Fatalf("non-regular frontend test error = %v, want %q", err, wantError)
			}
			if len(got) != 0 {
				t.Fatalf("non-regular frontend test targets = %v, want empty", got)
			}
		})
	}
}

func captureStderr(t *testing.T, run func() error) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = original }()
	runErr := run()
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	data, readErr := io.ReadAll(reader)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if runErr != nil {
		t.Fatalf("run error = %v", runErr)
	}
	return string(data)
}

func exactCurrentReviewPlan(t *testing.T, gates ...string) gatePlan {
	t.Helper()
	repoRoot := aiMaintenanceRepoRoot(t)
	object, err := resolveReviewObject(repoRoot, reviewObjectOptions{Base: "HEAD", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	return gatePlan{ReviewObject: object, RequiredGates: gates}
}

func TestBuildGatePlanRequiresDiagnosticsGateForGoScripts(t *testing.T) {
	plan := mustBuildGatePlan(t, []string{"scripts/ai_maintenance/main.go"})

	assertStringSetContains(t, plan.RequiredGates, "backend:test_with_guard", "lsp:changed-diagnostics")
	assertStringSetContains(t, plan.DiagnosticFiles, "scripts/ai_maintenance/main.go")
	assertStringSetOmits(t, plan.RequiredGates, "ai-maintenance:self-test")
	assertStringSetContains(t, plan.AffectedGoPackages, "./scripts/ai_maintenance")
}

func TestBuildGatePlanIncludesChangedBackendPackages(t *testing.T) {
	plan := mustBuildGatePlan(t, []string{
		"internal/store/thread/store.go",
		"internal/module/memory/service.go",
		"internal/contract/provider.go",
	})

	assertStringSetContains(t, plan.RequiredGates, "backend:test_with_guard", "capcontract:check")
	assertStringSetOmits(t, plan.RequiredGates, "repo:guard")
	assertStringSetContains(t, plan.AffectedGoPackages,
		"./internal/store/thread",
		"./internal/module/memory",
		"./internal/contract",
	)
	assertStringSetOmits(t, plan.AffectedGoPackages, "./internal/archtest")
	assertStringSetOmits(t, plan.AffectedGoPackages, "./cmd/mcp-lsp", "./cmd/mcp-orch", "./internal/app")

	modulePlan := mustBuildGatePlan(t, []string{"go.mod"})
	assertStringSetContains(t, modulePlan.AffectedGoPackages, "./cmd/mcp-lsp", "./internal/app", "./scripts/ai_maintenance")
}

func TestBuildGatePlanRoutesCapabilityContractProducerInputs(t *testing.T) {
	for _, path := range []string{
		"internal/contract/provider.go",
		"internal/provider/codexapp/support.go",
		"cmd/mcp-orch/orchestration/registry.go",
		"cmd/mcp-orch/tools/position.go",
	} {
		t.Run(path, func(t *testing.T) {
			plan := mustBuildGatePlan(t, []string{path})
			assertStringSetContains(t, plan.RequiredGates, "capcontract:check")
			assertStringSetContains(t, plan.GeneratedFiles, capabilityContractManifest)
		})
	}

	for _, path := range []string{"frontend-app/src/App.jsx", "docs/README.md"} {
		t.Run("unrelated/"+path, func(t *testing.T) {
			plan := mustBuildGatePlan(t, []string{path})
			assertStringSetOmits(t, plan.RequiredGates, "capcontract:check")
			assertStringSetOmits(t, plan.GeneratedFiles, capabilityContractManifest)
		})
	}
}

func TestBuildGatePlanRoutesGateInfrastructureToOwnedChecks(t *testing.T) {
	tests := []struct {
		path  string
		gates []string
	}{
		{"Makefile", []string{"backend:test_with_guard", "codemap:check", "frontend:embed-verify", "project-map:check", "sqlc:verify"}},
		{"scripts/test_with_guard.sh", []string{"backend:test_with_guard"}},
		{"scripts/sqlc_verify_worktree.sh", []string{"ai-maintenance:self-test", "sqlc:verify"}},
		{"scripts/frontend_embed_verify.sh", []string{"ai-maintenance:self-test", "frontend:embed-verify"}},
		{"scripts/refresh_generated_artifacts.sh", []string{"ai-maintenance:self-test", "codemap:check", "project-map:check"}},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			plan := mustBuildGatePlan(t, []string{test.path})
			assertStringSetContains(t, plan.RequiredGates, test.gates...)
			assertStringSetContains(t, plan.RequiredGates, "diff:whitespace")
		})
	}
}

func TestBuildGatePlanRoutesSQLCAndGoModuleInputs(t *testing.T) {
	tests := []struct {
		path        string
		wantBackend bool
	}{
		{"go.mod", true},
		{"go.sum", true},
		{"go.work", true},
		{"go.work.sum", true},
		{"sqlc.yaml", false},
		{"cmd/mcp-orch/sqlc.yaml", false},
		{"sql/queries.sql", false},
		{"cmd/mcp-orch/sql/queries.sql", false},
		{"cmd/mcp-orch/store/sqlc/types_ext.go", true},
		{"internal/platform/db/sqlite/migrations/001.sql", false},
		{"internal/store/thread/store.go", true},
		{"internal/devtools/sqlcconfig/config.go", true},
		{"internal/devtools/sqlcconfig/config_test.go", true},
		{"internal/devtools/gate/executor_sqlc.go", true},
		{"internal/devtools/gate/executor_sqlc_manifest.go", true},
		{"internal/devtools/gate/executor_sqlc_postprocess.go", true},
		{"internal/devtools/gate/executor_mapping.go", true},
		{"internal/devtools/gate/executor.go", true},
		{"internal/devtools/gate/executor_future_identity.go", true},
		{"internal/devtools/cicontract/worker_execution_environment.go", true},
		{"internal/devtools/cicontract/worker_execution_environment_sqlc_test.go", true},
		{"internal/devtools/gateprivate/trusted_git.go", true},
		{"internal/devtools/trustedgitpolicy/policy.go", true},
		{"internal/devtools/trustedgitpolicy/future_helper.go", true},
		{"internal/devtools/gateprivate/trusted_git_future.go", true},
		{"internal/archtest/backend_boundary_evaluator.go", true},
		{"internal/archtest/backend_boundary_sqlc.go", true},
		{"internal/archtest/backend_boundary_registry.go", true},
		{"internal/archtest/guardlib.go", true},
		{"internal/archtest/backend_boundary_sqlc_owner_test.go", true},
		{"internal/archtest/sqlc_bypass_guard_test.go", true},
		{"internal/archtest/migration_sqlc_guard_test.go", true},
		{"internal/archtest/sqlc_guard_fixture_test.go", true},
		{"internal/archtest/sqlc_guard_external_fixture_test.go", true},
		{"scripts/sqlc_config_outputs/main.go", true},
		{"scripts/sqlc_postprocess.sh", false},
		{"scripts/sqlc_verify_worktree.sh", false},
		{"scripts/sqlc_verify_worktree_guard_test.go", true},
		{"Makefile", true},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			plan := mustBuildGatePlan(t, []string{test.path})
			assertStringSetContains(t, plan.RequiredGates, "sqlc:verify", "diff:whitespace")
			if test.wantBackend {
				assertStringSetContains(t, plan.RequiredGates, "backend:test_with_guard")
			} else {
				assertStringSetOmits(t, plan.RequiredGates, "backend:test_with_guard")
			}
		})
	}
}

func TestSQLCRelevantFollowsCentralOutputOwnerParents(t *testing.T) {
	for _, file := range []string{
		sqlcconfig.RootOutputOwnerParent + "/future/store.go",
		sqlcconfig.OrchestratorOutputOwnerParent + "/future/store.go",
	} {
		if !sqlcRelevant(file) {
			t.Fatalf("central SQLC owner path %q did not route sqlc:verify", file)
		}
	}
}

func TestTrustedHostBashResolvesExecutableOnCurrentHost(t *testing.T) {
	bash, err := trustedHostBash()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(bash) {
		t.Fatalf("trusted host Bash is not absolute: %q", bash)
	}
}

func TestBuildGatePlanUsesBackendAsAIMaintenanceSelfTestSuperset(t *testing.T) {
	plan := mustBuildGatePlan(t, []string{"scripts/ai_maintenance/main.go"})
	assertStringSetContains(t, plan.RequiredGates, "backend:test_with_guard")
	assertStringSetOmits(t, plan.RequiredGates, "ai-maintenance:self-test")

	hookPlan := mustBuildGatePlan(t, []string{".githooks/pre-commit"})
	assertStringSetContains(t, hookPlan.RequiredGates, "ai-maintenance:self-test")
}

func TestBuildGatePlanRoutesTurnContractProductionChain(t *testing.T) {
	for _, file := range []string{
		"internal/dto/turn/schema/turn_terminal.v2.json",
		"internal/provider/shared/terminal_outcome.go",
		"frontend-app/package.json",
		"frontend-app/src/shared/api/backend/backendApiFactoryThread.js",
		"scripts/turncontract/main.go",
	} {
		plan := mustBuildGatePlan(t, []string{file})
		assertStringSetContains(t, plan.RequiredGates, "turncontract:verify")
	}
}

func TestBuildGatePlanRoutesFrontendStaticGuardInputs(t *testing.T) {
	for _, file := range []string{
		"frontend-app/package.json",
		"frontend-app/scripts/frontend-state-ownership-registry.json",
		"frontend-app/scripts/frontend-dependency-direction-guard.mjs",
		"frontend-app/src/entities/client/model/helpers/warningRuntime.js",
		"frontend-app/src/pages/chat/ChatPage.jsx",
	} {
		plan := mustBuildGatePlan(t, []string{file})
		assertStringSetContains(t, plan.RequiredGates, "frontend:static-guards")
	}

	plan := mustBuildGatePlan(t, []string{"docs/plans/frontend.md"})
	assertStringSetOmits(t, plan.RequiredGates, "frontend:static-guards")
}

func TestBuildGatePlanRoutesAllProductionGoToTurnContract(t *testing.T) {
	for _, file := range []string{
		"cmd/example/main.go",
		"internal/platform/uistate/projector_handlers.go",
		"pkg/example/consumer.go",
		"scripts/example/producer.go",
	} {
		plan := mustBuildGatePlan(t, []string{file})
		assertStringSetContains(t, plan.RequiredGates, "turncontract:verify")
	}
}

func TestBuildGatePlanDoesNotTreatTestOnlyGoAsTurnContractProduction(t *testing.T) {
	for _, file := range []string{
		"cmd/example/main_test.go",
		"internal/dto/turn/contract_field_guard_test.go",
		"internal/platform/uistate/projector_handlers_test.go",
		"pkg/example/consumer_test.go",
		"scripts/example/producer_test.go",
	} {
		plan := mustBuildGatePlan(t, []string{file})
		assertStringSetOmits(t, plan.RequiredGates, "turncontract:verify")
	}
}

func TestBuildGatePlanRoutesEveryTurnContractRegistryLocator(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "dto", "turn", "schema", "field_consumers.json"))
	if err != nil {
		t.Fatalf("read turn contract consumer registry: %v", err)
	}
	var registry any
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatalf("parse turn contract consumer registry: %v", err)
	}
	paths := map[string]bool{}
	collectRegistryLocatorPaths(registry, paths)
	if len(paths) == 0 {
		t.Fatal("turn contract consumer registry contains no locator paths")
	}
	for locatorPath := range paths {
		plan := mustBuildGatePlan(t, []string{locatorPath})
		assertStringSetContains(t, plan.RequiredGates, "turncontract:verify")
	}
}

func TestLoadTurnContractPathsFailsClosedForInvalidRegistry(t *testing.T) {
	tests := []struct {
		name string
		body *string
		want string
	}{
		{name: "missing", want: "read turn contract consumer registry"},
		{name: "malformed", body: new(`{"version":`), want: "parse turn contract consumer registry"},
		{name: "wrong version", body: new(`{"version":1,"schemas":{},"goChains":[],"goConstants":[],"jsMappers":[]}`), want: "version 2"},
		{name: "wrong structure", body: new(`{"version":2,"schemas":[],"goChains":[],"goConstants":[],"jsMappers":[]}`), want: "schemas"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			if test.body != nil {
				registryPath := filepath.Join(repoRoot, "internal", "dto", "turn", "schema", "field_consumers.json")
				if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
					t.Fatalf("mkdir registry dir: %v", err)
				}
				if err := os.WriteFile(registryPath, []byte(*test.body), 0o644); err != nil {
					t.Fatalf("write registry: %v", err)
				}
			}
			_, err := loadTurnContractPaths(repoRoot, newGatePlanPolicy())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("loadTurnContractPaths() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestNewGatePlanPolicyOwnsMutableDescriptors(t *testing.T) {
	first := newGatePlanPolicy()
	second := newGatePlanPolicy()

	first.aiMaintenanceFiles["Makefile"] = false
	first.coreBackendGatePackages[0] = "mutated"
	first.mcpLSPWorkloadExactFiles["Makefile"] = false
	first.mcpLSPWorkloadPrefixes[0] = "mutated"

	if !second.aiMaintenanceFiles["Makefile"] {
		t.Fatal("AI maintenance descriptors are shared between policies")
	}
	if second.coreBackendGatePackages[0] == "mutated" {
		t.Fatal("core backend package descriptors are shared between policies")
	}
	if !second.mcpLSPWorkloadExactFiles["Makefile"] {
		t.Fatal("MCP LSP exact-file descriptors are shared between policies")
	}
	if second.mcpLSPWorkloadPrefixes[0] == "mutated" {
		t.Fatal("MCP LSP prefix descriptors are shared between policies")
	}
}

func collectRegistryLocatorPaths(value any, paths map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		if locatorPath, ok := typed["path"].(string); ok && locatorPath != "" {
			paths[locatorPath] = true
		}
		for _, child := range typed {
			collectRegistryLocatorPaths(child, paths)
		}
	case []any:
		for _, child := range typed {
			collectRegistryLocatorPaths(child, paths)
		}
	}
}

func TestGatePlanProducerMatchesRunnerRegistry(t *testing.T) {
	producerGates := map[string]bool{}
	for _, files := range [][]string{
		{"scripts/ai_maintenance/main.go"},
		{".githooks/pre-commit"},
		{"frontend-app/src/App.jsx"},
		{"frontend-app/src/shared/ui/runUIAction.js"},
		{"internal/store/thread/store.go"},
		{"internal/contract/provider.go"},
		{"docs/doc/codemap/ai-index.json"},
		{"internal/provider/codexapp/session.go"},
		{"internal/dto/turn/terminal.go"},
		{".github/workflows/release.yml"},
		{"scripts/package_linux.sh"},
		{"frontend-app/tests/e2e/business-flows.spec.js"},
		{"internal/module/thread/session_test.go"},
		{"docs/automation/全仓夜间门禁健康巡检协议.md"},
		{"cmd/mcp-lsp/runtime.go"},
		{"scripts/mcp_lsp_workload_catalog.json"},
		{"Makefile"},
	} {
		for _, gate := range mustBuildGatePlan(t, files).RequiredGates {
			producerGates[gate] = true
		}
	}

	runners := gateRunners(mustBuildGatePlan(t, []string{"README.md"}))
	assertRegistryMatchesProducer(t, producerGates, runners, "runner", "diff:whitespace")
}
