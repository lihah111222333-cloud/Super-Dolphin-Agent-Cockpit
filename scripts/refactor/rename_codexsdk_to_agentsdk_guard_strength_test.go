package main

/* ROLLBACK_SKIP_START

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

	var (
		got        []string
		foundSwitch bool
	)
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switchStmt, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		foundSwitch = true
		for _, stmt := range switchStmt.Body.List {
			clause, ok := stmt.(*ast.CaseClause)
			if !ok || len(clause.List) == 0 {
				continue
			}
			for _, expr := range clause.List {
				value, ok := stringLiteralValue(expr)
				if !ok {
					t.Fatalf("skipRenameDir case literal = %T, want string literal", expr)
				}
				got = append(got, value)
			}
		}
		return false
	})

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

func TestRenameScriptGuardStrength_HarnessCollectEditsSortingAndPlanMetadata(t *testing.T) {
	t.Parallel()

	runRenameHarnessGoTest(t, "rename_codexsdk_to_agentsdk_guard_strength_harness_test.go", `package main

import (
	"io/fs"
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

func TestBuildRenamePlanNormalizesRelPathKeepsModeAndSkipsNoops(t *testing.T) {
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

	plan, ok, err := buildRenamePlan(root, target, collectEdits)
	if err != nil {
		t.Fatalf("buildRenamePlan() error = %v", err)
	}
	if !ok {
		t.Fatal("buildRenamePlan() ok = false, want true")
	}
	if plan.Rel != "dir/sub/demo.go" {
		t.Fatalf("plan.Rel = %q, want %q", plan.Rel, "dir/sub/demo.go")
	}
	if plan.FileMode != 0o640 {
		t.Fatalf("plan.FileMode = %#o, want %#o", plan.FileMode, fs.FileMode(0o640))
	}
	if string(plan.Src) != original {
		t.Fatalf("plan.Src = %q, want original content", string(plan.Src))
	}
	if len(plan.Edits) != 1 || len(plan.Replacements) != 1 {
		t.Fatalf("plan edits/replacements = %d/%d, want 1/1", len(plan.Edits), len(plan.Replacements))
	}

	plain := filepath.Join(root, "plain.go")
	if err := os.WriteFile(plain, []byte("package demo\n"), 0o644); err != nil {
		t.Fatalf("write plain.go: %v", err)
	}
	noPlan, ok, err := buildRenamePlan(root, plain, collectEdits)
	if err != nil {
		t.Fatalf("buildRenamePlan(no-op) error = %v", err)
	}
	if ok {
		t.Fatalf("buildRenamePlan(no-op) = %#v, want ok=false", noPlan)
	}
}
`)
}

ROLLBACK_SKIP_END */
