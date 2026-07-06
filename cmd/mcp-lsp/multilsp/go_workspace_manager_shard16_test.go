package multilsp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

type nonGoGOWORKPollutionTestCase struct {
	name           string
	languageID     string
	repo           string
	target         string
	wantRoot       string
	externalGoWork string
}

func nonGoGOWORKPollutionCases(t *testing.T) []nonGoGOWORKPollutionTestCase {
	t.Helper()
	languages := []string{"javascript", "typescript", "java", "python", "rust", "css", "json", "yaml", "markdown"}
	cases := make([]nonGoGOWORKPollutionTestCase, 0, len(languages))
	for _, lang := range languages {
		cases = append(cases, nonGoGOWORKPollutionCase(t, lang))
	}
	return cases
}

func nonGoGOWORKPollutionCase(t *testing.T, languageID string) nonGoGOWORKPollutionTestCase {
	t.Helper()
	repo := normalizedTempDir(t)
	external := normalizedTempDir(t)
	externalModule := filepath.Join(external, "external")
	writeGoMod(t, externalModule, "example.com/external")
	externalGoWork := filepath.Join(external, "go.work")
	writeFile(t, externalGoWork, "go 1.25.0\n\nuse ./external\n")

	targetRoot := repo
	var target string
	switch languageID {
	case "javascript":
		targetRoot = filepath.Join(repo, "web-js")
		writeFile(t, filepath.Join(targetRoot, "package.json"), `{"type":"module"}`+"\n")
		target = filepath.Join(targetRoot, "src", "app.js")
		writeFile(t, target, "export const app = 1\n")
	case "typescript":
		targetRoot = filepath.Join(repo, "web-ts")
		writeFile(t, filepath.Join(targetRoot, "tsconfig.json"), `{"compilerOptions":{}}`+"\n")
		target = filepath.Join(targetRoot, "src", "app.ts")
		writeFile(t, target, "export const app = 1\n")
	case "java":
		targetRoot = filepath.Join(repo, "java-app")
		writeFile(t, filepath.Join(targetRoot, "pom.xml"), "<project></project>\n")
		target = filepath.Join(targetRoot, "src", "main", "java", "App.java")
		writeFile(t, target, "class App {}\n")
	case "python":
		target = filepath.Join(repo, "py", "app.py")
		writeFile(t, target, "print('ok')\n")
	case "rust":
		target = filepath.Join(repo, "rust", "src", "main.rs")
		writeFile(t, target, "fn main() {}\n")
	case "css":
		target = filepath.Join(repo, "assets", "style.css")
		writeFile(t, target, "body { color: black; }\n")
	case "json":
		target = filepath.Join(repo, "config", "settings.json")
		writeFile(t, target, "{}\n")
	case "yaml":
		target = filepath.Join(repo, "config", "settings.yaml")
		writeFile(t, target, "key: value\n")
	case "markdown":
		targetRoot = filepath.Join(repo, "docs")
		target = filepath.Join(repo, "docs", "readme.md")
		writeFile(t, target, "# readme\n")
	default:
		t.Fatalf("unsupported non-Go test language %q", languageID)
	}

	return nonGoGOWORKPollutionTestCase{
		name:           languageID,
		languageID:     languageID,
		repo:           repo,
		target:         target,
		wantRoot:       targetRoot,
		externalGoWork: externalGoWork,
	}
}

func assertGOWORKDoesNotAffectWorkspace(t *testing.T, tc nonGoGOWORKPollutionTestCase) {
	t.Helper()
	t.Setenv("GOWORK", tc.externalGoWork)
	manager := NewManager(Config{
		WorkspaceRoot:      tc.repo,
		ClientFactory:      &goWorkspaceClientFactory{},
		DiagnosticsMaxWait: 1,
	}).(*manager)
	defer func() {
		if err := manager.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	cfg, err := manager.resolveWorkspaceForDocument(ctxWithCWD(tc.repo, "agent-"+tc.languageID, "thread-nongo"), documentRef{
		raw:        tc.target,
		uri:        fileURIFromPath(tc.target),
		absPath:    tc.target,
		languageID: tc.languageID,
	})
	if err != nil {
		t.Fatalf("%s workspace should ignore ambient GOWORK: %v", tc.languageID, err)
	}
	if cfg.rootPath != tc.wantRoot {
		t.Fatalf("%s workspace root = %q, want %q", tc.languageID, cfg.rootPath, tc.wantRoot)
	}
	if len(cfg.env) != 0 {
		t.Fatalf("%s workspace should not receive GOWORK env: %#v", tc.languageID, cfg.env)
	}
	if cfg.languageID != tc.languageID {
		t.Fatalf("%s workspace language id = %q", tc.languageID, cfg.languageID)
	}
}

type goWorkspaceClientFactory struct {
	mu      sync.Mutex
	calls   []goWorkspaceFactoryCall
	clients []*goWorkspaceClient
}

type goWorkspaceFactoryCall struct {
	rootDir string
	env     []string
}

func (f *goWorkspaceClientFactory) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	return f.NewClientWithEnv(rootDir, nil, handler)
}

func (f *goWorkspaceClientFactory) NewClientWithEnv(rootDir string, env []string, _ protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	client := &goWorkspaceClient{}
	f.calls = append(f.calls, goWorkspaceFactoryCall{
		rootDir: rootDir,
		env:     append([]string(nil), env...),
	})
	f.clients = append(f.clients, client)
	return client, nil
}

func (f *goWorkspaceClientFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *goWorkspaceClientFactory) callAt(t *testing.T, index int) goWorkspaceFactoryCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.calls) {
		t.Fatalf("factory call %d out of range, calls=%d", index, len(f.calls))
	}
	return f.calls[index]
}

func (f *goWorkspaceClientFactory) clientAt(t *testing.T, index int) *goWorkspaceClient {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.clients) {
		t.Fatalf("factory client %d out of range, clients=%d", index, len(f.clients))
	}
	return f.clients[index]
}

type goWorkspaceClient struct {
	mu                   sync.Mutex
	rootURI              string
	workspaceFolders     []protocol.WorkspaceFolder
	initializedFolders   []protocol.WorkspaceFolder
	initScopeKey         string
	initWorkspaceKey     string
	initManagerKey       string
	initLanguageSpecific map[string]string
	initResolvedOK       bool
	initToolScope        common.ToolScope
	initToolScopeOK      bool
	closed               bool
}

func (c *goWorkspaceClient) setWorkspaceFolders(folders []protocol.WorkspaceFolder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.workspaceFolders = cloneWorkspaceFolders(folders)
}

func (c *goWorkspaceClient) Initialize(ctx context.Context, rootURI string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rootURI = rootURI
	c.initializedFolders = cloneWorkspaceFolders(c.workspaceFolders)
	c.initScopeKey = lspScopeKeyFromContext(ctx)
	if resolved, ok := resolvedLSPToolScopeFromContext(ctx); ok {
		c.initResolvedOK = true
		c.initWorkspaceKey = resolved.WorkspaceKey
		c.initManagerKey = resolved.ManagerKey
		c.initLanguageSpecific = copyLanguageSpecific(resolved.LanguageSpecific)
	}
	if toolScope, ok := common.ToolScopeFromContext(ctx); ok {
		c.initToolScopeOK = true
		c.initToolScope = toolScope
	}
	return nil
}

func (c *goWorkspaceClient) Shutdown(context.Context) error { return nil }

func (c *goWorkspaceClient) Request(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage("null"), nil
}

func (c *goWorkspaceClient) Notify(context.Context, string, any) error { return nil }

func (c *goWorkspaceClient) DidOpen(context.Context, string, string, int, string) error {
	return nil
}

func (c *goWorkspaceClient) DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error {
	return nil
}

func (c *goWorkspaceClient) DidClose(context.Context, string) error { return nil }

func (c *goWorkspaceClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *goWorkspaceClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func assertFolderURIs(t *testing.T, folders []protocol.WorkspaceFolder, paths []string) {
	t.Helper()
	if len(folders) != len(paths) {
		t.Fatalf("workspace folders length = %d, want %d: %#v", len(folders), len(paths), folders)
	}
	for i, path := range paths {
		want := fileURIFromPath(path)
		if folders[i].URI != want {
			t.Fatalf("workspace folder %d URI = %q, want %q; folders=%#v", i, folders[i].URI, want, folders)
		}
	}
}
