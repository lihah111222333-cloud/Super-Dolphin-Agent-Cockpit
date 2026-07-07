package main

import (
	"go/ast"
	"reflect"
	"testing"
)

const (
	testOldImportRoot = "github.com/multi-agent/go-agent-v2/pkg/codexsdk"
)

func TestRenameScriptGuardStrength_SkipRenameDirDenylistIsFrozen(t *testing.T) {
	t.Parallel()

	_, fileAST := parseRenameScriptAST(t)
	fn := findTopLevelFuncDecl(t, fileAST, "skipRenameDir")

	got, foundSwitch := collectSkipRenameDirDenylist(t, fn)
	if !foundSwitch {
		t.Fatal("skipRenameDir switch not found")
	}

	want := []string{
		".git",
		".worktrees",
		".agent",
		"node_modules",
		".idea",
		".vscode",
		"dist",
		"build",
		"vendor",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("skipRenameDir denylist drifted\nwant: %#v\ngot:  %#v", want, got)
	}
}

func collectSkipRenameDirDenylist(t *testing.T, fn *ast.FuncDecl) ([]string, bool) {
	t.Helper()

	var switchStmt *ast.SwitchStmt
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if switchStmt != nil {
			return false
		}
		stmt, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		switchStmt = stmt
		return false
	})
	if switchStmt == nil {
		return nil, false
	}
	return collectSkipRenameDirCaseLiterals(t, switchStmt), true
}

func collectSkipRenameDirCaseLiterals(t *testing.T, switchStmt *ast.SwitchStmt) []string {
	t.Helper()

	var got []string
	for _, stmt := range switchStmt.Body.List {
		got = append(got, skipRenameDirCaseLiterals(t, stmt)...)
	}
	return got
}

func skipRenameDirCaseLiterals(t *testing.T, stmt ast.Stmt) []string {
	t.Helper()

	clause, ok := stmt.(*ast.CaseClause)
	if !ok || len(clause.List) == 0 {
		return nil
	}
	got := make([]string, 0, len(clause.List))
	for _, expr := range clause.List {
		value, ok := stringLiteralValue(expr)
		if !ok {
			t.Fatalf("skipRenameDir case literal = %T, want string literal", expr)
		}
		got = append(got, value)
	}
	return got
}

func TestRenameScriptGuardStrength_HarnessCollectEditsSortingAndPlanMetadata(t *testing.T) {
	t.Parallel()

	runRenameHarnessGoTest(t, "rename_codexsdk_to_agentsdk_guard_strength_harness_test.go", renameGuardStrengthHarnessSource)
}

const renameGuardStrengthHarnessSource = `package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectEditsSortsDescendingAndPreservesLineNumbers(t *testing.T) {
	src := []byte("package demo\n\nimport (\n\t_ \"` + testOldImportRoot + `\"\n\talias \"` + testOldImportRoot + `/service/runtime\"\n)\n")

	edits, replacements, err := collectEdits("demo.go", src)
	if err != nil {
		t.Fatalf("collectEdits() error = %v", err)
	}
	if len(edits) != 2 {
		t.Fatalf("edit count = %d, want 2", len(edits))
	}
	if len(replacements) != 2 {
		t.Fatalf("replacement count = %d, want 2", len(replacements))
	}
	if edits[0].Start <= edits[1].Start {
		t.Fatalf("edit ordering = [%d, %d], want descending offsets", edits[0].Start, edits[1].Start)
	}
	if replacements[0].Old != oldImportRoot || replacements[0].New != newImportRoot || replacements[0].Line != 4 {
		t.Fatalf("first replacement = %#v, want old/new roots at line 4", replacements[0])
	}
	if replacements[1].Old != oldImportRoot+"/service/runtime" || replacements[1].New != newImportRoot+"/service/runtime" || replacements[1].Line != 5 {
		t.Fatalf("second replacement = %#v, want nested import rewrite at line 5", replacements[1])
	}

	updated := string(applyEdits(src, edits))
	if strings.Contains(updated, oldImportRoot) {
		t.Fatalf("updated source still contains old root: %q", updated)
	}
	if !strings.Contains(updated, newImportRoot) || !strings.Contains(updated, newImportRoot+"/service/runtime") {
		t.Fatalf("updated source missing rewritten roots: %q", updated)
	}
}

func TestProcessRenameFileNormalizesRelPathAndSkipsNoops(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "dir", "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	target := filepath.Join(nested, "demo.go")
	original := "package demo\n\nimport _ \"` + testOldImportRoot + `\"\n"
	if err := os.WriteFile(target, []byte(original), 0o640); err != nil {
		t.Fatalf("write target: %v", err)
	}

	reports := []fileReport{}
	total := 0
	if err := processRenameFile(root, target, false, collectEdits, applyEdits, &reports, &total); err != nil {
		t.Fatalf("processRenameFile() error = %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("reports = %#v, want one report", reports)
	}
	if reports[0].File != "dir/sub/demo.go" {
		t.Fatalf("report file = %q, want %q", reports[0].File, "dir/sub/demo.go")
	}
	if reports[0].Count != 1 || len(reports[0].Replacements) != 1 || total != 1 {
		t.Fatalf("report counts = %#v total=%d, want 1 replacement", reports[0], total)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != original {
		t.Fatalf("dry-run process should not mutate target, got %q", string(data))
	}

	plain := filepath.Join(root, "plain.go")
	if err := os.WriteFile(plain, []byte("package demo\n"), 0o644); err != nil {
		t.Fatalf("write plain.go: %v", err)
	}
	if err := processRenameFile(root, plain, false, collectEdits, applyEdits, &reports, &total); err != nil {
		t.Fatalf("processRenameFile(no-op) error = %v", err)
	}
	if len(reports) != 1 || total != 1 {
		t.Fatalf("no-op should not record extra reports, reports=%#v total=%d", reports, total)
	}
}
`
