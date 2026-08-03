package multilsp

import (
	"context"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGoWorkWorkspaceFolderInitializeAndEnv(t *testing.T) {
	t.Setenv("GOWORK", "")
	repo := normalizedTempDir(t)
	ctx := ctxWithCWD(repo, "agent-go-work", "thread-go-work")
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
	if !reflect.DeepEqual(call.env, hostGoEnv("GOWORK="+filepath.Join(repo, "go.work"))) {
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
	repo := normalizedTempDir(t)
	ctx := ctxWithCWD(repo, "agent-go-work-key", "thread-go-work-key")
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
	repo := normalizedTempDir(t)
	ctx := ctxWithCWD(repo, "agent-go-language", "thread-go-language")
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
	repo := normalizedTempDir(t)
	ctx := ctxWithCWD(repo, "agent-gowork-off", "thread-gowork-off")
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
	if !reflect.DeepEqual(call.env, hostGoEnv("GOWORK=off")) {
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

func TestGOWORKDoesNotAffectDocumentLanguageServices(t *testing.T) {
	assertGOWORKDoesNotAffectWorkspace(t, nonGoGOWORKPollutionCase(t, "json"))
	assertGOWORKDoesNotAffectWorkspace(t, nonGoGOWORKPollutionCase(t, "yaml"))
	assertGOWORKDoesNotAffectWorkspace(t, nonGoGOWORKPollutionCase(t, "markdown"))
}

func TestEmptyLanguageIDDoesNotDefaultToGoAdapter(t *testing.T) {
	tc := nonGoGOWORKPollutionCase(t, "javascript")
	t.Setenv("GOWORK", tc.externalGoWork)
	ctx := ctxWithCWD(tc.repo, "agent-empty-language", "thread-empty-language")
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

	ref, err := manager.resolveDocumentRef(ctx, tc.target, "")
	if err != nil {
		t.Fatalf("resolve empty-language JS document ref: %v", err)
	}
	if ref.languageID != "javascript" {
		t.Fatalf("empty language id should be inferred from JS target; got %q", ref.languageID)
	}
	cfg, err := manager.resolveWorkspaceForDocument(ctx, ref)
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
	repo := normalizedTempDir(t)
	toolScope := common.ToolScope{
		Family:   defaultLSPToolFamily,
		AgentID:  "agent-worker-d",
		ThreadID: "thread-go",
		CWD:      repo,
	}
	ctx := common.WithToolScope(context.Background(), toolScope)
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
	workspaceSnapshot := snapshotWorkspaceClients(manager)
	if len(workspaceSnapshot) != 1 {
		t.Fatalf("workspace snapshot count = %d, want 1", len(workspaceSnapshot))
	}
	ageWorkspaceForLifecycleTest(t, manager, workspaceSnapshot[0].client)
	workspace, cfg := singleWorkspaceConfig(t, manager)
	resolved := mustResolveWorkspaceScope(t, manager, ctx, cfg, "recycle")
	assertFallbackRecycleScope(t, cfg, resolved)
	targetURI := fileURIFromPath(target)
	coordinator := seedBootstrapCache(t, manager, resolved, targetURI)
	didRecycle, err := recycleWorkspaceClient(manager, resolved, workspace)
	if err != nil {
		t.Fatalf("recycle go workspace client: %v", err)
	}
	if !didRecycle {
		t.Fatalf("recycle go workspace client: recycled=false, want true")
	}

	recycledClient := assertRecycledGoWorkspaceReplacement(t, factory, repo, goWorkPath, backend, tools)
	assertRecycledInitializeScope(t, recycledClient, resolved)
	assertRecycledInitializeToolScope(t, recycledClient, toolScope)
	assertBootstrapReady(t, coordinator, resolved, targetURI)
}

func singleWorkspaceConfig(t *testing.T, manager *manager) (workspaceClient, workspaceConfig) {
	t.Helper()
	workspaces := snapshotWorkspaceClients(manager)
	if len(workspaces) != 1 {
		t.Fatalf("workspace snapshot count = %d, want 1", len(workspaces))
	}
	workspace := workspaces[0]
	return workspace, workspaceConfig{
		key:              workspace.key,
		rootPath:         workspace.rootPath,
		rootURI:          workspace.rootURI,
		languageID:       workspace.languageID,
		env:              append([]string(nil), workspace.env...),
		workspaceFolders: cloneWorkspaceFolders(workspace.workspaceFolders),
	}
}

func mustResolveWorkspaceScope(t *testing.T, manager *manager, ctx context.Context, cfg workspaceConfig, purpose string) ResolvedLSPToolScope {
	t.Helper()
	resolved, err := manager.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("resolve scope for %s: %v", purpose, err)
	}
	return resolved
}

func assertFallbackRecycleScope(t *testing.T, cfg workspaceConfig, resolved ResolvedLSPToolScope) {
	t.Helper()
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
}

func seedBootstrapCache(t *testing.T, manager *manager, resolved ResolvedLSPToolScope, targetURI string) *bootstrapCoordinator {
	t.Helper()
	coordinator := mustBootstrapCoordinator(t, manager)
	if err := coordinator.cache.Upsert(lspCacheValue{
		Key:         resolved.cacheKey("go", targetURI),
		Version:     1,
		Fingerprint: "stale-before-recycle",
	}); err != nil {
		t.Fatalf("seed bootstrap cache: %v", err)
	}
	return coordinator
}

func assertRecycledGoWorkspaceReplacement(t *testing.T, factory *goWorkspaceClientFactory, repo, goWorkPath, backend, tools string) *goWorkspaceClient {
	t.Helper()
	if got := factory.callCount(); got != 2 {
		t.Fatalf("recycle should create replacement client, calls=%d", got)
	}
	call := factory.callAt(t, 1)
	if call.rootDir != repo {
		t.Fatalf("recycled gopls rootDir = %q, want %q", call.rootDir, repo)
	}
	if !reflect.DeepEqual(call.env, hostGoEnv("GOWORK="+goWorkPath)) {
		t.Fatalf("recycled gopls env = %#v", call.env)
	}
	recycledClient := factory.clientAt(t, 1)
	assertFolderURIs(t, recycledClient.initializedFolders, []string{repo, backend, tools})
	return recycledClient
}

func assertRecycledInitializeScope(t *testing.T, recycledClient *goWorkspaceClient, resolved ResolvedLSPToolScope) {
	t.Helper()
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
}

func assertRecycledInitializeToolScope(t *testing.T, recycledClient *goWorkspaceClient, want common.ToolScope) {
	t.Helper()
	if !recycledClient.initToolScopeOK {
		t.Fatalf("recycled Initialize ctx missing common ToolScope compatibility")
	}
	if recycledClient.initToolScope.AgentID != want.AgentID || recycledClient.initToolScope.ThreadID != want.ThreadID {
		t.Fatalf("recycled common ToolScope = %#v, want stored agent/thread", recycledClient.initToolScope)
	}
}

func assertBootstrapReady(t *testing.T, coordinator *bootstrapCoordinator, resolved ResolvedLSPToolScope, targetURI string) {
	t.Helper()
	if status := coordinator.states.status(resolved.bootstrapKey(), targetURI); status != bootstrapReady {
		t.Fatalf("bootstrap status for canonical scope = %s, want %s", status, bootstrapReady)
	}
}

func TestRecyclerDoesNotRecycleActiveLease(t *testing.T) {
	root := normalizedTempDir(t)
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		Family:   defaultLSPToolFamily,
		AgentID:  "agent-lease",
		ThreadID: "thread-lease",
		CWD:      root,
	})
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

	client, original := assertLeasedRecycleClient(t, manager, ctx, factory, target)
	ageWorkspaceForLifecycleTest(t, manager, client)
	workspace, cfg := singleWorkspaceConfig(t, manager)
	resolved := mustResolveWorkspaceScope(t, manager, ctx, cfg, "active-lease recycle")
	assertActiveLeaseSkipsRecycle(t, manager, client, original, resolved, workspace, factory)
	ageWorkspaceForLifecycleTest(t, manager, client)

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

func assertLeasedRecycleClient(t *testing.T, manager *manager, ctx context.Context, factory *goWorkspaceClientFactory, target string) (Client, *goWorkspaceClient) {
	t.Helper()
	client, ref, err := manager.documentClient(ctx, fileURIFromPath(target))
	if err != nil {
		t.Fatalf("lease document client: %v", err)
	}
	if client == nil {
		t.Fatalf("leased document client for %s is nil", ref.uri)
	}
	original := factory.clientAt(t, 0)
	got, ok := client.(*goWorkspaceClient)
	if !ok || got != original {
		t.Fatalf("leased client = %p, want original client %p", client, original)
	}
	return client, original
}

func assertActiveLeaseSkipsRecycle(
	t *testing.T,
	manager *manager,
	client Client,
	original *goWorkspaceClient,
	resolved ResolvedLSPToolScope,
	workspace workspaceClient,
	factory *goWorkspaceClientFactory,
) {
	t.Helper()
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
}
