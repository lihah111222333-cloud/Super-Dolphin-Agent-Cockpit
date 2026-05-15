package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

type structureTestRegistry struct {
	fileManager     lspmanager.Manager
	fileErr         error
	languageManager lspmanager.Manager
	languageErr     error

	gotFilePath   string
	gotLanguageID string
}

func (r *structureTestRegistry) GetManagerForFile(_ context.Context, filePath string) (lspmanager.Manager, error) {
	r.gotFilePath = filePath
	if r.fileErr != nil {
		return nil, r.fileErr
	}
	return r.fileManager, nil
}

func (r *structureTestRegistry) GetManagerForLanguage(_ context.Context, languageID string) (lspmanager.Manager, error) {
	r.gotLanguageID = languageID
	if r.languageErr != nil {
		return nil, r.languageErr
	}
	return r.languageManager, nil
}

func (*structureTestRegistry) Diagnostics(context.Context, []string) ([]protocol.PublishDiagnosticsParams, error) {
	return nil, nil
}

func (*structureTestRegistry) WaitDiagnosticsStable(context.Context, []string) error {
	return nil
}

func (*structureTestRegistry) CurrentDiagnosticGeneration() uint64 {
	return 0
}

func (*structureTestRegistry) BootstrapDocument(context.Context, string) error {
	return nil
}

func (*structureTestRegistry) Close() error {
	return nil
}

type structureTestManager struct {
	workspaceSymbols  []protocol.WorkspaceSymbolResult
	gotWorkspaceQuery string
	gotWorkspaceLang  string
	didOpenContext    context.Context
}

func (*structureTestManager) Close() error { return nil }

func (*structureTestManager) Definition(context.Context, string, protocol.Position) ([]protocol.LocationResult, error) {
	return nil, nil
}

func (*structureTestManager) Implementation(context.Context, string, protocol.Position) ([]protocol.LocationResult, error) {
	return nil, nil
}

func (*structureTestManager) TypeDefinition(context.Context, string, protocol.Position) ([]protocol.LocationResult, error) {
	return nil, nil
}

func (*structureTestManager) Hover(context.Context, string, protocol.Position) (*protocol.HoverResult, error) {
	return nil, nil
}

func (*structureTestManager) SignatureHelp(context.Context, string, protocol.Position) (*protocol.SignatureHelpResult, error) {
	return nil, nil
}

func (*structureTestManager) References(context.Context, string, protocol.Position, bool) ([]protocol.LocationResult, error) {
	return nil, nil
}

func (*structureTestManager) CallHierarchy(context.Context, string, protocol.Position, string) ([]protocol.CallHierarchyResult, error) {
	return nil, nil
}

func (*structureTestManager) TypeHierarchy(context.Context, string, protocol.Position, string) ([]protocol.TypeHierarchyResult, error) {
	return nil, nil
}

func (*structureTestManager) DocumentSymbol(context.Context, string) ([]protocol.DocumentSymbol, error) {
	return nil, nil
}

func (m *structureTestManager) WorkspaceSymbol(_ context.Context, query string, languageID string) ([]protocol.WorkspaceSymbolResult, error) {
	m.gotWorkspaceQuery = query
	m.gotWorkspaceLang = languageID
	return m.workspaceSymbols, nil
}

func (*structureTestManager) FoldingRange(context.Context, string) ([]protocol.FoldingRange, error) {
	return nil, nil
}

func (*structureTestManager) SemanticTokens(context.Context, string) (*protocol.SemanticTokensResult, error) {
	return nil, nil
}

func (*structureTestManager) Completion(context.Context, string, protocol.Position) (*protocol.CompletionList, error) {
	return nil, nil
}

func (*structureTestManager) Rename(context.Context, string, protocol.Position, string) (*protocol.WorkspaceEdit, error) {
	return nil, nil
}

func (*structureTestManager) CodeAction(context.Context, string, protocol.Range, []string) ([]protocol.CodeActionResult, error) {
	return nil, nil
}

func (*structureTestManager) Format(context.Context, string, protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	return nil, nil
}

func (m *structureTestManager) DidOpen(ctx context.Context, _ string, _ string, _ int, _ string) error {
	m.didOpenContext = ctx
	return nil
}

func (*structureTestManager) DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error {
	return nil
}

func (*structureTestManager) DidClose(context.Context, string) error {
	return nil
}

func (*structureTestManager) BootstrapDocument(context.Context, string) error {
	return nil
}

func (*structureTestManager) BootstrapDocumentOpenOnly(context.Context, string) error {
	return nil
}

func (*structureTestManager) Diagnostics(context.Context, []string) ([]protocol.PublishDiagnosticsParams, error) {
	return nil, nil
}

func (*structureTestManager) WaitDiagnosticsStable(context.Context, []string) error {
	return nil
}

func (*structureTestManager) CurrentDiagnosticGeneration() uint64 {
	return 0
}

func (*structureTestManager) AdvanceDiagnosticGeneration() uint64 {
	return 0
}

func TestStructureWorkspaceSymbolUsesLanguageManager(t *testing.T) {
	manager := &structureTestManager{
		workspaceSymbols: []protocol.WorkspaceSymbolResult{{
			WorkspaceSymbol: &protocol.WorkspaceSymbol{
				Name: "greet",
				Kind: int(protocol.SymbolKindFunction),
				Location: protocol.WorkspaceSymbolLocation{
					URI: "file:///tmp/app.js",
				},
			},
		}},
	}
	registry := &structureTestRegistry{languageManager: manager}
	handler := NewStructureHandler(registry)

	input, err := json.Marshal(structureParams{
		Action:    "workspace_symbol",
		Language:  "javascript",
		Query:     "greet",
		Verbosity: "compact",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(context.Background(), input); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if registry.gotFilePath != "" {
		t.Fatalf("GetManagerForFile called with %q, want empty", registry.gotFilePath)
	}
	if registry.gotLanguageID != "javascript" {
		t.Fatalf("GetManagerForLanguage called with %q", registry.gotLanguageID)
	}
	if manager.gotWorkspaceQuery != "greet" {
		t.Fatalf("WorkspaceSymbol query = %q", manager.gotWorkspaceQuery)
	}
	if manager.gotWorkspaceLang != "javascript" {
		t.Fatalf("WorkspaceSymbol language = %q", manager.gotWorkspaceLang)
	}
}

func TestStructureDocumentSymbolAcceptsLegacyPathAlias(t *testing.T) {
	registry := &structureTestRegistry{fileManager: &structureTestManager{}}
	handler := NewStructureHandler(registry)
	input, err := json.Marshal(structureParams{Action: "document_symbol", Path: "/tmp/sample.go"})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(context.Background(), input); err != nil {
		t.Fatalf("document_symbol with path alias returned error: %v", err)
	}
	if registry.gotFilePath != "/tmp/sample.go" {
		t.Fatalf("GetManagerForFile path = %q, want legacy path alias", registry.gotFilePath)
	}
}

func TestStructureWorkspaceSymbolDetectsLanguageFromFilePath(t *testing.T) {
	manager := &structureTestManager{
		workspaceSymbols: []protocol.WorkspaceSymbolResult{{
			WorkspaceSymbol: &protocol.WorkspaceSymbol{
				Name: "createService",
				Kind: int(protocol.SymbolKindFunction),
				Location: protocol.WorkspaceSymbolLocation{
					URI: "file:///tmp/service.ts",
				},
			},
		}},
	}
	registry := &structureTestRegistry{fileManager: manager}
	handler := NewStructureHandler(registry)

	input, err := json.Marshal(structureParams{
		Action:    "workspace_symbol",
		FilePath:  "frontend/service.ts",
		Query:     "createService",
		Verbosity: "compact",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(context.Background(), input); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if registry.gotFilePath != "frontend/service.ts" {
		t.Fatalf("GetManagerForFile called with %q", registry.gotFilePath)
	}
	if manager.gotWorkspaceLang != "typescript" {
		t.Fatalf("WorkspaceSymbol language = %q", manager.gotWorkspaceLang)
	}
}

func TestStructureWorkspaceSymbolWrapsUnsupportedPathError(t *testing.T) {
	registry := &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage}
	handler := NewStructureHandler(registry)

	input, err := json.Marshal(structureParams{
		Action:   "workspace_symbol",
		FilePath: "README.md",
		Query:    "intro",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = handler(context.Background(), input)
	if err == nil {
		t.Fatal("handler error = nil, want unsupported path error")
	}
	if !strings.Contains(err.Error(), "path must point to a source file with a configured language server") {
		t.Fatalf("handler error = %v", err)
	}
}
