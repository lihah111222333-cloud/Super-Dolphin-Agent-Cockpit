package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPBinaryGrepCapsEachFileWithoutDroppingOtherFiles(t *testing.T) {
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
	if payload.Total != 61 || payload.Showing != 51 || !payload.Truncated {
		t.Fatalf("grep payload = total:%d showing:%d truncated:%t, want 61/51/true; content=%s",
			payload.Total, payload.Showing, payload.Truncated, result.ContentText())
	}

	largeRows := lspBinaryGrepRowsForFile(t, root, payload, "00-large.txt")
	smallRows := lspBinaryGrepRowsForFile(t, root, payload, "zz-small.txt")
	if len(largeRows.Rows) != 50 {
		t.Fatalf("large file rows = %d, want 50", len(largeRows.Rows))
	}
	if len(smallRows.Rows) != 1 {
		t.Fatalf("small file rows = %d, want 1", len(smallRows.Rows))
	}
	if got := lspBinaryGrepRowText(t, smallRows.Rows[0]); got != "needle small" {
		t.Fatalf("small file row text = %q, want needle small", got)
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

func lspBinaryGrepRowText(t *testing.T, row []any) string {
	t.Helper()
	if len(row) < 3 {
		t.Fatalf("grep row has %d cells, want at least 3: %#v", len(row), row)
	}
	text, ok := row[2].(string)
	if !ok {
		t.Fatalf("grep row text cell = %T %[1]v, want string", row[2])
	}
	return text
}
