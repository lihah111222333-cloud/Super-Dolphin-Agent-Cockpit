package tools

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

// callMCPTool Helper to invoke an MCP tool handler in a configured CWD context.
func callMCPTool(t *testing.T, handler Handler, root string, input any) (any, error) {
	t.Helper()
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            root,
		WorkspaceRoots: []string{root},
		Family:         "lsp",
	})
	return handler(ctx, payload)
}

// TestLSPContractMatrix_StrictErrors_LineOutOfRange verifies standardized coded error
// format when requested line is beyond the file bounds.
func TestLSPContractMatrix_StrictErrors_LineOutOfRange(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sample.go")
	if err := os.WriteFile(target, []byte("package sample\n\nfunc hello() {}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root})
	_, _, err := resolveFilePositionRequest(ctx, filePositionParams{
		Pos: "sample.go:50:1",
	})
	if err == nil {
		t.Fatalf("expected error for line out of range, got nil")
	}

	var coded *common.CodedToolError
	if !errors.As(err, &coded) {
		t.Fatalf("expected coded error, got type %T: %v", err, err)
	}
	if coded.Code != "line_out_of_range" {
		t.Fatalf("expected error code line_out_of_range, got: %s", coded.Code)
	}
	if !strings.Contains(coded.Hint, "next:") {
		t.Fatalf("expected hint to contain 'next:', got: %s", coded.Hint)
	}
}

// TestLSPContractMatrix_StrictErrors_PositionOutOfRange verifies standardized coded error
// format when requested column is beyond the line bounds.
func TestLSPContractMatrix_StrictErrors_PositionOutOfRange(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sample.go")
	if err := os.WriteFile(target, []byte("package sample\n\nfunc hello() {}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root})
	_, _, err := resolveFilePositionRequest(ctx, filePositionParams{
		Pos: "sample.go:3:100",
	})
	if err == nil {
		t.Fatalf("expected error for column out of range, got nil")
	}

	var coded *common.CodedToolError
	if !errors.As(err, &coded) {
		t.Fatalf("expected coded error, got: %v", err)
	}
	if coded.Code != "position_out_of_range" {
		t.Fatalf("expected error code position_out_of_range, got: %s", coded.Code)
	}
	if !strings.Contains(coded.Hint, "next:") {
		t.Fatalf("expected hint to contain 'next:', got: %s", coded.Hint)
	}
	if coded.Meta["line_text"] != "func hello() {}" {
		t.Fatalf("expected meta line_text func hello() {}, got: %#v", coded.Meta["line_text"])
	}
}

// TestLSPContractMatrix_FileInputOutputs_Single validates the single file read_file contract.
func TestLSPContractMatrix_FileInputOutputs_Single(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sample.go")
	content := "package main\n\nimport \"fmt\"\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	handler := NewFileHandler(Config{WorkspaceRoot: root})

	got, err := callMCPTool(t, handler, root, map[string]any{
		"action": "read_file",
		"pos":    "sample.go:3",
		"scope":  "lines",
		"limit":  10,
	})
	if err != nil {
		t.Fatalf("read_file failed: %v", err)
	}
	text, ok := got.(string)
	if !ok {
		t.Fatalf("read_file output type = %T, want string", got)
	}
	if !strings.Contains(text, "3: import") {
		t.Fatalf("read_file missing line 3: %q", text)
	}
	if !strings.Contains(text, "scope=lines") {
		t.Fatalf("read_file footer missing scope=lines: %q", text)
	}
}

// TestLSPContractMatrix_FileInputOutputs_Batch validates the batch file read_file contract.
func TestLSPContractMatrix_FileInputOutputs_Batch(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sample.go")
	content := "package main\n\nimport \"fmt\"\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	handler := NewFileHandler(Config{WorkspaceRoot: root})

	gotBatch, err := callMCPTool(t, handler, root, map[string]any{
		"action":     "read_file",
		"file_paths": []string{"sample.go"},
	})
	if err != nil {
		t.Fatalf("batch read failed: %v", err)
	}
	resp, ok := gotBatch.(batchReadResponse)
	if !ok {
		t.Fatalf("batch read output type = %T, want batchReadResponse", gotBatch)
	}
	if !resp.Success {
		t.Fatalf("batch read failed internally")
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 item in Data, got: %d", len(resp.Data))
	}
	if !strings.Contains(resp.Data[0].FilePath, "sample.go") {
		t.Fatalf("expected FilePath containing sample.go, got: %s", resp.Data[0].FilePath)
	}
}

// TestLSPContractMatrix_GrepOutputs validates text_search returns a schema
// that encloses items in `data`, using `col` columns and `rows` arrays.
func TestLSPContractMatrix_GrepOutputs(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sample.go")
	if err := os.WriteFile(target, []byte("package main\n\nfunc mySearchFunc() {}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	handler := NewGrepHandler(Config{WorkspaceRoot: root})

	got, err := callMCPTool(t, handler, root, map[string]any{
		"action": "text_search",
		"query":  "mySearchFunc",
		"paths":  []string{root},
		"glob":   "*.go",
	})
	if err != nil {
		t.Fatalf("grep failed: %v", err)
	}

	resp, ok := got.(grepResponse)
	if !ok {
		t.Fatalf("grep output type = %T, want grepResponse", got)
	}

	if resp.Total != 1 {
		t.Fatalf("expected total 1, got: %d", resp.Total)
	}

	assertGrepResponseData(t, resp, "sample.go")
}

func assertGrepResponseData(t *testing.T, resp grepResponse, expectedName string) {
	t.Helper()
	var matchedFile string
	for k := range resp.Data {
		matchedFile = k
	}
	if !strings.Contains(matchedFile, expectedName) {
		t.Fatalf("matched file missing %s: %s", expectedName, matchedFile)
	}

	fileRows := resp.Data[matchedFile]
	assertGrepColumns(t, fileRows.Cols)

	if len(fileRows.Rows) != 1 {
		t.Fatalf("expected 1 row, got: %d", len(fileRows.Rows))
	}
	row := fileRows.Rows[0]
	if len(row) < 3 {
		t.Fatalf("expected at least 3 cells, got: %#v", row)
	}
	lineVal, _ := row[0].(int)
	colVal, _ := row[1].(int)
	textVal, _ := row[2].(string)
	if lineVal != 3 || colVal != 6 || !strings.Contains(textVal, "mySearchFunc") {
		t.Fatalf("unexpected search match row cells: %#v", row)
	}
}

func assertGrepColumns(t *testing.T, cols []string) {
	t.Helper()
	hasCol := false
	for _, colName := range cols {
		if colName == "col" {
			hasCol = true
		}
		if colName == "column" {
			t.Fatalf("found deprecated 'column' field in Grep columns: %#v", cols)
		}
	}
	if !hasCol {
		t.Fatalf("missing standard 'col' field in Grep columns: %#v", cols)
	}
}

// TestLSPContractMatrix_CompletionOutputs verifies completion tool responses.
func TestLSPContractMatrix_CompletionOutputs(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sample.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	reg := &structureTestRegistry{fileManager: &structureTestManager{}}
	handler := NewCompletionHandler(reg)
	got, err := callMCPTool(t, handler, root, map[string]any{
		"pos": "sample.go:1:1",
	})
	if err != nil {
		t.Fatalf("completion failed: %v", err)
	}
	assertEmptyCompletionProtocol(t, got)
}

// assertEmptyCompletionProtocol 校验零候选 completion 的严格行协议与运行时归因。
func assertEmptyCompletionProtocol(t *testing.T, got any) {
	t.Helper()
	provider, ok := got.(completionTextProvider)
	if !ok {
		t.Fatalf("completion output type = %T, want completionTextProvider", got)
	}
	doc, err := lineprotocol.Parse(provider.ToPlainText())
	if err != nil {
		t.Fatalf("parse completion line protocol: %v", err)
	}
	wantHeader := lineprotocol.Header{Total: 0, Showing: 0, Unit: "completion"}
	if doc.Error != nil || doc.Header != wantHeader {
		t.Fatalf("completion header = %#v error=%#v, want %#v", doc.Header, doc.Error, wantHeader)
	}
	var attributes []lineprotocol.Record
	for _, record := range doc.Records {
		if record.Kind == "ATTR" {
			attributes = append(attributes, record)
		}
	}
	if len(attributes) != 1 {
		t.Fatalf("completion records = %#v, want exactly one ATTR", doc.Records)
	}
	wantFields := map[string]string{
		"language_id": "go", "server_name": "unknown", "server_version": "unknown",
		"capability": "supported", "reason": "no_candidates",
	}
	if !maps.Equal(attributes[0].Fields, wantFields) {
		t.Fatalf("completion ATTR = %#v, want %#v", attributes[0].Fields, wantFields)
	}
}
