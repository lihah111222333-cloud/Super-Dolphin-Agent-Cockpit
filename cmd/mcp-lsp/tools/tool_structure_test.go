package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type structureTestRegistry struct {
	fileManager     lspmanager.Manager
	fileErr         error
	languageManager lspmanager.Manager
	languageErr     error

	gotFilePath           string
	gotLanguageID         string
	fileCalls             int
	fileWithLanguageCalls int
	languageCalls         int
}

func (r *structureTestRegistry) GetManagerForFile(_ context.Context, filePath string) (lspmanager.Manager, error) {
	r.fileCalls++
	r.gotFilePath = filePath
	if r.fileErr != nil {
		return nil, r.fileErr
	}
	return r.fileManager, nil
}

func (r *structureTestRegistry) GetManagerForFileWithLanguage(_ context.Context, filePath string, languageID string) (lspmanager.Manager, error) {
	r.fileWithLanguageCalls++
	r.gotFilePath = filePath
	r.gotLanguageID = languageID
	if r.fileErr != nil {
		return nil, r.fileErr
	}
	return r.fileManager, nil
}

func (r *structureTestRegistry) GetManagerForLanguage(_ context.Context, languageID string) (lspmanager.Manager, error) {
	r.languageCalls++
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
	testManagerNavigationNoop
	testManagerSymbolNoop
	testManagerEditNoop
	testManagerDiagnosticsNoop

	workspaceSymbols   []protocol.WorkspaceSymbolResult
	workspaceSymbolErr error
	documentSymbols    []protocol.DocumentSymbol
	completionItems    []protocol.CompletionItem
	definitions        []protocol.LocationResult
	references         []protocol.LocationResult
	callHierarchy      []protocol.CallHierarchyResult
	documentContext    context.Context
	gotWorkspaceQuery  string
	gotWorkspaceLang   string
	didOpenContext     context.Context
	bootstrapURIs      []string
	bootstrapErr       error
	events             []string
}

type testManagerNavigationNoop struct{}

func (testManagerNavigationNoop) Definition(context.Context, string, protocol.Position) ([]protocol.LocationResult, error) {
	return nil, nil
}

func (testManagerNavigationNoop) Implementation(context.Context, string, protocol.Position) ([]protocol.LocationResult, error) {
	return nil, nil
}

func (testManagerNavigationNoop) TypeDefinition(context.Context, string, protocol.Position) ([]protocol.LocationResult, error) {
	return nil, nil
}

func (testManagerNavigationNoop) Hover(context.Context, string, protocol.Position) (*protocol.HoverResult, error) {
	return nil, nil
}

func (testManagerNavigationNoop) SignatureHelp(context.Context, string, protocol.Position) (*protocol.SignatureHelpResult, error) {
	return nil, nil
}

func (testManagerNavigationNoop) References(context.Context, string, protocol.Position, bool) ([]protocol.LocationResult, error) {
	return nil, nil
}

func (testManagerNavigationNoop) CallHierarchy(context.Context, string, protocol.Position, string) ([]protocol.CallHierarchyResult, error) {
	return nil, nil
}

func (testManagerNavigationNoop) TypeHierarchy(context.Context, string, protocol.Position, string) ([]protocol.TypeHierarchyResult, error) {
	return nil, nil
}

type testManagerSymbolNoop struct{}

func (testManagerSymbolNoop) DocumentSymbol(context.Context, string) ([]protocol.DocumentSymbol, error) {
	return nil, nil
}

func (testManagerSymbolNoop) WorkspaceSymbol(context.Context, string, string) ([]protocol.WorkspaceSymbolResult, error) {
	return nil, nil
}

func (testManagerSymbolNoop) FoldingRange(context.Context, string) ([]protocol.FoldingRange, error) {
	return nil, nil
}

func (testManagerSymbolNoop) SemanticTokens(context.Context, string) (*protocol.SemanticTokensResult, error) {
	return nil, nil
}

func (testManagerSymbolNoop) Completion(context.Context, string, protocol.Position) (*protocol.CompletionList, error) {
	return nil, nil
}

type testManagerEditNoop struct{}

func (testManagerEditNoop) Close() error { return nil }

func (testManagerEditNoop) Rename(context.Context, string, protocol.Position, string) (*protocol.WorkspaceEdit, error) {
	return nil, nil
}

func (testManagerEditNoop) CodeAction(context.Context, string, protocol.Range, []string) ([]protocol.CodeActionResult, error) {
	return nil, nil
}

func (testManagerEditNoop) Format(context.Context, string, protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	return nil, nil
}

func (testManagerEditNoop) DidOpen(context.Context, string, string, int, string) error { return nil }

func (testManagerEditNoop) DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error {
	return nil
}

func (testManagerEditNoop) DidClose(context.Context, string) error { return nil }

func (testManagerEditNoop) BootstrapDocument(context.Context, string) error { return nil }

func (testManagerEditNoop) BootstrapDocumentOpenOnly(context.Context, string) error { return nil }

type testManagerDiagnosticsNoop struct{}

func (testManagerDiagnosticsNoop) Diagnostics(context.Context, []string) ([]protocol.PublishDiagnosticsParams, error) {
	return nil, nil
}

func (testManagerDiagnosticsNoop) WaitDiagnosticsStable(context.Context, []string) error { return nil }

func (testManagerDiagnosticsNoop) CurrentDiagnosticGeneration() uint64 { return 0 }

func (testManagerDiagnosticsNoop) AdvanceDiagnosticGeneration() uint64 { return 0 }

func (m *structureTestManager) Definition(context.Context, string, protocol.Position) ([]protocol.LocationResult, error) {
	return m.definitions, nil
}

func (m *structureTestManager) References(context.Context, string, protocol.Position, bool) ([]protocol.LocationResult, error) {
	return m.references, nil
}

func (m *structureTestManager) CallHierarchy(context.Context, string, protocol.Position, string) ([]protocol.CallHierarchyResult, error) {
	return m.callHierarchy, nil
}

func (m *structureTestManager) DocumentSymbol(ctx context.Context, _ string) ([]protocol.DocumentSymbol, error) {
	m.documentContext = ctx
	return m.documentSymbols, nil
}

func (m *structureTestManager) WorkspaceSymbol(_ context.Context, query string, languageID string) ([]protocol.WorkspaceSymbolResult, error) {
	m.events = append(m.events, "workspace_symbol")
	m.gotWorkspaceQuery = query
	m.gotWorkspaceLang = languageID
	return m.workspaceSymbols, m.workspaceSymbolErr
}

func (m *structureTestManager) Completion(context.Context, string, protocol.Position) (*protocol.CompletionList, error) {
	return &protocol.CompletionList{Items: m.completionItems}, nil
}

func (m *structureTestManager) DidOpen(ctx context.Context, _ string, _ string, _ int, _ string) error {
	m.didOpenContext = ctx
	return nil
}

func (m *structureTestManager) BootstrapDocument(_ context.Context, uri string) error {
	m.events = append(m.events, "bootstrap:"+uri)
	m.bootstrapURIs = append(m.bootstrapURIs, uri)
	return m.bootstrapErr
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
		Action:   "workspace_symbol",
		Language: "javascript",
		Query:    "greet",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), input); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if registry.gotFilePath != "" {
		t.Fatalf("GetManagerForFile called with %q, want empty", registry.gotFilePath)
	}
	if registry.fileCalls != 0 || registry.fileWithLanguageCalls != 0 {
		t.Fatalf("file manager calls = direct:%d with_language:%d, want none", registry.fileCalls, registry.fileWithLanguageCalls)
	}
	if registry.languageCalls != 1 {
		t.Fatalf("GetManagerForLanguage calls = %d, want 1", registry.languageCalls)
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

func TestStructureWorkspaceSymbolRejectsUnknownMatchMode(t *testing.T) {
	_, err := runWorkspaceSymbols(context.Background(), &structureTestManager{}, "go", structureParams{
		Query:     "Needle",
		MatchMode: "broad",
	})
	if err == nil || !strings.Contains(err.Error(), "match_mode must be exact or fuzzy") {
		t.Fatalf("unknown workspace symbol match_mode error = %v", err)
	}
}

func TestStructureWorkspaceSymbolSQLLanguageRequiresFilePath(t *testing.T) {
	root := t.TempDir()
	writeStructureTestFile(t, root, "sqlc.yaml", "version: \"2\"\nsql:\n  - engine: postgresql\n    queries: queries\n")
	registry := &structureTestRegistry{languageManager: &structureTestManager{}}
	handler := NewStructureHandler(registry)
	input := marshalStructureParams(t, structureParams{
		Action:   "workspace_symbol",
		Language: "sql",
		Query:    "card",
	})

	_, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input)
	if err == nil || !strings.Contains(err.Error(), "requires file_path") {
		t.Fatalf("SQL language-only workspace_symbol error = %v, want file_path requirement", err)
	}
	if registry.languageCalls != 0 {
		t.Fatalf("GetManagerForLanguage calls = %d, want fail-fast before manager lookup", registry.languageCalls)
	}
}

func TestStructureDocumentSymbolRejectsLegacyPathAlias(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sample.go")
	if err := os.WriteFile(target, []byte("package sample\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	registry := &structureTestRegistry{fileManager: &structureTestManager{}}
	handler := NewStructureHandler(registry)
	input, err := json.Marshal(map[string]any{"action": "document_symbol", "path": target})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input); err == nil || !strings.Contains(err.Error(), `unknown field "path"`) {
		t.Fatalf("document_symbol with path alias error = %v, want unknown field rejection", err)
	}
	if registry.gotFilePath != "" {
		t.Fatalf("GetManagerForFile path = %q, want handler not called", registry.gotFilePath)
	}
}

func TestStructureDocumentSymbolReportsTotalShowingAndTruncation(t *testing.T) {
	root := t.TempDir()
	target := writeStructureTestFile(t, root, "sample.go", "package sample\n")
	manager := &structureTestManager{
		documentSymbols: []protocol.DocumentSymbol{
			reproDocumentSymbol("Alpha"),
			reproDocumentSymbol("Beta"),
			reproDocumentSymbol("Gamma"),
		},
	}
	registry := &structureTestRegistry{fileManager: manager}
	handler := NewStructureHandler(registry)

	got, err := handler(testToolContext(root), marshalStructureParams(t, structureParams{
		Action:     "document_symbol",
		FilePath:   target,
		MaxResults: 2,
	}))
	if err != nil {
		t.Fatalf("document_symbol returned error: %v", err)
	}
	payload := mustMarshalObject(t, got)
	data, ok := payload["data"].([]any)
	if !ok {
		t.Fatalf("data = %#v, want array", payload["data"])
	}
	if len(data) != 2 {
		t.Fatalf("data length = %d, want 2", len(data))
	}
	requireNumberField(t, payload, "total", 3)
	requireNumberField(t, payload, "showing", 2)
	requireBoolField(t, payload, "truncated", true)
	requireStringFieldContains(t, payload, "hint", "max_results")
}

func TestStructureDocumentSymbolUsesSlowDeadlineBudget(t *testing.T) {
	root := t.TempDir()
	target := writeStructureTestFile(t, root, "frontend/big-store.js", "export const clientStore = {};\n")
	manager := &structureTestManager{
		documentSymbols: []protocol.DocumentSymbol{reproDocumentSymbol("clientStore")},
	}
	registry := &structureTestRegistry{fileManager: manager}
	handler := NewStructureHandler(registry)

	if _, err := handler(testToolContext(root), marshalStructureParams(t, structureParams{
		Action:     "document_symbol",
		FilePath:   target,
		MaxResults: 50,
	})); err != nil {
		t.Fatalf("document_symbol returned error: %v", err)
	}
	if manager.documentContext == nil {
		t.Fatal("DocumentSymbol was not called")
	}
	deadline, ok := manager.documentContext.Deadline()
	if !ok {
		t.Fatal("DocumentSymbol context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining < middleware.TierSlow-5*time.Second {
		t.Fatalf("DocumentSymbol deadline budget = %s, want slow tier near %s so large JS outlines do not hit the normal tool timeout", remaining.Round(time.Second), middleware.TierSlow)
	}
}

func reproDocumentSymbol(name string) protocol.DocumentSymbol {
	return protocol.DocumentSymbol{
		Name: name,
		Kind: protocol.SymbolKindFunction,
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 0, Character: 1},
		},
		SelectionRange: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 0, Character: 1},
		},
	}
}

func writeStructureTestFile(t *testing.T, root, relPath, content string) string {
	t.Helper()
	target := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return target
}

func structureWorkspaceSymbolManager(target, name string) *structureTestManager {
	return &structureTestManager{
		workspaceSymbols: []protocol.WorkspaceSymbolResult{{
			WorkspaceSymbol: &protocol.WorkspaceSymbol{
				Name: name,
				Kind: int(protocol.SymbolKindFunction),
				Location: protocol.WorkspaceSymbolLocation{
					URI: fileURI(target),
				},
			},
		}},
	}
}

func marshalStructureParams(t *testing.T, params structureParams) json.RawMessage {
	t.Helper()
	input, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return input
}

func assertWorkspaceSymbolFilePathRouting(t *testing.T, registry *structureTestRegistry, filePath string) {
	t.Helper()
	if registry.gotFilePath != filePath {
		t.Fatalf("GetManagerForFileWithLanguage called with %q", registry.gotFilePath)
	}
	if registry.fileWithLanguageCalls != 1 {
		t.Fatalf("GetManagerForFileWithLanguage calls = %d, want 1", registry.fileWithLanguageCalls)
	}
	if registry.languageCalls != 0 {
		t.Fatalf("GetManagerForLanguage calls = %d, want 0 for file_path routing", registry.languageCalls)
	}
	if registry.gotLanguageID != "" {
		t.Fatalf("GetManagerForFileWithLanguage language override = %q, want empty so file_path infers language", registry.gotLanguageID)
	}
}

func assertWorkspaceSymbolSearchWithoutToolBootstrap(t *testing.T, manager *structureTestManager) {
	t.Helper()
	if len(manager.bootstrapURIs) != 0 {
		t.Fatalf("tool-layer BootstrapDocument calls = %#v, want none before manager capability/dirty checks", manager.bootstrapURIs)
	}
	if len(manager.events) != 1 || manager.events[0] != "workspace_symbol" {
		t.Fatalf("events = %#v, want direct workspace_symbol delegation", manager.events)
	}
}

func TestStructureWorkspaceSymbolDetectsLanguageFromFilePath(t *testing.T) {
	root := t.TempDir()
	target := writeStructureTestFile(t, root, "frontend/service.ts", "export function createService() {}\n")
	manager := structureWorkspaceSymbolManager(target, "createService")
	registry := &structureTestRegistry{fileManager: manager}
	handler := NewStructureHandler(registry)

	input := marshalStructureParams(t, structureParams{
		Action:   "workspace_symbol",
		FilePath: "frontend/service.ts",
		Query:    "createService",
	})

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}}), input); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	assertWorkspaceSymbolFilePathRouting(t, registry, "frontend/service.ts")
	if manager.gotWorkspaceLang != "typescript" {
		t.Fatalf("WorkspaceSymbol language = %q", manager.gotWorkspaceLang)
	}
}

func TestStructureWorkspaceSymbolDoesNotPrebootstrapRequestedFilePath(t *testing.T) {
	root := t.TempDir()
	target := writeStructureTestFile(t, root, "frontend/app.js", "export function greet() {}\n")
	manager := structureWorkspaceSymbolManager(target, "greet")
	registry := &structureTestRegistry{fileManager: manager}
	handler := NewStructureHandler(registry)

	input := marshalStructureParams(t, structureParams{
		Action:   "workspace_symbol",
		FilePath: "frontend/app.js",
		Query:    "greet",
	})

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}}), input); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	assertWorkspaceSymbolSearchWithoutToolBootstrap(t, manager)
	if manager.gotWorkspaceQuery != "greet" {
		t.Fatalf("WorkspaceSymbol query = %q", manager.gotWorkspaceQuery)
	}
	if manager.gotWorkspaceLang != "javascript" {
		t.Fatalf("WorkspaceSymbol language = %q, want javascript inferred from file_path", manager.gotWorkspaceLang)
	}
}

func TestStructureWorkspaceSymbolUnsupportedFilePathHasNoBootstrapSideEffects(t *testing.T) {
	root := t.TempDir()
	target := writeStructureTestFile(t, root, "frontend/app.js", "export function unsupported() {}\n")
	manager := structureWorkspaceSymbolManager(target, "unsupported")
	manager.workspaceSymbolErr = lspmanager.ErrUnsupportedCapability
	handler := NewStructureHandler(&structureTestRegistry{fileManager: manager})
	input := marshalStructureParams(t, structureParams{
		Action: "workspace_symbol", FilePath: "frontend/app.js", Query: "unsupported",
	})
	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input); err != nil {
		t.Fatalf("unsupported workspace_symbol handler: %v", err)
	}
	assertWorkspaceSymbolSearchWithoutToolBootstrap(t, manager)
}

func TestStructureWorkspaceSymbolDoesNotSwallowFatalCapabilitySibling(t *testing.T) {
	root := t.TempDir()
	target := writeStructureTestFile(t, root, "frontend/app.js", "export function stale() {}\n")
	manager := structureWorkspaceSymbolManager(target, "stale")
	manager.workspaceSymbolErr = errors.Join(
		lspmanager.ErrUnsupportedCapability,
		multilsp.ErrStaleClientLease,
	)
	handler := NewStructureHandler(&structureTestRegistry{fileManager: manager})
	input := marshalStructureParams(t, structureParams{
		Action: "workspace_symbol", FilePath: "frontend/app.js", Query: "stale",
	})

	_, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input)
	if !errors.Is(err, multilsp.ErrStaleClientLease) {
		t.Fatalf("fatal capability sibling error = %v, want ErrStaleClientLease", err)
	}
}

func TestStructureWorkspaceSymbolWrapsUnsupportedPathError(t *testing.T) {
	registry := &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage}
	handler := NewStructureHandler(registry)

	input, err := json.Marshal(structureParams{
		Action:   "workspace_symbol",
		FilePath: "notes.txt",
		Query:    "intro",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), input)
	if err == nil {
		t.Fatal("handler error = nil, want unsupported path error")
	}
	if !strings.Contains(err.Error(), "path must point to a source file with a configured language server") {
		t.Fatalf("handler error = %v", err)
	}
}

func TestStructureMarkdownDocumentSymbolUsesFallback(t *testing.T) {
	root := t.TempDir()
	writeStructureTestFile(t, root, "README.md", "# Intro\n\n## Details\n")
	handler := NewStructureHandler(newMarkdownFallbackRegistry(t, root))

	got, err := handler(testToolContext(root), marshalStructureParams(t, structureParams{
		Action:   "document_symbol",
		FilePath: "README.md",
	}))
	if err != nil {
		t.Fatalf("markdown document_symbol returned error: %v", err)
	}
	payload := mustMarshalObject(t, got)
	requireNumberField(t, payload, "total", 2)
	requireNumberField(t, payload, "showing", 2)
}

func TestStructureTypeScriptDocumentSymbolUsesNavigationFallbackInsteadOfEmptyEnvelope(t *testing.T) {
	root := t.TempDir()
	writeStructureTestFile(t, root, "user-ui-v2/package.json", `{"name":"user-ui-v2"}`)
	content := "export default memo(function FancyWidget() { return null; })\n"
	writeStructureTestFile(t, root, "user-ui-v2/src/lib/market/Datafeed.ts", content)
	installStructureFakeTypeScriptNavigationNode(t, fakeStructureTypeScriptNavigationTree(t, content))
	handler := NewStructureHandler(newTypeScriptEmptyDocumentSymbolRegistry(t, root))

	got, err := handler(testToolContext(root), marshalStructureParams(t, structureParams{
		Action:     "document_symbol",
		FilePath:   "user-ui-v2/src/lib/market/Datafeed.ts",
		MaxResults: 50,
	}))
	if err != nil {
		t.Fatalf("typescript document_symbol returned error: %v", err)
	}
	if envelope, ok := got.(emptyListEnvelope); ok {
		t.Fatalf("typescript document_symbol returned empty envelope: %#v", envelope)
	}
	payload := mustMarshalObject(t, got)
	requireDocumentSymbolPayloadNames(t, payload, []string{
		"FromNavigationTree",
		"render",
	})
}

func TestStructureMarkdownWorkspaceSymbolReportsLimitedSupport(t *testing.T) {
	root := t.TempDir()
	writeStructureTestFile(t, root, "README.md", "# Intro\n")
	handler := NewStructureHandler(newMarkdownFallbackRegistry(t, root))

	got, err := handler(testToolContext(root), marshalStructureParams(t, structureParams{
		Action:   "workspace_symbol",
		FilePath: "README.md",
		Query:    "Intro",
	}))
	if err != nil {
		t.Fatalf("markdown workspace_symbol returned error: %v", err)
	}
	envelope := requireEmptyListEnvelope(t, got)
	requireLimitedMarkdownSupportMessage(t, envelope.Meta.Message, "workspace symbol")
}

func newMarkdownFallbackRegistry(t *testing.T, root string) lspmanager.Registry {
	t.Helper()
	registry := lspmanager.NewRegistryWithInstaller(nil)
	manager := multilsp.NewManager(multilsp.Config{
		WorkspaceRoot:                    root,
		LanguageAdapters:                 multilsp.NewDefaultLanguageAdapterRegistry(),
		DisableInitialWorkspaceBootstrap: true,
	})
	registry.RegisterNoInstall("markdown", manager)
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Fatalf("close markdown fallback registry: %v", err)
		}
	})
	return registry
}

func newTypeScriptEmptyDocumentSymbolRegistry(t *testing.T, root string) lspmanager.Registry {
	t.Helper()
	registry := lspmanager.NewRegistryWithInstaller(nil)
	manager := multilsp.NewManager(multilsp.Config{
		WorkspaceRoot: root,
		ClientFactory: multilsp.ClientFactoryFunc(func(string, protocol.NotificationHandler) (multilsp.Client, error) {
			return emptyDocumentSymbolClient{}, nil
		}),
		LanguageAdapters:                 multilsp.NewDefaultLanguageAdapterRegistry(),
		DisableInitialWorkspaceBootstrap: true,
	})
	registry.RegisterNoInstall("typescript", manager)
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Fatalf("close typescript fallback registry: %v", err)
		}
	})
	return registry
}

type emptyDocumentSymbolClient struct{}

func (emptyDocumentSymbolClient) Initialize(context.Context, string) error { return nil }
func (emptyDocumentSymbolClient) Shutdown(context.Context) error           { return nil }
func (emptyDocumentSymbolClient) Request(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method == protocol.MethodDocumentSymbol {
		return json.RawMessage("[]"), nil
	}
	return json.RawMessage("null"), nil
}
func (emptyDocumentSymbolClient) Notify(context.Context, string, any) error { return nil }
func (emptyDocumentSymbolClient) DidOpen(context.Context, string, string, int, string) error {
	return nil
}
func (emptyDocumentSymbolClient) DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error {
	return nil
}
func (emptyDocumentSymbolClient) DidClose(context.Context, string) error { return nil }
func (emptyDocumentSymbolClient) Close() error                           { return nil }

func fakeStructureTypeScriptNavigationTree(t *testing.T, content string) string {
	t.Helper()
	nameStart := max(0, strings.Index(content, "FancyWidget"))
	tree := map[string]any{
		"text":  "<global>",
		"kind":  "script",
		"spans": []map[string]int{{"start": 0, "length": len(content)}},
		"childItems": []map[string]any{{
			"text":     "FromNavigationTree",
			"kind":     "function",
			"spans":    []map[string]int{{"start": 0, "length": len(content)}},
			"nameSpan": map[string]int{"start": nameStart, "length": len("FancyWidget")},
			"childItems": []map[string]any{{
				"text":     "render",
				"kind":     "method",
				"spans":    []map[string]int{{"start": nameStart, "length": len("FancyWidget")}},
				"nameSpan": map[string]int{"start": nameStart, "length": len("FancyWidget")},
			}},
		}},
	}
	raw, err := json.Marshal(tree)
	if err != nil {
		t.Fatalf("marshal fake navigation tree: %v", err)
	}
	return string(raw)
}

func installStructureFakeTypeScriptNavigationNode(t *testing.T, output string) {
	t.Helper()
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "navigation.json")
	if err := os.WriteFile(outputPath, []byte(output), 0o600); err != nil {
		t.Fatalf("write fake navigation output: %v", err)
	}
	nodePath := filepath.Join(dir, "node")
	script := strings.Join([]string{
		"#!/bin/sh",
		"cat >/dev/null",
		"cat " + structureShellQuote(outputPath),
		"",
	}, "\n")
	if err := os.WriteFile(nodePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake node: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func structureShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func requireDocumentSymbolPayloadNames(t *testing.T, payload map[string]any, want []string) {
	t.Helper()
	data, ok := payload["data"].([]any)
	if !ok {
		t.Fatalf("data = %#v, want array", payload["data"])
	}
	if len(data) == 0 {
		t.Fatal("data is empty, want fallback document symbols")
	}
	names := collectDocumentSymbolPayloadNames(data)
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		seen[name] = struct{}{}
	}
	for _, name := range want {
		if _, ok := seen[name]; !ok {
			t.Fatalf("symbols = %v, missing %q", names, name)
		}
	}
}

func collectDocumentSymbolPayloadNames(items []any) []string {
	names := make([]string, 0, len(items))
	var walk func([]any)
	walk = func(values []any) {
		for _, value := range values {
			item, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if name, ok := item["name"].(string); ok {
				names = append(names, name)
			}
			if children, ok := item["children"].([]any); ok {
				walk(children)
			}
		}
	}
	walk(items)
	return names
}
