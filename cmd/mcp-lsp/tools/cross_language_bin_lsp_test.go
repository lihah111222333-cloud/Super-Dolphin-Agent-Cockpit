package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

// TestCrossLanguageBinLSPFixtures 使用 /bin/LSP/test 目录下的真实各语言代码样板进行全面的跨语言测试验证。
func TestCrossLanguageBinLSPFixtures(t *testing.T) {
	lspTestDir := crossLanguageLSPTestDir(t)
	t.Run("DocumentSymbolStructureFilteringAcrossLanguages", func(t *testing.T) {
		runCrossLanguageStructureFixtures(t, lspTestDir)
	})
	t.Run("DiagnosticsChangedLinesFocusAcrossLanguages", func(t *testing.T) {
		runCrossLanguageDiagnosticsFixture(t, lspTestDir)
	})
}

type crossLanguageFixture struct {
	lang string
	rel  string
}

func crossLanguageLSPTestDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := crossLanguageRepoRoot(t, wd)
	lspTestDir := filepath.Join(repoRoot, "bin", "LSP", "test")
	if _, err := os.Stat(lspTestDir); err != nil {
		t.Skipf("skipping: bin/LSP/test not found at %s", lspTestDir)
	}
	return lspTestDir
}

func crossLanguageRepoRoot(t *testing.T, start string) string {
	t.Helper()
	root := start
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			return root
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatalf("could not find repo root containing go.mod from %s", start)
		}
		root = parent
	}
}

func runCrossLanguageStructureFixtures(t *testing.T, lspTestDir string) {
	t.Helper()
	manager := newCrossLanguageStructureManager()
	handler := NewStructureHandler(&structureTestRegistry{fileManager: manager})
	for _, fixture := range crossLanguageFixtures() {
		fixture := fixture
		t.Run(fixture.lang, func(t *testing.T) {
			runCrossLanguageStructureFixture(t, handler, lspTestDir, fixture)
		})
	}
}

func crossLanguageFixtures() []crossLanguageFixture {
	return []crossLanguageFixture{
		{lang: "go", rel: "go/hello/hello.go"},
		{lang: "python", rel: "python/tests/test_calculator.py"},
		{lang: "svelte", rel: "svelte/src/App.svelte"},
		{lang: "terraform", rel: "terraform/main.tf"},
	}
}

func newCrossLanguageStructureManager() *structureTestManager {
	return &structureTestManager{
		documentSymbols: []protocol.DocumentSymbol{
			{
				Name: "GLOBAL_MAX_ITEMS",
				Kind: protocol.SymbolKindConstant,
				Range: protocol.Range{
					Start: protocol.Position{Line: 1, Character: 0},
					End:   protocol.Position{Line: 1, Character: 30},
				},
			},
			{
				Name: "AppClass",
				Kind: protocol.SymbolKindClass,
				Range: protocol.Range{
					Start: protocol.Position{Line: 10, Character: 0},
					End:   protocol.Position{Line: 50, Character: 1},
				},
				Children: []protocol.DocumentSymbol{
					{
						Name: "AppField",
						Kind: protocol.SymbolKindField,
						Range: protocol.Range{
							Start: protocol.Position{Line: 11, Character: 2},
							End:   protocol.Position{Line: 11, Character: 20},
						},
					},
					{
						Name: "AppProperty",
						Kind: protocol.SymbolKindProperty,
						Range: protocol.Range{
							Start: protocol.Position{Line: 12, Character: 2},
							End:   protocol.Position{Line: 12, Character: 20},
						},
					},
					{
						Name: "ActiveMember",
						Kind: protocol.SymbolKindEnumMember,
						Range: protocol.Range{
							Start: protocol.Position{Line: 13, Character: 2},
							End:   protocol.Position{Line: 13, Character: 20},
						},
					},
					{
						Name: "HandleRequest",
						Kind: protocol.SymbolKindMethod,
						Range: protocol.Range{
							Start: protocol.Position{Line: 15, Character: 2},
							End:   protocol.Position{Line: 25, Character: 3},
						},
						Children: []protocol.DocumentSymbol{
							{
								Name: "localVarTemp",
								Kind: protocol.SymbolKindVariable, // 骨架折叠已将其过滤
								Range: protocol.Range{
									Start: protocol.Position{Line: 16, Character: 4},
									End:   protocol.Position{Line: 16, Character: 20},
								},
							},
						},
					},
				},
			},
		},
	}
}

func runCrossLanguageStructureFixture(
	t *testing.T,
	handler ToolHandler,
	lspTestDir string,
	fixture crossLanguageFixture,
) {
	t.Helper()
	filePath := filepath.Join(lspTestDir, fixture.rel)
	if _, err := os.Stat(filePath); err != nil {
		t.Skipf("fixture not found at %s", filePath)
	}
	input, err := json.Marshal(structureParams{Action: "document_symbol", FilePath: filePath})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: lspTestDir})
	got, err := handler(ctx, input)
	if err != nil {
		t.Fatalf("document_symbol failed: %v", err)
	}
	resp, ok := got.(documentSymbolListResponse)
	if !ok {
		t.Fatalf("got %T, want documentSymbolListResponse", got)
	}
	assertCrossLanguageSymbols(t, resp.ToPlainText())
}

func assertCrossLanguageSymbols(t *testing.T, plainText string) {
	t.Helper()
	for _, want := range []string{
		"GLOBAL_MAX_ITEMS", "AppClass", "AppField", "AppProperty", "ActiveMember", "HandleRequest",
	} {
		if !strings.Contains(plainText, want) {
			t.Fatalf("plain text missing core symbol %s: %s", want, plainText)
		}
	}
	if strings.Contains(plainText, "localVarTemp") {
		t.Fatalf("non-core variable localVarTemp was NOT filtered by skeleton folding: %s", plainText)
	}
}

func runCrossLanguageDiagnosticsFixture(t *testing.T, lspTestDir string) {
	t.Helper()
	registry := newCrossLanguageDiagnosticsRegistry(lspTestDir)
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: lspTestDir, Registry: registry})
	input, err := json.Marshal(diagnosticsInput{
		FilePath: filepath.Join(lspTestDir, "go/hello/hello.go"), ChangedLines: []int{12, 13}, // 仅修改 12, 13 行
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: lspTestDir})
	got, err := handler(ctx, input)
	if err != nil {
		t.Fatalf("diagnostics handler returned error: %v", err)
	}
	resp, ok := got.(diagnosticsResponse)
	if !ok {
		t.Fatalf("got %T, want diagnosticsResponse", got)
	}
	plainText := resp.ToPlainText()
	doc, err := lineprotocol.Parse(plainText)
	if err != nil {
		t.Fatalf("parse diagnostics line protocol: %v", err)
	}
	active, legacy, omitted := crossLanguageDiagnosticsRecords(doc)
	assertCrossLanguageDiagnostics(t, plainText, active, legacy, omitted)
}

func newCrossLanguageDiagnosticsRegistry(lspTestDir string) *diagnosticsTestRegistry {
	fileURI := "file://" + filepath.ToSlash(filepath.Join(lspTestDir, "go/hello/hello.go"))
	return &diagnosticsTestRegistry{
		diagnosticsByURI: map[string][]protocol.Diagnostic{
			fileURI: {
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 12, Character: 4},
						End:   protocol.Position{Line: 12, Character: 10},
					},
					Severity: protocol.SeverityError,
					Message:  "type mismatch on active line",
				},
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 99, Character: 1},
						End:   protocol.Position{Line: 99, Character: 5},
					},
					Severity: protocol.SeverityWarning,
					Message:  "unused legacy parameter outside diff",
				},
			},
		},
	}
}

func crossLanguageDiagnosticsRecords(doc lineprotocol.Document) (bool, bool, bool) {
	var foundErrorOnActiveLine bool
	var foundLegacyWarning bool
	var foundOmittedHint bool
	for _, rec := range doc.Records {
		if rec.Kind == "ROW" {
			foundErrorOnActiveLine = foundErrorOnActiveLine || strings.Contains(
				rec.Fields["message"], "type mismatch on active line",
			)
			foundLegacyWarning = foundLegacyWarning || strings.Contains(
				rec.Fields["message"], "unused legacy parameter",
			)
		}
		if rec.Kind == "HINT" {
			foundOmittedHint = foundOmittedHint ||
				strings.Contains(rec.Value, "FILTERED records audit") &&
					strings.Contains(rec.Value, "filtered is not clean")
		}
	}
	return foundErrorOnActiveLine, foundLegacyWarning, foundOmittedHint
}

func assertCrossLanguageDiagnostics(t *testing.T, plainText string, active, legacy, omitted bool) {
	t.Helper()
	if !active {
		t.Fatalf("expected error on active line 12 to be retained, got: %s", plainText)
	}
	if legacy {
		t.Fatalf("legacy warning on line 99 outside diff should be omitted, but was present in: %s", plainText)
	}
	if !omitted {
		t.Fatalf("expected HINT indicating omitted legacy warnings, got: %s", plainText)
	}
}
