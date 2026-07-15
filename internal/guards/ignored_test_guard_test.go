package guards

import (
	"bytes"
	"fmt"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestTrackedGoTestsDoNotUseIgnoreBuildConstraint(t *testing.T) {
	root := findRepositoryRoot(t)
	offenders, err := findTrackedIgnoredGoTests(root)
	if err != nil {
		t.Fatalf("inspect tracked Go tests: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("tracked _test.go files must belong to an executable test chain; remove //go:build ignore from: %s", strings.Join(offenders, ", "))
	}
}

func TestFindTrackedIgnoredGoTestsRejectsIgnoredFixture(t *testing.T) {
	root := t.TempDir()
	writeIgnoredTestFixture(t, root, "fixture/ignored_test.go")
	runGitForIgnoredTestFixture(t, root, "init")
	runGitForIgnoredTestFixture(t, root, "add", "fixture/ignored_test.go")

	offenders, err := findTrackedIgnoredGoTests(root)
	if err != nil {
		t.Fatalf("inspect fixture: %v", err)
	}
	if got, want := strings.Join(offenders, ","), "fixture/ignored_test.go"; got != want {
		t.Fatalf("offenders = %q, want %q", got, want)
	}
}

func TestFindTrackedIgnoredGoTestsRejectsMissingTrackedFixture(t *testing.T) {
	root := t.TempDir()
	const rel = "fixture/missing_test.go"
	writeIgnoredTestFixture(t, root, rel)
	runGitForIgnoredTestFixture(t, root, "init")
	runGitForIgnoredTestFixture(t, root, "add", rel)
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("remove tracked fixture from worktree: %v", err)
	}

	offenders, err := findTrackedIgnoredGoTests(root)
	if err == nil {
		t.Fatalf("expected missing tracked test to fail, got offenders %v", offenders)
	}
	if !strings.Contains(err.Error(), rel) {
		t.Fatalf("error %q does not identify missing tracked test %q", err, rel)
	}
}

func findTrackedIgnoredGoTests(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--", "*_test.go")
	rawPaths, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list tracked _test.go files: %w", err)
	}

	var offenders []string
	for rawPath := range bytes.SplitSeq(bytes.TrimSuffix(rawPaths, []byte{0}), []byte{0}) {
		if len(rawPath) == 0 {
			continue
		}
		rel := filepath.ToSlash(string(rawPath))
		path := filepath.Join(root, filepath.FromSlash(rel))
		ignored, err := goTestUsesIgnoreConstraint(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		if ignored {
			offenders = append(offenders, rel)
		}
	}
	sort.Strings(offenders)
	return offenders, nil
}

func goTestUsesIgnoreConstraint(path string) (bool, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly|parser.ParseComments)
	if err != nil {
		return false, fmt.Errorf("parse Go file: %w", err)
	}
	for _, group := range parsed.Comments {
		for _, comment := range group.List {
			if !constraint.IsGoBuild(comment.Text) {
				continue
			}
			expr, err := constraint.Parse(comment.Text)
			if err != nil {
				return false, fmt.Errorf("parse build constraint: %w", err)
			}
			return containsPositiveBuildTag(expr, "ignore", false), nil
		}
	}
	return false, nil
}

func containsPositiveBuildTag(expr constraint.Expr, target string, negated bool) bool {
	switch typed := expr.(type) {
	case *constraint.TagExpr:
		return !negated && typed.Tag == target
	case *constraint.NotExpr:
		return containsPositiveBuildTag(typed.X, target, !negated)
	case *constraint.AndExpr:
		return containsPositiveBuildTag(typed.X, target, negated) || containsPositiveBuildTag(typed.Y, target, negated)
	case *constraint.OrExpr:
		return containsPositiveBuildTag(typed.X, target, negated) || containsPositiveBuildTag(typed.Y, target, negated)
	default:
		return false
	}
}

func writeIgnoredTestFixture(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	content := []byte("//go:build ignore\n\npackage fixture\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func runGitForIgnoredTestFixture(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}
