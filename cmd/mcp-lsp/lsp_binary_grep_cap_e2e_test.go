//go:build e2e

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPBinaryGrepStopsAtDefaultMaxResultsWithHint(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	root := canonicalToolTestRoot(t, t.TempDir())
	writeLSPBinaryFixture(t, filepath.Join(root, "00-large.txt"), lspBinaryGrepNeedleLines(60))
	writeLSPBinaryFixture(t, filepath.Join(root, "zz-small.txt"), "needle small\n")
	client := startLSPBinaryClient(t, root)

	result := client.callTool(t, "grep", map[string]any{
		"action": "text_search",
		"query":  "needle",
		"paths":  []string{root},
		"glob":   "*.txt",
	})
	if result.IsError {
		t.Fatalf("grep returned tool error: %s", result.ContentText())
	}
	payload := decodeLSPBinaryGrepContent(t, result.ContentText())
	if payload.Total != 61 || payload.Showing != 50 || !payload.Truncated {
		t.Fatalf("grep payload = total:%d showing:%d truncated:%t, want 61/50/true; content=%s",
			payload.Total, payload.Showing, payload.Truncated, result.ContentText())
	}
	lowerHint := strings.ToLower(payload.Hint)
	if !strings.Contains(lowerHint, "max_results") || (!strings.Contains(lowerHint, "paths") && !strings.Contains(lowerHint, "glob")) {
		t.Fatalf("grep truncation hint = %q, want guidance to raise max_results or narrow paths/glob", payload.Hint)
	}

	largeRows := lspBinaryGrepRowsForFile(t, root, payload, "00-large.txt")
	if len(largeRows.Rows) != 50 {
		t.Fatalf("large file rows = %d, want 50", len(largeRows.Rows))
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
