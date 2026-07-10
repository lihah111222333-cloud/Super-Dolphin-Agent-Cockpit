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
	input := "before\n| Architecture Tests | " + statsBeginMarker + "stale" + statsEndMarker + " |\nafter\n"
	got, err := replaceREADMEStats(input, archtestStats{Tests: 12, Files: 4})
	if err != nil {
		t.Fatalf("replace README stats: %v", err)
	}
	want := "before\n| Architecture Tests | " + statsBeginMarker + "Source AST: 12 runnable `Test*` functions across 4 `_test.go` files in `internal/archtest`" + statsEndMarker + " |\nafter\n"
	if got != want {
		t.Fatalf("README replacement:\n%s\nwant:\n%s", got, want)
	}
}

func TestReplaceREADMEStatsRejectsInvalidMarkers(t *testing.T) {
	t.Parallel()

	cases := []string{
		"no markers",
		statsBeginMarker + "missing end",
		statsEndMarker + "reversed" + statsBeginMarker,
		statsBeginMarker + "one" + statsEndMarker + statsBeginMarker + "two" + statsEndMarker,
		"| Other | " + statsBeginMarker + "stale" + statsEndMarker + " |",
		"| Architecture Tests | stale |\n" + statsBeginMarker + "stale" + statsEndMarker,
		"| Architecture Tests | " + statsBeginMarker + "stale\n" + statsEndMarker + " |",
	}
	for _, input := range cases {
		if _, err := replaceREADMEStats(input, archtestStats{}); err == nil {
			t.Fatalf("replaceREADMEStats(%q) succeeded, want marker error", input)
		}
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
