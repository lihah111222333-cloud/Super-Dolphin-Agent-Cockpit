package search

import (
	"path/filepath"
	"strconv"
	"testing"
)

func TestFilterAndCapSearchMatchesCapsEachFileWithoutDroppingOtherFiles(t *testing.T) {
	root := t.TempDir()
	large := filepath.Join(root, "00-large.txt")
	small := filepath.Join(root, "zz-small.txt")
	matches := make([]SearchMatch, 0, 61)
	for line := 1; line <= 60; line++ {
		matches = append(matches, SearchMatch{
			AbsPath:    large,
			SearchRoot: root,
			File:       "00-large.txt",
			Line:       line,
			Col:        1,
			Text:       "needle large " + strconv.Itoa(line),
		})
	}
	matches = append(matches, SearchMatch{
		AbsPath:    small,
		SearchRoot: root,
		File:       "zz-small.txt",
		Line:       1,
		Col:        1,
		Text:       "needle small",
	})

	filtered, total, truncated := FilterAndCapSearchMatches(matches, 50)
	if total != 61 {
		t.Fatalf("FilterAndCapSearchMatches() total = %d, want 61", total)
	}
	if !truncated {
		t.Fatal("FilterAndCapSearchMatches() truncated = false, want true")
	}
	if len(filtered) != 51 {
		t.Fatalf("FilterAndCapSearchMatches() returned %d matches, want 51", len(filtered))
	}

	counts := map[string]int{}
	for _, match := range filtered {
		counts[match.File]++
	}
	if counts["00-large.txt"] != 50 {
		t.Fatalf("large file matches = %d, want 50", counts["00-large.txt"])
	}
	if counts["zz-small.txt"] != 1 {
		t.Fatalf("small file matches = %d, want 1; filtered=%#v", counts["zz-small.txt"], filtered)
	}
}

func TestFilterAndCapSearchMatchesAllowsWorkspaceRootInsideWorktrees(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".worktrees", "p16-batch1-selfguard-ci")
	kept := filepath.Join(root, "backend", "cmd", "code_guard", "timeauthority_rules.go")
	filteredWorktreeChild := filepath.Join(root, ".worktrees", "nested", "skip.go")
	matches := []SearchMatch{
		{
			AbsPath:    kept,
			SearchRoot: root,
			File:       kept,
			Line:       13,
			Col:        35,
			Text:       "待实现的规则名占位符",
		},
		{
			AbsPath:    filteredWorktreeChild,
			SearchRoot: root,
			File:       filteredWorktreeChild,
			Line:       1,
			Col:        1,
			Text:       "待实现的规则名占位符",
		},
	}

	filtered, total, truncated := FilterAndCapSearchMatches(matches, 20)
	if total != 1 || len(filtered) != 1 || truncated {
		t.Fatalf("FilterAndCapSearchMatches() = len:%d total:%d truncated:%t, want one untruncated match", len(filtered), total, truncated)
	}
	if filtered[0].AbsPath != kept {
		t.Fatalf("FilterAndCapSearchMatches() kept %q, want %q", filtered[0].AbsPath, kept)
	}
}
