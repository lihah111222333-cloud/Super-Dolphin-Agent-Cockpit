package multilsp

import (
	"strings"
	"testing"
)

func TestResolveLSPToolScopeCanonicalKeysStable(t *testing.T) {
	root := t.TempDir()
	scopeA := LSPToolScope{
		AgentID:               " agent-a ",
		ThreadID:              " thread-1 ",
		TurnID:                "turn-1",
		CallID:                "call-1",
		LanguageID:            "GO",
		WorkspaceRoot:         root,
		LanguageWorkspaceRoot: root,
		ProjectRoot:           root,
		RootKind:              "GO_MOD",
		LanguageSpecific: map[string]string{
			"moduleRoot": root,
			"goWorkPath": "/tmp/go.work",
		},
	}
	scopeB := scopeA
	scopeB.TurnID = "turn-2"
	scopeB.CallID = "call-2"
	scopeB.LanguageSpecific = map[string]string{
		"goWorkPath": "/tmp/go.work",
		"moduleRoot": root,
	}

	resolvedA, err := ResolveLSPToolScope(scopeA)
	if err != nil {
		t.Fatalf("ResolveLSPToolScope(scopeA): %v", err)
	}
	resolvedB, err := ResolveLSPToolScope(scopeB)
	if err != nil {
		t.Fatalf("ResolveLSPToolScope(scopeB): %v", err)
	}

	if resolvedA.ScopeKey == "" {
		t.Fatalf("ScopeKey is empty for trusted agent/thread identity")
	}
	if !strings.Contains(resolvedA.ScopeKey, "agent-a") || !strings.Contains(resolvedA.ScopeKey, "thread-1") {
		t.Fatalf("ScopeKey = %q, want canonical agent/thread identity", resolvedA.ScopeKey)
	}
	if resolvedA.WorkspaceKey != resolvedB.WorkspaceKey {
		t.Fatalf("WorkspaceKey changed when map insertion order changed:\nA=%q\nB=%q", resolvedA.WorkspaceKey, resolvedB.WorkspaceKey)
	}
	if resolvedA.ManagerKey != resolvedB.ManagerKey {
		t.Fatalf("ManagerKey changed across turn/call IDs:\nA=%q\nB=%q", resolvedA.ManagerKey, resolvedB.ManagerKey)
	}
	if strings.Contains(resolvedA.ManagerKey, "turn-") || strings.Contains(resolvedA.ManagerKey, "call-") {
		t.Fatalf("ManagerKey must not include turn/call identity: %q", resolvedA.ManagerKey)
	}
}

func TestManagerPoolForScopeReusesSameScopedManager(t *testing.T) {
	root := t.TempDir()
	otherRoot := t.TempDir()
	mgr := newManagerPoolTestManager(t, root)

	first, err := mgr.pool.ForScope(testLSPToolScope(root, "agent-a", "thread-1"))
	if err != nil {
		t.Fatalf("ForScope(first): %v", err)
	}
	secondScope := testLSPToolScope(root, "agent-a", "thread-1")
	secondScope.TurnID = "turn-2"
	secondScope.CallID = "call-2"
	second, err := mgr.pool.ForScope(secondScope)
	if err != nil {
		t.Fatalf("ForScope(second): %v", err)
	}
	if first.Manager != second.Manager {
		t.Fatalf("same agent/thread/workspace should reuse manager: first=%p second=%p", first.Manager, second.Manager)
	}
	if first.ResolvedScope.ManagerKey != second.ResolvedScope.ManagerKey {
		t.Fatalf("same agent/thread/workspace should reuse ManagerKey")
	}

	otherAgent, err := mgr.pool.ForScope(testLSPToolScope(root, "agent-b", "thread-1"))
	if err != nil {
		t.Fatalf("ForScope(other agent): %v", err)
	}
	if otherAgent.Manager == first.Manager {
		t.Fatalf("different trusted agent should get an isolated scoped manager")
	}
	if otherAgent.ResolvedScope.ManagerKey == first.ResolvedScope.ManagerKey {
		t.Fatalf("different trusted agent should get a different ManagerKey")
	}

	otherWorkspace, err := mgr.pool.ForScope(testLSPToolScope(otherRoot, "agent-a", "thread-1"))
	if err != nil {
		t.Fatalf("ForScope(other workspace): %v", err)
	}
	if otherWorkspace.Manager == first.Manager {
		t.Fatalf("same agent with different workspace should get a different manager")
	}
	if otherWorkspace.ResolvedScope.WorkspaceKey == first.ResolvedScope.WorkspaceKey {
		t.Fatalf("different workspace should get a different WorkspaceKey")
	}
}

func TestManagerPoolForScopeFallsBackToWorkspaceOnlyWithoutTrustedIdentity(t *testing.T) {
	root := t.TempDir()
	mgr := newManagerPoolTestManager(t, root)
	scope := testLSPToolScope(root, "", "")

	first, err := mgr.pool.ForScope(scope)
	if err != nil {
		t.Fatalf("ForScope(first): %v", err)
	}
	second, err := mgr.pool.ForScope(scope)
	if err != nil {
		t.Fatalf("ForScope(second): %v", err)
	}
	if first.ResolvedScope.ScopeKey != "" {
		t.Fatalf("ScopeKey without trusted identity = %q, want empty", first.ResolvedScope.ScopeKey)
	}
	if first.ResolvedScope.ManagerKey != first.ResolvedScope.WorkspaceKey {
		t.Fatalf("workspace-only fallback ManagerKey=%q WorkspaceKey=%q", first.ResolvedScope.ManagerKey, first.ResolvedScope.WorkspaceKey)
	}
	if first.Manager != second.Manager {
		t.Fatalf("workspace-only fallback should reuse manager: first=%p second=%p", first.Manager, second.Manager)
	}
}

func TestManagerPoolShardCollisionKeepsDistinctClones(t *testing.T) {
	root := t.TempDir()
	primary := newManagerPoolTestManager(t, root)
	pool := NewManagerPool(primary, 1)
	primary.pool = pool

	first, err := pool.ForScope(testLSPToolScope(root, "agent-a", "thread-1"))
	if err != nil {
		t.Fatalf("ForScope(first): %v", err)
	}
	second, err := pool.ForScope(testLSPToolScope(root, "agent-b", "thread-1"))
	if err != nil {
		t.Fatalf("ForScope(second): %v", err)
	}
	if first.ResolvedScope.ShardKey == second.ResolvedScope.ShardKey {
		t.Fatalf("test setup expected distinct shard keys before modulo collision")
	}
	if first.Manager == second.Manager {
		t.Fatalf("same shard collision must not share manager clones")
	}

	snapshots := pool.SnapshotManagers()
	baseCount := 0
	cloneKeys := map[string]struct{}{}
	for _, snapshot := range snapshots {
		if snapshot.index != 0 {
			t.Fatalf("pool size 1 should only snapshot shard 0, got shard %d", snapshot.index)
		}
		if snapshot.base {
			baseCount++
			continue
		}
		cloneKeys[snapshot.managerKey] = struct{}{}
	}
	if baseCount != 1 {
		t.Fatalf("base snapshot count = %d, want 1", baseCount)
	}
	if _, ok := cloneKeys[first.ResolvedScope.ManagerKey]; !ok {
		t.Fatalf("first clone ManagerKey %q missing from snapshots", first.ResolvedScope.ManagerKey)
	}
	if _, ok := cloneKeys[second.ResolvedScope.ManagerKey]; !ok {
		t.Fatalf("second clone ManagerKey %q missing from snapshots", second.ResolvedScope.ManagerKey)
	}
}

func TestManagerPoolWorkspaceCloneRootAndClose(t *testing.T) {
	root := t.TempDir()
	mgr := newManagerPoolTestManager(t, root)
	scoped, err := mgr.pool.ForScope(testLSPToolScope(root, "agent-a", "thread-1"))
	if err != nil {
		t.Fatalf("ForScope: %v", err)
	}
	clone, ok := scoped.Manager.(*manager)
	if !ok {
		t.Fatalf("scoped manager type = %T, want *manager", scoped.Manager)
	}
	if clone.workspaceRoot != scoped.ResolvedScope.LanguageWorkspaceRoot {
		t.Fatalf("clone workspaceRoot = %q, want %q", clone.workspaceRoot, scoped.ResolvedScope.LanguageWorkspaceRoot)
	}
	if err := mgr.pool.Close(); err != nil {
		t.Fatalf("pool.Close(): %v", err)
	}
	if !managerIsClosed(clone) {
		t.Fatalf("scoped clone was not closed by pool.Close")
	}
}

func newManagerPoolTestManager(t *testing.T, root string) *manager {
	t.Helper()
	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	t.Cleanup(func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("manager.Close(): %v", err)
		}
	})
	return mgr
}

func testLSPToolScope(root, agentID, threadID string) LSPToolScope {
	return LSPToolScope{
		AgentID:               agentID,
		ThreadID:              threadID,
		LanguageID:            "go",
		WorkspaceRoot:         root,
		LanguageWorkspaceRoot: root,
		ProjectRoot:           root,
		RootKind:              "go_mod",
		LanguageSpecific: map[string]string{
			"moduleRoot": root,
		},
	}
}
