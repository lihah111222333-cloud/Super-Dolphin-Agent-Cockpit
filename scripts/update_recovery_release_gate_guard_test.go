package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	invokesExec   bool
	namesVerifier bool
	skips         bool
}

func (evidence *windowsVerifierInvocationEvidence) Visit(node ast.Node) ast.Visitor {
	switch value := node.(type) {
	case *ast.CallExpr:
		evidence.invokesExec = evidence.invokesExec || updateRecoveryCallMatches(value, "exec", "Command")
		evidence.skips = evidence.skips || updateRecoveryCallPrefixMatches(value, "t", "Skip")
	case *ast.BasicLit:
		evidence.namesVerifier = evidence.namesVerifier || strings.Contains(value.Value, "verify_packaged_app_windows.ps1")
	}
	return evidence
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

func TestUpdateRecoveryReleaseGateIsExposedByMake(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	makefile := updateRecoveryReleaseGateReadFile(t, filepath.Join(root, "Makefile"))

	want := "release-update-gate:\n\t./scripts/update_recovery_release_gate.sh"
	if !strings.Contains(makefile, want) {
		t.Fatalf("Makefile missing runnable release-update-gate target %q", want)
	}
}

func TestUpdateRecoveryReleaseGateCIRequiresNativeMacOSAndWindowsEvidence(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	workflow := updateRecoveryReleaseGateReadFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))

	macOSJob := updateRecoveryWorkflowJob(t, workflow, "update-recovery-release-gate-macos")
	for _, want := range []string{
		"runs-on: macos-latest",
		"run: make release-update-gate",
	} {
		if !strings.Contains(macOSJob, want) {
			t.Fatalf("macOS update recovery job missing %q", want)
		}
	}
	windowsJob := updateRecoveryWorkflowJob(t, workflow, "update-recovery-release-gate-windows")
	for _, want := range []string{
		"runs-on: windows-latest",
		"./scripts/test_with_guard.sh ./cmd/super-dolphin-updater -run 'Test(Guard|ConfigureGuard)' -count=1",
		"./scripts/test_with_guard.sh ./cmd/super-dolphin-guard -run 'TestGuard.*(Rollback|Probation|PIDReuse|Termination)' -count=1",
		"./scripts/test_with_guard.sh ./scripts -run '^TestWindowsPackageVerifierAcceptsRealFixture$' -count=1",
	} {
		if !strings.Contains(windowsJob, want) {
			t.Fatalf("Windows update recovery job missing %q", want)
		}
	}
	crossPlatform := updateRecoveryWorkflowJob(t, workflow, "cross-platform-smoke")
	if !strings.Contains(crossPlatform, "needs: [commit-guard, update-recovery-release-gate-macos, update-recovery-release-gate-windows]") {
		t.Fatal("cross-platform smoke must depend on native macOS and Windows recovery gates")
	}
}

func TestUpdateRecoveryReleaseGateCIMarksLinuxAsSupplemental(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	workflow := updateRecoveryReleaseGateReadFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))

	linuxJob := updateRecoveryWorkflowJob(t, workflow, "update-recovery-release-gate-linux-supplemental")
	for _, want := range []string{
		"runs-on: ubuntu-latest",
		"continue-on-error: true",
		"name: Supplemental Linux update recovery evidence",
		"run: make release-update-gate",
	} {
		if !strings.Contains(linuxJob, want) {
			t.Fatalf("CI supplemental Linux evidence missing %q", want)
		}
	}
}

func TestSQLitePackagingSmokeDependsOnNativeUpdateRecoveryGates(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	workflow := updateRecoveryReleaseGateReadFile(t, filepath.Join(root, ".github", "workflows", "sqlite-release-gates.yml"))
	macOSJob := updateRecoveryWorkflowJob(t, workflow, "update-recovery-release-gate-macos")
	if !strings.Contains(macOSJob, "runs-on: macos-latest") || !strings.Contains(macOSJob, "run: make release-update-gate") {
		t.Fatal("SQLite release workflow macOS job must run the full update recovery gate")
	}
	windowsJob := updateRecoveryWorkflowJob(t, workflow, "update-recovery-release-gate-windows")
	for _, want := range []string{"runs-on: windows-latest", "TestGuard.*(Rollback|Probation|PIDReuse|Termination)", "TestWindowsPackageVerifierAcceptsRealFixture"} {
		if !strings.Contains(windowsJob, want) {
			t.Fatalf("SQLite release workflow Windows gate missing %q", want)
		}
	}
	linuxJob := updateRecoveryWorkflowJob(t, workflow, "update-recovery-release-gate-linux-supplemental")
	if !strings.Contains(linuxJob, "continue-on-error: true") || !strings.Contains(linuxJob, "run: make release-update-gate") {
		t.Fatal("SQLite release workflow Linux evidence must remain supplemental")
	}
	packagingJob := updateRecoveryWorkflowJob(t, workflow, "sqlite-packaging-smoke")
	wantNeeds := "needs: [update-recovery-release-gate-macos, update-recovery-release-gate-windows]"
	if !strings.Contains(packagingJob, wantNeeds) {
		t.Fatalf("SQLite packaging smoke missing native update recovery dependencies %q", wantNeeds)
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
	if !evidence.invokesExec || !evidence.namesVerifier || evidence.skips {
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
	for _, line := range strings.Split(script, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "./scripts/test_with_guard.sh" {
			continue
		}
		for _, field := range fields[1:] {
			if field == packagePath {
				return true
			}
		}
	}
	return false
}

func updateRecoveryPackageTestNames(t *testing.T, dir string) []string {
	t.Helper()
	packages, err := parser.ParseDir(token.NewFileSet(), dir, func(info os.FileInfo) bool {
		return strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse tests under %s: %v", dir, err)
	}
	var names []string
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
					names = append(names, fn.Name.Name)
				}
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

func updateRecoveryWorkflowJob(t *testing.T, workflow, jobName string) string {
	t.Helper()
	lines := strings.Split(workflow, "\n")
	marker := "  " + jobName + ":"
	start := -1
	for index, line := range lines {
		if line == marker {
			start = index
			break
		}
	}
	if start < 0 {
		t.Fatalf("workflow missing job %s", jobName)
	}
	end := len(lines)
	for index := start + 1; index < len(lines); index++ {
		line := lines[index]
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") && strings.HasSuffix(line, ":") {
			end = index
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
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
