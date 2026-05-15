package multilsp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestGoWorkWorkspaceFolderInitializeAndEnv(t *testing.T) {
	t.Setenv("GOWORK", "")
	ctx := context.Background()
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	tools := filepath.Join(repo, "tools")
	writeGoMod(t, backend, "example.com/backend")
	writeGoMod(t, tools, "example.com/tools")
	writeFile(t, filepath.Join(repo, "go.work"), "go 1.25.0\n\nuse (\n\t./backend\n\t./tools\n)\n")
	target := writeGoFile(t, backend, "main.go")
	factory := &goWorkspaceClientFactory{}
	manager := NewManager(Config{
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
		t.Fatalf("ensure go client: %v", err)
	}

	call := factory.callAt(t, 0)
	if call.rootDir != repo {
		t.Fatalf("gopls rootDir = %q, want %q", call.rootDir, repo)
	}
	if !reflect.DeepEqual(call.env, []string{"GOWORK=" + filepath.Join(repo, "go.work")}) {
		t.Fatalf("gopls env = %#v", call.env)
	}
	client := factory.clientAt(t, 0)
	if client.rootURI != fileURIFromPath(repo) {
		t.Fatalf("initialize rootURI = %q, want %q", client.rootURI, fileURIFromPath(repo))
	}
	assertFolderURIs(t, client.initializedFolders, []string{repo, backend, tools})
	if caps := clientCapabilities(); caps.Workspace == nil || !caps.Workspace.WorkspaceFolders {
		t.Fatalf("client capabilities should enable workspaceFolders: %#v", caps.Workspace)
	}
}

func TestGoWorkWorkspaceKeyChangesWhenUseListChanges(t *testing.T) {
	t.Setenv("GOWORK", "")
	ctx := context.Background()
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	tools := filepath.Join(repo, "tools")
	writeGoMod(t, backend, "example.com/backend")
	writeGoMod(t, tools, "example.com/tools")
	goWorkPath := filepath.Join(repo, "go.work")
	writeFile(t, goWorkPath, "go 1.25.0\n\nuse ./backend\n")
	target := writeGoFile(t, backend, "main.go")
	factory := &goWorkspaceClientFactory{}
	manager := NewManager(Config{
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
		t.Fatalf("first ensure go client: %v", err)
	}
	writeFile(t, goWorkPath, "go 1.25.0\n\nuse (\n\t./backend\n\t./tools\n)\n")
	if _, err := manager.EnsureClient(ctx, target, "go"); err != nil {
		t.Fatalf("second ensure go client: %v", err)
	}
	if got := factory.callCount(); got != 2 {
		t.Fatalf("go.work use-list topology change should create a new client, calls=%d", got)
	}
	assertFolderURIs(t, factory.clientAt(t, 1).initializedFolders, []string{repo, backend, tools})
}

func TestWorkspaceFolderLanguageOnlySingleSubmodule(t *testing.T) {
	t.Setenv("GOWORK", "")
	ctx := context.Background()
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	writeGoMod(t, backend, "example.com/backend")
	factory := &goWorkspaceClientFactory{}
	manager := NewManager(Config{
		WorkspaceRoot:      repo,
		ClientFactory:      factory,
		DiagnosticsMaxWait: 1,
	})
	defer func() {
		if err := manager.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	if _, err := manager.EnsureClient(ctx, "", "go"); err != nil {
		t.Fatalf("ensure language-only go client: %v", err)
	}

	call := factory.callAt(t, 0)
	if call.rootDir != backend {
		t.Fatalf("single submodule language-only rootDir = %q, want %q", call.rootDir, backend)
	}
	assertFolderURIs(t, factory.clientAt(t, 0).initializedFolders, []string{backend})
}

func TestGOWORKOffManagerEnvIgnoresGoWork(t *testing.T) {
	t.Setenv("GOWORK", "off")
	ctx := context.Background()
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	writeGoMod(t, backend, "example.com/backend")
	writeFile(t, filepath.Join(repo, "go.work"), "go 1.25.0\n\nuse ./backend\n")
	target := writeGoFile(t, backend, "main.go")
	factory := &goWorkspaceClientFactory{}
	manager := NewManager(Config{
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
		t.Fatalf("ensure GOWORK=off go client: %v", err)
	}

	call := factory.callAt(t, 0)
	if call.rootDir != backend {
		t.Fatalf("GOWORK=off should initialize at module root %q, got %q", backend, call.rootDir)
	}
	if !reflect.DeepEqual(call.env, []string{"GOWORK=off"}) {
		t.Fatalf("GOWORK=off client env = %#v", call.env)
	}
	assertFolderURIs(t, factory.clientAt(t, 0).initializedFolders, []string{backend})
}

func TestGOWORKDoesNotAffectNonGoLanguageAdapters(t *testing.T) {
	for _, tc := range nonGoGOWORKPollutionCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			assertGOWORKDoesNotAffectWorkspace(t, tc)
		})
	}
}

func TestGOWORKDoesNotAffectJSTSWorkspaceRoot(t *testing.T) {
	assertGOWORKDoesNotAffectWorkspace(t, nonGoGOWORKPollutionCase(t, "javascript"))
	assertGOWORKDoesNotAffectWorkspace(t, nonGoGOWORKPollutionCase(t, "typescript"))
}

func TestGOWORKDoesNotAffectJavaWorkspaceRoot(t *testing.T) {
	assertGOWORKDoesNotAffectWorkspace(t, nonGoGOWORKPollutionCase(t, "java"))
}

func TestGOWORKDoesNotAffectPythonWorkspaceRoot(t *testing.T) {
	assertGOWORKDoesNotAffectWorkspace(t, nonGoGOWORKPollutionCase(t, "python"))
}

func TestGOWORKDoesNotAffectRustWorkspaceRoot(t *testing.T) {
	assertGOWORKDoesNotAffectWorkspace(t, nonGoGOWORKPollutionCase(t, "rust"))
}

func TestGOWORKDoesNotAffectCSSWorkspaceRoot(t *testing.T) {
	assertGOWORKDoesNotAffectWorkspace(t, nonGoGOWORKPollutionCase(t, "css"))
}

func TestGOWORKDoesNotAffectJSONYAMLMarkdownFallback(t *testing.T) {
	assertGOWORKDoesNotAffectWorkspace(t, nonGoGOWORKPollutionCase(t, "json"))
	assertGOWORKDoesNotAffectWorkspace(t, nonGoGOWORKPollutionCase(t, "yaml"))
	assertGOWORKDoesNotAffectWorkspace(t, nonGoGOWORKPollutionCase(t, "markdown"))
}

func TestEmptyLanguageIDDoesNotDefaultToGoAdapter(t *testing.T) {
	tc := nonGoGOWORKPollutionCase(t, "javascript")
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

	ref, err := manager.resolveDocumentRef(context.Background(), tc.target, "")
	if err != nil {
		t.Fatalf("resolve empty-language JS document ref: %v", err)
	}
	if ref.languageID != "javascript" {
		t.Fatalf("empty language id should be inferred from JS target; got %q", ref.languageID)
	}
	cfg, err := manager.resolveWorkspaceForDocument(context.Background(), ref)
	if err != nil {
		t.Fatalf("resolve workspace with empty language id for JS target: %v", err)
	}
	if cfg.languageID != "javascript" {
		t.Fatalf("workspace config should use inferred JS language id; got %q", cfg.languageID)
	}
	if cfg.rootPath != tc.wantRoot {
		t.Fatalf("empty language id JS target root = %q, want %q", cfg.rootPath, tc.wantRoot)
	}
	if len(cfg.env) != 0 {
		t.Fatalf("empty language id JS target should not inherit GOWORK env: %#v", cfg.env)
	}
}

func TestGoLanguageSpecificHashNotAddedToNonGoCacheKey(t *testing.T) {
	repo := normalizedTempDir(t)
	target := filepath.Join(repo, "web", "src", "app.ts")
	writeFile(t, target, "export const app = 1\n")

	resolved, err := ResolveLSPToolScope(LSPToolScope{
		Family:                defaultLSPToolFamily,
		LanguageID:            "typescript",
		CWD:                   repo,
		TargetPath:            target,
		WorkspaceRoot:         filepath.Join(repo, "web"),
		LanguageWorkspaceRoot: filepath.Join(repo, "web"),
		ProjectRoot:           repo,
		RootKind:              "dir_fallback",
	})
	if err != nil {
		t.Fatalf("resolve non-Go LSP tool scope: %v", err)
	}
	if len(resolved.LanguageSpecific) != 0 {
		t.Fatalf("non-Go resolved scope should not have Go language-specific values: %#v", resolved.LanguageSpecific)
	}
	for _, goOnly := range []string{"goWorkPath=", "goModPath=", "goworkMode=", "moduleRootsHash=", "workspaceFoldersHash="} {
		if strings.Contains(resolved.WorkspaceKey, goOnly) {
			t.Fatalf("non-Go workspace key %q contains Go-only fragment %q", resolved.WorkspaceKey, goOnly)
		}
	}
}

func TestRecyclerRestoresGoWorkspaceWithSavedRootEnvAndScope(t *testing.T) {
	t.Setenv("GOWORK", "")
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		Family:   defaultLSPToolFamily,
		AgentID:  "agent-worker-d",
		ThreadID: "thread-go",
	})
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	tools := filepath.Join(repo, "tools")
	writeGoMod(t, backend, "example.com/backend")
	writeGoMod(t, tools, "example.com/tools")
	goWorkPath := filepath.Join(repo, "go.work")
	writeFile(t, goWorkPath, "go 1.25.0\n\nuse (\n\t./backend\n\t./tools\n)\n")
	target := writeGoFile(t, backend, "main.go")
	factory := &goWorkspaceClientFactory{}
	manager := NewManager(Config{
		WorkspaceRoot:      repo,
		ClientFactory:      factory,
		DiagnosticsMaxWait: 1,
	}).(*manager)
	defer func() {
		if err := manager.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	if _, err := manager.EnsureClient(ctx, target, "go"); err != nil {
		t.Fatalf("ensure go client before recycle: %v", err)
	}
	workspaces := snapshotWorkspaceClients(manager)
	if len(workspaces) != 1 {
		t.Fatalf("workspace snapshot count = %d, want 1", len(workspaces))
	}
	workspace := workspaces[0]
	cfg := workspaceConfig{
		key:              workspace.key,
		rootPath:         workspace.rootPath,
		rootURI:          workspace.rootURI,
		languageID:       workspace.languageID,
		env:              append([]string(nil), workspace.env...),
		workspaceFolders: cloneWorkspaceFolders(workspace.workspaceFolders),
	}
	resolved, err := manager.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("resolve scope for recycle: %v", err)
	}
	fallbackCtx := recycleRestoreContext(ResolvedLSPToolScope{}, cfg)
	fallbackResolved, ok := resolvedLSPToolScopeFromContext(fallbackCtx)
	if !ok {
		t.Fatalf("recycle restore ctx without stored scope did not rebuild from saved workspace config")
	}
	if fallbackResolved.WorkspaceKey != resolved.WorkspaceKey {
		t.Fatalf("fallback recycle workspace key = %q, want %q", fallbackResolved.WorkspaceKey, resolved.WorkspaceKey)
	}
	if !reflect.DeepEqual(fallbackResolved.LanguageSpecific, resolved.LanguageSpecific) {
		t.Fatalf("fallback recycle language-specific = %#v, want %#v", fallbackResolved.LanguageSpecific, resolved.LanguageSpecific)
	}
	targetURI := fileURIFromPath(target)
	coordinator := bootstrapCoordinatorFor(manager)
	coordinator.cache.Upsert(lspCacheValue{
		Key:         resolved.cacheKey("go", targetURI),
		Version:     1,
		Fingerprint: "stale-before-recycle",
	})
	didRecycle, err := recycleWorkspaceClient(manager, resolved, workspace)
	if err != nil {
		t.Fatalf("recycle go workspace client: %v", err)
	}
	if !didRecycle {
		t.Fatalf("recycle go workspace client: recycled=false, want true")
	}

	if got := factory.callCount(); got != 2 {
		t.Fatalf("recycle should create replacement client, calls=%d", got)
	}
	call := factory.callAt(t, 1)
	if call.rootDir != repo {
		t.Fatalf("recycled gopls rootDir = %q, want %q", call.rootDir, repo)
	}
	if !reflect.DeepEqual(call.env, []string{"GOWORK=" + goWorkPath}) {
		t.Fatalf("recycled gopls env = %#v", call.env)
	}
	recycledClient := factory.clientAt(t, 1)
	assertFolderURIs(t, recycledClient.initializedFolders, []string{repo, backend, tools})
	if !recycledClient.initResolvedOK {
		t.Fatalf("recycled Initialize ctx missing canonical ResolvedLSPToolScope")
	}
	if recycledClient.initScopeKey != resolved.ScopeKey {
		t.Fatalf("recycled init scope key = %q, want %q", recycledClient.initScopeKey, resolved.ScopeKey)
	}
	if recycledClient.initWorkspaceKey != resolved.WorkspaceKey {
		t.Fatalf("recycled init workspace key = %q, want %q", recycledClient.initWorkspaceKey, resolved.WorkspaceKey)
	}
	if recycledClient.initManagerKey != resolved.ManagerKey {
		t.Fatalf("recycled init manager key = %q, want %q", recycledClient.initManagerKey, resolved.ManagerKey)
	}
	if !reflect.DeepEqual(recycledClient.initLanguageSpecific, resolved.LanguageSpecific) {
		t.Fatalf("recycled init language-specific = %#v, want %#v", recycledClient.initLanguageSpecific, resolved.LanguageSpecific)
	}
	if !recycledClient.initToolScopeOK {
		t.Fatalf("recycled Initialize ctx missing common ToolScope compatibility")
	}
	if recycledClient.initToolScope.AgentID != "agent-worker-d" || recycledClient.initToolScope.ThreadID != "thread-go" {
		t.Fatalf("recycled common ToolScope = %#v, want stored agent/thread", recycledClient.initToolScope)
	}
	if status := coordinator.states.status(resolved.bootstrapKey(), targetURI); status != bootstrapReady {
		t.Fatalf("bootstrap status for canonical scope = %s, want %s", status, bootstrapReady)
	}
}

func TestRecyclerDoesNotRecycleActiveLease(t *testing.T) {
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		Family:   defaultLSPToolFamily,
		AgentID:  "agent-lease",
		ThreadID: "thread-lease",
	})
	root := normalizedTempDir(t)
	writeGoMod(t, root, "example.com/lease")
	target := writeGoFile(t, root, "main.go")
	factory := &goWorkspaceClientFactory{}
	manager := NewManager(Config{
		WorkspaceRoot:      root,
		ClientFactory:      factory,
		DiagnosticsMaxWait: 1,
	}).(*manager)
	defer func() {
		if err := manager.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	client, ref, err := manager.documentClient(ctx, fileURIFromPath(target))
	if err != nil {
		t.Fatalf("lease document client: %v", err)
	}
	if client == nil {
		t.Fatalf("leased document client for %s is nil", ref.uri)
	}
	original := factory.clientAt(t, 0)
	if client != original {
		t.Fatalf("leased client = %p, want original client %p", client, original)
	}

	workspaces := snapshotWorkspaceClients(manager)
	if len(workspaces) != 1 {
		t.Fatalf("workspace snapshot count = %d, want 1", len(workspaces))
	}
	workspace := workspaces[0]
	cfg := workspaceConfig{
		key:              workspace.key,
		rootPath:         workspace.rootPath,
		rootURI:          workspace.rootURI,
		languageID:       workspace.languageID,
		env:              append([]string(nil), workspace.env...),
		workspaceFolders: cloneWorkspaceFolders(workspace.workspaceFolders),
	}
	resolved, err := manager.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("resolve scope for active-lease recycle: %v", err)
	}

	if err := manager.withPooledClient(client, func() error {
		didRecycleActive, err := recycleWorkspaceClient(manager, resolved, workspace)
		if err != nil {
			t.Fatalf("recycle with active lease: %v", err)
		}
		if didRecycleActive {
			t.Fatalf("recycle with active lease recycled client; want skip")
		}
		if original.isClosed() {
			t.Fatalf("active lease client was closed by recycler")
		}
		if got := factory.callCount(); got != 1 {
			t.Fatalf("active lease should not create replacement client, calls=%d", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("hold active lease: %v", err)
	}

	didRecycleAfterRelease, err := recycleWorkspaceClient(manager, resolved, workspace)
	if err != nil {
		t.Fatalf("recycle after lease release: %v", err)
	}
	if !didRecycleAfterRelease {
		t.Fatalf("recycle after lease release recycled=false, want true")
	}
	if !original.isClosed() {
		t.Fatalf("released original client was not closed by recycler")
	}
	if got := factory.callCount(); got != 2 {
		t.Fatalf("recycle after lease release should create replacement client, calls=%d", got)
	}
}

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

	cfg, err := manager.resolveWorkspaceForDocument(context.Background(), documentRef{
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
