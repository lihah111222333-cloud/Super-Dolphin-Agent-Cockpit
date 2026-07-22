package main

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type updateRecoveryTestFamily struct {
	label string
	terms []string
}

type updateRecoveryPackageCoverage struct {
	path     string
	families []updateRecoveryTestFamily
}

type windowsVerifierInvocationEvidence struct {
	invokesVerifierCommand bool
	definesVerifierPath    bool
	skips                  bool
}

func (evidence *windowsVerifierInvocationEvidence) Visit(node ast.Node) ast.Visitor {
	switch value := node.(type) {
	case *ast.CallExpr:
		if updateRecoveryCallMatches(value, "exec", "Command") {
			evidence.invokesVerifierCommand = evidence.invokesVerifierCommand || updateRecoveryCommandUsesVerifier(value)
		}
		evidence.skips = evidence.skips || updateRecoveryCallPrefixMatches(value, "t", "Skip")
	case *ast.AssignStmt:
		evidence.definesVerifierPath = evidence.definesVerifierPath || updateRecoveryDefinesVerifierPath(value)
	}
	return evidence
}

type updateRecoveryWorkflow struct {
	Jobs map[string]updateRecoveryWorkflowJob `yaml:"jobs"`
}

type updateRecoveryWorkflowJob struct {
	Needs           yaml.Node                    `yaml:"needs"`
	RunsOn          string                       `yaml:"runs-on"`
	ContinueOnError bool                         `yaml:"continue-on-error"`
	Steps           []updateRecoveryWorkflowStep `yaml:"steps"`
}

type updateRecoveryWorkflowStep struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
}

func updateRecoveryCallMatches(call *ast.CallExpr, receiver, method string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	ident, identOK := selectorXIdent(selector)
	return ok && identOK && ident.Name == receiver && selector.Sel.Name == method
}

func updateRecoveryCallPrefixMatches(call *ast.CallExpr, receiver, methodPrefix string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	ident, identOK := selectorXIdent(selector)
	return ok && identOK && ident.Name == receiver && strings.HasPrefix(selector.Sel.Name, methodPrefix)
}

func updateRecoveryCommandUsesVerifier(call *ast.CallExpr) bool {
	if len(call.Args) < 6 {
		return false
	}
	executable, executableOK := call.Args[0].(*ast.Ident)
	fileFlag, fileFlagOK := call.Args[4].(*ast.BasicLit)
	verifier, verifierOK := call.Args[5].(*ast.Ident)
	return executableOK && executable.Name == "powershell" && fileFlagOK && fileFlag.Value == `"-File"` && verifierOK && verifier.Name == "verifier"
}

func updateRecoveryDefinesVerifierPath(assignment *ast.AssignStmt) bool {
	if len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return false
	}
	ident, ok := assignment.Lhs[0].(*ast.Ident)
	call, callOK := assignment.Rhs[0].(*ast.CallExpr)
	if !ok || ident.Name != "verifier" || !callOK || !updateRecoveryCallMatches(call, "filepath", "Join") {
		return false
	}
	for _, argument := range call.Args {
		literal, literalOK := argument.(*ast.BasicLit)
		if literalOK && literal.Value == `"verify_packaged_app_windows.ps1"` {
			return true
		}
	}
	return false
}

func TestUpdateRecoveryReleaseGateUsesGuardForEveryRequiredPackage(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	script := updateRecoveryReleaseGateReadFile(t, filepath.Join(root, "scripts", "update_recovery_release_gate.sh"))

	for _, want := range []string{
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		`root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"`,
		`cd "$root"`,
		"./scripts/test_with_guard.sh ./internal/platform/appupdaterecovery ./cmd/super-dolphin-updater ./cmd/super-dolphin-guard ./internal/app -count=1",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("update recovery release gate missing %q", want)
		}
	}
	if strings.Contains(script, "go test") {
		t.Fatal("update recovery release gate must use test_with_guard, not direct go test")
	}
}

func TestUpdateRecoveryReleaseGateRunsFromArbitraryWorkingDirectory(t *testing.T) {
	fixture := t.TempDir()
	scriptsDir := filepath.Join(fixture, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := updateRecoveryReleaseGateReadFile(t, filepath.Join(updateRecoveryReleaseGateRepoRoot(t), "scripts", "update_recovery_release_gate.sh"))
	updateRecoveryReleaseGateWriteFile(t, filepath.Join(scriptsDir, "update_recovery_release_gate.sh"), source, 0o755)
	logPath := filepath.Join(fixture, "guard.log")
	fakeGuard := "#!/usr/bin/env bash\nset -euo pipefail\nprintf '%s\\n' \"$PWD\" > \"$UPDATE_RECOVERY_GATE_LOG\"\nprintf '%s\\n' \"$*\" >> \"$UPDATE_RECOVERY_GATE_LOG\"\n"
	updateRecoveryReleaseGateWriteFile(t, filepath.Join(scriptsDir, "test_with_guard.sh"), fakeGuard, 0o755)

	cmd := exec.Command("bash", filepath.Join(scriptsDir, "update_recovery_release_gate.sh"))
	cmd.Dir = filepath.Join(fixture, "child")
	if err := os.MkdirAll(cmd.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(), "UPDATE_RECOVERY_GATE_LOG="+logPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run gate from child cwd: %v\n%s", err, output)
	}
	lines := strings.Split(strings.TrimSpace(updateRecoveryReleaseGateReadFile(t, logPath)), "\n")
	if len(lines) != 2 || lines[0] != fixture {
		t.Fatalf("gate invocation log = %#v, want repo root %q plus args", lines, fixture)
	}
	if lines[1] != "./internal/platform/appupdaterecovery ./cmd/super-dolphin-updater ./cmd/super-dolphin-guard ./internal/app -count=1" {
		t.Fatalf("guard args = %q, want complete release package set", lines[1])
	}
}

func TestUpdateRecoveryReleaseGateRequiredTestFamiliesExistAndExecute(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	script := updateRecoveryReleaseGateReadFile(t, filepath.Join(root, "scripts", "update_recovery_release_gate.sh"))
	for _, coverage := range updateRecoveryRequiredCoverage() {
		if !updateRecoveryGateCommandContainsPackage(script, coverage.path) {
			t.Fatalf("release gate command does not execute package %s", coverage.path)
		}
		testNames := updateRecoveryPackageTestNames(t, filepath.Join(root, strings.TrimPrefix(coverage.path, "./")))
		for _, family := range coverage.families {
			if !updateRecoveryTestFamilyExists(testNames, family.terms) {
				t.Fatalf("package %s missing required %s test family; tests=%v", coverage.path, family.label, testNames)
			}
		}
	}
}

func TestUpdateRecoveryPackageTestNamesRejectsIncompatibleBuildTagCoverage(t *testing.T) {
	dir := t.TempDir()
	updateRecoveryReleaseGateWriteFile(t, filepath.Join(dir, "package.go"), "package fixture\n", 0o644)
	updateRecoveryReleaseGateWriteFile(t, filepath.Join(dir, "required_windows_test.go"), "//go:build windows\n\npackage fixture\n\nfunc TestRollbackRestartFakeCoverage(t *testing.T) {}\n", 0o644)

	testNames := updateRecoveryPackageTestNames(t, dir)
	if updateRecoveryTestFamilyExists(testNames, []string{"RollbackRestart"}) {
		t.Fatalf("darwin release gate accepted Windows-only fake coverage: %v", testNames)
	}
}

func TestUpdateRecoveryReleaseGateIsExposedByMake(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	makefile := updateRecoveryReleaseGateReadFile(t, filepath.Join(root, "Makefile"))

	want := "release-update-gate:\n\t./scripts/update_recovery_release_gate.sh"
	if !strings.Contains(makefile, want) {
		t.Fatalf("Makefile missing runnable release-update-gate target %q", want)
	}
}

func TestUpdateRecoveryCIUsesTruthImageCoordinator(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	for _, name := range []string{"ci.yml", "sqlite-release-gates.yml"} {
		workflow := updateRecoveryReleaseGateReadFile(t, filepath.Join(root, ".github", "workflows", name))
		for _, required := range []string{"truth-image-gates:", "Trusted bootstrap coordinator", "workflow-host", "target=/workspace/super-dolphin-checkout,readonly"} {
			if !strings.Contains(workflow, required) {
				t.Fatalf("%s missing truth-image coordinator requirement %q", name, required)
			}
		}
		for _, forbidden := range []string{"make release-update-gate", "test_with_guard.sh", "go test"} {
			if strings.Contains(workflow, forbidden) {
				t.Fatalf("%s runs update recovery CI on the workflow host: %q", name, forbidden)
			}
		}
	}
}

func TestUpdateRecoveryNativeReleaseEvidenceRemainsManualOnly(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	workflow := updateRecoveryReleaseGateReadFile(t, filepath.Join(root, ".github", "workflows", "release.yml"))
	for _, required := range []string{"workflow_dispatch:", "update-recovery-release", "package-macos-arm64:", "package-windows-arm64:", "make release-update-gate"} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("manual native release workflow missing %q", required)
		}
	}
	for _, forbidden := range []string{"\n  pull_request:", "\n  pull_request_target:", "\n  push:"} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("native release evidence workflow must remain manual-only: %q", forbidden)
		}
	}
}

func TestUpdateRecoveryWorkflowCommentsCannotFakeGateEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflow.yml")
	updateRecoveryReleaseGateWriteFile(t, path, "jobs:\n  native-gate:\n    runs-on: windows-latest\n    # run: make release-update-gate\n    steps:\n      - run: |\n          # make release-update-gate\n          echo unrelated\n", 0o644)
	workflow := updateRecoveryParseWorkflow(t, path)
	job := updateRecoveryRequireWorkflowJob(t, workflow, "native-gate")
	if updateRecoveryJobRuns(job, "make release-update-gate") {
		t.Fatal("workflow comments must not count as executable release gate evidence")
	}
}

func TestSQLiteReleaseWorkflowDelegatesNativeGateSelectionToCoordinator(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	workflow := updateRecoveryReleaseGateReadFile(t, filepath.Join(root, ".github", "workflows", "sqlite-release-gates.yml"))
	for _, required := range []string{"truth-image-gates:", "docker pull --platform=linux/amd64", "docker run --rm", "workflow-host"} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("SQLite release workflow missing coordinator delegation %q", required)
		}
	}
	for _, obsoleteJob := range []string{"update-recovery-release-gate-macos:", "update-recovery-release-gate-windows:", "sqlite-packaging-smoke:"} {
		if strings.Contains(workflow, obsoleteJob) {
			t.Fatalf("SQLite release workflow retained host gate job %q", obsoleteJob)
		}
	}
}

func TestWindowsPackageVerifierFixtureInvokesRealPowerShellVerifier(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	path := filepath.Join(root, "scripts", "windows_package_verifier_fixture_windows_test.go")
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		t.Fatalf("parse Windows verifier fixture: %v", err)
	}
	testFunc := updateRecoveryFindFunction(file, "TestWindowsPackageVerifierAcceptsRealFixture")
	if testFunc == nil {
		t.Fatal("Windows verifier fixture test function is missing")
	}
	evidence := &windowsVerifierInvocationEvidence{}
	ast.Walk(evidence, testFunc.Body)
	if !evidence.invokesVerifierCommand || !evidence.definesVerifierPath || evidence.skips {
		t.Fatalf("Windows fixture must invoke the real verifier without skip-success: %+v", evidence)
	}
	source := updateRecoveryReleaseGateReadFile(t, path)
	for _, required := range updateRecoveryWindowsVerifierFixturePaths() {
		if !strings.Contains(source, required) {
			t.Fatalf("Windows verifier fixture missing required package path %q", required)
		}
	}
}

func updateRecoveryReleaseGateRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, ".."))
}

func updateRecoveryReleaseGateReadFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read required file %s: %v", path, err)
	}
	return string(body)
}

func updateRecoveryReleaseGateWriteFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

func updateRecoveryRequiredCoverage() []updateRecoveryPackageCoverage {
	family := func(label string, terms ...string) updateRecoveryTestFamily {
		return updateRecoveryTestFamily{label: label, terms: terms}
	}
	return []updateRecoveryPackageCoverage{
		{path: "./internal/platform/appupdaterecovery", families: []updateRecoveryTestFamily{family("RollbackRestart", "RollbackRestart"), family("Artifact", "Artifact"), family("PackageTrust", "PackageTrust"), family("Digest", "Digest")}},
		{path: "./cmd/super-dolphin-updater", families: []updateRecoveryTestFamily{family("Probation", "Probation"), family("Rollback", "Rollback"), family("Signature", "Signature"), family("Restart", "Restart")}},
		{path: "./cmd/super-dolphin-guard", families: []updateRecoveryTestFamily{family("Rollback", "Rollback"), family("Probation", "Probation"), family("PIDReuse", "PIDReuse"), family("Termination", "Termination")}},
		{path: "./internal/app", families: []updateRecoveryTestFamily{family("RecoveryRestore", "RecoveryRestore"), family("RecoveryDigest", "Recovery", "Digest")}},
	}
}

func updateRecoveryGateCommandContainsPackage(script, packagePath string) bool {
	for line := range strings.SplitSeq(script, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "./scripts/test_with_guard.sh" {
			continue
		}
		if slices.Contains(fields[1:], packagePath) {
			return true
		}
	}
	return false
}

func updateRecoveryPackageTestNames(t *testing.T, dir string) []string {
	t.Helper()
	buildContext := build.Default
	buildContext.GOOS = "darwin"
	buildContext.GOARCH = "arm64"
	buildContext.CgoEnabled = true
	packageInfo, err := buildContext.ImportDir(dir, build.ImportComment)
	if err != nil {
		t.Fatalf("enumerate darwin/arm64 tests under %s: %v", dir, err)
	}
	var names []string
	testFiles := append(slices.Clone(packageInfo.TestGoFiles), packageInfo.XTestGoFiles...)
	for _, name := range testFiles {
		file, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
		if parseErr != nil {
			t.Fatalf("parse darwin/arm64 test %s: %v", name, parseErr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				names = append(names, fn.Name.Name)
			}
		}
	}
	return names
}

func updateRecoveryTestFamilyExists(testNames, terms []string) bool {
	for _, name := range testNames {
		matches := true
		for _, term := range terms {
			matches = matches && strings.Contains(name, term)
		}
		if matches {
			return true
		}
	}
	return false
}

func updateRecoveryParseWorkflow(t *testing.T, path string) updateRecoveryWorkflow {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workflow %s: %v", path, err)
	}
	var workflow updateRecoveryWorkflow
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatalf("parse workflow %s: %v", path, err)
	}
	return workflow
}

func updateRecoveryRequireWorkflowJob(t *testing.T, workflow updateRecoveryWorkflow, jobName string) updateRecoveryWorkflowJob {
	t.Helper()
	job, ok := workflow.Jobs[jobName]
	if !ok {
		t.Fatalf("workflow missing job %s", jobName)
	}
	return job
}

func updateRecoveryJobRuns(job updateRecoveryWorkflowJob, required string) bool {
	for _, step := range job.Steps {
		for line := range strings.SplitSeq(step.Run, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, required) {
				return true
			}
		}
	}
	return false
}

func updateRecoveryJobNeeds(t *testing.T, job updateRecoveryWorkflowJob) []string {
	t.Helper()
	if job.Needs.Kind == yaml.ScalarNode {
		return []string{job.Needs.Value}
	}
	if job.Needs.Kind != yaml.SequenceNode {
		t.Fatalf("workflow job needs must be a scalar or sequence, got YAML node kind %d", job.Needs.Kind)
	}
	needs := make([]string, 0, len(job.Needs.Content))
	for _, node := range job.Needs.Content {
		needs = append(needs, node.Value)
	}
	return needs
}

func updateRecoveryFindFunction(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function
		}
	}
	return nil
}

func selectorXIdent(selector *ast.SelectorExpr) (*ast.Ident, bool) {
	if selector == nil {
		return nil, false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ident, ok
}

func updateRecoveryWindowsVerifierFixturePaths() []string {
	return []string{
		"bin/agent-terminal.exe", "bin/mcp-orch.exe", "bin/mcp-lsp.exe",
		"bin/mcp-schema-compiler-helper.exe", "bin/mcp-schema-compiler-helper.exe.manifest.json",
		"bin/mcp-ida.exe", "bin/codex.exe", "bin/ffmpeg.exe", "bin/gopls.exe",
		"runtime-manifest.json", "codex-manifest.json", "lsp/lsp-manifest.json",
		"models.yaml", "run.cmd", "run.ps1", "lsp/node/node.exe",
		"internal/platform/db/sqlite/migrations/001_fixture.sql",
	}
}
