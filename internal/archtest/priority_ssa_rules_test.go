package archtest

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestPrioritySSAErrorStringRuleHonorsArchguardIgnore(t *testing.T) {
	pkg, ssaPkg := prioritySSAErrorStringFixturePackage(t)

	tagged := ssaPkg.Func("tagged")
	if tagged == nil {
		t.Fatal("tagged function was not built")
	}
	if got := collectPrioritySSAErrorStringViolations(pkg, tagged); len(got) != 0 {
		t.Fatalf("tagged error-string call violations = %#v, want none", got)
	}

	untagged := ssaPkg.Func("untagged")
	if untagged == nil {
		t.Fatal("untagged function was not built")
	}
	got := collectPrioritySSAErrorStringViolations(pkg, untagged)
	if len(got) != 1 {
		t.Fatalf("untagged error-string call violations = %#v, want one", got)
	}
	if got[0].Rule != PrioritySSAErrorStringRule {
		t.Fatalf("untagged violation rule = %q, want %q", got[0].Rule, PrioritySSAErrorStringRule)
	}
}

func TestPrioritySSAFunctionIndexesReuseCollectedFunctions(t *testing.T) {
	_, ssaPkg := prioritySSAErrorStringFixturePackage(t)
	functions := prioritySSAFunctions(ssaPkg)
	if len(functions) == 0 {
		t.Fatal("priority SSA fixture produced no functions")
	}
	byName := prioritySSAFunctionsByName(functions)
	byPos := prioritySSAFunctionsByPos(functions)
	for _, name := range []string{"tagged", "untagged"} {
		want := ssaPkg.Func(name)
		if got := byName[name]; got != want {
			t.Fatalf("function index %q = %p, want %p", name, got, want)
		}
		if got := byPos[want.Pos()]; got != want {
			t.Fatalf("position index %q = %p, want %p", name, got, want)
		}
	}
}

// BenchmarkPrioritySSAFunctionIndexReuse 衡量包扫描器复用已收集 SSA 函数图时的索引构建成本。
func BenchmarkPrioritySSAFunctionIndexReuse(b *testing.B) {
	_, ssaPkg := prioritySSAErrorStringFixturePackage(b)
	functions := prioritySSAFunctions(ssaPkg)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = prioritySSAFunctionsByName(functions)
		_ = prioritySSAFunctionsByPos(functions)
	}
}

func prioritySSAErrorStringFixturePackage(t testing.TB) (*prioritySSAPackage, *ssa.Package) {
	t.Helper()

	const source = `package priorityfixture

import "strings"

func tagged(err error) bool {
	// archguard:ignore priority_ssa_error_string -- external CLI text has no typed error
	return strings.Contains(err.Error(), "external failure")
}

func untagged(err error) bool {
	return strings.Contains(err.Error(), "must remain guarded")
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "priority_error_string_fixture.go", source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	typesInfo := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	typesPkg, err := (&types.Config{Importer: importer.Default()}).Check("priorityfixture", fset, []*ast.File{file}, typesInfo)
	if err != nil {
		t.Fatalf("type-check fixture: %v", err)
	}

	prog := ssa.NewProgram(fset, ssa.SanityCheckFunctions)
	for _, imported := range typesPkg.Imports() {
		prog.CreatePackage(imported, nil, nil, true)
	}
	ssaPkg := prog.CreatePackage(typesPkg, []*ast.File{file}, typesInfo, true)
	ssaPkg.Build()
	return &prioritySSAPackage{
		pkgPath:   "priorityfixture",
		repoRoot:  ".",
		fset:      fset,
		syntax:    []*ast.File{file},
		types:     typesPkg,
		typesInfo: typesInfo,
	}, ssaPkg
}
