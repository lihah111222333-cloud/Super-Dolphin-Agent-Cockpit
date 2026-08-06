package capcontract

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type invalidGeneratorSourceCase struct {
	name    string
	source  *string
	roots   []string
	wantErr string
}

func TestLoadPathRulesReadsDefaultRootsFromGeneratorAST(t *testing.T) {
	repoRoot := writePathRulesFixture(t, `package main

func defaultCapabilityRoots() []string {
	return []string{
		"internal/contract",
		"internal/provider",
		"cmd/mcp-orch/orchestration",
		"cmd/mcp-orch/tools",
	}
}
`, []string{
		"internal/contract",
		"internal/provider",
		"cmd/mcp-orch/orchestration",
		"cmd/mcp-orch/tools",
	})

	rules, err := LoadPathRules(repoRoot)
	if err != nil {
		t.Fatalf("load path rules: %v", err)
	}
	wantRoots := []string{
		"internal/contract",
		"internal/provider",
		"cmd/mcp-orch/orchestration",
		"cmd/mcp-orch/tools",
	}
	if !reflect.DeepEqual(rules.DefaultRoots, wantRoots) {
		t.Fatalf("default roots = %#v, want %#v", rules.DefaultRoots, wantRoots)
	}

	assertCapabilityPathsMatch(t, rules, []string{
		"internal/provider/claudecli/session.go",
		"cmd/mcp-orch/tools/pos.go",
		"docs/doc/codemap/capability-contract/capability_manifest.json",
		"internal/devtools/capcontract/scanner.go",
		"scripts/capcontract.go",
		"scripts/capcontract/main.go",
	})

	matched, err := rules.Match("docs/guide.md")
	if err != nil {
		t.Fatalf("match unrelated path: %v", err)
	}
	if matched {
		t.Fatal("unrelated docs path matched capability-contract rules")
	}

	lines, err := rules.MachineLines()
	if err != nil {
		t.Fatalf("render machine lines: %v", err)
	}
	for _, want := range []string{
		"tree\tinternal/contract",
		"tree\tinternal/provider",
		"tree\tcmd/mcp-orch/orchestration",
		"tree\tcmd/mcp-orch/tools",
		"exact\tscripts/capcontract.go",
	} {
		if !containsExactString(lines, want) {
			t.Errorf("machine lines missing %q: %#v", want, lines)
		}
	}
}

func assertCapabilityPathsMatch(t *testing.T, rules PathRules, candidates []string) {
	t.Helper()
	for _, candidate := range candidates {
		matched, err := rules.Match(candidate)
		if err != nil {
			t.Fatalf("match %q: %v", candidate, err)
		}
		if !matched {
			t.Errorf("expected %q to match capability-contract rules", candidate)
		}
	}
}

func TestLoadPathRulesFailsClosedOnInvalidGeneratorSource(t *testing.T) {
	tests := []invalidGeneratorSourceCase{
		{name: "missing source", wantErr: "read default capability roots source"},
		{name: "parse failure", source: new("package main\nfunc defaultCapabilityRoots() []string { return []string{"), wantErr: "parse default capability roots source"},
		{name: "missing function", source: new("package main\nfunc another() []string { return []string{\"internal/provider\"} }\n"), wantErr: "defaultCapabilityRoots function not found"},
		{name: "non literal return", source: new("package main\nvar roots = []string{\"internal/provider\"}\nfunc defaultCapabilityRoots() []string { return roots }\n"), wantErr: "must return a []string composite literal"},
		{name: "non string element", source: new("package main\nfunc defaultCapabilityRoots() []string { return []string{root} }\n"), wantErr: "must contain only string literals"},
		{name: "multiple functions", source: new("package main\nfunc defaultCapabilityRoots() []string { return []string{\"internal/provider\"} }\nfunc defaultCapabilityRoots() []string { return []string{\"cmd/mcp-orch/tools\"} }\n"), wantErr: "multiple defaultCapabilityRoots functions"},
		{name: "multiple statements", source: new("package main\nfunc defaultCapabilityRoots() []string { _ = 1; return []string{\"internal/provider\"} }\n"), wantErr: "must contain exactly one return statement"},
		{name: "invalid signature", source: new("package main\nfunc defaultCapabilityRoots() string { return \"internal/provider\" }\n"), wantErr: "must be declared as func defaultCapabilityRoots() []string"},
		{name: "duplicate root", source: new("package main\nfunc defaultCapabilityRoots() []string { return []string{\"internal/provider\", \"internal/provider\"} }\n"), roots: []string{"internal/provider"}, wantErr: "duplicate default capability root"},
		{name: "missing root directory", source: new("package main\nfunc defaultCapabilityRoots() []string { return []string{\"internal/provider\"} }\n"), wantErr: "default capability root does not exist"},
		{name: "non canonical root", source: new("package main\nfunc defaultCapabilityRoots() []string { return []string{\"internal/../provider\"} }\n"), wantErr: "must be a normalized repository-relative path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertInvalidGeneratorSource(t, tt)
		})
	}
}

func assertInvalidGeneratorSource(t *testing.T, tt invalidGeneratorSourceCase) {
	t.Helper()
	repoRoot := t.TempDir()
	if tt.source != nil {
		writePathRulesSource(t, repoRoot, *tt.source)
	}
	createPathRulesRootDirs(t, repoRoot, tt.roots)

	_, err := LoadPathRules(repoRoot)
	if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
		t.Fatalf("LoadPathRules() error = %v, want substring %q", err, tt.wantErr)
	}
}

func createPathRulesRootDirs(t *testing.T, repoRoot string, roots []string) {
	t.Helper()
	for _, root := range roots {
		if err := os.MkdirAll(filepath.Join(repoRoot, filepath.FromSlash(root)), 0o755); err != nil {
			t.Fatalf("mkdir root %q: %v", root, err)
		}
	}
}

func TestPathRulesMatchRejectsInvalidCandidate(t *testing.T) {
	rules := PathRules{DefaultRoots: []string{"internal/provider"}}
	for _, candidate := range []string{"", "/tmp/provider.go", "../internal/provider/provider.go", "internal/../provider/provider.go", "internal/provider\t/provider.go"} {
		if _, err := rules.Match(candidate); err == nil {
			t.Errorf("Match(%q) succeeded, want fail-closed error", candidate)
		}
	}
}

func writePathRulesFixture(t *testing.T, source string, roots []string) string {
	t.Helper()
	repoRoot := t.TempDir()
	writePathRulesSource(t, repoRoot, source)
	createPathRulesRootDirs(t, repoRoot, roots)
	return repoRoot
}

func writePathRulesSource(t *testing.T, repoRoot, source string) {
	t.Helper()
	path := filepath.Join(repoRoot, "scripts", "capcontract", "main.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
}

func containsExactString(values []string, want string) bool {
	return slices.Contains(values, want)
}
