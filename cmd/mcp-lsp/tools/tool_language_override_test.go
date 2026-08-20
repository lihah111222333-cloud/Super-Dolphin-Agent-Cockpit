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

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type languageOverrideRegistry struct {
	manager       *languageOverrideManager
	gotFilePath   string
	gotLanguageID string
	fileErr       error
}

func (r *languageOverrideRegistry) GetManagerForFile(_ context.Context, filePath string) (lspmanager.Manager, error) {
	languageID := lspmanager.DetectLanguageID(filePath)
	r.gotFilePath = filePath
	r.gotLanguageID = languageID
	return lspmanager.ManagerWithResolvedScope(r.manager, lspmanager.ResolvedToolScope{
		ToolScope: lspmanager.ToolScope{LanguageID: languageID, TargetPath: filePath},
		ScopeKey:  "scope",
		// The workspace/cache key carries the language selected for this file.
		WorkspaceKey: "workspace:" + languageID,
		ManagerKey:   "manager:" + languageID,
	}), nil
}

func (r *languageOverrideRegistry) GetManagerForFileWithLanguage(_ context.Context, filePath, languageID string) (lspmanager.Manager, error) {
	r.gotFilePath = filePath
	r.gotLanguageID = languageID
	if r.fileErr != nil {
		return nil, r.fileErr
	}
	return lspmanager.ManagerWithResolvedScope(r.manager, lspmanager.ResolvedToolScope{
		ToolScope: lspmanager.ToolScope{LanguageID: languageID, TargetPath: filePath},
		ScopeKey:  "scope",
		// The workspace/cache key carries the override, preventing same-URI
		// diagnostics/bootstrap reuse across language modes.
		WorkspaceKey: "workspace:" + languageID,
		ManagerKey:   "manager:" + languageID,
	}), nil
}

func (r *languageOverrideRegistry) GetManagerForLanguage(context.Context, string) (lspmanager.Manager, error) {
	return r.manager, nil
}

func (*languageOverrideRegistry) Diagnostics(context.Context, []string) ([]protocol.PublishDiagnosticsParams, error) {
	return nil, nil
}

func (*languageOverrideRegistry) WaitDiagnosticsStable(context.Context, []string) error { return nil }

func (*languageOverrideRegistry) CurrentDiagnosticGeneration() uint64 { return 0 }

func (*languageOverrideRegistry) BootstrapDocument(context.Context, string) error { return nil }

func (*languageOverrideRegistry) Close() error { return nil }

type languageOverrideManager struct {
	structureTestManager
	didOpenLanguageID string
	didOpenScope      lspmanager.ResolvedToolScope
	didOpenErr        error
	didOpenErrs       []error
	didOpenCalls      int
	reopenURI         string
	reopenScope       lspmanager.ResolvedToolScope
	waitURIs          []string
	diagnosticsURIs   []string
}

func (m *languageOverrideManager) DidOpen(ctx context.Context, _ string, languageID string, _ int, _ string) error {
	m.didOpenCalls++
	m.didOpenLanguageID = languageID
	m.didOpenScope, _ = lspmanager.ResolvedToolScopeFromContext(ctx)
	if m.didOpenCalls <= len(m.didOpenErrs) {
		return m.didOpenErrs[m.didOpenCalls-1]
	}
	return m.didOpenErr
}

func (m *languageOverrideManager) ReopenDocumentForDiagnostics(ctx context.Context, uri string) error {
	m.reopenURI = uri
	m.reopenScope, _ = lspmanager.ResolvedToolScopeFromContext(ctx)
	return nil
}

func (m *languageOverrideManager) WaitDiagnosticsStable(_ context.Context, uris []string) error {
	m.waitURIs = append([]string(nil), uris...)
	return nil
}

func (m *languageOverrideManager) Diagnostics(_ context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error) {
	m.diagnosticsURIs = append([]string(nil), uris...)
	return nil, nil
}

func TestLanguageOverrideParticipatesInCacheKey(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &languageOverrideManager{}
	registry := &languageOverrideRegistry{manager: manager}
	handler := NewFileHandler(Config{WorkspaceRoot: root, Registry: registry})
	payload, err := json.Marshal(map[string]any{
		"action":      "open_file",
		"file_path":   "sample.go",
		"language_id": "typescript",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), payload); err != nil {
		t.Fatalf("open_file with language_id returned error: %v", err)
	}
	if registry.gotLanguageID != "typescript" {
		t.Fatalf("registry language = %q, want override typescript", registry.gotLanguageID)
	}
	if manager.didOpenLanguageID != "typescript" {
		t.Fatalf("DidOpen language = %q, want override typescript", manager.didOpenLanguageID)
	}
	if manager.didOpenScope.LanguageID != "typescript" {
		t.Fatalf("resolved scope language = %q, want typescript", manager.didOpenScope.LanguageID)
	}
	if manager.didOpenScope.WorkspaceKey != "workspace:typescript" || manager.didOpenScope.ManagerKey != "manager:typescript" {
		t.Fatalf("resolved scope keys = workspace %q manager %q, want override-derived cache keys", manager.didOpenScope.WorkspaceKey, manager.didOpenScope.ManagerKey)
	}
}

func TestInspectLanguageOverrideRoutesAmbiguousExtension(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "contract.mod")
	if err := os.WriteFile(path, []byte("module contract.example/fake\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	registry := &languageOverrideRegistry{manager: &languageOverrideManager{}}
	handler := NewInspectHandler(registry)
	payload, err := json.Marshal(map[string]any{
		"action":      "hover",
		"pos":         path + ":1:1",
		"language_id": "gomod",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})

	if _, err := handler(ctx, payload); err != nil {
		t.Fatalf("inspect with language_id returned error: %v", err)
	}
	if registry.gotLanguageID != "gomod" {
		t.Fatalf("registry language = %q, want gomod override", registry.gotLanguageID)
	}
}

func TestManagerForFileAutomaticallyRoutesSQLiteSQL(t *testing.T) {
	root := t.TempDir()
	writeSQLDialectTestFile(t, root, "sqlc.yaml", "version: \"2\"\nsql:\n  - engine: sqlite\n    queries: queries\n")
	target := writeSQLDialectTestFile(t, root, "queries/query.sql", "SELECT ?;\n")
	registry := &languageOverrideRegistry{manager: &languageOverrideManager{}}

	if _, err := managerForFile(sqlDialectTestContext(root), registry, target, ""); err != nil {
		t.Fatalf("managerForFile SQLite SQL: %v", err)
	}
	if registry.gotLanguageID != sqliteSQLLanguageID {
		t.Fatalf("registry language = %q, want %q", registry.gotLanguageID, sqliteSQLLanguageID)
	}
}

func TestManagerForFileWrapsUnsupportedLanguageAttribution(t *testing.T) {
	root := t.TempDir()
	target := writeSQLDialectTestFile(t, root, "sample.unknown", "opaque\n")
	registry := &languageOverrideRegistry{
		manager: &languageOverrideManager{},
		fileErr: lspmanager.ErrUnsupportedLanguage,
	}

	_, err := managerForFile(context.Background(), registry, target, "proto")
	if err == nil {
		t.Fatal("managerForFile() error = nil, want language_unsupported")
	}
	if !errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
		t.Fatalf("managerForFile() error = %v, want errors.Is ErrUnsupportedLanguage", err)
	}
	var coded *common.CodedToolError
	if !errors.As(err, &coded) {
		t.Fatalf("managerForFile() error = %T, want *common.CodedToolError", err)
	}
	if coded.Code != "language_unsupported" {
		t.Fatalf("coded error = %q, want language_unsupported", coded.Code)
	}
	wantDetected := lspmanager.DetectLanguageID(target)
	for key, want := range map[string]any{
		"requested_language": "proto",
		"detected_language":  wantDetected,
		"resolved_language":  "proto",
		"file_extension":     ".unknown",
		"adapter_status":     "registry_lookup_miss",
	} {
		if got := coded.Meta[key]; got != want {
			t.Errorf("coded meta[%q] = %#v, want %#v", key, got, want)
		}
	}
}

func TestFuncRangeEnricherRoutesSQLToSQLite(t *testing.T) {
	root := t.TempDir()
	target := writeSQLDialectTestFile(t, root, "query.sql", "SELECT ?;\n")
	registry := &languageOverrideRegistry{manager: &languageOverrideManager{}}
	enricher := newFuncRangeEnricher(sqlDialectTestContext(root), registry)

	if _, err := enricher.Symbols(target); err != nil {
		t.Fatalf("enrich SQLite SQL symbols: %v", err)
	}
	if registry.gotLanguageID != sqliteSQLLanguageID {
		t.Fatalf("enricher registry language = %q, want %q", registry.gotLanguageID, sqliteSQLLanguageID)
	}
}

func TestOpenFileReturnsErrorWhenDidOpenFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &languageOverrideManager{didOpenErr: errors.New("did open boom")}
	registry := &languageOverrideRegistry{manager: manager}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})

	_, err := handlerBase{registry: registry}.openFile(ctx, path, "")
	if err == nil || !strings.Contains(err.Error(), "did open boom") {
		t.Fatalf("openFile() error = %v, want DidOpen failure", err)
	}
}

func TestOpenFileRetriesColdBootstrapTimeoutOnce(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &languageOverrideManager{didOpenErrs: []error{
		fmt.Errorf("initialize LSP client: %w", context.DeadlineExceeded),
		nil,
	}}
	registry := &languageOverrideRegistry{manager: manager}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})

	result, err := handlerBase{registry: registry}.openFile(ctx, path, "")
	if err != nil {
		t.Fatalf("openFile() error = %v, want cold bootstrap retry success", err)
	}
	if result.Status != "opened" || manager.didOpenCalls != 2 {
		t.Fatalf("openFile() result=%#v DidOpen calls=%d, want success after exactly one retry", result, manager.didOpenCalls)
	}
}

func TestDiagnosticsLanguageOverrideReopensDocumentWithResolvedScope(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(path, []byte("function staleName() {}\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &languageOverrideManager{}
	registry := &languageOverrideRegistry{manager: manager}
	handler := NewFileHandler(Config{WorkspaceRoot: root, Registry: registry})
	payload, err := json.Marshal(map[string]any{
		"action":      "diagnostics",
		"file_path":   "sample.txt",
		"language_id": "javascript",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), payload); err != nil {
		t.Fatalf("diagnostics with language_id returned error: %v", err)
	}
	canonicalPath, err := lspplatform.CanonicalExistingPath(path)
	if err != nil {
		t.Fatalf("resolve canonical fixture path: %v", err)
	}
	wantURI := fileURI(canonicalPath)
	if manager.reopenURI != wantURI {
		t.Fatalf("reopen URI = %q, want %q", manager.reopenURI, wantURI)
	}
	if manager.reopenScope.LanguageID != "javascript" || manager.reopenScope.ManagerKey != "manager:javascript" {
		t.Fatalf("reopen scope = %#v, want javascript manager scope", manager.reopenScope)
	}
	if len(manager.waitURIs) != 1 || manager.waitURIs[0] != wantURI {
		t.Fatalf("wait diagnostics URIs = %#v, want [%q]", manager.waitURIs, wantURI)
	}
	if len(manager.diagnosticsURIs) != 1 || manager.diagnosticsURIs[0] != wantURI {
		t.Fatalf("pull diagnostics URIs = %#v, want [%q]", manager.diagnosticsURIs, wantURI)
	}
}
