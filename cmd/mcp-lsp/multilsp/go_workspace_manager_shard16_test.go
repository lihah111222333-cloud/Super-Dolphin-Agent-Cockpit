package multilsp

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
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
	mu                            sync.Mutex
	calls                         []goWorkspaceFactoryCall
	clients                       []*goWorkspaceClient
	workspaceSymbolURIs           []string
	workspaceSymbolsFromDocuments bool
	workspaceSymbolDiskFiles      []string
}

type goWorkspaceFactoryCall struct {
	rootDir string
	env     []string
}

func (f *goWorkspaceClientFactory) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	return f.NewClientWithEnv(rootDir, nil, handler)
}

func (f *goWorkspaceClientFactory) NewClientWithEnv(rootDir string, env []string, handler protocol.NotificationHandler) (Client, error) {
	return f.NewClientWithOptions(rootDir, env, nil, handler)
}

func (f *goWorkspaceClientFactory) NewClientWithOptions(rootDir string, env []string, _ map[string]any, _ protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	client := &goWorkspaceClient{
		workspaceSymbolURIs:           append([]string(nil), f.workspaceSymbolURIs...),
		workspaceSymbolsFromDocuments: f.workspaceSymbolsFromDocuments,
		workspaceSymbolDiskFiles:      append([]string(nil), f.workspaceSymbolDiskFiles...),
		documents:                     make(map[string]string),
		versions:                      make(map[string]int),
	}
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
	mu                            sync.Mutex
	rootURI                       string
	workspaceFolders              []protocol.WorkspaceFolder
	initializedFolders            []protocol.WorkspaceFolder
	initScopeKey                  string
	initWorkspaceKey              string
	initManagerKey                string
	initLanguageSpecific          map[string]string
	initResolvedOK                bool
	initToolScope                 common.ToolScope
	initToolScopeOK               bool
	workspaceSymbolURIs           []string
	workspaceSymbolsFromDocuments bool
	workspaceSymbolDiskFiles      []string
	documents                     map[string]string
	versions                      map[string]int
	didOpenCount                  int
	didChangeCount                int
	didCloseCount                 int
	closed                        bool
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

func (c *goWorkspaceClient) Request(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method != protocol.MethodWorkspaceSymbol {
		return json.RawMessage("null"), nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.workspaceSymbolsFromDocuments {
		return c.workspaceDocumentSymbolsLocked()
	}
	symbols := make([]protocol.SymbolInformation, 0, len(c.workspaceSymbolURIs))
	for index, uri := range c.workspaceSymbolURIs {
		symbols = append(symbols, protocol.SymbolInformation{
			Name: fmt.Sprintf("workspaceSymbol%d", index), Kind: protocol.SymbolKindFunction,
			Location: protocol.Location{URI: uri},
		})
	}
	return json.Marshal(symbols)
}

func (c *goWorkspaceClient) workspaceDocumentSymbolsLocked() (json.RawMessage, error) {
	documents := make(map[string]string, len(c.documents)+len(c.workspaceSymbolDiskFiles))
	maps.Copy(documents, c.documents)
	for _, path := range c.workspaceSymbolDiskFiles {
		uri := fileURIFromPath(path)
		if _, opened := documents[uri]; opened {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		documents[uri] = string(data)
	}
	uris := make([]string, 0, len(documents))
	for uri := range documents {
		uris = append(uris, uri)
	}
	sort.Strings(uris)
	symbols := make([]protocol.SymbolInformation, 0, len(uris))
	for _, uri := range uris {
		symbols = append(symbols, protocol.SymbolInformation{
			Name: goWorkspaceFunctionName(documents[uri]), Kind: protocol.SymbolKindFunction,
			Location: protocol.Location{URI: uri},
		})
	}
	return json.Marshal(symbols)
}

func goWorkspaceFunctionName(text string) string {
	fields := strings.Fields(text)
	for index, field := range fields {
		if field == "func" && index+1 < len(fields) {
			return strings.TrimSuffix(strings.SplitN(fields[index+1], "(", 2)[0], "{")
		}
	}
	return "workspaceSymbol"
}

func (c *goWorkspaceClient) Notify(context.Context, string, any) error { return nil }

func (c *goWorkspaceClient) DidOpen(_ context.Context, uri, _ string, version int, text string) error {
	if !c.workspaceSymbolsFromDocuments {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.documents[uri]; exists {
		return fmt.Errorf("duplicate DidOpen for %s", uri)
	}
	c.documents[uri] = text
	c.versions[uri] = version
	c.didOpenCount++
	return nil
}

func (c *goWorkspaceClient) DidChange(_ context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error {
	if !c.workspaceSymbolsFromDocuments {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current, exists := c.versions[uri]
	if !exists {
		return fmt.Errorf("DidChange before DidOpen for %s", uri)
	}
	if version <= current {
		return fmt.Errorf("non-monotonic DidChange version %d after %d for %s", version, current, uri)
	}
	if len(changes) != 1 || changes[0].Range != nil {
		return fmt.Errorf("go workspace fake requires one full-document change for %s", uri)
	}
	c.documents[uri] = changes[0].Text
	c.versions[uri] = version
	c.didChangeCount++
	return nil
}

func (c *goWorkspaceClient) DidClose(_ context.Context, uri string) error {
	if !c.workspaceSymbolsFromDocuments {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.documents[uri]; !exists {
		return fmt.Errorf("duplicate DidClose for %s", uri)
	}
	delete(c.documents, uri)
	delete(c.versions, uri)
	c.didCloseCount++
	return nil
}

func (c *goWorkspaceClient) documentNotificationCounts() (int, int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.didOpenCount, c.didChangeCount, c.didCloseCount
}

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

func goWorkspaceSymbolResultURI(result protocol.WorkspaceSymbolResult) (string, error) {
	if result.SymbolInformation != nil {
		return result.SymbolInformation.Location.URI, nil
	}
	if result.WorkspaceSymbol == nil {
		return "", fmt.Errorf("workspace symbol result has no union member")
	}
	payload, err := json.Marshal(result.WorkspaceSymbol.Location)
	if err != nil {
		return "", err
	}
	var location protocol.WorkspaceSymbolLocation
	if err := json.Unmarshal(payload, &location); err != nil {
		return "", err
	}
	return location.URI, nil
}
