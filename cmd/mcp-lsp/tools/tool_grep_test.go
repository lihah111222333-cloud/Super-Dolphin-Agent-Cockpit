package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/search"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestGrepInvalidRegexReturnsErrorWithoutLiteralFallback(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("literal [ match\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, err := callGrepTool(t, root, grepToolInput{
		Action: "text_search",
		Query:  "[",
		Regex:  true,
		Path:   root,
		Glob:   "*.txt",
	})
	if err == nil || !strings.Contains(err.Error(), "regex") {
		t.Fatalf("grep invalid regex error = %v, want regex syntax error", err)
	}
}

func TestGrepInvalidGlobReturnsError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("needle\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, err := callGrepTool(t, root, grepToolInput{
		Action: "text_search",
		Query:  "needle",
		Path:   root,
		Glob:   "[",
	})
	if err == nil || !strings.Contains(err.Error(), "glob") {
		t.Fatalf("grep invalid glob error = %v, want glob syntax error", err)
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

func TestGrepTextSearchSingleTSVFileHonorsGlob(t *testing.T) {
	root := canonicalGrepPath(t, t.TempDir())
	target := filepath.Join(root, "index.tsv")
	if err := os.WriteFile(target, []byte("path\tmodule\ncmd/mcp-orch/main.go\tcmd\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := callGrepTool(t, root, grepToolInput{
		Action:     "text_search",
		Query:      "cmd/mcp-orch/main.go",
		Path:       target,
		Glob:       "*.tsv",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("grep returned error: %v", err)
	}
	resp, ok := got.(grepResponse)
	if !ok {
		t.Fatalf("grep result type = %T, want grepResponse", got)
	}
	if resp.Total != 1 || resp.Showing != 1 {
		t.Fatalf("grep totals = total:%d showing:%d, want single TSV match", resp.Total, resp.Showing)
	}
	if _, ok := resp.Data[target]; !ok {
		t.Fatalf("grep data = %#v, want match for %s", resp.Data, target)
	}
}

func TestGrepTextSearchAcceptsCommonMultiPathParams(t *testing.T) {
	root := t.TempDir()
	writeGrepFixtureFile(t, filepath.Join(root, "first", "one.go"), "package first\nconst needleOne = true\n")
	writeGrepFixtureFile(t, filepath.Join(root, "second", "two.go"), "package second\nconst needleTwo = true\n")
	writeGrepFixtureFile(t, filepath.Join(root, "third", "skip.go"), "package third\nconst needleThree = true\n")

	cases := map[string]map[string]any{
		"path_array":       {"path": []string{"first", "second"}},
		"paths_array":      {"paths": []string{"first", "second"}},
		"paths_string":     {"paths": "first,second"},
		"file_paths_array": {"file_paths": []string{"first", "second"}},
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			input := map[string]any{
				"action": "text_search",
				"query":  "needle",
				"glob":   "*.go",
			}
			maps.Copy(input, fields)
			got, err := callGrepToolRaw(t, root, input)
			if err != nil {
				t.Fatalf("grep returned error: %v", err)
			}
			resp, ok := got.(grepResponse)
			if !ok {
				t.Fatalf("grep result type = %T, want grepResponse", got)
			}
			assertGrepResponseRelativeFiles(t, root, resp, []string{"first/one.go", "second/two.go"}, "third/skip.go")
		})
	}
}

func TestGrepTextSearchRejectsEmptyPathArray(t *testing.T) {
	root := t.TempDir()
	_, err := callGrepToolRaw(t, root, map[string]any{
		"action": "text_search",
		"query":  "needle",
		"path":   []string{},
	})
	if err == nil || !strings.Contains(err.Error(), "path array must contain at least one path") {
		t.Fatalf("grep empty path array error = %v, want path array validation", err)
	}
}

func TestGrepDefaultMaxResultsIsFifty(t *testing.T) {
	root := t.TempDir()
	var body strings.Builder
	for i := range 60 {
		fmt.Fprintf(&body, "needle-%02d\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte(body.String()), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := callGrepTool(t, root, grepToolInput{
		Action: "text_search",
		Query:  "needle",
		Path:   root,
		Glob:   "*.txt",
	})
	if err != nil {
		t.Fatalf("grep returned error: %v", err)
	}
	resp, ok := got.(grepResponse)
	if !ok {
		t.Fatalf("grep result type = %T, want grepResponse", got)
	}
	if resp.Showing != maxSearchResults {
		t.Fatalf("showing = %d, want %d", resp.Showing, maxSearchResults)
	}
	if !resp.Truncated {
		t.Fatalf("truncated = false, want true")
	}
	if resp.DroppedForPayload != 0 {
		t.Fatalf("dropped_for_payload = %d, want 0", resp.DroppedForPayload)
	}
}

func TestGrepTextSearchExcludesWorkspaceCacheDirectoriesFromRootSearch(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "admin-ui-v2", "eslint.config.js")
	writeGrepFixtureFile(t, keep, "export default defineConfig([])\n")
	for _, rel := range []string{
		".gomodcache/github.com/arl/statsviz@v0.8.0/internal/static/vite.config.js",
		".tools/gomodcache/github.com/arl/statsviz@v0.8.0/internal/static/vite.config.js",
		"cache/vite.config.js",
		"dist/vite.config.js",
		"node_modules/vite/config.js",
	} {
		writeGrepFixtureFile(t, filepath.Join(root, rel), "export default defineConfig({})\n")
	}

	got, err := callGrepTool(t, root, grepToolInput{
		Action:     "text_search",
		Query:      "defineConfig",
		Path:       root,
		Glob:       "*.js",
		MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("grep returned error: %v", err)
	}
	resp, ok := got.(grepResponse)
	if !ok {
		t.Fatalf("grep result type = %T, want grepResponse", got)
	}
	if resp.Total != 1 || resp.Showing != 1 || resp.Truncated {
		t.Fatalf("grep totals = total:%d showing:%d truncated:%t, want only source match", resp.Total, resp.Showing, resp.Truncated)
	}
	wantKeep := canonicalGrepPath(t, keep)
	if _, ok := resp.Data[wantKeep]; !ok {
		t.Fatalf("grep data = %#v, want only %q", resp.Data, wantKeep)
	}
}

func TestGrepRuntimeFallbackRejectsClaudeWorktreeSearch(t *testing.T) {
	root := t.TempDir()
	relPath := filepath.Join("docs", "li", "p15", "TASKS", "TN-integration.md")
	worktreeRoot := filepath.Join(root, ".claude", "worktrees", "feature")
	target := filepath.Join(worktreeRoot, relPath)
	writeGrepFixtureFile(t, filepath.Join(root, relPath), "stale notes\n")
	writeGrepFixtureFile(t, target, "fresh notes\nBenchmarkTickAppendStrictParallel\n")

	handler := NewGrepHandler(Config{WorkspaceRoot: root})
	payload, err := json.Marshal(grepToolInput{
		Action:     "text_search",
		Query:      "BenchmarkTickAppendStrictParallel",
		Path:       relPath,
		MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("marshal grep input: %v", err)
	}
	ctx := common.WithRuntimeWorkspaceScopeFallback(testToolContext(root))

	_, err = handler(ctx, payload)
	if err == nil {
		t.Fatal("grep returned nil error, want stale workspace root rejection")
	}
	if !strings.Contains(err.Error(), "mcp-lsp: stale workspace root; pass work_dir or _workspaceRoots") {
		t.Fatalf("grep error = %v, want stale workspace root guidance for %s", err, target)
	}
}

func TestGrepRuntimeFallbackDoesNotSearchSiblingWorktree(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "main")
	sibling := filepath.Join(parent, "feature")
	relPath := filepath.Join("docs", "risk.md")
	writeGrepFixtureFile(t, filepath.Join(root, relPath), "stale notes\n")
	writeGrepFixtureFile(t, filepath.Join(sibling, relPath), "fresh L06L07Needle\n")

	handler := NewGrepHandler(Config{WorkspaceRoot: root})
	payload, err := json.Marshal(grepToolInput{
		Action:     "text_search",
		Query:      "L06L07Needle",
		Path:       relPath,
		MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("marshal grep input: %v", err)
	}

	_, err = handler(common.WithRuntimeWorkspaceScopeFallback(testToolContext(root)), payload)
	if err == nil {
		t.Fatal("grep returned nil error, want stale workspace root rejection before sibling search")
	}
	if !strings.Contains(err.Error(), "mcp-lsp: stale workspace root; pass work_dir or _workspaceRoots") {
		t.Fatalf("grep error = %v, want stale workspace root guidance", err)
	}
}

func TestGrepRuntimeFallbackAllowsExplicitNestedWorkDirEmptyResult(t *testing.T) {
	root := t.TempDir()
	worktreeRoot := filepath.Join(root, ".worktrees", "feature")
	relPath := filepath.Join("docs", "risk.md")
	writeGrepFixtureFile(t, filepath.Join(worktreeRoot, relPath), "fresh notes\n")

	handler := NewGrepHandler(Config{WorkspaceRoot: root})
	payload, err := json.Marshal(map[string]any{
		"action":      "text_search",
		"query":       "NeedleThatDoesNotExist",
		"path":        relPath,
		"work_dir":    worktreeRoot,
		"max_results": 5,
	})
	if err != nil {
		t.Fatalf("marshal grep input: %v", err)
	}

	got, err := handler(common.WithRuntimeWorkspaceScopeFallback(testToolContext(root)), payload)
	if err != nil {
		t.Fatalf("grep with explicit nested work_dir returned error: %v", err)
	}
	resp, ok := got.(grepResponse)
	if !ok {
		t.Fatalf("grep result type = %T, want grepResponse", got)
	}
	if resp.Total != 0 || resp.Message != "no matches found" {
		t.Fatalf("grep response = total:%d message:%q, want empty no-match result", resp.Total, resp.Message)
	}
}

func TestGrepRequiresTrustedWorkspaceRootsWhenRuntimeFallbackWouldApply(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "main")
	sibling := filepath.Join(parent, "feature")
	relPath := filepath.Join("docs", "risk.md")
	writeGrepFixtureFile(t, filepath.Join(root, relPath), "stale notes\n")
	writeGrepFixtureFile(t, filepath.Join(sibling, relPath), "fresh L06L07Needle\n")

	handler := NewGrepHandler(Config{WorkspaceRoot: root})
	payload, err := json.Marshal(grepToolInput{
		Action:     "text_search",
		Query:      "L06L07Needle",
		Path:       relPath,
		MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("marshal grep input: %v", err)
	}

	ctx := context.WithValue(context.Background(), common.CwdContextKey, root)
	ctx = common.WithRuntimeWorkspaceScopeFallback(ctx)
	_, err = handler(ctx, payload)
	if err == nil {
		t.Fatal("grep returned nil error, want missing trusted workspace roots rejection")
	}
	if !strings.Contains(err.Error(), "mcp-lsp: stale workspace root; pass work_dir or _workspaceRoots") {
		t.Fatalf("grep error = %v, want trusted roots guidance", err)
	}
}

func TestGrepHandlerAppliesSixteenKiBPayloadBudget(t *testing.T) {
	root := t.TempDir()
	writeLargeGrepFixture(t, root)

	got, err := callGrepTool(t, root, grepToolInput{
		Action:     "text_search",
		Query:      "needle",
		Path:       root,
		Glob:       "*.txt",
		MaxResults: maxSearchResults,
	})
	if err != nil {
		t.Fatalf("grep returned error: %v", err)
	}
	resp, ok := got.(grepResponse)
	if !ok {
		t.Fatalf("grep result type = %T, want grepResponse", got)
	}
	raw := mustMarshalGrepResponse(t, resp)
	if len(raw) > middleware.ToolBudget("grep") {
		t.Fatalf("grep payload = %d bytes, want <= %d", len(raw), middleware.ToolBudget("grep"))
	}
	if resp.DroppedForPayload == 0 || resp.Showing >= maxSearchResults {
		t.Fatalf("grep budget did not drop rows: showing=%d dropped=%d", resp.Showing, resp.DroppedForPayload)
	}
}

func TestBuildGrepResponseOmitsFuncCellsWhenAbsent(t *testing.T) {
	resp := buildGrepResponse([]search.SearchMatch{{
		File: "/tmp/sample.go",
		Line: 10,
		Col:  3,
		Text: "match",
	}}, 1, false)
	rows := resp.Data["/tmp/sample.go"].Rows
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
	row := resp.Data["/tmp/sample.go"].Rows[0]
	if len(row) != 5 {
		t.Fatalf("row = %#v, want func_start/func_end cells", row)
	}
	if row[3] != 8 || row[4] != 12 {
		t.Fatalf("func cells = %#v, want 8/12", row[3:])
	}
}

func TestBuildGrepResponsePadsMixedFuncRows(t *testing.T) {
	resp := buildGrepResponse([]search.SearchMatch{
		{
			File: "/tmp/sample.go",
			Line: 10,
			Col:  3,
			Text: "plain match",
		},
		{
			File:      "/tmp/sample.go",
			Line:      12,
			Col:       5,
			Text:      "function match",
			FuncStart: 8,
			FuncEnd:   14,
		},
	}, 2, false)
	block := resp.Data["/tmp/sample.go"]
	if len(block.Cols) != 5 {
		t.Fatalf("cols = %#v, want func range columns", block.Cols)
	}
	for idx, row := range block.Rows {
		if len(row) != len(block.Cols) {
			t.Fatalf("row %d = %#v, want %d cells for cols %#v", idx, row, len(block.Cols), block.Cols)
		}
	}
	if block.Rows[0][3] != nil || block.Rows[0][4] != nil {
		t.Fatalf("plain row func cells = %#v, want nil placeholders", block.Rows[0][3:])
	}
	if block.Rows[1][3] != 8 || block.Rows[1][4] != 14 {
		t.Fatalf("function row func cells = %#v, want 8/14", block.Rows[1][3:])
	}
}

func TestCapGrepResponseBytesCountsTruncationMessage(t *testing.T) {
	const budget = 256
	resp := grepResponseCrossingBudgetByMessage(t, budget)

	capGrepResponseBytes(&resp, budget)
	raw := mustMarshalGrepResponse(t, resp)
	if len(raw) > budget {
		t.Fatalf("capped response = %d bytes, want <= %d: %s", len(raw), budget, raw)
	}
	if resp.Showing != 0 {
		t.Fatalf("showing = %d, want dropped to 0", resp.Showing)
	}
	if resp.DroppedForPayload <= 1 {
		t.Fatalf("dropped_for_payload = %d, want incremented", resp.DroppedForPayload)
	}
}

func grepResponseCrossingBudgetByMessage(t *testing.T, budget int) grepResponse {
	t.Helper()
	resp := grepResponse{
		Data: map[string]grepFileRows{
			"/tmp/sample.go": {
				Cols: []string{"line", "col", "text"},
			},
		},
		Total:             1,
		Showing:           1,
		Truncated:         true,
		DroppedForPayload: 1,
	}
	for size := 1; size < 512; size++ {
		candidate := resp
		fileRows := candidate.Data["/tmp/sample.go"]
		fileRows.Rows = [][]any{{1, 1, strings.Repeat("x", size)}}
		candidate.Data = map[string]grepFileRows{"/tmp/sample.go": fileRows}
		rawWithoutMessage := mustMarshalGrepResponse(t, candidate)
		candidate.Message = grepMessage(candidate.RegexFallback, candidate.DroppedForPayload)
		rawWithMessage := mustMarshalGrepResponse(t, candidate)
		if len(rawWithoutMessage) <= budget && len(rawWithMessage) > budget {
			return candidate
		}
	}
	t.Fatalf("test fixture did not find a response crossing budget only after message")
	return grepResponse{}
}

func mustMarshalGrepResponse(t *testing.T, resp grepResponse) []byte {
	t.Helper()
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal grep response: %v", err)
	}
	return raw
}

func writeLargeGrepFixture(t *testing.T, root string) {
	t.Helper()
	for i := range 50 {
		name := fmt.Sprintf("file-%02d-%s.txt", i, strings.Repeat("x", 120))
		body := "needle " + strings.Repeat("payload", 30) + "\n"
		writeGrepFixtureFile(t, filepath.Join(root, name), body)
	}
}

func writeGrepFixtureFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir grep fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write grep fixture: %v", err)
	}
}

func canonicalGrepPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalize grep path %s: %v", path, err)
	}
	return filepath.ToSlash(resolved)
}

func assertGrepResponseRelativeFiles(t *testing.T, root string, resp grepResponse, wants []string, unwanted string) {
	t.Helper()
	resolvedRoot := canonicalGrepPath(t, root)
	got := make(map[string]bool, len(resp.Data))
	for file := range resp.Data {
		rel, err := filepath.Rel(resolvedRoot, file)
		if err != nil {
			t.Fatalf("relative grep result path %s: %v", file, err)
		}
		got[filepath.ToSlash(rel)] = true
	}
	for _, want := range wants {
		if !got[want] {
			t.Fatalf("grep paths = %#v, missing %s", got, want)
		}
	}
	if got[unwanted] {
		t.Fatalf("grep paths = %#v, searched path outside requested scopes", got)
	}
}

func callGrepTool(t *testing.T, root string, input grepToolInput) (any, error) {
	t.Helper()
	return callGrepToolRaw(t, root, input)
}

func callGrepToolRaw(t *testing.T, root string, input any) (any, error) {
	t.Helper()
	handler := NewGrepHandler(Config{WorkspaceRoot: root})
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), payload)
}
