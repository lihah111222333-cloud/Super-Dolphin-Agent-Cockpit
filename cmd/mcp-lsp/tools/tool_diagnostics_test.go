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

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
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
	diagnosticsCalls  int
	manager           lspmanager.Manager
	waitErrs          []error
	waitFn            func(call int, uris []string) error
	waitURIs          [][]string
	waitCalls         int
	reopenURIs        [][]string
	generation        uint64
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
	r.diagnosticsCalls++
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

func (r *diagnosticsTestRegistry) CurrentDiagnosticGeneration() uint64 {
	if r.generation > 0 {
		return r.generation
	}
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
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: mainRoot, Registry: registry})
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
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: mainRoot, Registry: registry})
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
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: mainRoot, Registry: registry})
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

func TestDiagnosticsPreservesLiteralPercentInAbsoluteWorkspacePath(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "100% ready")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir percent workspace: %v", err)
	}
	target := writeDiagnosticsFixture(t, workspace, "main.go")
	registry := &diagnosticsTestRegistry{}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: workspace, Registry: registry})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: workspace, WorkspaceRoots: []string{workspace}})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: target})

	if _, err := handler(ctx, req); err != nil {
		t.Fatalf("diagnostics literal percent path: %v", err)
	}
	assertDiagnosticURIs(t, registry.lastURIs, []string{canonicalFileURI(t, target)})
}

func TestDiagnosticsRejectsDeletedManagedFile(t *testing.T) {
	workspace := t.TempDir()
	target := writeDiagnosticsFixture(t, workspace, "HISTORY.md")
	uri := canonicalFileURI(t, target)
	registry := &diagnosticsTestRegistry{
		diagnosticsByURI: map[string][]protocol.Diagnostic{
			uri: {{Message: "stale cached diagnostic"}},
		},
	}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: workspace, Registry: registry})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: workspace, WorkspaceRoots: []string{workspace}})
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove HISTORY.md fixture: %v", err)
	}
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: target})

	_, err := handler(ctx, req)
	if err == nil {
		t.Fatal("diagnostics returned OK for deleted HISTORY.md, want file_not_found")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("diagnostics error = %v, want os.ErrNotExist (file_not_found)", err)
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
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: workspace, Registry: registry})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: workspace, WorkspaceRoots: []string{workspace}})
	ctx = WithAppManagedReadCapability(ctx)
	target := fileURI(appFile)
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: target})

	_, err := handler(ctx, req)
	if err == nil {
		t.Fatal("diagnostics returned nil error, want app-managed target outside workspace roots rejected")
	}
	if !diagnosticErrorMentionsPath(err, appFile) || !strings.Contains(err.Error(), "outside workspace roots") {
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
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: workspace, Registry: registry})
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
	if !diagnosticErrorMentionsPath(err, appFile) || !strings.Contains(err.Error(), "outside workspace roots") {
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
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: workspace, Registry: registry})
	ctx := common.WithToolScope(
		context.Background(),
		common.ToolScope{CWD: workspace, WorkspaceRoots: []string{workspace}},
	)
	ctx = WithAppManagedReadCapability(ctx)
	target := fileURI(appFile)
	req := marshalDiagnosticsInput(t, fileToolInput{
		Action:    "diagnostics",
		FilePaths: []string{workspaceFile, target},
	})

	_, err := handler(ctx, req)
	if err == nil {
		t.Fatal("diagnostics returned nil error, want mixed batch app-managed target outside workspace roots rejected")
	}
	if !diagnosticErrorMentionsPath(err, appFile) || !strings.Contains(err.Error(), "outside workspace roots") {
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

func diagnosticErrorMentionsPath(err error, target string) bool {
	if err == nil {
		return false
	}
	normalize := func(value string) string {
		value = strings.ToLower(filepath.ToSlash(value))
		for strings.Contains(value, "//") {
			value = strings.ReplaceAll(value, "//", "/")
		}
		return value
	}
	return strings.Contains(normalize(err.Error()), normalize(target))
}

func TestDiagnosticsWithoutMetaCWDRejectsExternalAbsolutePath(t *testing.T) {
	mainRoot := t.TempDir()
	externalRoot := t.TempDir()
	externalFile := writeDiagnosticsFixture(t, externalRoot, "external.go")

	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: mainRoot, Registry: &diagnosticsTestRegistry{}})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: externalFile})

	_, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: mainRoot}), req)
	if err == nil {
		t.Fatalf("diagnostics succeeded for external path without MetaCWD")
	}
	if !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("diagnostics error = %v, want outside workspace root", err)
	}
}

func TestDiagnosticsDeletedFileFailsFast(t *testing.T) {
	root := t.TempDir()
	registry := &diagnosticsTestRegistry{}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
	req := marshalDiagnosticsInput(t, fileToolInput{Action: "diagnostics", FilePath: "deleted.go"})

	_, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), req)
	if err == nil {
		t.Fatal("diagnostics returned OK for deleted file, want file_not_found")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("diagnostics error = %v, want os.ErrNotExist (file_not_found)", err)
	}
	if len(registry.lastURIs) != 0 {
		t.Fatalf("registry Diagnostics URIs = %#v, want no call after file_not_found", registry.lastURIs)
	}
}

func TestDiagnosticsRefreshesStaleFileBeforeReturn(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "stale.go")

	registry := &diagnosticsTestRegistry{}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
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
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
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

func TestDiagnosticsLanguageOverrideDeletedFilesFailFast(t *testing.T) {
	root := t.TempDir()
	for _, input := range []fileToolInput{
		{Action: "diagnostics", FilePath: "deleted-single.txt", LanguageID: "javascript"},
		{Action: "diagnostics", FilePaths: []string{"deleted-batch-a.txt", "deleted-batch-b.txt"}, LanguageID: "javascript"},
	} {
		t.Run(strings.Join(collectDiagnosticTargetPaths(input), ","), func(t *testing.T) {
			registry := &diagnosticsTestRegistry{manager: &languageOverrideDiagnosticsManager{}}
			handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
			_, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), marshalDiagnosticsInput(t, input))
			if err == nil {
				t.Fatal("diagnostics returned OK for deleted language override target, want file_not_found")
			}
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("diagnostics error = %v, want os.ErrNotExist (file_not_found)", err)
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
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
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
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: mainRoot, Registry: registry})
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
	raw, err := json.Marshal(diagnosticsInput{
		FilePath:     input.FilePath,
		FilePaths:    input.FilePaths,
		LanguageID:   input.LanguageID,
		ChangedLines: input.ChangedLines,
	})
	if err != nil {
		t.Fatalf("marshal diagnostics input: %v", err)
	}
	return raw
}

func canonicalFileURI(t *testing.T, path string) string {
	t.Helper()
	parent, err := lspplatform.CanonicalDirectoryPath(filepath.Dir(path))
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

func TestDiagnosticsExplicitDiffFilterAuditsFilteredEntries(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsFixture(t, root, "script.py")
	targetURI := canonicalFileURI(t, target)
	registry := &diagnosticsTestRegistry{diagnosticsByURI: map[string][]protocol.Diagnostic{
		targetURI: {
			{Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}}, Severity: protocol.SeverityError, Source: "Pyright", Code: "reportMissingImports", Message: "Import could not be resolved"},
			{Range: protocol.Range{Start: protocol.Position{Line: 4, Character: 1}}, Severity: protocol.SeverityError, Source: "pyright", Code: "error-outside-changed-lines", Message: "error outside changed lines"},
			{Range: protocol.Range{Start: protocol.Position{Line: 8, Character: 1}}, Severity: protocol.SeverityWarning, Source: "pyright", Code: "reportUnusedImport", Message: "unused import"},
			{Range: protocol.Range{Start: protocol.Position{Line: 9, Character: 1}}, Severity: protocol.SeverityInformation, Source: "pyright", Code: "info-code", Message: "informational note"},
			{Range: protocol.Range{Start: protocol.Position{Line: 10, Character: 1}}, Severity: protocol.SeverityHint, Source: "pyright", Code: "hint-code", Message: "hint note"},
		},
	}}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
	request, err := json.Marshal(diagnosticsInput{
		FilePath: "script.py",
		DiffFilter: &diagnosticsDiffFilterInput{
			Mode:         "changed_lines",
			ChangedLines: map[string][]int{"script.py": {1}},
		},
	})
	if err != nil {
		t.Fatalf("marshal explicit diff filter: %v", err)
	}
	result, err := handler(testToolContext(root), request)
	if err != nil {
		t.Fatalf("explicit diff filter diagnostics: %v", err)
	}
	response := result.(diagnosticsResponse)
	if response.Total != 2 || response.Meta.DiffFilter == nil || response.Meta.DiffFilter.Filtered != 3 {
		t.Fatalf("response = %#v, want both Errors and three audited filtered diagnostics", response)
	}
	if len(response.Meta.DiffFilter.FilteredDiagnostics) != 3 {
		t.Fatalf("filtered diagnostics = %#v, want three entries", response.Meta.DiffFilter.FilteredDiagnostics)
	}
	plainText := response.ToPlainText()
	for _, want := range []string{
		"FILTERED\tmode=changed_lines\tcount=3",
		"code=error-outside-changed-lines",
		"code=reportUnusedImport",
		"severity=information",
		"code=hint-code",
		"returned rows are changed-line findings",
		"FILTERED records audit",
		"filtered is not clean",
	} {
		if !strings.Contains(plainText, want) {
			t.Fatalf("plain text = %q, want audit field %q", plainText, want)
		}
	}
}

func TestDiagnosticsExplicitDiffFilterMapsChangedLinesPerFile(t *testing.T) {
	root := t.TempDir()
	first := writeDiagnosticsFixture(t, root, "first.go")
	second := writeDiagnosticsFixture(t, root, "second.go")
	firstURI, secondURI := canonicalFileURI(t, first), canonicalFileURI(t, second)
	registry := &diagnosticsTestRegistry{diagnosticsByURI: map[string][]protocol.Diagnostic{
		firstURI:  {{Range: protocol.Range{Start: protocol.Position{Line: 1}}, Severity: protocol.SeverityWarning, Code: "first-warning", Message: "first warning"}},
		secondURI: {{Range: protocol.Range{Start: protocol.Position{Line: 8}}, Severity: protocol.SeverityWarning, Code: "second-warning", Message: "second warning"}},
	}}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: registry})
	request, err := json.Marshal(diagnosticsInput{
		FilePaths: []string{"first.go", "second.go"},
		DiffFilter: &diagnosticsDiffFilterInput{
			Mode: "changed_lines",
			ChangedLines: map[string][]int{
				"first.go":  {2},
				"second.go": {9},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal per-file diff filter: %v", err)
	}
	result, err := handler(testToolContext(root), request)
	if err != nil {
		t.Fatalf("per-file diff filter diagnostics: %v", err)
	}
	response := result.(diagnosticsResponse)
	if response.Total != 2 || response.Meta.DiffFilter == nil || response.Meta.DiffFilter.Filtered != 0 {
		t.Fatalf("response = %#v, want both per-file changed lines retained", response)
	}
}

func TestDiagnosticsDiffFilterRejectsMissingFileMapping(t *testing.T) {
	root := t.TempDir()
	first := writeDiagnosticsFixture(t, root, "first.go")
	second := writeDiagnosticsFixture(t, root, "second.go")
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: &diagnosticsTestRegistry{}})
	request, err := json.Marshal(diagnosticsInput{
		FilePaths:  []string{first, second},
		DiffFilter: &diagnosticsDiffFilterInput{Mode: "changed_lines", ChangedLines: map[string][]int{first: {1}}},
	})
	if err != nil {
		t.Fatalf("marshal incomplete diff filter: %v", err)
	}
	_, err = handler(testToolContext(root), request)
	if err == nil || !strings.Contains(err.Error(), "must map every diagnostics target") {
		t.Fatalf("error = %v, want fail-fast per-file mapping error", err)
	}
}

func TestDiagnosticsStrictDecoderRejectsDroppedDiffFilterField(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsFixture(t, root, "main.go")
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: root, Registry: &diagnosticsTestRegistry{}})
	request := []byte(`{"file_path":"main.go","diff_filter":{"mode":"changed_lines","changed_lines":{"main.go":[1]},"dropped_field":true}}`)
	_, err := handler(testToolContext(root), request)
	if err == nil || !strings.Contains(err.Error(), `unknown field "dropped_field"`) {
		t.Fatalf("error = %v, want strict unknown nested diff_filter field", err)
	}
}
