package archtest_test

import (
	"go/ast"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest/ssaload"
	"golang.org/x/tools/go/packages"
)

type ssaBoundaryFixtureCase struct {
	name                    string
	wantReject              bool
	token                   string
	wantViolationToken      string
	forbiddenFunction       string
	forbiddenViolationToken string
}

type ssaBoundaryFixtureResult struct {
	violations []string
	syntaxFile string
}

// TestBackendBoundarySingleSourceSSAFixtures 锁定旧 AST 调用边的 SSA 语义盲点。
func TestBackendBoundarySingleSourceSSAFixtures(t *testing.T) {
	cases := []ssaBoundaryFixtureCase{
		{name: "direct_helper", wantReject: true, token: "direct helper", wantViolationToken: "store boundary"},
		{name: "receiver_method_value", wantReject: true, token: "method value", wantViolationToken: "store boundary"},
		{name: "function_alias", wantReject: true, token: "function alias", wantViolationToken: "store boundary"},
		{name: "make_closure", wantReject: true, token: "MakeClosure", wantViolationToken: "store boundary"},
		{name: "returned_closure", wantReject: true, token: "returned closure", wantViolationToken: "store boundary"},
		{name: "interface_invoke", wantReject: true, token: "interface invoke", wantViolationToken: "store boundary"},
		{
			name:                    "interface_multiple_reachable",
			wantReject:              true,
			token:                   "interface invoke reachable implementation",
			wantViolationToken:      "store boundary",
			forbiddenFunction:       "unreachablePolicy.apply",
			forbiddenViolationToken: "fx boundary",
		},
		{name: "unresolved_dynamic", wantReject: true, token: "unresolved dynamic", wantViolationToken: "ssa/unresolved-boundary-call"},
		{
			name:                    "fact_root_bridge_function_param",
			wantReject:              true,
			token:                   "function parameter bridge",
			wantViolationToken:      "store boundary",
			forbiddenViolationToken: "ssa/unresolved-boundary-call",
		},
		{name: "connected_helper_split", wantReject: true, token: "connected helper", wantViolationToken: "store boundary"},
		{name: "unrelated_helpers", wantReject: false, token: "unrelated helpers"},
		{name: "two_unrelated_functions", wantReject: false, token: "two unrelated functions"},
		{name: "ordinary_import_string", wantReject: false, token: "ordinary import string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertBackendBoundarySSAFixtureCase(t, tc, loadBackendBoundarySSAFixture(t, tc.name))
		})
	}
}

func assertBackendBoundarySSAFixtureCase(t *testing.T, tc ssaBoundaryFixtureCase, result ssaBoundaryFixtureResult) {
	t.Helper()
	rejected := len(result.violations) > 0
	joined := strings.Join(result.violations, "\n")
	t.Logf("ssa boundary case=%s syntax=%s want_reject=%t actual_reject=%t token=%s want_violation=%s forbidden_function=%s forbidden_violation=%s", tc.name, result.syntaxFile, tc.wantReject, rejected, tc.token, tc.wantViolationToken, tc.forbiddenFunction, tc.forbiddenViolationToken)
	assertBackendBoundarySSAOutcome(t, tc, result, rejected)
	assertBackendBoundarySSAWantedToken(t, tc, joined, result.violations, rejected)
	assertBackendBoundarySSAForbiddenTokens(t, tc, joined, result.violations)
}

func assertBackendBoundarySSAOutcome(t *testing.T, tc ssaBoundaryFixtureCase, result ssaBoundaryFixtureResult, rejected bool) {
	t.Helper()
	if tc.wantReject && !rejected {
		t.Errorf("%s: ssa boundary expected reject; semantic token=%q; analyzer missed reachable facts", tc.name, tc.token)
	}
	if !tc.wantReject && rejected {
		t.Errorf("%s: ssa boundary expected allow; semantic token=%q; violations=%v", tc.name, tc.token, result.violations)
	}
}

func assertBackendBoundarySSAWantedToken(t *testing.T, tc ssaBoundaryFixtureCase, joined string, violations []string, rejected bool) {
	t.Helper()
	if tc.wantViolationToken != "" && rejected && !strings.Contains(joined, tc.wantViolationToken) {
		t.Errorf("%s: ssa boundary missing violation token %q: %v", tc.name, tc.wantViolationToken, violations)
	}
}

func assertBackendBoundarySSAForbiddenTokens(t *testing.T, tc ssaBoundaryFixtureCase, joined string, violations []string) {
	t.Helper()
	if tc.forbiddenFunction != "" && strings.Contains(joined, tc.forbiddenFunction) {
		t.Errorf("%s: ssa boundary resolved unreachable function %q: %v", tc.name, tc.forbiddenFunction, violations)
	}
	if tc.forbiddenViolationToken != "" && strings.Contains(strings.ToLower(joined), strings.ToLower(tc.forbiddenViolationToken)) {
		t.Errorf("%s: ssa boundary emitted forbidden violation token %q: %v", tc.name, tc.forbiddenViolationToken, violations)
	}
}

// loadBackendBoundarySSAFixture 精确加载单个 fixture，证明 Syntax 来源后构建 SSA。
func loadBackendBoundarySSAFixture(t *testing.T, caseName string) ssaBoundaryFixtureResult {
	t.Helper()
	root := repoRoot(t)
	relDir := filepath.Join("internal", "archtest", "testdata", "backend_boundary_ssa", caseName)
	wantFile := filepath.Join(root, relDir, "fixture.go")
	wantPkgPath := archtestImportPath + "/testdata/backend_boundary_ssa/" + caseName
	pkgs, err := ssaload.Load(ssaload.Options{
		RepoRoot: root,
		Patterns: []string{"./" + filepath.ToSlash(relDir)},
		Tests:    false,
		Include: func(pkg *packages.Package) bool {
			return pkg.PkgPath == wantPkgPath && len(pkg.GoFiles) > 0
		},
	})
	if err != nil {
		t.Fatalf("load SSA boundary fixture %s: %v", caseName, err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("load SSA boundary fixture %s: got %d packages, want 1", caseName, len(pkgs))
	}
	node, syntaxFile := findBackendBoundaryFixtureSyntax(t, pkgs[0], wantFile)
	program, built, err := ssaload.Build(pkgs)
	if err != nil {
		t.Fatalf("build SSA boundary fixture %s: %v", caseName, err)
	}
	if len(built) != 1 {
		t.Fatalf("build SSA boundary fixture %s: got %d SSA packages, want 1", caseName, len(built))
	}
	rel := filepath.ToSlash(filepath.Join(relDir, "fixture.go"))
	violations := backendBoundaryConsumerFactViolations(rel, node)
	violations = append(violations, backendBoundarySSAConnectivityViolations(root, program, pkgs[0], built[0], []string{wantFile})...)
	return ssaBoundaryFixtureResult{
		violations: dedupeBackendBoundarySSAViolations(violations),
		syntaxFile: syntaxFile,
	}
}

// findBackendBoundaryFixtureSyntax 返回 loader 实际纳入的目标 fixture syntax。
func findBackendBoundaryFixtureSyntax(t *testing.T, pkg *packages.Package, wantFile string) (*ast.File, string) {
	t.Helper()
	var loaded []string
	for _, node := range pkg.Syntax {
		filename := filepath.Clean(pkg.Fset.Position(node.Pos()).Filename)
		loaded = append(loaded, filename)
		if filename == filepath.Clean(wantFile) && strings.HasSuffix(filepath.ToSlash(filename), "/fixture.go") {
			return node, filepath.ToSlash(filename)
		}
	}
	t.Fatalf("fixture syntax %s not loaded; got %v", wantFile, loaded)
	return nil, ""
}
