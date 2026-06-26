package search

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
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

func TestSearchASTCancelsAtMaxResults(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell script as the fake sg binary")
	}
	root, err := NormalizeRoot(t.TempDir())
	if err != nil {
		t.Fatalf("NormalizeRoot() error = %v", err)
	}
	writeSearchTestFile(t, filepath.Join(root, "main.go"), "package main\nfunc main() {}\n")
	marker := filepath.Join(t.TempDir(), "sg-completed")
	sg := writeSlowFakeSG(t, marker)
	setFakeSGPath(t, sg)

	start := time.Now()
	matches, err := SearchAST(context.Background(), ASTSearchOptions{
		Root:       root,
		Path:       "main.go",
		Query:      "fmt.Println($A)",
		Language:   "go",
		MaxResults: 2,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("SearchAST() error = %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("SearchAST() returned %d matches, want exactly max_results=2", len(matches))
	}
	requireSearchLimitReached(t, matches, 2)
	if elapsed > time.Second {
		t.Fatalf("SearchAST() elapsed = %v, want ast-grep canceled before fake sg completed", elapsed)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("SearchAST() let fake sg complete; want ast-grep canceled once max_results is reached")
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat fake sg marker: %v", err)
	}
}

func requireSearchLimitReached(t *testing.T, matches []SearchMatch, want int) {
	t.Helper()
	filtered, total, truncated := FilterAndCapSearchMatches(matches, want)
	if len(filtered) != want || total != want || !truncated {
		t.Fatalf("FilterAndCapSearchMatches() after limit = len:%d total:%d truncated:%t, want %d/%d/true", len(filtered), total, truncated, want, want)
	}
}

func writeSlowFakeSG(t *testing.T, marker string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, fakeSGName())
	lines := []string{
		`{"file":"main.go","range":{"start":{"line":0,"column":0}},"lines":"func one() {}","text":"func one() {}"}`,
		`{"file":"main.go","range":{"start":{"line":1,"column":0}},"lines":"func two() {}","text":"func two() {}"}`,
		`{"file":"main.go","range":{"start":{"line":2,"column":0}},"lines":"func three() {}","text":"func three() {}"}`,
	}
	script := "#!/bin/sh\n" +
		"printf '%s\\n' " + shellQuote(lines[0]) + "\n" +
		"printf '%s\\n' " + shellQuote(lines[1]) + "\n" +
		"printf '%s\\n' " + shellQuote(lines[2]) + "\n" +
		"/bin/sleep 2\n" +
		"printf done > " + shellQuote(marker) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write slow fake sg: %v", err)
	}
	return path
}
