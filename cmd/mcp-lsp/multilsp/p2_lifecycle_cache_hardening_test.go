package multilsp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestTransportClosedDetachesWorkspaceClientAndRebuilds(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &p2LifecycleFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	firstClient, err := mgr.EnsureClient(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, "typescript")
	if err != nil {
		t.Fatalf("EnsureClient(first): %v", err)
	}
	first := firstClient.(*p2LifecycleClient)
	first.markUnhealthy()

	secondClient, err := mgr.EnsureClient(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, "typescript")
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

func TestRequestFailureReturnsRetriedResultAfterRebootstrap(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &p2LifecycleFactory{requestFailures: []error{ErrTransportClosed, nil}}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DiagnosticsMaxWait: 1}).(*manager)
	defer func() { _ = mgr.Close() }()

	before := mgr.CurrentDiagnosticGeneration()
	symbols, err := mgr.DocumentSymbol(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target)
	if err != nil {
		t.Fatalf("DocumentSymbol error = %v, want successful replay after transport rebuild", err)
	}
	if len(symbols) != 1 || symbols[0].Name != "rebuilt" {
		t.Fatalf("DocumentSymbol = %#v, want retried result from rebuilt client", symbols)
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

	_, err := mgr.Rename(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, protocol.Position{Line: 0, Character: 13}, "newName")
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

	if _, err := mgr.EnsureClient(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, "typescript"); err == nil {
		t.Fatalf("EnsureClient(first) error = nil, want initialize failure")
	}
	if got := len(snapshotWorkspaceClients(mgr)); got != 0 {
		t.Fatalf("workspace clients after initialize failure = %d, want 0 stale clients", got)
	}
	if _, err := mgr.EnsureClient(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, "typescript"); err != nil {
		t.Fatalf("EnsureClient(second): %v", err)
	}
	if got := len(snapshotWorkspaceClients(mgr)); got != 1 {
		t.Fatalf("workspace clients after successful retry = %d, want 1", got)
	}
}

func TestGoRSSLimitUsesLanguageSpecificDefault(t *testing.T) {
	if got := rssLimitBytesForLanguage("go"); got != defaultGoRSSLimitBytes {
		t.Fatalf("rssLimitBytesForLanguage(go) = %d, want %d", got, defaultGoRSSLimitBytes)
	}
}

func TestGenericRSSLimitUsesDefaultLimit(t *testing.T) {
	if got := rssLimitBytesForLanguage("typescript"); got != defaultRSSLimitBytes {
		t.Fatalf("rssLimitBytesForLanguage(typescript) = %d, want %d", got, defaultRSSLimitBytes)
	}
}

func TestPoolRecyclerIdleWorkspaceWinsOverRSSRecycle(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module idle\n\ngo 1.25.0\n")
	target := filepath.Join(root, "main.go")
	writeGenericTestFile(t, target, "package main\n")
	factory := &p2LifecycleFactory{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, Logger: logger}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	scoped := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-idle", "thread-1"))

	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root})
	client, err := scoped.EnsureClient(ctx, target, "go")
	if err != nil {
		t.Fatalf("EnsureClient(scoped): %v", err)
	}
	forceWorkspaceLastActivity(t, scoped, client, time.Now().Add(-idleTimeout-time.Minute))

	originalProbe := mgr.pool.recycler.rssProbe
	mgr.pool.recycler.rssProbe = func(Client) (uint64, int, error) {
		return defaultGoRSSLimitBytes + 1, 4242, nil
	}
	t.Cleanup(func() {
		mgr.pool.recycler.rssProbe = originalProbe
	})

	mgr.pool.recycler.check()

	if got := factory.callCount(); got != 1 {
		t.Fatalf("idle workspace was RSS-recycled and recreated; factory calls = %d, want 1", got)
	}
	if got := len(snapshotWorkspaceClients(scoped)); got != 0 {
		t.Fatalf("idle workspace clients after recycler check = %d, want 0", got)
	}
	if first := factory.clientAt(t, 0); !first.closed {
		t.Fatalf("idle workspace client was not closed")
	}
	assertIdleRecyclerDebugLog(t, logs.String())
}

func assertIdleRecyclerDebugLog(t *testing.T, logText string) {
	t.Helper()
	for _, want := range []string{
		"LSP recycler idle window exceeded",
		`"idle_timeout":"10m0s"`,
		`"action":"shutdown"`,
		`"pid":4242`,
		`"rss_bytes":402653185`,
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("idle recycler debug log missing %q; log=%s", want, logText)
		}
	}
}

func forceWorkspaceLastActivity(t *testing.T, mgr *manager, client Client, at time.Time) {
	t.Helper()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	for _, workspace := range mgr.workspaces {
		if workspace != nil && workspace.client == client {
			workspace.lastActivity = at
			return
		}
	}
	t.Fatalf("workspace for client %T was not found", client)
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
	client, err := scoped.EnsureClient(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, "go")
	if err != nil {
		t.Fatalf("EnsureClient(scoped): %v", err)
	}
	mgr.pool.acquire(client)

	busy, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{ScopeKind: ReleaseScopeAgentThread, AgentID: "agent-busy", ThreadID: "thread-1", Reason: "busy_check"})
	if err != nil {
		t.Fatalf("ReleaseScope(busy): %v", err)
	}
	assertBusyReleaseScopeResult(t, busy, scoped)
	mgr.pool.release(client)

	drained, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{ScopeKind: ReleaseScopeAgentThread, AgentID: "agent-busy", ThreadID: "thread-1", Drain: true})
	if err != nil {
		t.Fatalf("ReleaseScope(drain): %v", err)
	}
	assertDrainedReleaseScopeResult(t, drained, scoped)
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
	coordinator := mustBootstrapCoordinator(t, scoped)
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
	scoped.coordinatorMu.Lock()
	hasCoordinator := scoped.coordinator != nil
	scoped.coordinatorMu.Unlock()
	if hasCoordinator {
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
	client, err := active.EnsureClient(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, "go")
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

	firstClient, err := mgr.EnsureClient(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, "typescript")
	if err != nil {
		t.Fatalf("EnsureClient(first): %v", err)
	}
	firstClient.(*p2LifecycleClient).markUnhealthy()
	if _, err := mgr.EnsureClient(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, "typescript"); err != nil {
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
	if err := mgr.BootstrapDocument(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target); err != nil {
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
