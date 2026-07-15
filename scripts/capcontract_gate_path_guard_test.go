package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type capabilityGateTest struct {
	name string
	run  func(t *testing.T, repoRoot, probe string)
}

func TestCapabilityContractDefaultRootsTriggerEveryGate(t *testing.T) {
	repoRoot, roots := capabilityDefaultRootsForGuardTest(t)
	for _, root := range roots {
		probe := capabilityRootProbe(root)
		t.Run(strings.ReplaceAll(root, "/", "_"), func(t *testing.T) {
			runCapabilityGateTests(t, repoRoot, probe)
		})
	}
}

func runCapabilityGateTests(t *testing.T, repoRoot, probe string) {
	t.Helper()
	tests := []capabilityGateTest{
		{name: "ai-maintenance", run: assertAIMaintenanceCapabilityGate},
		{name: "codex-stop-gate", run: assertCodexStopCapabilityGate},
		{name: "pre-push", run: assertPrePushCapabilityGate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, repoRoot, probe)
		})
	}
}

func assertAIMaintenanceCapabilityGate(t *testing.T, repoRoot, probe string) {
	t.Helper()
	cmd := exec.Command("go", "run", "./scripts/ai_maintenance", "plan", "--changed-file", probe)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("AI maintenance plan failed: %v\n%s", err, out)
	}
	var plan struct {
		RequiredGates []string `json:"required_gates"`
	}
	if err := json.Unmarshal(out, &plan); err != nil {
		t.Fatalf("decode AI maintenance plan: %v\n%s", err, out)
	}
	if !containsExactString(plan.RequiredGates, "capcontract:check") {
		t.Fatalf("%q did not trigger capcontract:check: %#v", probe, plan.RequiredGates)
	}
}

func assertCodexStopCapabilityGate(t *testing.T, repoRoot, probe string) {
	t.Helper()
	changedFile := filepath.Join(t.TempDir(), "changed-files.txt")
	if err := os.WriteFile(changedFile, []byte(probe+"\n"), 0o644); err != nil {
		t.Fatalf("write changed files: %v", err)
	}
	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts", "codex_stop_gate.sh"), "--print-plan")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"CODEX_STOP_GATE_CHANGED_FILES_FILE="+changedFile,
		"CODEX_STOP_GATE_LOG_DIR="+filepath.Join(t.TempDir(), "logs"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Codex Stop plan failed: %v\n%s", err, out)
	}
	if !containsOutputLine(string(out), "capcontract_check make capcontract-check") {
		t.Fatalf("%q did not trigger Codex Stop capcontract check\n%s", probe, out)
	}
}

func assertPrePushCapabilityGate(t *testing.T, _ string, probe string) {
	t.Helper()
	fixture := newPrePushScopeFixture(t)
	writeFixTestGuardFile(t, fixture.root, probe, "capcontract probe\n")
	runFixTestGuardGit(t, fixture.root, "add", probe)
	runFixTestGuardGit(t, fixture.root, "commit", "-m", "test: 更新 capability contract probe")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, fixture.root, "rev-parse", "HEAD"))
	out := fixture.run(t, head)
	assertOutputContainsAll(t, out, "[pre-push] capability contract check", "pre-push OK")
	assertOutputContainsAll(t, fixture.log(t), "make capcontract-check")
}

func TestPrePushCapabilityPathRulesFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{name: "command failure", script: "#!/usr/bin/env bash\necho synthetic path-rules failure >&2\nexit 23\n"},
		{name: "empty rule kind", script: pathRulesOutputScript("\tinternal/provider\n")},
		{name: "empty rule path", script: pathRulesOutputScript("tree\t\n")},
		{name: "trailing empty third column", script: pathRulesOutputScript("tree\tinternal/provider\t\n")},
		{name: "non-empty third column", script: pathRulesOutputScript("tree\tinternal/provider\textra\n")},
		{name: "double tab separator", script: pathRulesOutputScript("tree\t\tinternal/provider\n")},
		{name: "additional tab field", script: pathRulesOutputScript("tree\tinternal/provider\t\textra\n")},
		{name: "missing tab separator", script: pathRulesOutputScript("tree internal/provider\n")},
		{name: "carriage return", script: pathRulesOutputScript("tree\tinternal/provider\r\n")},
		{name: "internal blank line", script: pathRulesOutputScript("tree\tinternal/provider\n\nexact\tscripts/capcontract.go\n")},
		{name: "trailing blank line", script: pathRulesOutputScript("tree\tinternal/provider\n\n")},
		{name: "missing final newline", script: pathRulesOutputScript("tree\tinternal/provider")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPrePushScopeFixture(t)
			probe := "internal/provider/claudecli/capcontract_probe.md"
			writeFixTestGuardFile(t, fixture.root, probe, "capcontract probe\n")
			runFixTestGuardGit(t, fixture.root, "add", probe)
			runFixTestGuardGit(t, fixture.root, "commit", "-m", "test: 更新 capability contract probe")
			head := strings.TrimSpace(runFixTestGuardGitOutput(t, fixture.root, "rev-parse", "HEAD"))

			fakeGo := filepath.Join(t.TempDir(), "go")
			if err := os.WriteFile(fakeGo, []byte(tt.script), 0o755); err != nil {
				t.Fatalf("write fake path-rules go: %v", err)
			}
			t.Setenv("CAPCONTRACT_PATH_RULES_GO_BIN", bashAbsolutePath(fakeGo))
			out, err := runPrePushScopeHook(t, fixture.root, prePushStdin(fixture.base, head), fixture.binDir, fixture.logPath)
			if err == nil {
				t.Fatalf("pre-push accepted unavailable capability path rules\n%s", out)
			}
			if !strings.Contains(out, "capcontract path rules") {
				t.Fatalf("pre-push failure missing actionable path-rules error\n%s", out)
			}
		})
	}
}

func pathRulesOutputScript(output string) string {
	return "#!/usr/bin/env bash\nprintf '%b' " + strconv.Quote(output) + "\n"
}

func capabilityDefaultRootsForGuardTest(t *testing.T) (string, []string) {
	t.Helper()
	source := locateFixTestGuardRepoFile(t, "scripts/capcontract/main.go")
	var err error
	source, err = filepath.Abs(source)
	if err != nil {
		t.Fatalf("resolve %s: %v", source, err)
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	parsed, err := parser.ParseFile(token.NewFileSet(), source, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", source, err)
	}
	literals := capabilityRootLiteralsForGuardTest(t, parsed)
	roots := decodeCapabilityRootLiteralsForGuardTest(t, literals)
	if len(roots) == 0 {
		t.Fatal("defaultCapabilityRoots declaration not found or empty")
	}
	return repoRoot, roots
}

func capabilityRootLiteralsForGuardTest(t *testing.T, parsed *ast.File) []*ast.CompositeLit {
	t.Helper()
	var literals []*ast.CompositeLit
	for _, decl := range parsed.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			literals = append(literals, capabilityRootLiteralsFromValueSpec(t, valueSpec)...)
		}
	}
	return literals
}

func capabilityRootLiteralsFromValueSpec(t *testing.T, valueSpec *ast.ValueSpec) []*ast.CompositeLit {
	t.Helper()
	var literals []*ast.CompositeLit
	for index, name := range valueSpec.Names {
		if name.Name != "defaultCapabilityRoots" || index >= len(valueSpec.Values) {
			continue
		}
		literal, ok := valueSpec.Values[index].(*ast.CompositeLit)
		if !ok {
			t.Fatal("defaultCapabilityRoots is not a composite literal")
		}
		literals = append(literals, literal)
	}
	return literals
}

func decodeCapabilityRootLiteralsForGuardTest(t *testing.T, literals []*ast.CompositeLit) []string {
	t.Helper()
	var roots []string
	for _, literal := range literals {
		for _, element := range literal.Elts {
			basic, ok := element.(*ast.BasicLit)
			if !ok || basic.Kind != token.STRING {
				t.Fatal("defaultCapabilityRoots contains a non-string literal")
			}
			root, err := strconv.Unquote(basic.Value)
			if err != nil {
				t.Fatalf("unquote default root: %v", err)
			}
			roots = append(roots, root)
		}
	}
	return roots
}

func capabilityRootProbe(root string) string {
	if root == "internal/provider" {
		return "internal/provider/claudecli/capcontract_probe.md"
	}
	return strings.TrimSuffix(root, "/") + "/capcontract_probe.md"
}

func containsOutputLine(output, want string) bool {
	for line := range strings.SplitSeq(output, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func containsExactString(values []string, want string) bool {
	return slices.Contains(values, want)
}
