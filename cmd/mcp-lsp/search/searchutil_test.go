package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchTextCapsLineSnippet(t *testing.T) {
	root := t.TempDir()
	long := "needle " + strings.Repeat("x", 220)
	path := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(path, []byte(long+"\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	matches, err := SearchText(context.Background(), TextSearchOptions{
		Root:         root,
		Path:         path,
		Query:        "needle",
		MaxFileBytes: 1024,
	})
	if err != nil {
		t.Fatalf("SearchText error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(matches))
	}
	if got := len([]rune(matches[0].Text)); got != 153 {
		t.Fatalf("snippet length = %d, want 153 with ellipsis", got)
	}
	if !strings.HasSuffix(matches[0].Text, "...") {
		t.Fatalf("snippet = %q, want ellipsis suffix", matches[0].Text)
	}
}
