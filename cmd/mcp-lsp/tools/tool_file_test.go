package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestRenderReadContentDefaultLimitIsTwoHundredFifty(t *testing.T) {
	var body strings.Builder
	for line := 1; line <= 260; line++ {
		if line > 1 {
			body.WriteByte('\n')
		}
		fmt.Fprintf(&body, "line-%03d", line)
	}

	got := renderReadContent(body.String(), 1, 0, false)
	if !strings.Contains(got, "250: line-250") {
		t.Fatalf("read_file default output missing line 250: %q", got)
	}
	if strings.Contains(got, "251: line-251") {
		t.Fatalf("read_file default output includes line 251: %q", got)
	}
	if !strings.Contains(got, `[scope=lines L1-L250 of 260 total; use pos="file:251" to continue]`) {
		t.Fatalf("read_file continuation hint = %q, want 250-line default", got)
	}
}

func TestFileReadPosWithoutLineReadsFullFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(target, []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := callFileTool(t, root, fileToolInput{Action: "read_file", Pos: "sample.txt"})
	if err != nil {
		t.Fatalf("read_file returned error: %v", err)
	}
	content, ok := got.(string)
	if !ok {
		t.Fatalf("read_file result type = %T, want string", got)
	}
	if !strings.Contains(content, "1: alpha") || !strings.Contains(content, "2: beta") {
		t.Fatalf("read_file content = %q, want full file", content)
	}
	if !strings.Contains(content, "[scope=file 3 lines]") {
		t.Fatalf("read_file footer = %q, want file scope", content)
	}
}

func TestFileHandlerTruncatesSingleReadToTextBudget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "large.txt")
	if err := os.WriteFile(target, []byte(largeLineFileContent(260, 240)), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := callFileTool(t, root, fileToolInput{Action: "read_file", FilePath: target})
	if err != nil {
		t.Fatalf("read_file returned error: %v", err)
	}
	content, ok := got.(string)
	if !ok {
		t.Fatalf("read_file result type = %T, want truncated string", got)
	}
	if len([]byte(content)) > middleware.ToolBudget("file") {
		t.Fatalf("read_file text bytes = %d, want <= %d", len([]byte(content)), middleware.ToolBudget("file"))
	}
	if !strings.Contains(content, "truncated to fit output budget") {
		t.Fatalf("read_file missing budget truncation hint: %q", content)
	}
	if !strings.Contains(content, `large.txt:`) || !strings.Contains(content, "to continue") {
		t.Fatalf("read_file missing continuation hint: %q", content)
	}
}

func TestFileBatchResponseUsesTopLevelTotalShowingAndHint(t *testing.T) {
	root := t.TempDir()
	paths := writeBatchReadFixturePaths(t, root, lspReadFileBatchMax+2)

	got, err := callFileTool(t, root, fileToolInput{Action: "read_file", FilePaths: paths})
	if err != nil {
		t.Fatalf("read_file batch returned error: %v", err)
	}
	payload := mustMarshalObject(t, got)
	requireNumberField(t, payload, "total", len(paths))
	requireNumberField(t, payload, "showing", lspReadFileBatchMax)
	requireBoolField(t, payload, "truncated", true)
	requireStringFieldContains(t, payload, "hint", "file_paths", "batch")
	meta := requireObjectField(t, payload, "meta")
	requireNumberField(t, meta, "max_batch", lspReadFileBatchMax)
	requireNumberField(t, meta, "dropped", len(paths)-lspReadFileBatchMax)
	requireAbsentField(t, meta, "count")
	requireAbsentField(t, meta, "success_count")
	requireAbsentField(t, meta, "total")
	requireAbsentField(t, meta, "showing")
	requireAbsentField(t, meta, "truncated")
	requireAbsentField(t, meta, "hint")
}

func callFileTool(t *testing.T, root string, input fileToolInput) (any, error) {
	t.Helper()
	handler := NewFileHandler(Config{WorkspaceRoot: root})
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), payload)
}

func writeBatchReadFixturePaths(t *testing.T, root string, count int) []string {
	t.Helper()
	paths := make([]string, 0, count)
	for i := range count {
		name := fmt.Sprintf("file-%02d.txt", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("content\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		paths = append(paths, name)
	}
	return paths
}

func largeLineFileContent(lines int, width int) string {
	var body strings.Builder
	for line := 1; line <= lines; line++ {
		if line > 1 {
			body.WriteByte('\n')
		}
		fmt.Fprintf(&body, "line-%03d %s", line, strings.Repeat("x", width))
	}
	return body.String()
}

func TestReadFileWithLimitDoesNotForceLineWindow(t *testing.T) {
	// limit should cap function-mode output, not switch to line-window.
	// Without a registry (no LSP), function mode falls back to line-window
	// with a reason tag. The key assertion: the reason must NOT be
	// "explicit" (which means wantsLineWindow returned true), it should be
	// a fallback reason like "no symbol provider available".
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	content := "package main\n\nfunc hello() {\n\treturn\n}\n"
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// pos=main.go:3 (has line), limit=10 — should attempt function mode
	got, err := callFileTool(t, root, fileToolInput{
		Action: "read_file",
		Pos:    "main.go:3",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("read_file error: %v", err)
	}
	result := got.(string)
	// With no registry, function mode falls back with reason "no symbol
	// provider available", NOT the explicit line-window path.
	if strings.Contains(result, "scope=lines") && !strings.Contains(result, "no symbol provider") {
		t.Fatalf("limit forced line-window without fallback reason: %q", result)
	}
}

func TestReadFileFunctionWindowUsesBestEffortDocumentSymbols(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	content := "package main\n\nfunc hello() {\n\treturn\n}\n"
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &readFileBestEffortSymbolManager{
		bestEffortSymbols: []protocol.DocumentSymbol{{
			Name: "hello",
			Kind: protocol.SymbolKindFunction,
			Range: protocol.Range{
				Start: protocol.Position{Line: 2, Character: 0},
				End:   protocol.Position{Line: 5, Character: 0},
			},
			SelectionRange: protocol.Range{
				Start: protocol.Position{Line: 2, Character: 5},
				End:   protocol.Position{Line: 2, Character: 10},
			},
		}},
	}
	registry := &structureTestRegistry{fileManager: manager}
	handler := NewFileHandler(Config{WorkspaceRoot: root, Registry: registry})

	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), marshalFileToolInput(t, fileToolInput{
		Action: "read_file",
		Pos:    "main.go:3",
		Limit:  20,
	}))
	if err != nil {
		t.Fatalf("read_file returned error: %v", err)
	}
	if manager.documentSymbolCalls != 0 {
		t.Fatalf("DocumentSymbol calls = %d, want best-effort path only", manager.documentSymbolCalls)
	}
	if manager.bestEffortCalls != 1 {
		t.Fatalf("DocumentSymbolBestEffort calls = %d, want 1", manager.bestEffortCalls)
	}
	text, ok := got.(string)
	if !ok {
		t.Fatalf("read_file result = %T, want string", got)
	}
	if !strings.Contains(text, "[scope=function hello L3-L5]") {
		t.Fatalf("read_file result = %q, want function window from best-effort symbols", text)
	}
}

type readFileBestEffortSymbolManager struct {
	structureTestManager
	bestEffortSymbols   []protocol.DocumentSymbol
	documentSymbolCalls int
	bestEffortCalls     int
}

func (m *readFileBestEffortSymbolManager) DocumentSymbol(context.Context, string) ([]protocol.DocumentSymbol, error) {
	m.documentSymbolCalls++
	return nil, nil
}

func (m *readFileBestEffortSymbolManager) DocumentSymbolBestEffort(context.Context, string) ([]protocol.DocumentSymbol, error) {
	m.bestEffortCalls++
	return m.bestEffortSymbols, nil
}

func TestFileReadRejectsExplicitAbsoluteWorkDirOutsideWorkspaceRoots(t *testing.T) {
	staleRoot := t.TempDir()
	explicitRoot := t.TempDir()
	target := filepath.Join(explicitRoot, "sample.txt")
	if err := os.WriteFile(target, []byte("needle\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	handler := NewFileHandler(Config{WorkspaceRoot: staleRoot})
	payload, err := json.Marshal(map[string]any{
		"action":    "read_file",
		"file_path": "sample.txt",
		"work_dir":  explicitRoot,
		"scope":     "lines",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            staleRoot,
		WorkspaceRoots: []string{staleRoot},
		Family:         "lsp",
	})

	got, err := handler(ctx, payload)
	if err == nil || !strings.Contains(err.Error(), "outside workspace roots") {
		t.Fatalf("file read error = %v, want outside workspace roots rejection", err)
	}
	if got != nil {
		t.Fatalf("file read result = %#v, want nil on rejected work_dir", got)
	}
}
