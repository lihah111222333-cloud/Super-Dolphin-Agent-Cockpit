package main

import (
	"encoding/json"
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
		"path":   root,
		"glob":   "*.txt",
	})
	if result.IsError {
		t.Fatalf("grep returned tool error: %s", result.ContentText())
	}
	var payload lspBinaryGrepResponse
	decodeLSPBinaryStructuredContent(t, result, &payload)
	if payload.Total != 50 || payload.Showing != 50 || !payload.Truncated {
		t.Fatalf("grep payload = total:%d showing:%d truncated:%t, want 50/50/true; content=%s",
			payload.Total, payload.Showing, payload.Truncated, result.ContentText())
	}
	lowerHint := strings.ToLower(payload.Hint)
	if !strings.Contains(lowerHint, "max_results") || (!strings.Contains(lowerHint, "path") && !strings.Contains(lowerHint, "glob")) {
		t.Fatalf("grep truncation hint = %q, want guidance to raise max_results or narrow path/glob", payload.Hint)
	}

	largeRows := lspBinaryGrepRowsForFile(t, root, payload, "00-large.txt")
	if len(largeRows.Rows) != 50 {
		t.Fatalf("large file rows = %d, want 50", len(largeRows.Rows))
	}
}

type lspBinaryGrepFileRows struct {
	Cols []string `json:"cols"`
	Rows [][]any  `json:"rows"`
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
	for path, raw := range payload.Data {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relative grep path %q: %v", path, err)
		}
		if filepath.ToSlash(rel) != wantRel {
			continue
		}
		var rows lspBinaryGrepFileRows
		if err := json.Unmarshal(raw, &rows); err != nil {
			t.Fatalf("decode grep rows for %s: %v; raw=%s", wantRel, err, raw)
		}
		return rows
	}
	t.Fatalf("grep payload missing %s; structured files=%#v", wantRel, payload.Data)
	return lspBinaryGrepFileRows{}
}
