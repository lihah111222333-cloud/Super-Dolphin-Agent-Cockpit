package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

// TestMcpLSPToolPlainTextContractB1 锁定基础工具的精确计数、选择器、错误和可逆行协议。
func TestMcpLSPToolPlainTextContractB1(t *testing.T) {
	t.Run("row escaping round trips tab newline and backslash", testLineProtocolEscaping)
	t.Run("typed error envelope renders stable error records", testToolErrorEnvelopeLineProtocol)
	t.Run("grep preserves pre-limit total", testGrepPreLimitTotal)
	t.Run("invalid regex is typed invalid params", testInvalidRegexTypedError)
	t.Run("signature empty result has executable cursor hint", testSignatureEmptyHint)
	t.Run("workspace file selector accepts language id override", testWorkspaceFileLanguageOverride)
	t.Run("workspace language selector rejects conflicting language id", testWorkspaceLanguageConflict)
	t.Run("folding range preserves pre-limit total", testFoldingRangeTotal)
	t.Run("document symbol renders recursive nodes compactly", testDocumentSymbolLineProtocol)
	t.Run("document symbol hard limit explains recovery", testDocumentSymbolHardLimitRecovery)
}

func testToolErrorEnvelopeLineProtocol(t *testing.T) {
	text, handled := FormatToPlainText(common.ToolErrorEnvelope{
		Error: "invalid regex", Code: "invalid_params", Hint: "set regex=false", Retryable: false,
	})
	if !handled {
		t.Fatal("FormatToPlainText(ToolErrorEnvelope) handled = false")
	}
	want := "ERROR code=invalid_params retryable=0\nMESSAGE\tinvalid regex\nHINT\tset regex=false"
	if text != want {
		t.Fatalf("error envelope text = %q, want %q", text, want)
	}
}

func testLineProtocolEscaping(t *testing.T) {
	file := "dir/tab\tname\\file.go"
	lineText := "alpha\tbeta\nslash\\tail"
	response := grepResponse{
		Data:  map[string]grepFileRows{file: {Cols: []string{"line", "col", "text"}, Rows: [][]any{{7, 3, lineText}}}},
		Total: 1, Showing: 1,
	}
	text := response.ToPlainText()
	assertProtocolHeaderAndRows(t, text, "OK total=1 showing=1 truncated=0 unit=match", 1)
	fields := decodeProtocolRowForTest(t, firstProtocolRow(t, text))
	for key, want := range map[string]string{"file": file, "line": "7", "col": "3", "text": lineText} {
		if fields[key] != want {
			t.Errorf("decoded %s = %q, want %q; text=%q", key, fields[key], want, text)
		}
	}
}

func testGrepPreLimitTotal(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 16)
	for i := range lines {
		lines[i] = fmt.Sprintf("needle-%02d", i)
	}
	if err := os.WriteFile(filepath.Join(root, "matches.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write grep fixture: %v", err)
	}
	handler := NewGrepHandler(Config{WorkspaceRoot: root})
	result, err := callPlainTextContractHandler(t, handler, root, grepToolInput{
		Action: "text_search", Query: "needle", Paths: []string{root}, Glob: "*.txt", MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("real grep handler error = %v", err)
	}
	text := plainTextForContractResult(t, result)
	assertProtocolHeaderAndRows(t, text, "OK total=16 showing=5 truncated=1 unit=match", 5)
}

func testInvalidRegexTypedError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	handler := NewGrepHandler(Config{WorkspaceRoot: root})
	raw := mustJSONForPlainTextContract(t, grepToolInput{Action: "text_search", Query: "[", Paths: []string{root}, Regex: true})
	_, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}}), raw)
	assertCodedToolError(t, err, "invalid_params", false)
}

func testSignatureEmptyHint(t *testing.T) {
	result, err := runSignatureHelp(context.Background(), &structureTestManager{}, "sample.go", protocol.Position{})
	if err != nil {
		t.Fatalf("runSignatureHelp() error = %v", err)
	}
	text := plainTextForContractResult(t, result)
	for _, want := range []string{
		"OK total=0 showing=0 truncated=0 unit=signature",
		"HINT\tmove-cursor-inside-call-arguments-or-after-comma",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("signature empty text missing %q: %q", want, text)
		}
	}
}

func testWorkspaceFileLanguageOverride(t *testing.T) {
	root, target := writeWorkspaceSelectorFixture(t)
	manager := &structureTestManager{workspaceSymbols: makeWorkspaceSymbols(1)}
	registry := &structureTestRegistry{fileManager: manager}
	handler := NewStructureHandler(registry)
	result, err := callPlainTextContractHandler(t, handler, root, structureParams{
		Action: "workspace_symbol", FilePath: target, LanguageID: "go", Query: "Symbol00",
	})
	if err != nil {
		t.Fatalf("file_path+language_id=go returned error: %v", err)
	}
	if result == nil || registry.fileWithLanguageCalls != 1 || registry.gotLanguageID != "go" {
		t.Fatalf("file selector did not use go override: result=%T calls=%d language=%q",
			result, registry.fileWithLanguageCalls, registry.gotLanguageID)
	}
}

func testWorkspaceLanguageConflict(t *testing.T) {
	root, _ := writeWorkspaceSelectorFixture(t)
	registry := &structureTestRegistry{languageManager: &structureTestManager{}}
	handler := NewStructureHandler(registry)
	_, err := callPlainTextContractHandler(t, handler, root, structureParams{
		Action: "workspace_symbol", WorkspaceLanguage: "python", LanguageID: "go", Query: "Needle",
	})
	assertCodedToolError(t, err, "invalid_params", false)
}

func testFoldingRangeTotal(t *testing.T) {
	root, target := writeWorkspaceSelectorFixture(t)
	ranges := make([]protocol.FoldingRange, 16)
	for i := range ranges {
		ranges[i] = protocol.FoldingRange{StartLine: i, EndLine: i + 1, Kind: "region"}
	}
	manager := &foldingContractManager{structureTestManager: &structureTestManager{}, ranges: ranges}
	result, err := runFoldingRanges(plainTextToolScope(root), manager, structureParams{FilePath: target, MaxResults: 5})
	if err != nil {
		t.Fatalf("runFoldingRanges() error = %v", err)
	}
	text := plainTextForContractResult(t, result)
	assertProtocolHeaderAndRows(t, text, "OK total=16 showing=5 truncated=1 unit=range", 5)
	fields := decodeProtocolRowForTest(t, firstProtocolRow(t, text))
	if fields["start_line"] != "1" || fields["end_line"] != "2" {
		t.Errorf("folding coordinates = start_line=%q end_line=%q, want 1/2", fields["start_line"], fields["end_line"])
	}
}

func testDocumentSymbolLineProtocol(t *testing.T) {
	root, target := writeWorkspaceSelectorFixture(t)
	children := make([]protocol.DocumentSymbol, 7)
	for i := range children {
		children[i] = protocol.DocumentSymbol{Name: fmt.Sprintf("child_%02d", i), Kind: protocol.SymbolKindFunction}
	}
	manager := &structureTestManager{documentSymbols: []protocol.DocumentSymbol{{
		Name: "root", Kind: protocol.SymbolKindClass,
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 1, Character: 1},
		},
		Children: children,
	}}}
	result, err := runDocumentSymbols(plainTextToolScope(root), manager, structureParams{FilePath: target, MaxResults: 5})
	if err != nil {
		t.Fatalf("runDocumentSymbols() error = %v", err)
	}
	text := plainTextForContractResult(t, result)
	assertProtocolHeaderAndRows(t, text, "OK total=8 showing=5 truncated=1 unit=symbol", 5)
	fields := decodeProtocolRowForTest(t, firstProtocolRow(t, text))
	for key, want := range map[string]string{"start_line": "1", "start_col": "1", "end_line": "2", "end_col": "2"} {
		if fields[key] != want {
			t.Errorf("document symbol %s = %q, want %q", key, fields[key], want)
		}
	}
}

func testDocumentSymbolHardLimitRecovery(t *testing.T) {
	root, target := writeWorkspaceSelectorFixture(t)
	symbols := make([]protocol.DocumentSymbol, 60)
	for index := range symbols {
		symbols[index] = protocol.DocumentSymbol{
			Name: fmt.Sprintf("symbol_%02d", index),
			Kind: protocol.SymbolKindFunction,
		}
	}
	manager := &structureTestManager{documentSymbols: symbols}
	result, err := runDocumentSymbols(plainTextToolScope(root), manager, structureParams{FilePath: target, MaxResults: 50})
	if err != nil {
		t.Fatalf("runDocumentSymbols() error = %v", err)
	}
	text := plainTextForContractResult(t, result)
	document, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("parse document symbol hard limit text: %v", err)
	}
	if document.Error != nil || document.Header.Total != 60 || document.Header.Showing != 50 || !document.Header.Truncated {
		t.Fatalf("document symbol hard limit = error:%#v total:%d showing:%d truncated:%v, want success/60/50/true; text=%q",
			document.Error, document.Header.Total, document.Header.Showing, document.Header.Truncated, text)
	}

	var attributes map[string]string
	var hint string
	for _, record := range document.Records {
		switch record.Kind {
		case "ATTR":
			attributes = record.Fields
		case "HINT":
			hint = record.Value
		}
	}
	if got := attributes["failure_reason"]; got != "document_symbol_hard_limit_reached" {
		t.Fatalf("document symbol hard limit failure_reason = %q, want document_symbol_hard_limit_reached; text=%q", got, text)
	}
	if got := attributes["effective_limit"]; got != "50" {
		t.Fatalf("document symbol hard limit effective_limit = %q, want 50; text=%q", got, text)
	}
	if got := attributes["next_step"]; got != "narrow_file_or_symbol_scope" {
		t.Fatalf("document symbol hard limit next_step = %q, want narrow_file_or_symbol_scope; text=%q", got, text)
	}
	if hint != "next: document_symbol reached the protocol hard limit (50); narrow the file/symbol scope" {
		t.Fatalf("document symbol hard limit hint = %q, want recovery plan; text=%q", hint, text)
	}
}

type foldingContractManager struct {
	*structureTestManager
	ranges []protocol.FoldingRange
}

func (m *foldingContractManager) FoldingRange(context.Context, string) ([]protocol.FoldingRange, error) {
	return m.ranges, nil
}

func writeWorkspaceSelectorFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "sample.go")
	if err := os.WriteFile(target, []byte("package sample\n\nfunc Needle() {}\n"), 0o600); err != nil {
		t.Fatalf("write sample.go: %v", err)
	}
	return root, target
}

func callPlainTextContractHandler(t *testing.T, handler ToolHandler, root string, params any) (any, error) {
	t.Helper()
	return handler(plainTextToolScope(root), mustJSONForPlainTextContract(t, params))
}

func mustJSONForPlainTextContract(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test params: %v", err)
	}
	return raw
}

func plainTextToolScope(root string) context.Context {
	return common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})
}

func assertCodedToolError(t *testing.T, err error, code string, retryable bool) {
	t.Helper()
	var coded *common.CodedToolError
	if !errors.As(err, &coded) || coded.Code != code || coded.Retryable != retryable {
		t.Fatalf("error = %T %v, want CodedToolError code=%s retryable=%v", err, err, code, retryable)
	}
	if strings.TrimSpace(coded.Hint) == "" {
		t.Errorf("coded error %s has empty hint", code)
	}
}

func plainTextForContractResult(t *testing.T, result any) string {
	t.Helper()
	if provider, ok := result.(interface{ ToPlainText() string }); ok {
		return provider.ToPlainText()
	}
	if text, handled := FormatToPlainText(result); handled {
		return text
	}
	if text, ok := result.(string); ok {
		return text
	}
	t.Fatalf("result %T has no plain text renderer", result)
	return ""
}

func assertProtocolHeaderAndRows(t *testing.T, text, header string, rowCount int) {
	t.Helper()
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || lines[0] != header {
		t.Errorf("protocol header = %q, want %q; text=%q", firstLine(text), header, text)
	}
	if rows := protocolRows(text); len(rows) != rowCount {
		t.Errorf("protocol ROW count = %d, want %d; text=%q", len(rows), rowCount, text)
	}
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

func protocolRows(text string) []string {
	var rows []string
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, "ROW\t") {
			rows = append(rows, line)
		}
	}
	return rows
}

func firstProtocolRow(t *testing.T, text string) string {
	t.Helper()
	rows := protocolRows(text)
	if len(rows) == 0 {
		t.Fatalf("text has no ROW record: %q", text)
	}
	return rows[0]
}

func decodeProtocolRowForTest(t *testing.T, row string) map[string]string {
	t.Helper()
	doc, err := lineprotocol.Parse("OK total=1 showing=1 truncated=0 unit=test\n" + row)
	if err != nil {
		t.Fatalf("decode ROW record: %v; row=%q", err, row)
	}
	if len(doc.Records) != 1 || doc.Records[0].Kind != "ROW" {
		t.Fatalf("decoded ROW records = %#v, want one ROW", doc.Records)
	}
	return doc.Records[0].Fields
}
