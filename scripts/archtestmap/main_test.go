package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

func TestRenderRuleMapIsDeterministic(t *testing.T) {
	registry := archtest.DefaultBackendBoundaryRegistry()
	want := renderRuleMap(registry)
	slices.Reverse(registry.Owners)
	slices.Reverse(registry.Rules)
	slices.Reverse(registry.Guards)
	slices.Reverse(registry.Surfaces)
	got := renderRuleMap(registry)
	if got != want {
		t.Fatal("rule map depends on registry insertion order")
	}
	if strings.Index(got, "`cmd/agent-runtime`") > strings.Index(got, "`pkg/skillmetrics`") {
		t.Fatal("surface table is not sorted by path")
	}
	if !strings.Contains(got, "policies across") {
		t.Fatal("large import policy sets are not summarized for AI readability")
	}
}

func TestCollectArchtestStatsUsesRunnableTopLevelTests(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/archtest/alpha_test.go", `package archtest
import "testing"
func TestAlpha(t *testing.T) {}
func Testlower(t *testing.T) {}
func TestWrong() {}
func TestOdd(t *struct{}) {}
`)
	writeTestFile(t, root, "internal/archtest/beta_test.go", `package archtest
import check "testing"
func TestBeta(t *check.T) {}
type suite struct{}
func (suite) TestMethod(t *check.T) {}
`)
	writeTestFile(t, root, "internal/archtest/empty_test.go", "package archtest\n")

	stats, err := collectArchtestStats(root)
	if err != nil {
		t.Fatalf("collect stats: %v", err)
	}
	if stats.Tests != 2 || stats.Files != 2 {
		t.Fatalf("stats = %#v, want 2 tests across 2 files", stats)
	}
}

func TestReplaceREADMEStatsOnlyTouchesInlineMarker(t *testing.T) {
	input := readmeFixture("| Architecture Tests | " + statsBeginMarker + "stale" + statsEndMarker + " |")
	got, err := replaceREADMEStats(input, archtestStats{Tests: 12, Files: 4})
	if err != nil {
		t.Fatalf("replace README stats: %v", err)
	}
	want := readmeFixture("| Architecture Tests | " + statsBeginMarker + "Source AST: 12 runnable `Test*` functions across 4 `_test.go` files in `internal/archtest`" + statsEndMarker + " |")
	if got != want {
		t.Fatalf("README replacement:\n%s\nwant:\n%s", got, want)
	}
}

func TestReplaceREADMEStatsRejectsInvalidMarkers(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"no markers":             "no markers",
		"missing end":            statsBeginMarker + "missing end",
		"reversed":               statsEndMarker + "reversed" + statsBeginMarker,
		"duplicate markers":      statsBeginMarker + "one" + statsEndMarker + statsBeginMarker + "two" + statsEndMarker,
		"wrong row":              readmeFixture("| Other | " + statsBeginMarker + "stale" + statsEndMarker + " |"),
		"markers outside row":    readmeFixture("| Architecture Tests | stale |") + statsBeginMarker + "stale" + statsEndMarker,
		"multiline markers":      readmeFixture("| Architecture Tests | " + statsBeginMarker + "stale\n" + statsEndMarker + " |"),
		"code fence pseudo row":  readmeFixture("```\n| Architecture Tests | " + statsBeginMarker + "stale" + statsEndMarker + " |\n```"),
		"indented duplicate row": readmeFixture("| Architecture Tests | " + statsBeginMarker + "stale" + statsEndMarker + " |\n  | Architecture Tests | duplicate |"),
		"third table cell":       readmeFixture("| Architecture Tests | " + statsBeginMarker + "stale" + statsEndMarker + " | unexpected |"),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := replaceREADMEStats(input, archtestStats{}); err == nil {
				t.Fatalf("replaceREADMEStats(%q) succeeded, want marker error", input)
			}
		})
	}
}

func TestRunCheckReportsDriftWithoutWriting(t *testing.T) {
	root, registry := archtestMapRunFixture(t)
	beforeREADME := mustReadTestFile(t, filepath.Join(root, readmePath))
	beforeMap := mustReadTestFile(t, filepath.Join(root, ruleMapPath))
	if err := runWithRegistry(root, registry, true); err == nil {
		t.Fatal("run check accepted stale generated artifacts")
	}
	if got := mustReadTestFile(t, filepath.Join(root, readmePath)); got != beforeREADME {
		t.Fatal("run check modified README")
	}
	if got := mustReadTestFile(t, filepath.Join(root, ruleMapPath)); got != beforeMap {
		t.Fatal("run check modified rule map")
	}
}

func TestRunRefreshThenCheck(t *testing.T) {
	root, registry := archtestMapRunFixture(t)
	if err := runWithRegistry(root, registry, false); err != nil {
		t.Fatalf("run refresh: %v", err)
	}
	if err := runWithRegistry(root, registry, true); err != nil {
		t.Fatalf("run check after refresh: %v", err)
	}
}

func TestSyncGeneratedFilesRollsBackOnSecondCommitFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := filepath.Join(root, "first.md")
	second := filepath.Join(root, "second.md")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
			t.Fatalf("write existing generated file: %v", err)
		}
	}
	renames := 0
	ops := generatedFileOps{rename: func(oldPath, newPath string) error {
		renames++
		if renames == 2 {
			return errors.New("synthetic second commit failure")
		}
		return os.Rename(oldPath, newPath)
	}}
	err := syncGeneratedFilesWithOps([]generatedArtifact{
		{path: first, content: "new first\n"},
		{path: second, content: "new second\n"},
	}, false, ops)
	if err == nil {
		t.Fatal("multi-artifact refresh succeeded after second commit failure")
	}
	for _, path := range []string{first, second} {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read rolled-back generated file: %v", readErr)
		}
		if string(data) != "old\n" {
			t.Fatalf("generated file %s was not rolled back: %q", path, data)
		}
	}
}

func TestSyncGeneratedFilesReportsCommitAndRollbackFailures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := filepath.Join(root, "first.md")
	second := filepath.Join(root, "second.md")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
			t.Fatalf("write existing generated file: %v", err)
		}
	}
	renames := 0
	ops := generatedFileOps{rename: func(oldPath, newPath string) error {
		renames++
		switch renames {
		case 2:
			return errors.New("synthetic commit failure")
		case 3:
			return errors.New("synthetic rollback failure")
		default:
			return os.Rename(oldPath, newPath)
		}
	}}
	err := syncGeneratedFilesWithOps([]generatedArtifact{{path: first, content: "new first\n"}, {path: second, content: "new second\n"}}, false, ops)
	if err == nil || !strings.Contains(err.Error(), "synthetic commit failure") || !strings.Contains(err.Error(), "synthetic rollback failure") {
		t.Fatalf("double failure error = %v, want commit and rollback causes", err)
	}
}

func TestSyncGeneratedFileCheckIsReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generated.md")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write existing generated file: %v", err)
	}
	if err := syncGeneratedFile(path, "new\n", true); err == nil {
		t.Fatal("check mode accepted drift")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated file after check: %v", err)
	}
	if string(data) != "old\n" {
		t.Fatalf("check mode wrote file: %q", data)
	}
	if err := syncGeneratedFile(path, "new\n", false); err != nil {
		t.Fatalf("refresh generated file: %v", err)
	}
	if err := syncGeneratedFile(path, "new\n", true); err != nil {
		t.Fatalf("check refreshed generated file: %v", err)
	}
}

func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create test fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test fixture: %v", err)
	}
}

func readmeFixture(row string) string {
	return "before\n## Code Quality\n\n| Metric | Value |\n|--------|-------|\n" + row + "\nafter\n"
}

func archtestMapRunFixture(t *testing.T) (string, archtest.BackendBoundaryRegistry) {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "internal/archtest/guard_test.go", "package archtest\n\nimport \"testing\"\n\nfunc TestFixtureGuard(t *testing.T) {}\n")
	writeTestFile(t, root, "internal/service/service.go", "package service\n")
	writeTestFile(t, root, readmePath, readmeFixture("| Architecture Tests | "+statsBeginMarker+"stale"+statsEndMarker+" |"))
	writeTestFile(t, root, ruleMapPath, "stale map\n")
	registry := archtest.DefaultBackendBoundaryRegistry()
	registry.Guards = []archtest.BackendBoundaryGuard{{ID: "fixture_guard", File: "internal/archtest/guard_test.go", TestNames: []string{"TestFixtureGuard"}, Reason: "fixture guard"}}
	registry.Surfaces = []archtest.BackendBoundarySurface{
		{Path: "internal/archtest", GuardIDs: []archtest.BoundaryGuardID{"fixture_guard"}, Reason: "fixture archtest"},
		{Path: "internal/service", RuleIDs: []archtest.BoundaryRuleID{"fx_assembly_scope"}, Reason: "fixture service"},
	}
	return root, registry
}

func mustReadTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read test file %s: %v", path, err)
	}
	return string(data)
}
