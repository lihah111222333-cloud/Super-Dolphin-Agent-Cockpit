package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
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
	guardTable, surfaceTable := generatedBoundaryMapTables(t, got)
	if strings.Index(surfaceTable, "`cmd/agent-runtime`") > strings.Index(surfaceTable, "`pkg/skillmetrics`") {
		t.Fatal("surface table is not sorted by path")
	}
	if !strings.Contains(got, "policies across") {
		t.Fatal("large import policy sets are not summarized for AI readability")
	}
	if !strings.Contains(guardTable, "| Guard | Test file | Build tags | Runnable tests | Applies to | Reason |") {
		t.Fatal("guard table does not expose typed surface applicability")
	}
	if !strings.Contains(guardTable, "`backend_surface_governance`") || !strings.Contains(guardTable, "`internal/archtest`") {
		t.Fatal("guard table does not render canonical guard applicability")
	}
}

func generatedBoundaryMapTables(t *testing.T, output string) (string, string) {
	t.Helper()
	guardStart := strings.Index(output, "## Specialized guards")
	surfaceStart := strings.Index(output, "## Governed backend surfaces")
	if guardStart < 0 || surfaceStart <= guardStart {
		t.Fatalf("generated boundary map is missing ordered guard/surface sections:\n%s", output)
	}
	return output[guardStart:surfaceStart], output[surfaceStart:]
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

	markerRow := "| Architecture Tests | " + statsBeginMarker + "stale" + statsEndMarker + " |"
	cases := map[string]string{
		"no markers":               "no markers",
		"missing end":              statsBeginMarker + "missing end",
		"reversed":                 statsEndMarker + "reversed" + statsBeginMarker,
		"duplicate markers":        statsBeginMarker + "one" + statsEndMarker + statsBeginMarker + "two" + statsEndMarker,
		"wrong row":                readmeFixture("| Other | " + statsBeginMarker + "stale" + statsEndMarker + " |"),
		"markers outside row":      readmeFixture("| Architecture Tests | stale |") + statsBeginMarker + "stale" + statsEndMarker,
		"multiline markers":        readmeFixture("| Architecture Tests | " + statsBeginMarker + "stale\n" + statsEndMarker + " |"),
		"code fence pseudo row":    readmeFixture("```\n| Architecture Tests | " + statsBeginMarker + "stale" + statsEndMarker + " |\n```"),
		"indented duplicate row":   readmeFixture("| Architecture Tests | " + statsBeginMarker + "stale" + statsEndMarker + " |\n  | Architecture Tests | duplicate |"),
		"third table cell":         readmeFixture("| Architecture Tests | " + statsBeginMarker + "stale" + statsEndMarker + " | unexpected |"),
		"blank before marker row":  readmeFixture("\n" + markerRow),
		"paragraph before marker":  readmeFixture("README prose\n" + markerRow),
		"different trailing table": readmeFixture("\n| Other Metric | Value |\n|--------------|-------|\n| Other | stale |\n" + markerRow),
		"h1 section boundary":      "before\n## Code Quality\n\n| Metric | Value |\n|--------|-------|\n| Other | stale |\n# New Section\n\n| Metric | Value |\n|--------|-------|\n" + markerRow + "\nafter\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := replaceREADMEStats(input, archtestStats{}); err == nil {
				t.Fatalf("replaceREADMEStats(%q) succeeded, want marker error", input)
			}
		})
	}
}

func TestRunRejectsGeneratedParentSymlinkEscape(t *testing.T) {
	root, registry := archtestMapRunFixture(t)
	external := t.TempDir()
	if err := os.RemoveAll(filepath.Join(root, "docs")); err != nil {
		t.Fatalf("remove generated docs fixture: %v", err)
	}
	if err := os.Symlink(external, filepath.Join(root, "docs")); err != nil {
		t.Skipf("create generated parent symlink fixture: %v", err)
	}
	if err := runWithRegistry(root, registry, false); err == nil {
		t.Fatal("run accepted generated artifact parent symlink escape")
	}
	escapedMap := filepath.Join(external, filepath.FromSlash(strings.TrimPrefix(ruleMapPath, "docs/")))
	if _, err := os.Stat(escapedMap); !os.IsNotExist(err) {
		t.Fatalf("escaped generated artifact was written outside repository: %v", err)
	}
}

func TestSyncGeneratedFilesRejectsLexicalEscape(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "generated.md")
	err := syncGeneratedFiles(root, []generatedArtifact{{path: external, content: "escaped\n"}}, false)
	if err == nil {
		t.Fatal("syncGeneratedFiles accepted artifact outside repository root")
	}
	if _, statErr := os.Stat(external); !os.IsNotExist(statErr) {
		t.Fatalf("generated artifact was written outside repository: %v", statErr)
	}
}

func TestSyncGeneratedFilesRejectsGeneratedDirectoryRaceSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "docs", "generated")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("create generated target directory: %v", err)
	}
	external := t.TempDir()
	target := filepath.Join(targetDir, "artifact.md")
	ops := generatedFileOps{
		rename: renameGeneratedArtifact,
		afterValidate: func() error {
			if err := os.RemoveAll(targetDir); err != nil {
				return err
			}
			return os.Symlink(external, targetDir)
		},
	}
	err := syncGeneratedFilesWithOps(root, []generatedArtifact{{path: target, content: "escaped\n"}}, false, ops)
	if err == nil {
		t.Error("generated refresh followed a directory symlink inserted after validation")
	}
	if _, statErr := os.Stat(filepath.Join(external, "artifact.md")); !os.IsNotExist(statErr) {
		t.Fatalf("generated refresh wrote outside repository after directory race: %v", statErr)
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
	ops := generatedFileOps{rename: func(root *os.Root, oldPath, newPath string) error {
		renames++
		if renames == 2 {
			return errors.New("synthetic second commit failure")
		}
		return root.Rename(oldPath, newPath)
	}}
	err := syncGeneratedFilesWithOps(root, []generatedArtifact{
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
	ops := generatedFileOps{rename: func(root *os.Root, oldPath, newPath string) error {
		renames++
		switch renames {
		case 2:
			return errors.New("synthetic commit failure")
		case 3:
			return errors.New("synthetic rollback failure")
		default:
			return root.Rename(oldPath, newPath)
		}
	}}
	err := syncGeneratedFilesWithOps(root, []generatedArtifact{{path: first, content: "new first\n"}, {path: second, content: "new second\n"}}, false, ops)
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
	fixtureRule, ok := registry.Rule("fx_assembly_scope")
	if !ok {
		t.Fatal("fixture canonical rule is missing")
	}
	registry.Rules = []archtest.BackendBoundaryRule{fixtureRule}
	registry.Guards = []archtest.BackendBoundaryGuard{{
		ID:        "fixture_guard",
		File:      "internal/archtest/guard_test.go",
		TestNames: []string{"TestFixtureGuard"},
		AppliesTo: []archtest.BoundarySurfaceID{"internal/archtest"},
		Reason:    "fixture guard",
	}}
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
