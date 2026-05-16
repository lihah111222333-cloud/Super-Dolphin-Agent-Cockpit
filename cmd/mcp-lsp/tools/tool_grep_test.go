package tools

import (
	"context"
	"encoding/json"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/search"
)

func TestGrepTextSearchFallsBackToLiteralOnRegexParseError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("literal [ match\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	handler := NewGrepHandler(Config{WorkspaceRoot: root})
	input, err := json.Marshal(grepToolInput{
		Action: "text_search",
		Query:  "[",
		Regex:  true,
		Path:   root,
		Glob:   "*.txt",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input)
	if err != nil {
		t.Fatalf("grep returned error: %v", err)
	}
	resp, ok := got.(grepResponse)
	if !ok {
		t.Fatalf("grep result type = %T, want grepResponse", got)
	}
	if !resp.RegexFallback {
		t.Fatalf("RegexFallback = false, want true")
	}
	if resp.Showing != 1 || resp.Total != 1 {
		t.Fatalf("counts = showing %d total %d, want 1/1", resp.Showing, resp.Total)
	}
	if !strings.Contains(resp.Message, "retried query as literal") {
		t.Fatalf("message = %q, want literal fallback hint", resp.Message)
	}
}

func TestGrepTextSearchEmptyResultHasMessage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	handler := NewGrepHandler(Config{WorkspaceRoot: root})
	input, err := json.Marshal(grepToolInput{Action: "text_search", Query: "missing", Path: root})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input)
	if err != nil {
		t.Fatalf("grep returned error: %v", err)
	}
	resp, ok := got.(grepResponse)
	if !ok {
		t.Fatalf("grep result type = %T, want grepResponse", got)
	}
	if resp.Message != "no matches found" {
		t.Fatalf("message = %q, want no matches found", resp.Message)
	}
}

func TestBuildGrepResponseOmitsFuncCellsWhenAbsent(t *testing.T) {
	resp := buildGrepResponse([]search.SearchMatch{{
		File: "/tmp/sample.go",
		Line: 10,
		Col:  3,
		Text: "match",
	}}, 1, false)
	rows := resp.Files["/tmp/sample.go"].Rows
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if len(rows[0]) != 3 {
		t.Fatalf("row = %#v, want only line/col/text cells", rows[0])
	}
}

func TestBuildGrepResponseIncludesFuncCellsWhenPresent(t *testing.T) {
	resp := buildGrepResponse([]search.SearchMatch{{
		File:      "/tmp/sample.go",
		Line:      10,
		Col:       3,
		Text:      "match",
		FuncStart: 8,
		FuncEnd:   12,
	}}, 1, false)
	row := resp.Files["/tmp/sample.go"].Rows[0]
	if len(row) != 5 {
		t.Fatalf("row = %#v, want func_start/func_end cells", row)
	}
	if row[3] != 8 || row[4] != 12 {
		t.Fatalf("func cells = %#v, want 8/12", row[3:])
	}
}
