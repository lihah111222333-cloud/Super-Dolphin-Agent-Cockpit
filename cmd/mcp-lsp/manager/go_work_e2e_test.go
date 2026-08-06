//go:build lsp_integration

package manager_test

import (
	"context"
	"encoding/json"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

func TestGoWorkMultiModuleDiagnostics(t *testing.T) {
	t.Setenv("GOWORK", "")
	repo := normalizedGoWorkE2ETempDir(t)
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: repo, WorkspaceRoots: []string{repo}})
	backend := filepath.Join(repo, "backend")
	tools := filepath.Join(repo, "tools")
	writeGoWorkE2EGoMod(t, backend, "example.com/backend")
	writeGoWorkE2EGoMod(t, tools, "example.com/tools")
	goWorkPath := filepath.Join(repo, "go.work")
	writeGoWorkE2EFile(t, goWorkPath, "go 1.25.0\n\nuse (\n\t./backend\n\t./tools\n)\n")
	target := writeGoWorkE2EGoFile(t, backend, "main.go")
	targetURI := goWorkE2EFileURI(target)

	factory := &goWorkE2EClientFactory{diagnosticURI: targetURI}
	manager := multilsp.NewManager(multilsp.Config{
		WorkspaceRoot:      repo,
		ClientFactory:      factory,
		DiagnosticsMaxWait: 1,
	})
	defer closeGoWorkE2EManager(t, manager)

	if _, err := manager.EnsureClient(ctx, target, "go"); err != nil {
		t.Fatalf("ensure go.work multi-module client: %v", err)
	}
	call := factory.callAt(t, 0)
	if call.rootDir != repo {
		t.Fatalf("gopls rootDir = %q, want go.work root %q", call.rootDir, repo)
	}
	if !reflect.DeepEqual(call.env, []string{"GOWORK=" + goWorkPath}) {
		t.Fatalf("gopls env = %#v, want explicit go.work", call.env)
	}

	diagnostics, err := manager.Diagnostics(ctx, nil)
	if err != nil {
		t.Fatalf("Diagnostics for go.work multi-module workspace: %v", err)
	}
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics count = %d, want 1: %#v", len(diagnostics), diagnostics)
	}
	assertGoWorkE2EDiagnostic(t, diagnostics[0], targetURI)
}

func TestTwoWorktreesNoWorkspaceKeyCollision(t *testing.T) {
	t.Setenv("GOWORK", "")
	wtA := filepath.Join(normalizedGoWorkE2ETempDir(t), "wt-a")
	wtB := filepath.Join(normalizedGoWorkE2ETempDir(t), "wt-b")
	writeGoWorkE2EGoMod(t, wtA, "example.com/root")
	writeGoWorkE2EGoMod(t, wtB, "example.com/root")
	targetA := writeGoWorkE2EGoFile(t, wtA, "main.go")
	targetB := writeGoWorkE2EGoFile(t, wtB, "main.go")

	manager := multilsp.NewManager(multilsp.Config{WorkspaceRoot: wtA})
	defer closeGoWorkE2EManager(t, manager)
	resolver := multilsp.NewRegistryScopedResolver(manager)
	if resolver == nil {
		t.Fatalf("NewRegistryScopedResolver returned nil")
	}

	scopedA, err := resolver.ForToolScope(lspmanager.ToolScope{
		AgentID:    "agent-32",
		ThreadID:   "thread-go",
		CWD:        wtA,
		LanguageID: "go",
		TargetPath: targetA,
	})
	if err != nil {
		t.Fatalf("resolve worktree A scope: %v", err)
	}
	scopedB, err := resolver.ForToolScope(lspmanager.ToolScope{
		AgentID:    "agent-32",
		ThreadID:   "thread-go",
		CWD:        wtB,
		LanguageID: "go",
		TargetPath: targetB,
	})
	if err != nil {
		t.Fatalf("resolve worktree B scope: %v", err)
	}

	if scopedA.ResolvedScope.WorkspaceKey == scopedB.ResolvedScope.WorkspaceKey {
		t.Fatalf("two physical worktrees shared WorkspaceKey: %q", scopedA.ResolvedScope.WorkspaceKey)
	}
	assertGoWorkE2EResolvedScopes(t, []struct {
		name  string
		scope lspmanager.ResolvedToolScope
		root  string
	}{
		{name: "A", scope: scopedA.ResolvedScope, root: wtA},
		{name: "B", scope: scopedB.ResolvedScope, root: wtB},
	})
}

func closeGoWorkE2EManager(t *testing.T, manager interface{ Close() error }) {
	t.Helper()
	if err := manager.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}
}

func assertGoWorkE2EDiagnostic(t *testing.T, got protocol.PublishDiagnosticsParams, targetURI string) {
	t.Helper()
	if got.URI != targetURI || len(got.Diagnostics) != 1 {
		t.Fatalf("diagnostics payload = %#v, want one diagnostic for target URI", got)
	}
	if got.Diagnostics[0].Source != "go-work-e2e" || got.Diagnostics[0].Message != "multi-module diagnostic" {
		t.Fatalf("diagnostic = %#v, want go-work-e2e multi-module marker", got.Diagnostics[0])
	}
}

func assertGoWorkE2EResolvedScopes(t *testing.T, cases []struct {
	name  string
	scope lspmanager.ResolvedToolScope
	root  string
}) {
	t.Helper()
	for _, tc := range cases {
		assertGoWorkE2ERoots(t, tc.name, tc.scope, tc.root)
		assertGoWorkE2EWorkspaceKey(t, tc.name, tc.scope.WorkspaceKey, tc.root)
	}
}

func assertGoWorkE2ERoots(t *testing.T, name string, scope lspmanager.ResolvedToolScope, root string) {
	t.Helper()
	if scope.WorkspaceRoot != root || scope.ProjectRoot != root {
		t.Fatalf("worktree %s resolved roots = workspace:%q project:%q, want %q", name, scope.WorkspaceRoot, scope.ProjectRoot, root)
	}
}

func assertGoWorkE2EWorkspaceKey(t *testing.T, name, workspaceKey, root string) {
	t.Helper()
	for _, fragment := range []string{root, "moduleRootsHash=", "workspaceFoldersHash="} {
		if !strings.Contains(workspaceKey, fragment) {
			t.Fatalf("worktree %s WorkspaceKey %q missing %q", name, workspaceKey, fragment)
		}
	}
}

type goWorkE2EClientFactory struct {
	mu            sync.Mutex
	calls         []goWorkE2EFactoryCall
	diagnosticURI string
}

type goWorkE2EFactoryCall struct {
	rootDir string
	env     []string
}

func (f *goWorkE2EClientFactory) NewClient(rootDir string, handler protocol.NotificationHandler) (multilsp.Client, error) {
	return f.NewClientWithEnv(rootDir, nil, handler)
}

func (f *goWorkE2EClientFactory) NewClientWithEnv(rootDir string, env []string, handler protocol.NotificationHandler) (multilsp.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, goWorkE2EFactoryCall{
		rootDir: rootDir,
		env:     append([]string(nil), env...),
	})
	return &goWorkE2EClient{
		handler:       handler,
		diagnosticURI: f.diagnosticURI,
	}, nil
}

func (f *goWorkE2EClientFactory) callAt(t *testing.T, index int) goWorkE2EFactoryCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.calls) {
		t.Fatalf("factory call %d out of range, calls=%d", index, len(f.calls))
	}
	return f.calls[index]
}

type goWorkE2EClient struct {
	handler       protocol.NotificationHandler
	diagnosticURI string
}

func (c *goWorkE2EClient) Initialize(context.Context, string) error {
	if c.handler == nil || c.diagnosticURI == "" {
		return nil
	}
	return c.handler.PublishDiagnostics(protocol.PublishDiagnosticsParams{
		URI: c.diagnosticURI,
		Diagnostics: []protocol.Diagnostic{{
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 1},
			},
			Severity: protocol.SeverityWarning,
			Source:   "go-work-e2e",
			Message:  "multi-module diagnostic",
		}},
	})
}

func (*goWorkE2EClient) Shutdown(context.Context) error { return nil }

func (*goWorkE2EClient) Request(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage("null"), nil
}

func (*goWorkE2EClient) Notify(context.Context, string, any) error { return nil }

func (*goWorkE2EClient) DidOpen(context.Context, string, string, int, string) error {
	return nil
}

func (*goWorkE2EClient) DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error {
	return nil
}

func (*goWorkE2EClient) DidClose(context.Context, string) error { return nil }

func (*goWorkE2EClient) Close() error { return nil }

func normalizedGoWorkE2ETempDir(t *testing.T) string {
	t.Helper()
	dir, err := platformshared.NormalizeAbsolutePath(t.TempDir())
	if err != nil {
		t.Fatalf("normalize temp dir: %v", err)
	}
	return dir
}

func writeGoWorkE2EGoMod(t *testing.T, dir, module string) {
	t.Helper()
	writeGoWorkE2EFile(t, filepath.Join(dir, "go.mod"), "module "+module+"\n\ngo 1.25.0\n")
}

func writeGoWorkE2EGoFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	writeGoWorkE2EFile(t, path, "package main\n")
	return path
}

func writeGoWorkE2EFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func goWorkE2EFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}
