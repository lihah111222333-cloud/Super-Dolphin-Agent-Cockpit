package search

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
)

func TestSearchTextStopsAtMaxResults(t *testing.T) {
	root, err := NormalizeRoot(t.TempDir())
	if err != nil {
		t.Fatalf("NormalizeRoot() error = %v", err)
	}
	for index := 0; index < 5; index++ {
		writeSearchTestFile(t, filepath.Join(root, "src", "file"+strconv.Itoa(index)+".go"), "package main\nconst needle = true\n")
	}

	matches, err := SearchText(context.Background(), TextSearchOptions{
		Root:         root,
		Query:        "needle",
		MaxResults:   2,
		MaxFileBytes: 1024,
	})
	if err != nil {
		t.Fatalf("SearchText() error = %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("SearchText() returned %d matches, want exactly max_results=2", len(matches))
	}
	requireSearchLimitReached(t, matches, 2)
}

func requireSearchLimitReached(t *testing.T, matches []SearchMatch, want int) {
	t.Helper()
	filtered, total, truncated := FilterAndCapSearchMatches(matches, want)
	if len(filtered) != want || total != want || !truncated {
		t.Fatalf("FilterAndCapSearchMatches() after limit = len:%d total:%d truncated:%t, want %d/%d/true", len(filtered), total, truncated, want, want)
	}
}
