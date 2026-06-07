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
			AbsPath: large,
			File:    "00-large.txt",
			Line:    line,
			Col:     1,
			Text:    "needle large " + strconv.Itoa(line),
		})
	}
	matches = append(matches, SearchMatch{
		AbsPath: small,
		File:    "zz-small.txt",
		Line:    1,
		Col:     1,
		Text:    "needle small",
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
