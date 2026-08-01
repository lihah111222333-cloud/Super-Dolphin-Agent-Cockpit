package multilsp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

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

func TestManagerPoolCapDetachRejectsNewClientBeforeClose(t *testing.T) {
	t.Setenv(lspPoolSizeEnv, "1")
	t.Setenv("AGENT_LSP_POOL_SHARD_CAP", "2")
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module cap-gate\n\ngo 1.25.0\n")
	target := filepath.Join(root, "main.go")
	writeGenericTestFile(t, target, "package main\n")
	factory := &p2LifecycleFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	firstScope := testLSPToolScope(root, "agent-cap-old", "thread")
	secondScope := testLSPToolScope(root, "agent-cap-keep", "thread")
	first := scopedManagerForTest(t, mgr, firstScope)
	_ = scopedManagerForTest(t, mgr, secondScope)
	firstResolved, err := ResolveLSPToolScope(firstScope)
	if err != nil {
		t.Fatalf("ResolveLSPToolScope(first): %v", err)
	}
	secondResolved, err := ResolveLSPToolScope(secondScope)
	if err != nil {
		t.Fatalf("ResolveLSPToolScope(second): %v", err)
	}
	shard := mgr.pool.shardForKey(firstResolved.ShardKey)
	shard.mu.Lock()
	shard.clones[firstResolved.ManagerKey].lastUsedAt = time.Now().Add(-time.Minute)
	mgr.pool.shardCap = 1
	toClose := mgr.pool.evictIdleClonesLocked(shard, secondResolved.ManagerKey)
	shard.mu.Unlock()
	t.Cleanup(func() {
		for _, release := range toClose {
			if release.manager != nil {
				_ = release.manager.closeWithoutPool()
			}
		}
	})
	if len(toClose) != 1 || toClose[0].manager != first {
		t.Fatalf("evictIdleClonesLocked() = %#v, want first manager", toClose)
	}

	_, err = first.EnsureClient(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), target, "go")
	if !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("EnsureClient() error = %v, want ErrManagerClosed after cap detach", err)
	}
	if got := factory.callCount(); got != 0 {
		t.Fatalf("factory calls after cap detach = %d, want 0", got)
	}
}

func TestManagerPoolCapEvictionPropagatesCloseFailure(t *testing.T) {
	t.Setenv(lspPoolSizeEnv, "1")
	t.Setenv("AGENT_LSP_POOL_SHARD_CAP", "1")
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	firstScope := testLSPToolScope(root, "agent-cap-close-failure", "thread-old")
	first := scopedManagerForTest(t, mgr, firstScope)
	closeErr := errors.New("cap eviction close failed")
	first.mu.Lock()
	first.workspaces["close-failure"] = &workspaceClient{
		key:    "close-failure",
		client: &failingCloseP2Client{p2LifecycleClient: &p2LifecycleClient{}, err: closeErr},
	}
	first.mu.Unlock()

	_, err := mgr.pool.ForScope(testLSPToolScope(root, "agent-cap-close-failure", "thread-new"))
	if !errors.Is(err, closeErr) {
		t.Fatalf("ForScope() error = %v, want %v", err, closeErr)
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
