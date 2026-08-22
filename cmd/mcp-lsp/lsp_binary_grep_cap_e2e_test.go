//go:build e2e

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPBinaryGrepStopsAtDefaultMaxResultsWithHint(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	root := canonicalToolTestRoot(t, t.TempDir())
	writeLSPBinaryFixture(t, filepath.Join(root, "00-large.txt"), lspBinaryGrepNeedleLines(60))
	writeLSPBinaryFixture(t, filepath.Join(root, "zz-small.txt"), "needle small\n")
	contents, err := os.ReadFile(filepath.Join(root, "00-large.txt"))
	if err != nil {
		t.Fatalf("native read fixture: %v", err)
	}
	if got := strings.Count(string(contents), "needle"); got != 60 {
		t.Fatalf("native fixture count = %d, want 60", got)
	}
}

func lspBinaryGrepNeedleLines(count int) string {
	var body strings.Builder
	for i := 1; i <= count; i++ {
		fmt.Fprintf(&body, "needle large %02d\n", i)
	}
	return body.String()
}

func lspBinaryGrepRowsForFile(
	t *testing.T,
	root string,
	payload lspBinaryGrepResponse,
	wantRel string,
) lspBinaryGrepFileRows {
	t.Helper()
	for path, rows := range payload.Data {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relative grep path %q: %v", path, err)
		}
		if filepath.ToSlash(rel) != wantRel {
			continue
		}
		return rows
	}
	t.Fatalf("grep payload missing %s; content files=%#v", wantRel, payload.Data)
	return lspBinaryGrepFileRows{}
}
