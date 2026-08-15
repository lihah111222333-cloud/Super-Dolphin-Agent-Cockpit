package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestReviewObjectBindsChangedFilesWhitespaceAndMaterialization(t *testing.T) {
	t.Run("range ignores ambient dirt", testRangeReviewObject)
	t.Run("range freezes merge base", testRangeReviewObjectUsesMergeBase)
	t.Run("staged excludes unstaged and untracked", testStagedReviewObject)
	t.Run("staged preserves effective alternate index", testStagedReviewObjectUsesEffectiveIndex)
	t.Run("staged whitespace is checked", testStagedWhitespaceReviewObject)
	t.Run("worktree captures staged unstaged and untracked once", testWorktreeReviewObject)
	t.Run("paths only cannot execute and modes are exclusive", testReviewObjectModeGuards)
	t.Run("runner executes in materialized candidate", testReviewObjectRunnerMaterialization)
	t.Run("plan reads candidate rules", testReviewObjectPlanUsesCandidateRules)
	t.Run("frontend seed binding avoids whole-tree scans", testReviewFrontendSeedBindingIsLightweight)
	t.Run("frontend seed binds exact candidate and cleans up", testReviewFrontendSeedBindingBehavior)
	t.Run("git local environment cannot redirect repository", testReviewObjectRejectsAmbientGitRepository)
	t.Run("materialization ignores local checkout hooks", testReviewObjectMaterializationIgnoresLocalHooks)
	t.Run("execution helpers reject ambient fallback", testReviewExecutionHelpersRejectAmbientFallback)
}

func testRangeReviewObject(t *testing.T) {
	repo := newReviewObjectRepository(t)
	writeReviewObjectFile(t, repo, "range.txt", "candidate\n")
	runAIMaintenanceGit(t, repo, "add", "range.txt")
	runAIMaintenanceGit(t, repo, "commit", "--quiet", "-m", "范围候选")
	writeReviewObjectFile(t, repo, "tracked.txt", "ambient bad   \n")
	writeReviewObjectFile(t, repo, "ambient-untracked.txt", "ambient bad   \n")

	object, err := resolveReviewObject(repo, reviewObjectOptions{Base: "HEAD^", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	resolvedCandidateCommit := object.CandidateCommit
	writeReviewObjectFile(t, repo, "late-ref-move.txt", "late ref bad   \n")
	runAIMaintenanceGit(t, repo, "add", "late-ref-move.txt")
	runAIMaintenanceGit(t, repo, "commit", "--quiet", "-m", "移动 HEAD")
	movedHead, err := resolveGitCommit(repo, "HEAD")
	if err != nil || movedHead == resolvedCandidateCommit || object.CandidateCommit != resolvedCandidateCommit {
		t.Fatalf("range commit provenance was not frozen: object=%q moved HEAD=%q error=%v", object.CandidateCommit, movedHead, err)
	}
	assertExactReviewObject(t, object, reviewModeRange)
	assertReviewObjectFiles(t, repo, object, []string{"range.txt"})
	if err := checkReviewObjectWhitespace(repo, object); err != nil {
		t.Fatalf("range whitespace was polluted by ambient dirt: %v", err)
	}
	materialized := materializeReviewObjectForTest(t, repo, object)
	assertReviewObjectFile(t, materialized, "range.txt", "candidate\n")
	assertReviewObjectFile(t, materialized, "tracked.txt", "base\n")
	assertReviewObjectFileAbsent(t, materialized, "ambient-untracked.txt")
	assertReviewObjectFileAbsent(t, materialized, "late-ref-move.txt")
}

func testRangeReviewObjectUsesMergeBase(t *testing.T) {
	repo := newReviewObjectRepository(t)
	common := mustResolveReviewCommit(t, repo, "HEAD")
	runAIMaintenanceGit(t, repo, "switch", "--quiet", "-c", "base-side")
	writeReviewObjectFile(t, repo, "base-only.txt", "base side\n")
	runAIMaintenanceGit(t, repo, "add", "base-only.txt")
	runAIMaintenanceGit(t, repo, "commit", "--quiet", "-m", "基线侧")
	requestedBase := mustResolveReviewCommit(t, repo, "HEAD")
	runAIMaintenanceGit(t, repo, "switch", "--quiet", "-c", "candidate-side", common)
	writeReviewObjectFile(t, repo, "candidate-only.txt", "candidate side\n")
	runAIMaintenanceGit(t, repo, "add", "candidate-only.txt")
	runAIMaintenanceGit(t, repo, "commit", "--quiet", "-m", "候选侧")
	candidate := mustResolveReviewCommit(t, repo, "HEAD")
	object := mustResolveReviewObject(t, repo, reviewObjectOptions{Base: requestedBase, Head: candidate})
	if object.BaseCommit != requestedBase || object.ComparisonBaseCommit != common || object.CandidateCommit != candidate {
		t.Fatalf("range provenance = %#v, want requested=%s merge-base=%s candidate=%s", object, requestedBase, common, candidate)
	}
	assertReviewObjectJSONProvenance(t, object)
	assertReviewObjectFiles(t, repo, object, []string{"candidate-only.txt"})
	emptyTree := mustReviewGitSingleLine(t, repo, "mktree")
	unrelated := mustCreateReviewCommit(t, repo, emptyTree, "", "unrelated root")
	if _, err := resolveReviewObject(repo, reviewObjectOptions{Base: requestedBase, Head: unrelated}); err == nil || !strings.Contains(err.Error(), "merge-base") {
		t.Fatalf("unrelated range did not fail closed: %v", err)
	}
}

func testStagedReviewObject(t *testing.T) {
	repo := newReviewObjectRepository(t)
	writeReviewObjectFile(t, repo, "staged.txt", "staged candidate\n")
	runAIMaintenanceGit(t, repo, "add", "staged.txt")
	writeReviewObjectFile(t, repo, "tracked.txt", "unstaged bad   \n")
	writeReviewObjectFile(t, repo, "untracked.txt", "untracked bad   \n")

	object, err := resolveReviewObject(repo, reviewObjectOptions{Staged: true})
	if err != nil {
		t.Fatal(err)
	}
	assertExactReviewObject(t, object, reviewModeStaged)
	assertReviewObjectFiles(t, repo, object, []string{"staged.txt"})
	if err := checkReviewObjectWhitespace(repo, object); err != nil {
		t.Fatalf("staged whitespace was polluted by unstaged/untracked files: %v", err)
	}
	materialized := materializeReviewObjectForTest(t, repo, object)
	assertReviewObjectFile(t, materialized, "staged.txt", "staged candidate\n")
	assertReviewObjectFile(t, materialized, "tracked.txt", "base\n")
	assertReviewObjectFileAbsent(t, materialized, "untracked.txt")
}

func testStagedReviewObjectUsesEffectiveIndex(t *testing.T) {
	repo := newReviewObjectRepository(t)
	writeReviewObjectFile(t, repo, "alternate-index.txt", "alternate index\n")
	indexPath := filepath.Join(t.TempDir(), "alternate-index")
	environment := replaceEnvironment(reviewGitEnvironment(), "GIT_INDEX_FILE", indexPath)
	if _, err := gitOutput(repo, environment, "read-tree", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(repo, environment, "add", "--", "alternate-index.txt"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_INDEX_FILE", indexPath)
	object, err := resolveReviewObject(repo, reviewObjectOptions{Staged: true})
	if err != nil {
		t.Fatal(err)
	}
	assertReviewObjectFiles(t, repo, object, []string{"alternate-index.txt"})
}

func testStagedWhitespaceReviewObject(t *testing.T) {
	repo := newReviewObjectRepository(t)
	writeReviewObjectFile(t, repo, "staged.txt", "bad staged whitespace   \n")
	runAIMaintenanceGit(t, repo, "add", "staged.txt")

	object, err := resolveReviewObject(repo, reviewObjectOptions{Staged: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkReviewObjectWhitespace(repo, object); err == nil {
		t.Fatal("exact staged whitespace error was not detected")
	}
}

func testWorktreeReviewObject(t *testing.T) {
	repo := newReviewObjectRepository(t)
	writeReviewObjectFile(t, repo, "staged.txt", "staged candidate\n")
	runAIMaintenanceGit(t, repo, "add", "staged.txt")
	writeReviewObjectFile(t, repo, "tracked.txt", "unstaged candidate\n")
	writeReviewObjectFile(t, repo, "untracked.txt", "untracked candidate\n")

	object, err := resolveReviewObject(repo, reviewObjectOptions{Worktree: true})
	if err != nil {
		t.Fatal(err)
	}
	assertExactReviewObject(t, object, reviewModeWorktree)
	assertReviewObjectFiles(t, repo, object, []string{"staged.txt", "tracked.txt", "untracked.txt"})

	writeReviewObjectFile(t, repo, "late-unrelated.txt", "late bad   \n")
	if err := checkReviewObjectWhitespace(repo, object); err != nil {
		t.Fatalf("captured worktree whitespace was polluted after resolution: %v", err)
	}
	materialized := materializeReviewObjectForTest(t, repo, object)
	assertReviewObjectFile(t, materialized, "staged.txt", "staged candidate\n")
	assertReviewObjectFile(t, materialized, "tracked.txt", "unstaged candidate\n")
	assertReviewObjectFile(t, materialized, "untracked.txt", "untracked candidate\n")
	assertReviewObjectFileAbsent(t, materialized, "late-unrelated.txt")
}

func testReviewObjectModeGuards(t *testing.T) {
	plan := gatePlan{
		ReviewObject:  pathsOnlyReviewObject(),
		ChangedFiles:  []string{"README.md"},
		RequiredGates: []string{"diff:whitespace"},
	}
	if err := executeGatePlan(plan); err == nil || !strings.Contains(err.Error(), "paths-only review object is not executable") {
		t.Fatalf("paths-only execution error = %v", err)
	}
	if err := runGates([]string{"--changed-file", "README.md"}); err == nil || !strings.Contains(err.Error(), "paths-only review object is not executable") {
		t.Fatalf("run --changed-file error = %v", err)
	}

	repo := newReviewObjectRepository(t)
	if _, err := resolveReviewObject(repo, reviewObjectOptions{Staged: true, Worktree: true}); err == nil {
		t.Fatal("staged plus worktree modes were accepted")
	}
	if _, err := resolveReviewObject(repo, reviewObjectOptions{Staged: true, Base: "HEAD^"}); err == nil {
		t.Fatal("staged plus range mode were accepted")
	}
}

func testReviewObjectRunnerMaterialization(t *testing.T) {
	repo := newReviewObjectRepository(t)
	writeReviewObjectFile(t, repo, "staged.txt", "runner candidate\n")
	runAIMaintenanceGit(t, repo, "add", "staged.txt")
	writeReviewObjectFile(t, repo, "staged.txt", "ambient overwrite\n")

	object, err := resolveReviewObject(repo, reviewObjectOptions{Staged: true})
	if err != nil {
		t.Fatal(err)
	}
	files, err := changedFilesFromReviewObject(repo, object)
	if err != nil {
		t.Fatal(err)
	}
	plan := gatePlan{ReviewObject: object, ChangedFiles: files}
	plan.RequiredGates = []string{"test:materialized-probe"}
	var executedRoot string
	factory := func(executionPlan gatePlan) map[string]gateRunner {
		executedRoot = executionPlan.executionRoot
		return map[string]gateRunner{"test:materialized-probe": {run: func() error {
			return runCommand(gateExecutionDir(executionPlan, ""), "sh", "-c",
				`test "$PWD" = "$1" && test "$(cat staged.txt)" = "runner candidate"`, "probe", executionPlan.executionRoot)
		}}}
	}
	if err := executeGatePlanWithRunnerFactory(repo, plan, factory); err != nil {
		t.Fatal(err)
	}
	if executedRoot == "" || executedRoot == repo {
		t.Fatalf("runner execution root = %q, want isolated materialization distinct from %q", executedRoot, repo)
	}
	if _, err := os.Stat(executedRoot); !os.IsNotExist(err) {
		t.Fatalf("materialized runner root was not cleaned: %v", err)
	}
}

func testReviewObjectPlanUsesCandidateRules(t *testing.T) {
	repository := aiMaintenanceRepoRoot(t)
	cleanObject, err := resolveReviewObject(repository, reviewObjectOptions{Base: "HEAD", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	fixture := materializeReviewObjectForTest(t, repository, cleanObject)
	policyPath := filepath.Join(fixture, "scripts", "capcontract", "main.go")
	original, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, append(original, []byte("\n// staged candidate policy remains parseable.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	runAIMaintenanceGit(t, fixture, "add", "scripts/capcontract/main.go")
	if err := os.WriteFile(policyPath, []byte("package main\nfunc defaultCapabilityRoots() []string {\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stagedObject, err := resolveReviewObject(fixture, reviewObjectOptions{Staged: true})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildGatePlanForReviewObject(fixture, stagedObject, newGatePlanPolicy())
	if err != nil {
		t.Fatalf("plan read ambient unstaged policy instead of staged candidate: %v", err)
	}
	if plan.ReviewObject != stagedObject || !slices.Contains(plan.ChangedFiles, "scripts/capcontract/main.go") {
		t.Fatalf("candidate-bound plan = %#v files=%v", plan.ReviewObject, plan.ChangedFiles)
	}
}

func testReviewFrontendSeedBindingIsLightweight(t *testing.T) {
	source, err := os.ReadFile("review_dependencies.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{"filepath.WalkDir", "os.Link(", "copyReviewDependencyFile", "dependencyIntegrityForTree(appRoot)"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("frontend dependency binding retained whole-tree operation %q", forbidden)
		}
	}
	for _, required := range []string{
		"packageLockSha256", "assertHiddenPackageLockClosure", "expected.requiredTools",
		"expectedOptionalLockSha256", "runtimeOnly: true", "binLockSha256",
		"process.env.SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED = seedRoot",
		"candidate package-lock does not match its dependency integrity identity",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("frontend dependency binding missing identity check %q", required)
		}
	}
	if strings.Contains(text, `filepath.Join(repoRoot, "frontend-app", "node_modules")`) {
		t.Error("frontend dependency binding silently retained ambient node_modules fallback")
	}
	if reviewPlanNeedsFrontendDependencies([]string{"frontend:embed-verify"}) {
		t.Error("self-isolating frontend embed verifier should not pre-materialize dependency seed")
	}
}

func testReviewFrontendSeedBindingBehavior(t *testing.T) {
	repository := aiMaintenanceRepoRoot(t)
	sourceSeed := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED"))
	if sourceSeed == "" {
		sourceSeed = filepath.Join(repository, "frontend-app", "node_modules")
	}
	sourceInfo := mustReviewFileInfo(t, sourceSeed)
	t.Setenv("SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED", sourceSeed)
	beforeResidue := reviewTemporaryResidue(t, repository)
	object := mustResolveReviewObject(t, repository, reviewObjectOptions{Base: "HEAD", Head: "HEAD"})
	plan := gatePlan{ReviewObject: object, RequiredGates: []string{"frontend:lint"}}
	testReviewFrontendSeedPositive(t, repository, sourceSeed, sourceInfo, object, plan)
	testReviewFrontendSeedLockMismatch(t, repository, object, plan)
	afterResidue := reviewTemporaryResidue(t, repository)
	if !slices.Equal(beforeResidue, afterResidue) {
		t.Fatalf("frontend dependency residue changed: before=%v after=%v", beforeResidue, afterResidue)
	}
}

func testReviewFrontendSeedPositive(t *testing.T, repository, sourceSeed string, sourceInfo os.FileInfo, object reviewObject, plan gatePlan) {
	executionRoot, cleanupObject := mustMaterializeReviewObjectWithCleanup(t, repository, object)
	cleanupDependencies, err := prepareReviewExecutionDependencies(repository, executionRoot, plan)
	if err != nil {
		t.Fatalf("bind controlled frontend dependency seed: %v", err)
	}
	seedRoot := assertNormalizedReviewDependencyOverlay(t, repository, sourceSeed, executionRoot)
	if err := cleanupDependencies(); err != nil {
		t.Fatalf("cleanup candidate dependency overlay seed: %v", err)
	}
	if _, err := os.Stat(seedRoot); !os.IsNotExist(err) {
		t.Fatalf("normalized candidate dependency seed residue remains: %v", err)
	}
	afterSourceInfo := mustReviewFileInfo(t, sourceSeed)
	if !os.SameFile(sourceInfo, afterSourceInfo) {
		t.Fatalf("controlled source seed was changed by cleanup: info=%v", afterSourceInfo)
	}
	cleanupObject()
	if _, err := os.Stat(executionRoot); !os.IsNotExist(err) {
		t.Fatalf("positive dependency review object residue remains: %v", err)
	}
}

func assertNormalizedReviewDependencyOverlay(t *testing.T, repository, sourceSeed, executionRoot string) string {
	t.Helper()
	overlayEntry := filepath.Join(executionRoot, "frontend-app", "node_modules", "eslint")
	entryInfo, err := os.Lstat(overlayEntry)
	if err != nil || entryInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("candidate dependency overlay entry is not a symlink: info=%v error=%v", entryInfo, err)
	}
	target, err := os.Readlink(overlayEntry)
	if err != nil {
		t.Fatal(err)
	}
	seedRoot := filepath.Dir(target)
	worktreeRoot := filepath.Join(repository, ".worktrees")
	if seedRoot == sourceSeed || validateReviewWorktreePath(worktreeRoot, seedRoot) != nil {
		t.Fatalf("candidate dependency overlay target = %s, want normalized view under %s", seedRoot, worktreeRoot)
	}
	assertPhysicalReviewDependencyRoot(t, filepath.Join(seedRoot, "fsevents"))
	assertPhysicalReviewDependencyRoot(t, filepath.Join(seedRoot, "@playwright", "test"))
	if _, err := os.ReadFile(filepath.Join(seedRoot, "eslint", "bin", "eslint.js")); err != nil {
		t.Fatalf("required tool is unreadable through normalized seed: %v", err)
	}
	return seedRoot
}

func assertPhysicalReviewDependencyRoot(t *testing.T, root string) {
	t.Helper()
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("normalized dependency package root is not physical: path=%s info=%v error=%v", root, info, err)
	}
}

func testReviewFrontendSeedLockMismatch(t *testing.T, repository string, object reviewObject, plan gatePlan) {
	mismatchRoot, cleanupMismatch := mustMaterializeReviewObjectWithCleanup(t, repository, object)
	lockPath := filepath.Join(mismatchRoot, "frontend-app", "package-lock.json")
	lock, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, append(lock, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareReviewExecutionDependencies(repository, mismatchRoot, plan); err == nil {
		t.Fatalf("package-lock mismatch did not fail closed: %v", err)
	}
	cleanupMismatch()
}

func testReviewObjectRejectsAmbientGitRepository(t *testing.T) {
	repository := newReviewObjectRepository(t)
	wantCommit, err := resolveGitCommit(repository, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	foreign := newReviewObjectRepository(t)
	writeReviewObjectFile(t, foreign, "foreign.txt", "foreign\n")
	runAIMaintenanceGit(t, foreign, "add", "foreign.txt")
	runAIMaintenanceGit(t, foreign, "commit", "--quiet", "-m", "外部仓库")
	t.Setenv("GIT_DIR", filepath.Join(foreign, ".git"))
	t.Setenv("GIT_WORK_TREE", foreign)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(foreign, ".git", "foreign-index"))
	object, err := resolveReviewObject(repository, reviewObjectOptions{Base: "HEAD", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if object.BaseCommit != wantCommit || object.CandidateCommit != wantCommit {
		t.Fatalf("ambient Git repository redirected exact review object: %#v want commit=%s", object, wantCommit)
	}
	for _, commandName := range []string{"git", "go"} {
		for _, entry := range gateCommandEnvironment(commandName) {
			if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_WORK_TREE=") || strings.HasPrefix(entry, "GIT_INDEX_FILE=") {
				t.Fatalf("%s runner retained repository-redirect environment %q", commandName, entry)
			}
		}
	}
}

func testReviewObjectMaterializationIgnoresLocalHooks(t *testing.T) {
	repository := newReviewObjectRepository(t)
	hookPath := filepath.Join(repository, ".git", "hooks", "post-checkout")
	hook := []byte("#!/bin/sh\nprintf 'polluted by local hook\\n' > tracked.txt\n")
	if err := os.WriteFile(hookPath, hook, 0o755); err != nil {
		t.Fatal(err)
	}
	object, err := resolveReviewObject(repository, reviewObjectOptions{Base: "HEAD", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	materialized := materializeReviewObjectForTest(t, repository, object)
	assertReviewObjectFile(t, materialized, "tracked.txt", "base\n")
}

func testReviewExecutionHelpersRejectAmbientFallback(t *testing.T) {
	testMaterializedExecutionRootGuards(t)
	if _, err := backendTestWithGuardArgs(gatePlan{ChangedFiles: []string{"internal/app/app.go"}}); err == nil || !strings.Contains(err.Error(), "exact candidate execution root is required") {
		t.Fatalf("backend execution helper ambient fallback error = %v", err)
	}
	frontendPlan := gatePlan{RequiredGates: []string{"frontend:lint"}}
	if _, err := prepareReviewExecutionDependencies(aiMaintenanceRepoRoot(t), "", frontendPlan); err == nil || !strings.Contains(err.Error(), "exact candidate execution root is required") {
		t.Fatalf("frontend dependency helper ambient fallback error = %v", err)
	}
	if _, err := excludeDeferredE2EGoPackages([]string{"./internal/app"}, "scripts/ai_maintenance/deferred_e2e_packages.txt"); err == nil {
		t.Fatal("deferred E2E manifest silently fell back to ambient repository")
	}
}

func testMaterializedExecutionRootGuards(t *testing.T) {
	t.Helper()
	factoryCalled := false
	factory := func(gatePlan) map[string]gateRunner {
		factoryCalled = true
		return nil
	}
	if err := executeMaterializedGatePlan(gatePlan{}, factory); err == nil || !strings.Contains(err.Error(), "exact candidate execution root is required") || factoryCalled {
		t.Fatalf("materialized execution ambient fallback error=%v factoryCalled=%v", err, factoryCalled)
	}
	if err := runWhitespaceCheck(gatePlan{}); err == nil || !strings.Contains(err.Error(), "exact candidate execution root is required") {
		t.Fatalf("whitespace ambient fallback error = %v", err)
	}
}

func TestExternalEvidenceControllerSurfaceIsRetired(t *testing.T) {
	t.Run("CLI", testExternalEvidenceCLIIsRetired)
	t.Run("plan fields", testExternalEvidencePlanFieldsAreRetired)
	t.Run("parser", testExternalEvidenceParserIsRetired)
}

func testExternalEvidenceCLIIsRetired(t *testing.T) {
	if err := runMain([]string{"validate-evidence"}); err == nil || !strings.Contains(err.Error(), `unknown subcommand "validate-evidence"`) {
		t.Errorf("validate-evidence retirement error = %v", err)
	}
	if err := runGates([]string{"--print-plan", "--changed-file", "README.md", "--evidence", "agent-report.md"}); err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -evidence") {
		t.Errorf("--evidence retirement error = %v", err)
	}
}

func testExternalEvidencePlanFieldsAreRetired(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "plan-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err := runPlan([]string{"--changed-file", "README.md"}, output); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	if err := json.NewDecoder(output).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{"required_evidence", "requires_evidence_doc"} {
		if _, ok := plan[retired]; ok {
			t.Errorf("plan retained external evidence field %q", retired)
		}
	}
}

func testExternalEvidenceParserIsRetired(t *testing.T) {
	if _, err := os.Stat("evidence.go"); !os.IsNotExist(err) {
		t.Errorf("external evidence parser still exists: %v", err)
	}
	if newGatePlanPolicy().mcpLSPWorkloadExactFiles["scripts/ai_maintenance/evidence.go"] {
		t.Error("MCP LSP route still references retired evidence parser")
	}
	diagnosticsPlan, err := os.ReadFile("diagnostics_plan.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(diagnosticsPlan), "func sourceLike(") {
		t.Error("external evidence source-like validator residue still exists")
	}
}

func mustResolveReviewCommit(t *testing.T, repository, revision string) string {
	t.Helper()
	commit, err := resolveGitCommit(repository, revision)
	if err != nil {
		t.Fatal(err)
	}
	return commit
}

func mustResolveReviewObject(t *testing.T, repository string, options reviewObjectOptions) reviewObject {
	t.Helper()
	object, err := resolveReviewObject(repository, options)
	if err != nil {
		t.Fatal(err)
	}
	return object
}

func mustReviewGitSingleLine(t *testing.T, repository string, args ...string) string {
	t.Helper()
	value, err := gitSingleLine(repository, nil, args...)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustCreateReviewCommit(t *testing.T, repository, tree, parent, message string) string {
	t.Helper()
	commit, err := createReviewCommit(repository, tree, parent, message)
	if err != nil {
		t.Fatal(err)
	}
	return commit
}

func mustReviewFileInfo(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func mustMaterializeReviewObjectWithCleanup(t *testing.T, repository string, object reviewObject) (string, func()) {
	t.Helper()
	root, cleanup, err := materializeReviewObject(repository, object)
	if err != nil {
		t.Fatal(err)
	}
	cleaned := false
	cleanupOnce := func() {
		if cleaned {
			return
		}
		cleaned = true
		if err := cleanup(); err != nil {
			t.Errorf("cleanup exact review object: %v", err)
		}
	}
	t.Cleanup(cleanupOnce)
	return root, cleanupOnce
}

func reviewTemporaryResidue(t *testing.T, repository string) []string {
	t.Helper()
	var residue []string
	for _, pattern := range []string{"ai-maintenance-review-*", ".ai-maintenance-*"} {
		matches, err := filepath.Glob(filepath.Join(repository, ".worktrees", pattern))
		if err != nil {
			t.Fatal(err)
		}
		residue = append(residue, matches...)
	}
	slices.Sort(residue)
	return residue
}

func newReviewObjectRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runAIMaintenanceGit(t, repo, "init", "--quiet")
	runAIMaintenanceGit(t, repo, "config", "user.name", "ai-maintenance-test")
	runAIMaintenanceGit(t, repo, "config", "user.email", "ai-maintenance@example.invalid")
	writeReviewObjectFile(t, repo, "range.txt", "base\n")
	writeReviewObjectFile(t, repo, "staged.txt", "base\n")
	writeReviewObjectFile(t, repo, "tracked.txt", "base\n")
	runAIMaintenanceGit(t, repo, "add", ".")
	runAIMaintenanceGit(t, repo, "commit", "--quiet", "-m", "初始化")
	return repo
}

func writeReviewObjectFile(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertExactReviewObject(t *testing.T, object reviewObject, mode string) {
	t.Helper()
	if object.Mode != mode || !object.Executable || object.BaseTree == "" || object.CandidateTree == "" || object.BaseCommit == "" {
		t.Fatalf("review object = %#v, want executable %s with exact tree pair", object, mode)
	}
	if (mode == reviewModeRange) != (object.CandidateCommit != "") {
		t.Fatalf("review object = %#v, range must freeze candidate commit and staged/worktree must leave it empty", object)
	}
	if (mode == reviewModeRange) != (object.ComparisonBaseCommit != "") {
		t.Fatalf("review object = %#v, range must freeze merge-base and staged/worktree must leave it empty", object)
	}
}

func assertReviewObjectJSONProvenance(t *testing.T, object reviewObject) {
	t.Helper()
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"base_commit"`, `"comparison_base_commit"`, `"candidate_commit"`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("range review object JSON omitted provenance field %s: %s", field, encoded)
		}
	}
}

func assertReviewObjectFiles(t *testing.T, repo string, object reviewObject, want []string) {
	t.Helper()
	files, err := changedFilesFromReviewObject(repo, object)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, want) {
		t.Fatalf("changed files = %v, want %v", files, want)
	}
	plan, err := buildGatePlanForRepoAndObject(aiMaintenanceRepoRoot(t), files, object, newGatePlanPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if plan.ReviewObject != object || !slices.Equal(plan.ChangedFiles, want) {
		t.Fatalf("plan review binding = %#v files=%v, want object=%#v files=%v", plan.ReviewObject, plan.ChangedFiles, object, want)
	}
}

func materializeReviewObjectForTest(t *testing.T, repo string, object reviewObject) string {
	t.Helper()
	root, cleanup, err := materializeReviewObject(repo, object)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("cleanup materialized review object: %v", err)
		}
	})
	return root
}

func assertReviewObjectFile(t *testing.T, root, relative, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", relative, data, want)
	}
}

func assertReviewObjectFileAbsent(t *testing.T, root, relative string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); !os.IsNotExist(err) {
		t.Fatalf("%s unexpectedly materialized: %v", relative, err)
	}
}
