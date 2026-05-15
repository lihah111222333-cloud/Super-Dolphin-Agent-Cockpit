package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

func TestTransportClosedDetachesWorkspaceClientAndRebuilds(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &p2LifecycleFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	firstClient, err := mgr.EnsureClient(context.Background(), target, "typescript")
	if err != nil {
		t.Fatalf("EnsureClient(first): %v", err)
	}
	first := firstClient.(*p2LifecycleClient)
	first.markUnhealthy()

	secondClient, err := mgr.EnsureClient(context.Background(), target, "typescript")
	if err != nil {
		t.Fatalf("EnsureClient(second): %v", err)
	}
	if secondClient == firstClient {
		t.Fatalf("dead transport client was reused instead of detached and rebuilt")
	}
	if !first.closed {
		t.Fatalf("dead client was not closed when detached")
	}
	if got := factory.callCount(); got != 2 {
		t.Fatalf("factory calls = %d, want 2 after rebuild", got)
	}
}

func TestRequestFailureAdvancesGenerationAndRebootstrap(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &p2LifecycleFactory{requestFailures: []error{ErrTransportClosed, nil}}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DiagnosticsMaxWait: 1}).(*manager)
	defer func() { _ = mgr.Close() }()

	before := mgr.CurrentDiagnosticGeneration()
	symbols, err := mgr.DocumentSymbol(context.Background(), target)
	if err != nil {
		t.Fatalf("DocumentSymbol after request failure should rebuild/retry: %v", err)
	}
	if len(symbols) != 1 || symbols[0].Name != "rebuilt" {
		t.Fatalf("DocumentSymbol = %#v, want rebuilt symbol from second client", symbols)
	}
	if got := mgr.CurrentDiagnosticGeneration(); got <= before {
		t.Fatalf("diagnostic generation = %d, want advanced beyond %d", got, before)
	}
	first := factory.clientAt(t, 0)
	second := factory.clientAt(t, 1)
	if !first.closed {
		t.Fatalf("failed request client was not closed")
	}
	if !second.opened(fileURIFromPath(target), "typescript") {
		t.Fatalf("rebuilt client did not restore bootstrapped TypeScript document; opens=%#v", second.openEvents())
	}
}

func TestRequestDeadClientDoesNotAutoReplayRename(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"rename-web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const oldName = 1\n")
	factory := &p2LifecycleFactory{requestFailures: []error{ErrTransportClosed, nil}}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DiagnosticsMaxWait: 1}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	_, err := mgr.Rename(context.Background(), target, protocol.Position{Line: 0, Character: 13}, "newName")
	if err == nil {
		t.Fatalf("Rename error = nil, want retryable dead-client error without auto replay")
	}
	first := factory.clientAt(t, 0)
	if got := first.requestCount(); got != 1 {
		t.Fatalf("first client Request count = %d, want exactly one rename attempt", got)
	}
	if got := first.requestMethods(); len(got) != 1 || got[0] != protocol.MethodRename {
		t.Fatalf("first client Request methods = %#v, want one %s", got, protocol.MethodRename)
	}
	if !first.closed {
		t.Fatalf("dead rename client was not closed/detached")
	}
	if got := factory.callCount(); got != 2 {
		t.Fatalf("factory calls = %d, want rebuild for retryable follow-up without replay", got)
	}
	second := factory.clientAt(t, 1)
	if got := second.requestCount(); got != 0 {
		t.Fatalf("rebuilt client Request count = %d, want no automatic rename replay; methods=%#v", got, second.requestMethods())
	}
	if !second.opened(fileURIFromPath(target), "typescript") {
		t.Fatalf("rebuilt client did not restore bootstrapped document; opens=%#v", second.openEvents())
	}
	if !errors.Is(err, ErrClientClosed) {
		t.Fatalf("Rename error = %v, want retryable ErrClientClosed marker", err)
	}
}

func TestInitializeFailureDoesNotLeaveStaleWorkspaceClient(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &p2LifecycleFactory{initializeFailures: []error{errors.New("initialize boom"), nil}}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	if _, err := mgr.EnsureClient(context.Background(), target, "typescript"); err == nil {
		t.Fatalf("EnsureClient(first) error = nil, want initialize failure")
	}
	if got := len(snapshotWorkspaceClients(mgr)); got != 0 {
		t.Fatalf("workspace clients after initialize failure = %d, want 0 stale clients", got)
	}
	if _, err := mgr.EnsureClient(context.Background(), target, "typescript"); err != nil {
		t.Fatalf("EnsureClient(second): %v", err)
	}
	if got := len(snapshotWorkspaceClients(mgr)); got != 1 {
		t.Fatalf("workspace clients after successful retry = %d, want 1", got)
	}
}

func TestReleaseScopeClosesOnlyMatchingAgentThreadClone(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	alpha := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-a", "thread-a"))
	otherThread := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-a", "thread-b"))
	otherAgent := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-b", "thread-a"))

	result, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{
		ScopeKind: ReleaseScopeAgentThread,
		AgentID:   "agent-a",
		ThreadID:  "thread-a",
		Drain:     true,
	})
	if err != nil {
		t.Fatalf("ReleaseScope(agent/thread): %v", err)
	}
	if result.MatchedManagers != 1 || result.ClosedManagers != 1 || result.BusyLeases != 0 || !result.Drained {
		t.Fatalf("ReleaseScope result = %#v, want one drained close", result)
	}
	if !managerIsClosed(alpha) {
		t.Fatalf("matching manager was not closed")
	}
	if managerIsClosed(otherThread) || managerIsClosed(otherAgent) {
		t.Fatalf("ReleaseScope closed unrelated manager: otherThread=%v otherAgent=%v", managerIsClosed(otherThread), managerIsClosed(otherAgent))
	}
}

func TestReleaseScopeRespectsActiveLeaseBusyOrDrain(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module busy\n\ngo 1.25.0\n")
	target := filepath.Join(root, "main.go")
	writeGenericTestFile(t, target, "package main\n")
	factory := &p2LifecycleFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	scoped := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-busy", "thread-1"))
	client, err := scoped.EnsureClient(context.Background(), target, "go")
	if err != nil {
		t.Fatalf("EnsureClient(scoped): %v", err)
	}
	mgr.pool.acquire(client)

	busy, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{ScopeKind: ReleaseScopeAgentThread, AgentID: "agent-busy", ThreadID: "thread-1"})
	if err != nil {
		t.Fatalf("ReleaseScope(busy): %v", err)
	}
	if busy.BusyLeases == 0 || busy.ClosedManagers != 0 || managerIsClosed(scoped) {
		t.Fatalf("busy ReleaseScope result = %#v closed=%v, want busy without close", busy, managerIsClosed(scoped))
	}
	mgr.pool.release(client)

	drained, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{ScopeKind: ReleaseScopeAgentThread, AgentID: "agent-busy", ThreadID: "thread-1", Drain: true})
	if err != nil {
		t.Fatalf("ReleaseScope(drain): %v", err)
	}
	if drained.BusyLeases != 0 || drained.ClosedManagers != 1 || !drained.Drained || !managerIsClosed(scoped) {
		t.Fatalf("drain ReleaseScope result = %#v closed=%v, want drained close", drained, managerIsClosed(scoped))
	}
}

func TestReleaseScopeClearsDiagnosticsBootstrapAndCache(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	scope := testLSPToolScope(root, "agent-clean", "thread-1")
	scoped := scopedManagerForTest(t, mgr, scope)
	resolved, err := ResolveLSPToolScope(scope)
	if err != nil {
		t.Fatalf("ResolveLSPToolScope: %v", err)
	}
	uri := fileURIFromPath(filepath.Join(root, "main.go"))
	coordinator := bootstrapCoordinatorFor(scoped)
	key := resolved.cacheKey("go", uri)
	coordinator.cache.Upsert(lspCacheValue{Key: key, Version: 1, UpdatedAt: time.Now()})
	coordinator.states.complete(resolved.bootstrapKey(), uri, "fp", 1)
	scoped.diagnostics[diagnosticStoreKeyFor(resolved, uri).String()] = diagnosticSnapshot{
		scopeKey:     resolved.ScopeKey,
		workspaceKey: resolved.WorkspaceKey,
		language:     "go",
		uri:          uri,
		generation:   scoped.CurrentDiagnosticGeneration(),
		state:        diagnosticStateReady,
		params:       protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: "stale"}}},
	}

	result, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{ScopeKind: ReleaseScopeAgentThread, AgentID: "agent-clean", ThreadID: "thread-1", Drain: true})
	if err != nil {
		t.Fatalf("ReleaseScope(clean): %v", err)
	}
	if result.ClosedManagers != 1 {
		t.Fatalf("ClosedManagers = %d, want 1", result.ClosedManagers)
	}
	if _, ok := bootstrapCoordinators.Load(scoped); ok {
		t.Fatalf("bootstrap/cache coordinator survived ReleaseScope close")
	}
	if !managerIsClosed(scoped) {
		t.Fatalf("scoped manager was not closed")
	}
}

func TestReleaseScopeRejectsEmptyIdentityWithoutExplicitScopeKind(t *testing.T) {
	mgr := newManagerPoolTestManager(t, canonicalScopePath(t.TempDir(), ""))
	if _, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{}); err == nil {
		t.Fatalf("ReleaseScope(empty) error = nil, want explicit scope kind/identity rejection")
	}
}

func TestManagerPoolEvictsOldIdleCloneAtCap(t *testing.T) {
	t.Setenv(lspPoolSizeEnv, "1")
	t.Setenv("AGENT_LSP_POOL_SHARD_CAP", "2")
	root := canonicalScopePath(t.TempDir(), "")
	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	first := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-1", "thread"))
	_ = scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-2", "thread"))
	_ = scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-3", "thread"))

	snapshots := cloneSnapshotsForShard(t, mgr.pool, 0)
	if len(snapshots) != 2 {
		t.Fatalf("clone count after cap eviction = %d, want 2; snapshots=%#v", len(snapshots), snapshots)
	}
	if !managerIsClosed(first) {
		t.Fatalf("oldest idle clone was not closed during cap eviction")
	}
}

func TestManagerPoolDoesNotEvictActiveLeaseClone(t *testing.T) {
	t.Setenv(lspPoolSizeEnv, "1")
	t.Setenv("AGENT_LSP_POOL_SHARD_CAP", "1")
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module active\n\ngo 1.25.0\n")
	target := filepath.Join(root, "main.go")
	writeGenericTestFile(t, target, "package main\n")
	factory := &p2LifecycleFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	active := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-active", "thread"))
	client, err := active.EnsureClient(context.Background(), target, "go")
	if err != nil {
		t.Fatalf("EnsureClient(active): %v", err)
	}
	mgr.pool.acquire(client)
	defer mgr.pool.release(client)

	_ = scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-new", "thread"))
	if managerIsClosed(active) {
		t.Fatalf("active-lease clone was evicted")
	}
	if len(cloneSnapshotsForShard(t, mgr.pool, 0)) != 2 {
		t.Fatalf("active clone should be retained even if cap is exceeded by active lease")
	}
}

func TestDeadClientRebuildPreservesTypeScriptWorkspace(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &p2LifecycleFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	firstClient, err := mgr.EnsureClient(context.Background(), target, "typescript")
	if err != nil {
		t.Fatalf("EnsureClient(first): %v", err)
	}
	firstClient.(*p2LifecycleClient).markUnhealthy()
	if _, err := mgr.EnsureClient(context.Background(), target, "typescript"); err != nil {
		t.Fatalf("EnsureClient(second): %v", err)
	}
	if factory.callAt(t, 1).rootDir != root {
		t.Fatalf("rebuilt TypeScript rootDir = %q, want %q", factory.callAt(t, 1).rootDir, root)
	}
	if containsEnvKey(factory.callAt(t, 1).env, "GOWORK") {
		t.Fatalf("rebuilt TypeScript env leaked GOWORK: %#v", factory.callAt(t, 1).env)
	}
}

func TestRecyclerRebuildDoesNotDefaultNonGoLanguageToGo(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &genericMatrixClientFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	if err := mgr.BootstrapDocument(context.Background(), target); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}
	workspace := snapshotWorkspaceClients(mgr)
	if len(workspace) != 1 {
		t.Fatalf("workspace clients = %d, want 1", len(workspace))
	}
	if _, err := recycleWorkspaceClient(mgr, ResolvedLSPToolScope{}, workspace[0]); err != nil {
		t.Fatalf("recycleWorkspaceClient: %v", err)
	}
	if got := factory.clientAt(t, 1).initLanguageID; got != "typescript" {
		t.Fatalf("recycled non-Go language = %q, want typescript", got)
	}
}

func TestEvictionKeepsJavaWorkspaceKeyAndLanguageSpecificHash(t *testing.T) {
	t.Setenv(lspPoolSizeEnv, "1")
	t.Setenv("AGENT_LSP_POOL_SHARD_CAP", "1")
	root := canonicalScopePath(t.TempDir(), "")
	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	scopeA := testLSPToolScopeForLanguage(root, "agent-java-a", "thread", "java")
	scopeA.LanguageSpecific = map[string]string{"classpath": "a"}
	scopeB := testLSPToolScopeForLanguage(root, "agent-java-b", "thread", "java")
	scopeB.LanguageSpecific = map[string]string{"classpath": "b"}

	_ = scopedManagerForTest(t, mgr, scopeA)
	_ = scopedManagerForTest(t, mgr, scopeB)

	snapshots := cloneSnapshotsForShard(t, mgr.pool, 0)
	if len(snapshots) != 1 {
		t.Fatalf("clone count = %d, want 1 after cap eviction", len(snapshots))
	}
	remaining := snapshots[0].resolvedScope
	if remaining.LanguageID != "java" {
		t.Fatalf("remaining language = %q, want java", remaining.LanguageID)
	}
	if remaining.WorkspaceKey == "" || !strings.Contains(remaining.WorkspaceKey, "java") {
		t.Fatalf("remaining WorkspaceKey = %q, want Java-specific key", remaining.WorkspaceKey)
	}
	key := remaining.cacheKey("java", "file:///repo/Main.java")
	if key.LanguageSpecificHash == "" {
		t.Fatalf("Java cache key lost LanguageSpecificHash: %#v", key)
	}
}

func TestDiagnosticsAllRefreshesStaleScopedDiagnosticBeforeReturn(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"diagnostics-stale"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "function staleName() { return 1; }\n")
	factory := &diagnosticsRefreshClientFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DiagnosticsMaxWait: 1}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext("agent-stale-all", "thread-1")
	uri := fileURIFromPath(target)
	if err := mgr.BootstrapDocument(ctx, uri); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: "stale"}}}); err != nil {
		t.Fatalf("PublishDiagnostics: %v", err)
	}
	writeGenericTestFile(t, target, "function freshName() { return 2; }\n")

	items, err := mgr.Diagnostics(ctx, nil)
	if err != nil {
		t.Fatalf("Diagnostics(all): %v", err)
	}
	if got := factory.currentClient().changeCount(); got == 0 {
		t.Fatalf("Diagnostics(all) did not refresh stale scoped diagnostic; returned %#v", items)
	}
	if len(items) != 0 {
		t.Fatalf("Diagnostics(all) = %#v, want empty after refresh cleared stale diagnostics", items)
	}
}

func TestDiagnosticsAllBootstrapsUntrackedExistingDiagnosticURI(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"diagnostics-cache"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &p2DiagnosticsFactory{publishOnOpen: "bootstrapped"}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DiagnosticsMaxWait: 1}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext("agent-cache", "thread-1")
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "typescript")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	bootstrapCoordinatorFor(mgr).cache.Upsert(lspCacheValue{Key: scope.cacheKey("typescript", ref.uri), Version: 1, UpdatedAt: time.Now()})
	bootstrapCoordinatorFor(mgr).cache.RememberDocumentScope(ref.uri, scope, "fp")

	items, err := mgr.Diagnostics(WithResolvedLSPToolScope(ctx, scope), nil)
	if err != nil {
		t.Fatalf("Diagnostics(all): %v", err)
	}
	if len(items) != 1 || len(items[0].Diagnostics) != 1 || items[0].Diagnostics[0].Message != "bootstrapped" {
		t.Fatalf("Diagnostics(all) = %#v, want bootstrapped diagnostic from cached URI", items)
	}
}

func TestDiagnosticsAllDeletedFileClearsDiagnosticsAndTombstones(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module deletedall\n\ngo 1.25.0\n")
	target := filepath.Join(root, "deleted.go")
	writeGenericTestFile(t, target, "package deletedall\n")
	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext("agent-deleted-all", "thread-1")
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "go")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	key := scope.cacheKey("go", ref.uri)
	bootstrapCoordinatorFor(mgr).cache.Upsert(lspCacheValue{Key: key, Version: 1, UpdatedAt: time.Now()})
	bootstrapCoordinatorFor(mgr).cache.RememberDocumentScope(ref.uri, scope, "fp")
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: ref.uri, Diagnostics: []protocol.Diagnostic{{Message: "deleted"}}}); err != nil {
		t.Fatalf("PublishDiagnostics: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove target: %v", err)
	}

	items, err := mgr.Diagnostics(WithResolvedLSPToolScope(ctx, scope), nil)
	if err != nil {
		t.Fatalf("Diagnostics(all): %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("Diagnostics(all) = %#v, want deleted diagnostic cleared", items)
	}
	if _, ok := bootstrapCoordinatorFor(mgr).cache.Load(key); ok {
		t.Fatalf("deleted file cache key still loads after Diagnostics(all)")
	}
	bootstrapCoordinatorFor(mgr).cache.mu.RLock()
	_, tombstoned := bootstrapCoordinatorFor(mgr).cache.tombstones[key.String()]
	bootstrapCoordinatorFor(mgr).cache.mu.RUnlock()
	if !tombstoned {
		t.Fatalf("deleted file cache key was not tombstoned")
	}
}

func TestDidChangeAdvancesBootstrapCacheVersionForFullDiskBackedText(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"change"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext("agent-change", "thread-1")
	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}
	nextText := "let value = 2\n"
	writeGenericTestFile(t, target, nextText)
	if err := mgr.DidChange(ctx, target, 7, []protocol.TextDocumentContentChangeEvent{{Text: nextText}}); err != nil {
		t.Fatalf("DidChange(full): %v", err)
	}
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "javascript")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	record, ok := bootstrapCoordinatorFor(mgr).cache.Load(scope.cacheKey("javascript", ref.uri))
	if !ok {
		t.Fatalf("cache record missing after full disk-backed DidChange")
	}
	if record.Version != 7 || record.Fingerprint != hashDocument([]byte(nextText)) {
		t.Fatalf("cache record = %#v, want version 7 and changed fingerprint", record)
	}
	if got := bootstrapCoordinatorFor(mgr).states.status(scope.bootstrapKey(), ref.uri); got != bootstrapReady {
		t.Fatalf("bootstrap state = %s, want ready after full DidChange", got)
	}
}

func TestIncrementalDidChangeDoesNotWritePersistentCache(t *testing.T) {
	root, cacheDir := setupPersistentCacheEnv(t)
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"incremental"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	rng := protocol.Range{Start: protocol.Position{}, End: protocol.Position{Line: 0, Character: 3}}
	if err := mgr.DidChange(context.Background(), target, 2, []protocol.TextDocumentContentChangeEvent{{Range: &rng, Text: "var"}}); err != nil {
		t.Fatalf("DidChange(incremental): %v", err)
	}
	assertPersistentCacheDocumentCount(t, cacheDir, 0)
}

func TestMemoryOnlyDidChangeDoesNotWritePersistentCache(t *testing.T) {
	root, cacheDir := setupPersistentCacheEnv(t)
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"memory"}`)
	target := filepath.Join(root, "virtual.js")
	factory := &p2DiagnosticsFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	if err := mgr.DidChange(context.Background(), target, 2, []protocol.TextDocumentContentChangeEvent{{Text: "let value = 1\n"}}); err != nil {
		t.Fatalf("DidChange(memory-only): %v", err)
	}
	assertPersistentCacheDocumentCount(t, cacheDir, 0)
}

func TestDidChangeFailureFallsBackToReopenThenRestart(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"fallback"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{
		didChangeErrors: []error{errors.New("change failed")},
		didCloseErrors:  []error{ErrTransportClosed},
	}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	if err := mgr.BootstrapDocument(context.Background(), target); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}
	nextText := "let value = 2\n"
	writeGenericTestFile(t, target, nextText)
	if err := mgr.DidChange(context.Background(), target, 3, []protocol.TextDocumentContentChangeEvent{{Text: nextText}}); err != nil {
		t.Fatalf("DidChange should reopen/restart after failure: %v", err)
	}
	if factory.callCount() < 2 {
		t.Fatalf("factory calls = %d, want restart after close/reopen failure", factory.callCount())
	}
	if !factory.clientAt(t, 1).opened(fileURIFromPath(target), "javascript") {
		t.Fatalf("restarted client did not reopen changed document")
	}
}

func TestDidChangeDeadClientDoesNotAutoReplayNotify(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"dead-change"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{didChangeErrors: []error{ErrTransportClosed, nil}}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := context.Background()
	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}

	nextText := "let value = 2\n"
	writeGenericTestFile(t, target, nextText)
	err := mgr.DidChange(ctx, target, 2, []protocol.TextDocumentContentChangeEvent{{Text: nextText}})
	if err == nil {
		t.Fatalf("DidChange error = nil, want retryable dead-client error without notify replay")
	}
	first := factory.clientAt(t, 0)
	if got := first.didChangeCount(); got != 1 {
		t.Fatalf("first client DidChange count = %d, want exactly one attempt", got)
	}
	if got := first.openCount(); got != 1 {
		t.Fatalf("first client DidOpen count = %d, want no reopen replay on dead DidChange", got)
	}
	if !first.closed {
		t.Fatalf("dead DidChange client was not closed/detached")
	}
	if got := factory.callCount(); got != 2 {
		t.Fatalf("factory calls = %d, want repair rebuild without replay", got)
	}
	second := factory.clientAt(t, 1)
	if got := second.didChangeCount(); got != 0 {
		t.Fatalf("rebuilt client DidChange count = %d, want no automatic DidChange replay", got)
	}
	if !second.opened(fileURIFromPath(target), "javascript") {
		t.Fatalf("rebuilt client did not restore safe bootstrap open; opens=%#v", second.opens)
	}
	if !errors.Is(err, ErrClientClosed) {
		t.Fatalf("DidChange error = %v, want retryable ErrClientClosed marker", err)
	}
}

func TestDidCloseClearsBootstrapReadyAndNextOpenReopens(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"close"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	if err := mgr.BootstrapDocumentOpenOnly(context.Background(), target); err != nil {
		t.Fatalf("BootstrapDocumentOpenOnly(first): %v", err)
	}
	if err := mgr.DidClose(context.Background(), target); err != nil {
		t.Fatalf("DidClose: %v", err)
	}
	if err := mgr.BootstrapDocumentOpenOnly(context.Background(), target); err != nil {
		t.Fatalf("BootstrapDocumentOpenOnly(second): %v", err)
	}
	if got := factory.clientAt(t, 0).openCount(); got != 2 {
		t.Fatalf("DidOpen count = %d, want 2 after DidClose cleared ready state", got)
	}
}

func TestDidCloseDoesNotTombstoneExistingFile(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"close"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := context.Background()
	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "javascript")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	key := scope.cacheKey("javascript", ref.uri)
	if err := mgr.DidClose(ctx, target); err != nil {
		t.Fatalf("DidClose: %v", err)
	}
	if _, ok := bootstrapCoordinatorFor(mgr).cache.Load(key); !ok {
		t.Fatalf("existing file cache was removed by DidClose")
	}
	bootstrapCoordinatorFor(mgr).cache.mu.RLock()
	_, tombstoned := bootstrapCoordinatorFor(mgr).cache.tombstones[key.String()]
	bootstrapCoordinatorFor(mgr).cache.mu.RUnlock()
	if tombstoned {
		t.Fatalf("existing file was tombstoned by DidClose")
	}
}

func TestBootstrapBootstrappingTimeoutAllowsRetry(t *testing.T) {
	store := newBootstrapStateStore()
	wait := make(chan struct{})
	key := bootstrapKey{workspace: "workspace", uri: "file:///stuck.go"}
	store.entries[key] = &bootstrapEntry{
		status:    bootstrapBootstrapping,
		updatedAt: time.Now().Add(-time.Hour),
		wait:      wait,
	}
	decision := store.prepare(key.workspace, key.uri, "fp")
	if decision.action != bootstrapActionRun {
		t.Fatalf("prepare(stale bootstrapping) action = %v, want run retry", decision.action)
	}
	select {
	case <-wait:
	default:
		t.Fatalf("stale bootstrapping waiter was not released")
	}
}

func TestPersistentCacheCorruptFileQuarantinedAndRewritten(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(lspCachePersistentEnv, "1")
	t.Setenv(lspCacheDirEnv, cacheDir)
	path := filepath.Join(cacheDir, lspCacheFileName)
	if err := os.WriteFile(path, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}
	store := newLSPCacheStoreFromEnv(nil)
	if !store.persistent || !store.persistentReady {
		t.Fatalf("persistent cache fell back to memory after corruption")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten cache: %v", err)
	}
	var disk lspCacheDiskState
	if err := json.Unmarshal(payload, &disk); err != nil {
		t.Fatalf("rewritten cache is not valid JSON: %s err=%v", payload, err)
	}
	matches, err := filepath.Glob(filepath.Join(cacheDir, lspCacheFileName+".corrupt-*"))
	if err != nil {
		t.Fatalf("glob corrupt quarantine: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("corrupt quarantine files = %#v, want one", matches)
	}
}

func TestPersistentCacheWritesAtomically(t *testing.T) {
	root, cacheDir := setupPersistentCacheEnv(t)
	store := newLSPCacheStoreFromEnv(nil)
	scope := ResolvedLSPToolScope{ScopeKey: "scope", WorkspaceKey: "workspace", LSPToolScope: LSPToolScope{LanguageID: "go"}}
	store.Upsert(lspCacheValue{Key: scope.cacheKey("go", fileURIFromPath(filepath.Join(root, "main.go"))), Version: 1})
	path := filepath.Join(cacheDir, lspCacheFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat persistent cache: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(cacheDir, lspCacheFileName+".tmp-*"))
	if err != nil {
		t.Fatalf("glob tmp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("atomic cache write left tmp files: %#v", matches)
	}
	assertPersistentCacheDocumentCount(t, cacheDir, 1)
}

func TestPersistentCacheExpiredEntryFilteredAndPersisted(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(lspCachePersistentEnv, "1")
	t.Setenv(lspCacheDirEnv, cacheDir)
	scope := ResolvedLSPToolScope{ScopeKey: "scope", WorkspaceKey: "workspace", LSPToolScope: LSPToolScope{LanguageID: "go"}}
	expired := lspCacheValue{Key: scope.cacheKey("go", "file:///expired.go"), Version: 1, UpdatedAt: time.Now().Add(-8 * 24 * time.Hour)}
	fresh := lspCacheValue{Key: scope.cacheKey("go", "file:///fresh.go"), Version: 1, UpdatedAt: time.Now()}
	writeCacheDiskState(t, cacheDir, lspCacheDiskState{Documents: []lspCacheValue{expired, fresh}})

	_ = newLSPCacheStoreFromEnv(nil)
	disk := readCacheDiskState(t, cacheDir)
	if len(disk.Documents) != 1 || disk.Documents[0].Key.URI != "file:///fresh.go" {
		t.Fatalf("persisted cache documents = %#v, want only fresh entry", disk.Documents)
	}
}

func TestDeletedPersistentCacheRecordDoesNotResurrectAfterRestart(t *testing.T) {
	_, cacheDir := setupPersistentCacheEnv(t)
	scope := ResolvedLSPToolScope{ScopeKey: "scope", WorkspaceKey: "workspace", LSPToolScope: LSPToolScope{LanguageID: "go"}}
	key := scope.cacheKey("go", "file:///deleted.go")
	store := newLSPCacheStoreFromEnv(nil)
	store.Upsert(lspCacheValue{Key: key, Version: 1})
	store.Tombstone(key)
	restarted := newLSPCacheStore(lspCacheConfig{Persistent: true, Dir: cacheDir})
	if _, ok := restarted.Load(key); ok {
		t.Fatalf("deleted persistent cache record resurrected after restart")
	}
}

func TestDiagnosticsAfterServerExitDoesNotReturnOldGeneration(t *testing.T) {
	mgr := NewManager(Config{WorkspaceRoot: canonicalScopePath(t.TempDir(), "")}).(*manager)
	uri := "file:///repo/stale.go"
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: "old"}}}); err != nil {
		t.Fatalf("PublishDiagnostics: %v", err)
	}
	mgr.AdvanceDiagnosticGeneration()
	items, err := mgr.Diagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("Diagnostics: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("Diagnostics returned old-generation entries after server exit: %#v", items)
	}
}

func TestDiagnosticsAllDoesNotReturnCrossLanguageSameURI(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	sharedPath := filepath.Join(root, "shared.txt")
	writeGenericTestFile(t, sharedPath, "shared\n")
	uri := fileURIFromPath(sharedPath)
	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	gen := mgr.CurrentDiagnosticGeneration()
	goScope := ResolvedLSPToolScope{ScopeKey: "scope", WorkspaceKey: "go-workspace", LSPToolScope: LSPToolScope{LanguageID: "go", WorkspaceRoot: root}}
	tsScope := ResolvedLSPToolScope{ScopeKey: "scope", WorkspaceKey: "ts-workspace", LSPToolScope: LSPToolScope{LanguageID: "typescript", WorkspaceRoot: root}}
	mgr.diagnostics[diagnosticStoreKeyFor(goScope, uri).String()] = diagnosticSnapshot{scopeKey: goScope.ScopeKey, workspaceKey: goScope.WorkspaceKey, language: "go", uri: uri, generation: gen, state: diagnosticStateReady, params: protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: "go"}}}}
	mgr.diagnostics[diagnosticStoreKeyFor(tsScope, uri).String()] = diagnosticSnapshot{scopeKey: tsScope.ScopeKey, workspaceKey: tsScope.WorkspaceKey, language: "typescript", uri: uri, generation: gen, state: diagnosticStateReady, params: protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: "ts"}}}}

	items, err := mgr.Diagnostics(WithResolvedLSPToolScope(context.Background(), tsScope), nil)
	if err != nil {
		t.Fatalf("Diagnostics(all): %v", err)
	}
	if len(items) != 1 || items[0].Diagnostics[0].Message != "ts" {
		t.Fatalf("Diagnostics(all) = %#v, want only TypeScript same-URI diagnostic", items)
	}
}

func TestDeletedPersistentCacheRecordDoesNotResurrectAcrossLanguages(t *testing.T) {
	_, cacheDir := setupPersistentCacheEnv(t)
	scope := ResolvedLSPToolScope{ScopeKey: "scope", WorkspaceKey: "workspace", LSPToolScope: LSPToolScope{LanguageID: "go"}}
	goKey := scope.cacheKey("go", "file:///shared")
	tsKey := scope.cacheKey("typescript", "file:///shared")
	store := newLSPCacheStoreFromEnv(nil)
	store.Upsert(lspCacheValue{Key: goKey, Version: 1})
	store.Upsert(lspCacheValue{Key: tsKey, Version: 2})
	store.Tombstone(tsKey)

	restarted := newLSPCacheStore(lspCacheConfig{Persistent: true, Dir: cacheDir})
	if _, ok := restarted.Load(tsKey); ok {
		t.Fatalf("deleted TypeScript cache record resurrected after restart")
	}
	if _, ok := restarted.Load(goKey); !ok {
		t.Fatalf("Go cache record was incorrectly removed by TypeScript tombstone")
	}
}

func TestBootstrapCacheMatrixForRegisteredLanguageIDs(t *testing.T) {
	for _, tc := range []struct {
		languageID string
		fileName   string
		markerName string
		markerBody string
	}{
		{languageID: "go", fileName: "main.go", markerName: "go.mod", markerBody: "module example.test/matrix\n\ngo 1.25.0\n"},
		{languageID: "javascript", fileName: "app.js", markerName: "package.json", markerBody: `{"name":"matrix"}`},
		{languageID: "typescript", fileName: "app.ts", markerName: "tsconfig.json", markerBody: `{"compilerOptions":{}}`},
		{languageID: "python", fileName: "app.py", markerName: "pyproject.toml", markerBody: "[project]\nname = \"matrix\"\n"},
		{languageID: "rust", fileName: "main.rs", markerName: "Cargo.toml", markerBody: "[package]\nname = \"matrix\"\nversion = \"0.1.0\"\n"},
		{languageID: "java", fileName: filepath.Join("src", "Main.java"), markerName: "pom.xml", markerBody: "<project></project>\n"},
		{languageID: "css", fileName: "style.css", markerName: "package.json", markerBody: `{"name":"matrix-css"}`},
	} {
		t.Run(tc.languageID, func(t *testing.T) {
			root := canonicalScopePath(t.TempDir(), "")
			writeGenericTestFile(t, filepath.Join(root, tc.markerName), tc.markerBody)
			target := filepath.Join(root, tc.fileName)
			writeGenericTestFile(t, target, "symbol\n")
			factory := &genericMatrixClientFactory{}
			mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
			t.Cleanup(func() { _ = mgr.Close() })
			if err := mgr.BootstrapDocument(context.Background(), target); err != nil {
				t.Fatalf("BootstrapDocument(%s): %v", tc.languageID, err)
			}
			client := factory.clientAt(t, 0)
			if !client.opened(fileURIFromPath(target), tc.languageID) {
				t.Fatalf("%s target was not opened during bootstrap; opens=%#v", tc.languageID, client.openEvents())
			}
			indexed, ok := bootstrapCoordinatorFor(mgr).cache.LastResolvedScope(fileURIFromPath(target))
			if !ok {
				t.Fatalf("%s bootstrap did not remember resolved scope", tc.languageID)
			}
			if indexed.LastResolvedScope.LanguageID != tc.languageID || indexed.LastResolvedScope.WorkspaceKey == "" {
				t.Fatalf("%s resolved scope = %#v", tc.languageID, indexed.LastResolvedScope)
			}
		})
	}
}

func scopedManagerForTest(t *testing.T, mgr *manager, scope LSPToolScope) *manager {
	t.Helper()
	scoped, err := mgr.pool.ForScope(scope)
	if err != nil {
		t.Fatalf("ForScope(%#v): %v", scope, err)
	}
	typed, ok := scoped.Manager.(*manager)
	if !ok {
		t.Fatalf("ScopedManager.Manager type = %T, want *manager", scoped.Manager)
	}
	return typed
}

func cloneSnapshotsForShard(t *testing.T, pool *ManagerPool, shardIndex int) []pooledManager {
	t.Helper()
	if pool == nil || shardIndex < 0 || shardIndex >= len(pool.shards) {
		t.Fatalf("invalid shard index %d", shardIndex)
	}
	return pool.shards[shardIndex].snapshotClones()
}

type p2LifecycleFactory struct {
	mu                 sync.Mutex
	clients            []*p2LifecycleClient
	calls              []genericMatrixFactoryCall
	initializeFailures []error
	requestFailures    []error
}

func (f *p2LifecycleFactory) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	return f.NewClientWithEnv(rootDir, nil, handler)
}

func (f *p2LifecycleFactory) NewClientWithEnv(rootDir string, env []string, handler protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := len(f.clients)
	client := &p2LifecycleClient{
		handler:           handler,
		healthy:           true,
		documents:         map[string]string{},
		initializeFailure: failureAt(f.initializeFailures, idx),
		requestFailure:    failureAt(f.requestFailures, idx),
	}
	f.clients = append(f.clients, client)
	f.calls = append(f.calls, genericMatrixFactoryCall{rootDir: rootDir, env: append([]string(nil), env...)})
	return client, nil
}

func (f *p2LifecycleFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *p2LifecycleFactory) callAt(t *testing.T, idx int) genericMatrixFactoryCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if idx < 0 || idx >= len(f.calls) {
		t.Fatalf("factory call %d out of range; calls=%d", idx, len(f.calls))
	}
	return f.calls[idx]
}

func (f *p2LifecycleFactory) clientAt(t *testing.T, idx int) *p2LifecycleClient {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if idx < 0 || idx >= len(f.clients) {
		t.Fatalf("factory client %d out of range; clients=%d", idx, len(f.clients))
	}
	return f.clients[idx]
}

func failureAt(values []error, idx int) error {
	if idx < 0 || idx >= len(values) {
		return nil
	}
	return values[idx]
}

type p2LifecycleClient struct {
	mu                sync.Mutex
	handler           protocol.NotificationHandler
	healthy           bool
	closed            bool
	documents         map[string]string
	opens             []genericOpenEvent
	requestLog        []string
	initializeFailure error
	requestFailure    error
}

func (c *p2LifecycleClient) Healthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.healthy && !c.closed
}

func (c *p2LifecycleClient) markUnhealthy() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.healthy = false
}

func (c *p2LifecycleClient) Initialize(context.Context, string) error {
	if c.initializeFailure != nil {
		c.markUnhealthy()
		return c.initializeFailure
	}
	return nil
}

func (c *p2LifecycleClient) Shutdown(context.Context) error { return nil }

func (c *p2LifecycleClient) Request(_ context.Context, method string, _ any) (json.RawMessage, error) {
	c.mu.Lock()
	c.requestLog = append(c.requestLog, method)
	c.mu.Unlock()
	if c.requestFailure != nil {
		c.markUnhealthy()
		return nil, c.requestFailure
	}
	return json.Marshal([]protocol.DocumentSymbol{{
		Name:           "rebuilt",
		Kind:           protocol.SymbolKindVariable,
		Range:          protocol.Range{},
		SelectionRange: protocol.Range{},
	}})
}

func (c *p2LifecycleClient) Notify(context.Context, string, any) error { return nil }

func (c *p2LifecycleClient) DidOpen(_ context.Context, uri, languageID string, _ int, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opens = append(c.opens, genericOpenEvent{uri: uri, language: languageID})
	c.documents[uri] = text
	return nil
}

func (c *p2LifecycleClient) DidChange(_ context.Context, uri string, _ int, changes []protocol.TextDocumentContentChangeEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(changes) > 0 {
		c.documents[uri] = changes[len(changes)-1].Text
	}
	return nil
}

func (c *p2LifecycleClient) DidClose(context.Context, string) error { return nil }

func (c *p2LifecycleClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.healthy = false
	return nil
}

func (c *p2LifecycleClient) opened(uri, languageID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range c.opens {
		if event.uri == uri && event.language == languageID {
			return true
		}
	}
	return false
}

func (c *p2LifecycleClient) openEvents() []genericOpenEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]genericOpenEvent(nil), c.opens...)
}

func (c *p2LifecycleClient) requestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requestLog)
}

func (c *p2LifecycleClient) requestMethods() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.requestLog...)
}

type p2DiagnosticsFactory struct {
	mu              sync.Mutex
	clients         []*p2DiagnosticsClient
	publishOnOpen   string
	didChangeErrors []error
	didCloseErrors  []error
}

func (f *p2DiagnosticsFactory) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := len(f.clients)
	client := &p2DiagnosticsClient{
		handler:        handler,
		healthy:        true,
		documents:      map[string]string{},
		publishOnOpen:  f.publishOnOpen,
		didChangeError: failureAt(f.didChangeErrors, idx),
		didCloseError:  failureAt(f.didCloseErrors, idx),
	}
	f.clients = append(f.clients, client)
	return client, nil
}

func (f *p2DiagnosticsFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.clients)
}

func (f *p2DiagnosticsFactory) clientAt(t *testing.T, idx int) *p2DiagnosticsClient {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if idx < 0 || idx >= len(f.clients) {
		t.Fatalf("diagnostics client %d out of range; clients=%d", idx, len(f.clients))
	}
	return f.clients[idx]
}

type p2DiagnosticsClient struct {
	mu             sync.Mutex
	handler        protocol.NotificationHandler
	healthy        bool
	closed         bool
	documents      map[string]string
	opens          []genericOpenEvent
	didChanges     []string
	publishOnOpen  string
	didChangeError error
	didCloseError  error
}

func (c *p2DiagnosticsClient) Healthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.healthy && !c.closed
}

func (c *p2DiagnosticsClient) Initialize(context.Context, string) error { return nil }
func (c *p2DiagnosticsClient) Shutdown(context.Context) error           { return nil }
func (c *p2DiagnosticsClient) Request(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage("null"), nil
}
func (c *p2DiagnosticsClient) Notify(context.Context, string, any) error { return nil }

func (c *p2DiagnosticsClient) DidOpen(_ context.Context, uri, languageID string, _ int, text string) error {
	c.mu.Lock()
	c.opens = append(c.opens, genericOpenEvent{uri: uri, language: languageID})
	c.documents[uri] = text
	message := c.publishOnOpen
	handler := c.handler
	c.mu.Unlock()
	if message != "" && handler != nil {
		return handler.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: message}}})
	}
	return nil
}

func (c *p2DiagnosticsClient) DidChange(_ context.Context, uri string, _ int, changes []protocol.TextDocumentContentChangeEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.didChanges = append(c.didChanges, uri)
	if c.didChangeError != nil {
		c.healthy = false
		return c.didChangeError
	}
	if len(changes) > 0 {
		c.documents[uri] = changes[len(changes)-1].Text
	}
	return nil
}

func (c *p2DiagnosticsClient) DidClose(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.didCloseError != nil {
		c.healthy = false
		return c.didCloseError
	}
	return nil
}

func (c *p2DiagnosticsClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.healthy = false
	return nil
}

func (c *p2DiagnosticsClient) opened(uri, languageID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range c.opens {
		if event.uri == uri && event.language == languageID {
			return true
		}
	}
	return false
}

func (c *p2DiagnosticsClient) openCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.opens)
}

func (c *p2DiagnosticsClient) didChangeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.didChanges)
}

func setupPersistentCacheEnv(t *testing.T) (string, string) {
	t.Helper()
	root := canonicalScopePath(t.TempDir(), "")
	cacheDir := t.TempDir()
	t.Setenv(lspCachePersistentEnv, "1")
	t.Setenv(lspCacheDirEnv, cacheDir)
	return root, cacheDir
}

func writeCacheDiskState(t *testing.T, cacheDir string, state lspCacheDiskState) {
	t.Helper()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal cache state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, lspCacheFileName), payload, 0o644); err != nil {
		t.Fatalf("write cache state: %v", err)
	}
}

func readCacheDiskState(t *testing.T, cacheDir string) lspCacheDiskState {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(cacheDir, lspCacheFileName))
	if err != nil {
		t.Fatalf("read cache state: %v", err)
	}
	var state lspCacheDiskState
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatalf("decode cache state: %v payload=%s", err, payload)
	}
	return state
}

func assertPersistentCacheDocumentCount(t *testing.T, cacheDir string, want int) {
	t.Helper()
	path := filepath.Join(cacheDir, lspCacheFileName)
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if want == 0 {
			return
		}
		t.Fatalf("persistent cache file missing, want %d documents", want)
	}
	if err != nil {
		t.Fatalf("read persistent cache: %v", err)
	}
	var state lspCacheDiskState
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatalf("decode persistent cache: %v payload=%s", err, payload)
	}
	if len(state.Documents) != want {
		t.Fatalf("persistent cache documents = %#v, want count %d", state.Documents, want)
	}
}
