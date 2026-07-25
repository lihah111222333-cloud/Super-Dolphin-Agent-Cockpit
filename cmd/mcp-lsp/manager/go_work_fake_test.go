//go:build !lsp_integration

package manager_test

import (
	"context"
	"encoding/json"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

func TestGoWorkEnvPropagatedToGopls(t *testing.T) {
	t.Setenv("GOWORK", "")
	repo := normalizedGoWorkFakeTempDir(t)
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: repo, WorkspaceRoots: []string{repo}})
	backend := filepath.Join(repo, "backend")
	tools := filepath.Join(repo, "tools")
	writeGoWorkFakeGoMod(t, backend, "example.com/backend")
	writeGoWorkFakeGoMod(t, tools, "example.com/tools")
	goWorkPath := filepath.Join(repo, "go.work")
	writeGoWorkFakeFile(t, goWorkPath, "go 1.25.0\n\nuse (\n\t./backend\n\t./tools\n)\n")
	target := writeGoWorkFakeGoFile(t, backend, "main.go")
	factory := &goWorkFakeClientFactory{}
	manager := multilsp.NewManager(multilsp.Config{
		WorkspaceRoot:      repo,
		ClientFactory:      factory,
		DiagnosticsMaxWait: 1,
	})
	defer func() {
		if err := manager.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	if _, err := manager.EnsureClient(ctx, target, "go"); err != nil {
		t.Fatalf("ensure go.work client: %v", err)
	}
	call := factory.callAt(t, 0)
	if call.rootDir != repo {
		t.Fatalf("gopls rootDir = %q, want go.work root %q", call.rootDir, repo)
	}
	wantEnv := []string{"GOOS=" + runtime.GOOS, "GOARCH=" + runtime.GOARCH, "GOMEMLIMIT=384MiB", "GOWORK=" + goWorkPath}
	if !reflect.DeepEqual(call.env, wantEnv) {
		t.Fatalf("gopls env = %#v, want %#v", call.env, wantEnv)
	}
	client := factory.clientAt(t, 0)
	if client.rootURI != goWorkFakeFileURI(repo) {
		t.Fatalf("initialize rootURI = %q, want %q", client.rootURI, goWorkFakeFileURI(repo))
	}
}

func TestTwoWorktreesNoWorkspaceKeyCollision(t *testing.T) {
	t.Setenv("GOWORK", "")
	wtA := filepath.Join(normalizedGoWorkFakeTempDir(t), "wt-a")
	wtB := filepath.Join(normalizedGoWorkFakeTempDir(t), "wt-b")
	writeGoWorkFakeGoMod(t, wtA, "example.com/root")
	writeGoWorkFakeGoMod(t, wtB, "example.com/root")
	targetA := writeGoWorkFakeGoFile(t, wtA, "main.go")
	targetB := writeGoWorkFakeGoFile(t, wtB, "main.go")

	manager := multilsp.NewManager(multilsp.Config{WorkspaceRoot: wtA})
	defer func() {
		if err := manager.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()
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
	assertResolvedGoWorkFakeScope(t, "A", scopedA.ResolvedScope, wtA)
	assertResolvedGoWorkFakeScope(t, "B", scopedB.ResolvedScope, wtB)
}

func assertResolvedGoWorkFakeScope(t *testing.T, name string, scope lspmanager.ResolvedToolScope, root string) {
	t.Helper()
	if scope.WorkspaceRoot != root || scope.ProjectRoot != root {
		t.Fatalf("worktree %s resolved roots = workspace:%q project:%q, want %q", name, scope.WorkspaceRoot, scope.ProjectRoot, root)
	}
	for _, fragment := range []string{root, "moduleRootsHash=", "workspaceFoldersHash="} {
		if !strings.Contains(scope.WorkspaceKey, fragment) {
			t.Fatalf("worktree %s WorkspaceKey %q missing %q", name, scope.WorkspaceKey, fragment)
		}
	}
}

type goWorkFakeClientFactory struct {
	mu      sync.Mutex
	calls   []goWorkFakeFactoryCall
	clients []*goWorkFakeClient
}

type goWorkFakeFactoryCall struct {
	rootDir string
	env     []string
}

func (f *goWorkFakeClientFactory) NewClient(rootDir string, handler protocol.NotificationHandler) (multilsp.Client, error) {
	return f.NewClientWithEnv(rootDir, nil, handler)
}

func (f *goWorkFakeClientFactory) NewClientWithEnv(rootDir string, env []string, _ protocol.NotificationHandler) (multilsp.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	client := &goWorkFakeClient{}
	f.calls = append(f.calls, goWorkFakeFactoryCall{
		rootDir: rootDir,
		env:     append([]string(nil), env...),
	})
	f.clients = append(f.clients, client)
	return client, nil
}

func (f *goWorkFakeClientFactory) callAt(t *testing.T, index int) goWorkFakeFactoryCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.calls) {
		t.Fatalf("factory call %d out of range, calls=%d", index, len(f.calls))
	}
	return f.calls[index]
}

func (f *goWorkFakeClientFactory) clientAt(t *testing.T, index int) *goWorkFakeClient {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.clients) {
		t.Fatalf("factory client %d out of range, clients=%d", index, len(f.clients))
	}
	return f.clients[index]
}

type goWorkFakeClient struct {
	mu      sync.Mutex
	rootURI string
}

func (c *goWorkFakeClient) Initialize(_ context.Context, rootURI string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rootURI = rootURI
	return nil
}

func (*goWorkFakeClient) Shutdown(context.Context) error { return nil }

func (*goWorkFakeClient) Request(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage("null"), nil
}

func (*goWorkFakeClient) Notify(context.Context, string, any) error { return nil }

func (*goWorkFakeClient) DidOpen(context.Context, string, string, int, string) error {
	return nil
}

func (*goWorkFakeClient) DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error {
	return nil
}

func (*goWorkFakeClient) DidClose(context.Context, string) error { return nil }

func (*goWorkFakeClient) Close() error { return nil }

func normalizedGoWorkFakeTempDir(t *testing.T) string {
	t.Helper()
	dir, err := platformshared.NormalizeAbsolutePath(t.TempDir())
	if err != nil {
		t.Fatalf("normalize temp dir: %v", err)
	}
	return dir
}

func writeGoWorkFakeGoMod(t *testing.T, dir, module string) {
	t.Helper()
	writeGoWorkFakeFile(t, filepath.Join(dir, "go.mod"), "module "+module+"\n\ngo 1.25.0\n")
}

func writeGoWorkFakeGoFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	writeGoWorkFakeFile(t, path, "package main\n")
	return path
}

func writeGoWorkFakeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func goWorkFakeFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}
