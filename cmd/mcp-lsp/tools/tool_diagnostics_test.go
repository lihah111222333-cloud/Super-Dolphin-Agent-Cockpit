package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

type diagnosticsTestRegistry struct {
	lastURIs      []string
	bootstrapURIs []string
	callOrder     []string
	lastScope     common.ToolScope
	scopeOK       bool
}

func (r *diagnosticsTestRegistry) GetManagerForFile(context.Context, string) (lspmanager.Manager, error) {
	return nil, lspmanager.ErrUnsupportedLanguage
}

func (r *diagnosticsTestRegistry) GetManagerForFileWithLanguage(context.Context, string, string) (lspmanager.Manager, error) {
	return nil, lspmanager.ErrUnsupportedLanguage
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
		items = append(items, protocol.PublishDiagnosticsParams{URI: uri})
	}
	return items, nil
}

func (*diagnosticsTestRegistry) WaitDiagnosticsStable(context.Context, []string) error {
	return nil
}

func (*diagnosticsTestRegistry) CurrentDiagnosticGeneration() uint64 {
	return 1
}

func (r *diagnosticsTestRegistry) BootstrapDocument(_ context.Context, uri string) error {
	r.callOrder = append(r.callOrder, "bootstrap")
	r.bootstrapURIs = append(r.bootstrapURIs, uri)
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
