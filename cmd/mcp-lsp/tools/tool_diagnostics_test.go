package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type diagnosticsTestRegistry struct {
	lastURIs          []string
	bootstrapURIs     []string
	callOrder         []string
	managerCalls      int
	languageIDs       []string
	lastScope         common.ToolScope
	scopeOK           bool
	bootstrapErrByURI map[string]error
	diagnosticsByURI  map[string][]protocol.Diagnostic
	manager           lspmanager.Manager
	waitErrs          []error
	waitFn            func(call int, uris []string) error
	waitURIs          [][]string
	waitCalls         int
	reopenURIs        [][]string
}

func (r *diagnosticsTestRegistry) ReopenDocumentsForDiagnostics(_ context.Context, uris []string) error {
	r.callOrder = append(r.callOrder, "reopen")
	r.reopenURIs = append(r.reopenURIs, append([]string(nil), uris...))
	return nil
}

func (r *diagnosticsTestRegistry) GetManagerForFile(context.Context, string) (lspmanager.Manager, error) {
	return nil, lspmanager.ErrUnsupportedLanguage
}

func (r *diagnosticsTestRegistry) GetManagerForFileWithLanguage(_ context.Context, _ string, languageID string) (lspmanager.Manager, error) {
	r.managerCalls++
	r.languageIDs = append(r.languageIDs, languageID)
	if r.manager != nil {
		return r.manager, nil
	}
	return nil, lspmanager.ErrUnsupportedLanguage
}

type languageOverrideDiagnosticsManager struct {
	structureTestManager
	diagnostics     []protocol.Diagnostic
	uri             string
	languageID      string
	reopenURI       string
	didOpenCalls    int
	reopenCalls     int
	diagnosticsURIs [][]string
}

func (m *languageOverrideDiagnosticsManager) DidOpen(_ context.Context, uri, languageID string, _ int, _ string) error {
	m.didOpenCalls++
	m.uri = uri
	m.languageID = languageID
	return nil
}

func (m *languageOverrideDiagnosticsManager) ReopenDocumentForDiagnostics(_ context.Context, uri string) error {
	m.reopenCalls++
	m.reopenURI = uri
	return nil
}

func (m *languageOverrideDiagnosticsManager) Diagnostics(_ context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error) {
	m.diagnosticsURIs = append(m.diagnosticsURIs, append([]string(nil), uris...))
	uri := m.uri
	if uri == "" && len(uris) > 0 {
		uri = uris[0]
	}
	return []protocol.PublishDiagnosticsParams{{URI: uri, Diagnostics: m.diagnostics}}, nil
}

func (r *diagnosticsTestRegistry) GetManagerForLanguage(context.Context, string) (lspmanager.Manager, error) {
	return nil, lspmanager.ErrUnsupportedLanguage
}

func (r *diagnosticsTestRegistry) Diagnostics(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error) {
	r.callOrder = append(r.callOrder, "diagnostics")
	r.lastURIs = append([]string(nil), uris...)
	r.lastScope, r.scopeOK = common.ToolScopeFromContext(ctx)
	items := make([]protocol.PublishDiagnosticsParams, 0, len(uris))
	for _, uri := range uris {
		items = append(items, protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: r.diagnosticsByURI[uri]})
	}
	return items, nil
}

func (r *diagnosticsTestRegistry) WaitDiagnosticsStable(_ context.Context, uris []string) error {
	r.callOrder = append(r.callOrder, "wait")
	index := r.waitCalls
	r.waitCalls++
	r.waitURIs = append(r.waitURIs, append([]string(nil), uris...))
	if r.waitFn != nil {
		return r.waitFn(index, uris)
	}
	if index < len(r.waitErrs) {
		return r.waitErrs[index]
	}
	return nil
}

func (*diagnosticsTestRegistry) CurrentDiagnosticGeneration() uint64 {
	return 1
}

func (r *diagnosticsTestRegistry) BootstrapDocument(_ context.Context, uri string) error {
	r.callOrder = append(r.callOrder, "bootstrap")
	r.bootstrapURIs = append(r.bootstrapURIs, uri)
	if r.bootstrapErrByURI != nil {
		return r.bootstrapErrByURI[uri]
	}
	return nil
}

func (*diagnosticsTestRegistry) Close() error {
	return nil
}

func TestDiagnosticsUsesMetaCWDForExternalAbsolutePath(t *testing.T) {
	mainRoot := t.TempDir()
	externalRoot := t.TempDir()
	externalFile := writeDiagnosticsFixture(t, externalRoot, "external.go")

	registry := &diagnosticsTestRegistry{}
	handler := NewFileHandler(Config{WorkspaceRoot: mainRoot, Registry: registry})
	ctx := context.WithValue(context.Background(), common.CwdContextKey, externalRoot)
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: externalFile})

	if _, err := handler(ctx, req); err != nil {
		t.Fatalf("diagnostics returned error: %v", err)
	}
	assertDiagnosticURIs(t, registry.lastURIs, []string{canonicalFileURI(t, externalFile)})
}

func TestDiagnosticsUsesMetaCWDForRelativePath(t *testing.T) {
	mainRoot := t.TempDir()
	externalRoot := t.TempDir()
	writeDiagnosticsFixture(t, mainRoot, "same.go")
	externalFile := writeDiagnosticsFixture(t, externalRoot, "same.go")

	registry := &diagnosticsTestRegistry{}
	handler := NewFileHandler(Config{WorkspaceRoot: mainRoot, Registry: registry})
	ctx := context.WithValue(context.Background(), common.CwdContextKey, externalRoot)
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "same.go"})

	if _, err := handler(ctx, req); err != nil {
		t.Fatalf("diagnostics returned error: %v", err)
	}
	assertDiagnosticURIs(t, registry.lastURIs, []string{canonicalFileURI(t, externalFile)})
}

func TestDiagnosticsBatchUsesMetaCWD(t *testing.T) {
	mainRoot := t.TempDir()
	externalRoot := t.TempDir()
	first := writeDiagnosticsFixture(t, externalRoot, "first.go")
	second := writeDiagnosticsFixture(t, externalRoot, "second.go")

	registry := &diagnosticsTestRegistry{}
	handler := NewFileHandler(Config{WorkspaceRoot: mainRoot, Registry: registry})
	ctx := context.WithValue(context.Background(), common.CwdContextKey, externalRoot)
	req := marshalDiagnosticsInput(t, fileToolInput{
		Action:    "diagnostics",
		FilePaths: []string{"first.go", second},
	})

	if _, err := handler(ctx, req); err != nil {
		t.Fatalf("diagnostics returned error: %v", err)
	}
	assertDiagnosticURIs(t, registry.lastURIs, []string{canonicalFileURI(t, first), canonicalFileURI(t, second)})
}

func TestDiagnosticsAllowsEncodedAppManagedPathOutsideWorkspace(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "home")
	appHome := filepath.Join(fakeHome, "Library", "Application Support", "Super Dolphin")
	appFile := filepath.Join(appHome, "providers", "codex", "mcp-lsp.go")
	if err := os.MkdirAll(filepath.Dir(appFile), 0o700); err != nil {
		t.Fatalf("mkdir app managed parent: %v", err)
	}
	if err := os.WriteFile(appFile, []byte("package fixture\n"), 0o600); err != nil {
		t.Fatalf("write app managed file: %v", err)
	}
	t.Setenv("HOME", fakeHome)
	t.Setenv("SUPER_DOLPHIN_HOME", appHome)

	workspace := t.TempDir()
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: workspace, WorkspaceRoots: []string{workspace}})
	ctx = WithAppManagedReadCapability(ctx)
	tests := []struct {
		name   string
		target string
	}{
		{name: "file URI", target: fileURI(appFile)},
		{name: "encoded absolute path", target: strings.ReplaceAll(appFile, "Application Support", "Application%20Support")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := handlerBase{}
			targets, _, err := handler.collectDiagnosticTargets(ctx, fileToolInput{Action: "diagnostics", FilePath: tt.target})
			if err != nil {
				t.Fatalf("diagnostics returned error: %v", err)
			}
			assertDiagnosticURIs(t, diagnosticTargetURIs(targets), []string{canonicalFileURI(t, appFile)})
		})
	}
}

func TestDiagnosticsRejectsAppManagedRootWithoutCapability(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "home")
	appHome := filepath.Join(fakeHome, "Library", "Application Support", "Super Dolphin")
	appFile := filepath.Join(appHome, "providers", "codex", "mcp-lsp.go")
	if err := os.MkdirAll(filepath.Dir(appFile), 0o700); err != nil {
		t.Fatalf("mkdir app managed parent: %v", err)
	}
	if err := os.WriteFile(appFile, []byte("package fixture\n"), 0o600); err != nil {
		t.Fatalf("write app managed file: %v", err)
	}
	t.Setenv("HOME", fakeHome)
	t.Setenv("SUPER_DOLPHIN_HOME", appHome)

	workspace := t.TempDir()
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: workspace, WorkspaceRoots: []string{workspace}})
	handler := handlerBase{}

	_, _, err := handler.collectDiagnosticTargets(ctx, fileToolInput{Action: "diagnostics", FilePath: fileURI(appFile)})
	if err == nil {
		t.Fatal("diagnostics returned nil error, want app-managed path rejected without read capability")
	}
	if !strings.Contains(err.Error(), "outside workspace roots") {
		t.Fatalf("diagnostics error = %q, want path_outside_workspace rejection", err.Error())
	}
}

func TestDiagnosticsAppManagedOutsideWorkspaceSkipsStartupOpenRecovery(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "home")
	appHome := filepath.Join(fakeHome, "Library", "Application Support", "Super Dolphin")
	appFile := filepath.Join(appHome, "providers", "codex", "mcp-lsp.go")
	if err := os.MkdirAll(filepath.Dir(appFile), 0o700); err != nil {
		t.Fatalf("mkdir app managed parent: %v", err)
	}
	if err := os.WriteFile(appFile, []byte("package fixture\n"), 0o600); err != nil {
		t.Fatalf("write app managed file: %v", err)
	}
	t.Setenv("HOME", fakeHome)
	t.Setenv("SUPER_DOLPHIN_HOME", appHome)

	workspace := t.TempDir()
	registry := &diagnosticsTestRegistry{waitErrs: []error{lspmanager.ErrDiagnosticsNotReady}}
	handler := NewFileHandler(Config{WorkspaceRoot: workspace, Registry: registry})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: workspace, WorkspaceRoots: []string{workspace}})
	ctx = WithAppManagedReadCapability(ctx)
	target := strings.ReplaceAll(appFile, "Application Support", "Application%20Support")
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: target})

	_, err := handler(ctx, req)
	if err == nil {
		t.Fatal("diagnostics returned nil error, want app-managed target outside workspace roots rejected")
	}
	if !strings.Contains(err.Error(), appFile) || !strings.Contains(err.Error(), "outside workspace roots") {
		t.Fatalf("diagnostics error = %q, want target path %q and outside workspace roots context", err.Error(), appFile)
	}
	assertAppManagedDiagnosticsRegistryNotCalled(t, registry)
}

type appManagedDiagnosticsManager struct {
	structureTestManager
	didOpenCalls     int
	reopenCalls      int
	waitCalls        int
	diagnosticsCalls int
}

func (m *appManagedDiagnosticsManager) DidOpen(context.Context, string, string, int, string) error {
	m.didOpenCalls++
	return nil
}

func (m *appManagedDiagnosticsManager) ReopenDocumentForDiagnostics(context.Context, string) error {
	m.reopenCalls++
	return nil
}

func (m *appManagedDiagnosticsManager) WaitDiagnosticsStable(context.Context, []string) error {
	m.waitCalls++
	return nil
}

func (m *appManagedDiagnosticsManager) Diagnostics(context.Context, []string) ([]protocol.PublishDiagnosticsParams, error) {
	m.diagnosticsCalls++
	return nil, nil
}

func TestDiagnosticsLanguageOverrideRejectsAppManagedOutsideWorkspaceBeforeAnyLSPCall(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "home")
	appHome := filepath.Join(fakeHome, "Library", "Application Support", "Super Dolphin")
	appFile := filepath.Join(appHome, "providers", "codex", "mcp-lsp.txt")
	if err := os.MkdirAll(filepath.Dir(appFile), 0o700); err != nil {
		t.Fatalf("mkdir app managed parent: %v", err)
	}
	if err := os.WriteFile(appFile, []byte("package fixture\n"), 0o600); err != nil {
		t.Fatalf("write app managed file: %v", err)
	}
	t.Setenv("HOME", fakeHome)
	t.Setenv("SUPER_DOLPHIN_HOME", appHome)

	workspace := t.TempDir()
	manager := &appManagedDiagnosticsManager{}
	registry := &diagnosticsTestRegistry{manager: manager}
	handler := NewFileHandler(Config{WorkspaceRoot: workspace, Registry: registry})
	ctx := common.WithToolScope(
		context.Background(),
		common.ToolScope{CWD: workspace, WorkspaceRoots: []string{workspace}},
	)
	ctx = WithAppManagedReadCapability(ctx)
	req := marshalDiagnosticsInput(t, fileToolInput{
		Action:     "diagnostics",
		FilePath:   appFile,
		LanguageID: "go",
	})

	_, err := handler(ctx, req)
	assertAppManagedDiagnosticsRejected(t, err, appFile)
	assertAppManagedDiagnosticsLSPNotCalled(t, registry, manager)
}

func assertAppManagedDiagnosticsRejected(t *testing.T, err error, appFile string) {
	t.Helper()
	if err == nil {
		t.Fatal("diagnostics returned nil error, want language override app-managed target outside workspace roots rejected")
	}
	if !strings.Contains(err.Error(), appFile) || !strings.Contains(err.Error(), "outside workspace roots") {
		t.Fatalf("diagnostics error = %q, want target path %q and outside workspace roots context", err.Error(), appFile)
	}
}

func assertAppManagedDiagnosticsLSPNotCalled(
	t *testing.T,
	registry *diagnosticsTestRegistry,
	manager *appManagedDiagnosticsManager,
) {
	t.Helper()
	assertAppManagedDiagnosticsRegistryNotCalled(t, registry)
	if registry.managerCalls != 0 {
		t.Fatalf("manager lookup calls = %d, want app-managed target rejected before manager lookup", registry.managerCalls)
	}
	if manager.didOpenCalls != 0 || manager.reopenCalls != 0 || manager.waitCalls != 0 || manager.diagnosticsCalls != 0 {
		t.Fatalf(
			"manager calls open/reopen/wait/diagnostics = %d/%d/%d/%d, want all zero",
			manager.didOpenCalls,
			manager.reopenCalls,
			manager.waitCalls,
			manager.diagnosticsCalls,
		)
	}
}

func TestDiagnosticsMixedBatchRejectsAppManagedOutsideWorkspaceBeforeRegistry(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "home")
	appHome := filepath.Join(fakeHome, "Library", "Application Support", "Super Dolphin")
	appFile := filepath.Join(appHome, "providers", "codex", "mcp-lsp.go")
	if err := os.MkdirAll(filepath.Dir(appFile), 0o700); err != nil {
		t.Fatalf("mkdir app managed parent: %v", err)
	}
	if err := os.WriteFile(appFile, []byte("package fixture\n"), 0o600); err != nil {
		t.Fatalf("write app managed file: %v", err)
	}
	t.Setenv("HOME", fakeHome)
	t.Setenv("SUPER_DOLPHIN_HOME", appHome)

	workspace := t.TempDir()
	workspaceFile := writeDiagnosticsFixture(t, workspace, "workspace.go")
	registry := &diagnosticsTestRegistry{}
	handler := NewFileHandler(Config{WorkspaceRoot: workspace, Registry: registry})
	ctx := common.WithToolScope(
		context.Background(),
		common.ToolScope{CWD: workspace, WorkspaceRoots: []string{workspace}},
	)
	ctx = WithAppManagedReadCapability(ctx)
	target := strings.ReplaceAll(appFile, "Application Support", "Application%20Support")
	req := marshalDiagnosticsInput(t, fileToolInput{
		Action:    "diagnostics",
		FilePaths: []string{workspaceFile, target},
	})

	_, err := handler(ctx, req)
	if err == nil {
		t.Fatal("diagnostics returned nil error, want mixed batch app-managed target outside workspace roots rejected")
	}
	if !strings.Contains(err.Error(), appFile) || !strings.Contains(err.Error(), "outside workspace roots") {
		t.Fatalf("diagnostics error = %q, want target path %q and outside workspace roots context", err.Error(), appFile)
	}
	assertAppManagedDiagnosticsRegistryNotCalled(t, registry)
}

func assertAppManagedDiagnosticsRegistryNotCalled(t *testing.T, registry *diagnosticsTestRegistry) {
	t.Helper()
	if len(registry.lastURIs) != 0 {
		t.Fatalf("registry diagnostics URIs = %#v, want app-managed target handled before registry diagnostics", registry.lastURIs)
	}
	if len(registry.bootstrapURIs) != 0 {
		t.Fatalf("bootstrap URIs = %#v, want app-managed target skipped", registry.bootstrapURIs)
	}
	if registry.waitCalls != 0 {
		t.Fatalf("WaitDiagnosticsStable calls = %d, want app-managed target handled before wait", registry.waitCalls)
	}
	if len(registry.reopenURIs) != 0 {
		t.Fatalf("ReopenDocumentsForDiagnostics URIs = %#v, want app-managed target handled before reopen", registry.reopenURIs)
	}
}

func TestDiagnosticsResponseUsesTopLevelMetaFields(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "broken.go")
	targetURI := canonicalFileURI(t, target)
	duplicate := protocol.Diagnostic{
		Range: protocol.Range{
			Start: protocol.Position{Line: 2, Character: 4},
			End:   protocol.Position{Line: 2, Character: 8},
		},
		Severity: protocol.SeverityError,
		Source:   "gopls",
		Message:  "undefined: missing",
	}
	registry := &diagnosticsTestRegistry{
		diagnosticsByURI: map[string][]protocol.Diagnostic{
			targetURI: {
				duplicate,
				duplicate,
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 3, Character: 1},
						End:   protocol.Position{Line: 3, Character: 5},
					},
					Severity: protocol.SeverityWarning,
					Source:   "gopls",
					Message:  "unused value",
				},
			},
		},
	}
	handler := NewFileHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "broken.go"})

	result, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req)
	if err != nil {
		t.Fatalf("diagnostics returned error: %v", err)
	}
	payload := mustMarshalObject(t, result)
	if payload["total"] != float64(2) || payload["showing"] != float64(2) {
		t.Fatalf("diagnostics total/showing = %#v/%#v, want 2/2", payload["total"], payload["showing"])
	}
	if hint, _ := payload["hint"].(string); !strings.Contains(hint, "read_file") || !strings.Contains(hint, "replace_range") {
		t.Fatalf("diagnostics hint = %q, want repair guidance", hint)
	}
	meta, ok := payload["meta"].(map[string]any)
	if !ok {
		t.Fatalf("diagnostics meta = %#v, want object", payload["meta"])
	}
	if _, ok := meta["count"]; ok {
		t.Fatalf("diagnostics meta contains legacy count: %#v", meta)
	}
}

func TestDiagnosticsRendersTypeScriptDeprecatedSuggestionsAsHint(t *testing.T) {
	tables := buildDiagnosticsTables([]protocol.PublishDiagnosticsParams{{
		URI: "file:///workspace/form.tsx",
		Diagnostics: []protocol.Diagnostic{{
			Range: protocol.Range{
				Start: protocol.Position{Line: 9, Character: 17},
				End:   protocol.Position{Line: 9, Character: 26},
			},
			Severity: protocol.SeverityWarning,
			Source:   "typescript",
			Code:     float64(6385),
			Message:  "'FormEvent' is deprecated.",
		}},
	}}, nil)

	if len(tables) != 1 || len(tables[0].Rows) != 1 {
		t.Fatalf("diagnostics tables = %#v, want one row", tables)
	}
	if got := tables[0].Rows[0][2]; got != "hint" {
		t.Fatalf("deprecated TypeScript diagnostic severity = %#v, want hint", got)
	}
}

func TestDiagnosticsWithoutMetaCWDRejectsExternalAbsolutePath(t *testing.T) {
	mainRoot := t.TempDir()
	externalRoot := t.TempDir()
	externalFile := writeDiagnosticsFixture(t, externalRoot, "external.go")

	handler := NewFileHandler(Config{WorkspaceRoot: mainRoot, Registry: &diagnosticsTestRegistry{}})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: externalFile})

	_, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: mainRoot}), req)
	if err == nil {
		t.Fatalf("diagnostics succeeded for external path without MetaCWD")
	}
	if !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("diagnostics error = %v, want outside workspace root", err)
	}
}

func TestDiagnosticsDeletedFileStillCallsRegistryForCleanup(t *testing.T) {
	root := t.TempDir()
	deletedFile := filepath.Join(root, "deleted.go")

	registry := &diagnosticsTestRegistry{}
	handler := NewFileHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "deleted.go"})

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req); err != nil {
		t.Fatalf("diagnostics returned error for deleted file: %v", err)
	}
	assertDiagnosticURIs(t, registry.lastURIs, []string{canonicalDeletedFileURI(t, deletedFile)})
}

func TestDiagnosticsRefreshesStaleFileBeforeReturn(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "stale.go")

	registry := &diagnosticsTestRegistry{}
	handler := NewFileHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "stale.go"})

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req); err != nil {
		t.Fatalf("diagnostics returned error for stale file refresh: %v", err)
	}
	wantURI := canonicalFileURI(t, target)
	assertDiagnosticURIs(t, registry.bootstrapURIs, []string{wantURI})
	assertDiagnosticURIs(t, registry.lastURIs, []string{wantURI})
	if len(registry.callOrder) < 2 || registry.callOrder[0] != "bootstrap" || registry.callOrder[len(registry.callOrder)-1] != "diagnostics" {
		t.Fatalf("diagnostics call order = %#v, want bootstrap before diagnostics", registry.callOrder)
	}
}

func TestDiagnosticsShellScriptBootstrapsShellscriptLanguage(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "broken.sh")

	registry := &diagnosticsShellRegistry{}
	handler := NewFileHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "broken.sh"})

	_, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req)
	if err != nil {
		t.Fatalf("diagnostics returned error for shell script: %v", err)
	}
	wantURI := canonicalFileURI(t, target)
	assertDiagnosticURIs(t, registry.bootstrapURIs, []string{wantURI})
	assertDiagnosticURIs(t, registry.lastURIs, []string{wantURI})
	if len(registry.bootstrapLanguageIDs) != 1 || registry.bootstrapLanguageIDs[0] != "shellscript" {
		t.Fatalf("bootstrap language IDs = %#v, want shellscript", registry.bootstrapLanguageIDs)
	}
}

func TestDiagnosticsLanguageOverrideSingleAndBatchShellResultsAreEquivalent(t *testing.T) {
	root := t.TempDir()
	hookDir := filepath.Join(root, ".githooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(hookDir, "pre-commit")
	if err := os.WriteFile(target, []byte("#!/usr/bin/env bash\nunused=1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	diagnostic := protocol.Diagnostic{
		Range: protocol.Range{
			Start: protocol.Position{Line: 1, Character: 0},
			End:   protocol.Position{Line: 1, Character: 8},
		},
		Severity: protocol.SeverityWarning,
		Source:   "shellcheck",
		Code:     "SC2034",
		Message:  "unused appears unused",
	}

	single, singleManager := runLanguageOverrideDiagnosticsFixture(t, root, fileToolInput{
		Action:     "diagnostics",
		FilePath:   target,
		LanguageID: "shellscript",
	}, diagnostic)
	batch, batchManager := runLanguageOverrideDiagnosticsFixture(t, root, fileToolInput{
		Action:     "diagnostics",
		FilePaths:  []string{target},
		LanguageID: "shellscript",
	}, diagnostic)

	if !reflect.DeepEqual(single.Data, batch.Data) || single.Total != batch.Total {
		t.Fatalf("single diagnostics = %#v, batch diagnostics = %#v", single, batch)
	}
	for name, manager := range map[string]*languageOverrideDiagnosticsManager{"single": singleManager, "batch": batchManager} {
		if manager.languageID != "shellscript" || manager.reopenURI != manager.uri {
			t.Errorf("%s language override lifecycle = language %q uri %q reopen %q", name, manager.languageID, manager.uri, manager.reopenURI)
		}
	}
}

func TestDiagnosticsLanguageOverrideDeletedFilesUseManagerCleanup(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name  string
		input fileToolInput
		paths []string
	}{
		{
			name:  "single",
			input: fileToolInput{Action: "diagnostics", FilePath: "deleted-single.txt", LanguageID: "javascript"},
			paths: []string{"deleted-single.txt"},
		},
		{
			name:  "batch",
			input: fileToolInput{Action: "diagnostics", FilePaths: []string{"deleted-batch-a.txt", "deleted-batch-b.txt"}, LanguageID: "javascript"},
			paths: []string{"deleted-batch-a.txt", "deleted-batch-b.txt"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &languageOverrideDiagnosticsManager{}
			registry := &diagnosticsTestRegistry{manager: manager}
			handler := NewFileHandler(Config{WorkspaceRoot: root, Registry: registry})

			if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), marshalDiagnosticsInput(t, tc.input)); err != nil {
				t.Fatalf("diagnostics for deleted language override target: %v", err)
			}
			wantURIs := make([][]string, 0, len(tc.paths))
			for _, path := range tc.paths {
				wantURIs = append(wantURIs, []string{canonicalDeletedFileURI(t, filepath.Join(root, path))})
			}
			if !reflect.DeepEqual(manager.diagnosticsURIs, wantURIs) {
				t.Fatalf("manager cleanup diagnostics URIs = %#v, want %#v", manager.diagnosticsURIs, wantURIs)
			}
			wantLanguageIDs := make([]string, len(tc.paths))
			for index := range wantLanguageIDs {
				wantLanguageIDs[index] = "javascript"
			}
			if !reflect.DeepEqual(registry.languageIDs, wantLanguageIDs) {
				t.Fatalf("manager language overrides = %#v, want %#v", registry.languageIDs, wantLanguageIDs)
			}
			if manager.didOpenCalls != 0 || manager.reopenCalls != 0 {
				t.Fatalf("deleted target manager open/reopen calls = %d/%d, want 0/0", manager.didOpenCalls, manager.reopenCalls)
			}
		})
	}
}

func runLanguageOverrideDiagnosticsFixture(
	t *testing.T,
	root string,
	input fileToolInput,
	diagnostic protocol.Diagnostic,
) (diagnosticsResponse, *languageOverrideDiagnosticsManager) {
	t.Helper()
	manager := &languageOverrideDiagnosticsManager{diagnostics: []protocol.Diagnostic{diagnostic}}
	registry := &diagnosticsTestRegistry{manager: manager}
	handler := NewFileHandler(Config{WorkspaceRoot: root, Registry: registry})
	request := marshalDiagnosticsInput(t, input)
	result, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), request)
	if err != nil {
		t.Fatalf("run language override diagnostics: %v", err)
	}
	response, ok := result.(diagnosticsResponse)
	if !ok {
		t.Fatalf("language override diagnostics result = %T, want diagnosticsResponse", result)
	}
	return response, manager
}

type diagnosticsShellRegistry struct {
	diagnosticsTestRegistry
	bootstrapLanguageIDs []string
}

func (r *diagnosticsShellRegistry) BootstrapDocument(_ context.Context, uri string) error {
	r.callOrder = append(r.callOrder, "bootstrap")
	r.bootstrapURIs = append(r.bootstrapURIs, uri)
	lang := lspmanager.DetectLanguageID(strings.TrimPrefix(uri, "file://"))
	r.bootstrapLanguageIDs = append(r.bootstrapLanguageIDs, lang)
	if lang != "shellscript" {
		return fmt.Errorf("%s: %w", uri, lspmanager.ErrUnsupportedLanguage)
	}
	return nil
}

func TestDiagnosticsPassesTrustedToolScopeToRegistry(t *testing.T) {
	mainRoot := t.TempDir()
	scopedRoot := t.TempDir()
	target := writeDiagnosticsFixture(t, scopedRoot, "scoped.go")

	registry := &diagnosticsTestRegistry{}
	handler := NewFileHandler(Config{WorkspaceRoot: mainRoot, Registry: registry})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		AgentID:  "agent-scope",
		ThreadID: "thread-scope",
		CallID:   "call-scope",
		CWD:      scopedRoot,
		Family:   "lsp",
	})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "scoped.go"})

	if _, err := handler(ctx, req); err != nil {
		t.Fatalf("diagnostics returned error: %v", err)
	}
	assertDiagnosticURIs(t, registry.lastURIs, []string{canonicalFileURI(t, target)})
	if !registry.scopeOK {
		t.Fatalf("registry Diagnostics ctx missing trusted ToolScope")
	}
	if registry.lastScope.AgentID != "agent-scope" || registry.lastScope.ThreadID != "thread-scope" || registry.lastScope.CallID != "call-scope" {
		t.Fatalf("registry scope = %#v, want trusted identity", registry.lastScope)
	}
	if registry.lastScope.CWD != scopedRoot {
		t.Fatalf("registry scope cwd = %q, want %q", registry.lastScope.CWD, scopedRoot)
	}
}

func TestDiagnosticsReportsPartialBootstrapFailure(t *testing.T) {
	registry := &diagnosticsTestRegistry{
		bootstrapErrByURI: map[string]error{
			"file:///repo/bad.ts": errors.New("bootstrap boom"),
		},
	}
	handler := handlerBase{registry: registry}

	_, err := handler.reactiveBootstrap(context.Background(), []string{"file:///repo/bad.ts", "file:///repo/good.ts"})
	if err == nil || !strings.Contains(err.Error(), "bootstrap boom") || !strings.Contains(err.Error(), "bad.ts") {
		t.Fatalf("fetchDiagnosticsWithRetry() error = %v, want partial bootstrap failure", err)
	}
}

func TestDiagnosticsRecoversStartupWaitByBootstrappingTarget(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "startup.go")
	registry := &diagnosticsTestRegistry{
		waitErrs: []error{lspmanager.ErrDiagnosticsNotReady},
	}
	handler := NewFileHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "startup.go"})

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req); err != nil {
		t.Fatalf("diagnostics returned startup wait error: %v", err)
	}
	wantURI := canonicalFileURI(t, target)
	assertDiagnosticURIs(t, registry.bootstrapURIs, []string{wantURI, wantURI})
	if registry.waitCalls != 2 {
		t.Fatalf("WaitDiagnosticsStable calls = %d, want retry after startup bootstrap", registry.waitCalls)
	}
	if len(registry.callOrder) == 0 || registry.callOrder[len(registry.callOrder)-1] != "diagnostics" {
		t.Fatalf("diagnostics call order = %#v, want diagnostics after startup recovery", registry.callOrder)
	}
}

func TestDiagnosticsRetriesStartupWaitUntilFifthRetry(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "slow.go")
	registry := &diagnosticsTestRegistry{
		waitErrs: []error{
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
		},
	}
	handler := NewFileHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "slow.go"})

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req); err != nil {
		t.Fatalf("diagnostics returned startup retry error: %v", err)
	}
	wantURI := canonicalFileURI(t, target)
	assertDiagnosticURIs(t, registry.bootstrapURIs, []string{wantURI, wantURI})
	if registry.waitCalls != 6 {
		t.Fatalf("WaitDiagnosticsStable calls = %d, want initial wait plus 5 retries", registry.waitCalls)
	}
}

func TestDiagnosticsReportsStartupTimeoutAfterFiveRetries(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "never.go")
	registry := &diagnosticsTestRegistry{
		waitErrs: []error{
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
			lspmanager.ErrDiagnosticsNotReady,
		},
	}
	handler := NewFileHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "never.go"})

	_, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req)
	if err == nil || !errors.Is(err, lspmanager.ErrDiagnosticsNotReady) {
		t.Fatalf("diagnostics error = %v, want ErrDiagnosticsNotReady after retries", err)
	}
	wantURI := canonicalFileURI(t, target)
	assertDiagnosticURIs(t, registry.bootstrapURIs, []string{wantURI, wantURI})
	if registry.waitCalls != 6 {
		t.Fatalf("WaitDiagnosticsStable calls = %d, want initial wait plus 5 retries", registry.waitCalls)
	}
}

func TestDiagnosticsRetryBackoffSequence(t *testing.T) {
	tests := []struct {
		retry int
		want  time.Duration
	}{
		{retry: 0, want: 0},
		{retry: 1, want: 300 * time.Millisecond},
		{retry: 2, want: 600 * time.Millisecond},
		{retry: 3, want: 1200 * time.Millisecond},
		{retry: 4, want: 2400 * time.Millisecond},
		{retry: 5, want: 4800 * time.Millisecond},
	}

	for _, tt := range tests {
		if got := diagnosticsRetryBackoff(tt.retry); got != tt.want {
			t.Fatalf("diagnosticsRetryBackoff(%d) = %s, want %s", tt.retry, got, tt.want)
		}
	}
}

func TestDiagnosticsBatchReturnsPartialAfterStartupRetryMissesOneTarget(t *testing.T) {
	root := t.TempDir()
	first := writeDiagnosticsFixture(t, root, "ready.go")
	second := writeDiagnosticsFixture(t, root, "slow.go")
	firstURI := canonicalFileURI(t, first)
	secondURI := canonicalFileURI(t, second)
	registry := &diagnosticsTestRegistry{
		waitFn: func(_ int, uris []string) error {
			if len(uris) == 1 && uris[0] == firstURI {
				return nil
			}
			return fmt.Errorf("%w: diagnostics did not publish for requested targets before 1.5s: %s", lspmanager.ErrDiagnosticsNotReady, secondURI)
		},
	}
	handler := NewFileHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePaths: []string{"ready.go", "slow.go"}})

	result, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req)
	if err != nil {
		t.Fatalf("diagnostics returned batch readiness error: %v", err)
	}
	envelope, ok := result.(diagnosticsResponse)
	if !ok {
		t.Fatalf("diagnostics result = %T, want diagnosticsResponse for no diagnostic rows", result)
	}
	if !strings.Contains(envelope.Meta.Message, "partial") || !strings.Contains(envelope.Meta.Message, secondURI) {
		t.Fatalf("diagnostics message = %q, want partial warning for %s", envelope.Meta.Message, secondURI)
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal diagnostics response: %v", err)
	}
	if strings.Contains(string(raw), "source") {
		t.Fatalf("diagnostics response exposes source: %s", string(raw))
	}
	assertDiagnosticURIs(t, registry.bootstrapURIs, []string{firstURI, secondURI, firstURI, secondURI})
	if registry.waitCalls < 3 {
		t.Fatalf("WaitDiagnosticsStable calls = %d, want batch retry plus per-target wait", registry.waitCalls)
	}
}

func TestDiagnosticsPropagatesNonStartupWaitError(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "broken.go")
	registry := &diagnosticsTestRegistry{
		waitErrs: []error{errors.New("diagnostic cache corrupt")},
	}
	handler := NewFileHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "broken.go"})

	_, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req)
	if err == nil || !strings.Contains(err.Error(), "diagnostic cache corrupt") {
		t.Fatalf("diagnostics error = %v, want non-startup wait failure", err)
	}
	assertDiagnosticURIs(t, registry.bootstrapURIs, []string{canonicalFileURI(t, target)})
}

func writeDiagnosticsFixture(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("package fixture\n"), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
	return path
}

func marshalDiagnosticsInput(t *testing.T, input fileToolInput) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal diagnostics input: %v", err)
	}
	return raw
}

func canonicalFileURI(t *testing.T, path string) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		t.Fatalf("resolve fixture parent: %v", err)
	}
	return fileURI(filepath.Join(parent, filepath.Base(path)))
}

func canonicalDeletedFileURI(t *testing.T, path string) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		t.Fatalf("resolve fixture parent: %v", err)
	}
	return fileURI(filepath.Join(parent, filepath.Base(path)))
}

func assertDiagnosticURIs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("diagnostic URIs = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("diagnostic URIs = %#v, want %#v", got, want)
		}
	}
}
