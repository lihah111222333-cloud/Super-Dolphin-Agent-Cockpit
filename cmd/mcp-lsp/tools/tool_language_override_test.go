package tools

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

type languageOverrideRegistry struct {
	manager       *languageOverrideManager
	gotFilePath   string
	gotLanguageID string
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
	reopenURI         string
	reopenScope       lspmanager.ResolvedToolScope
}

func (m *languageOverrideManager) DidOpen(ctx context.Context, _ string, languageID string, _ int, _ string) error {
	m.didOpenLanguageID = languageID
	m.didOpenScope, _ = lspmanager.ResolvedToolScopeFromContext(ctx)
	return m.didOpenErr
}

func (m *languageOverrideManager) ReopenDocumentForDiagnostics(ctx context.Context, uri string) error {
	m.reopenURI = uri
	m.reopenScope, _ = lspmanager.ResolvedToolScopeFromContext(ctx)
	return nil
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
	canonicalPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve canonical fixture path: %v", err)
	}
	wantURI := "file://" + canonicalPath
	if manager.reopenURI != wantURI {
		t.Fatalf("reopen URI = %q, want %q", manager.reopenURI, wantURI)
	}
	if manager.reopenScope.LanguageID != "javascript" || manager.reopenScope.ManagerKey != "manager:javascript" {
		t.Fatalf("reopen scope = %#v, want javascript manager scope", manager.reopenScope)
	}
}
