package multilsp

import (
	"path/filepath"
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

	resolvedA := resolveLSPToolScopeForTest(t, "scopeA", scopeA)
	resolvedB := resolveLSPToolScopeForTest(t, "scopeB", scopeB)

	assertScopeKeyHasTrustedIdentity(t, resolvedA.ScopeKey)
	assertEqualString(t, "WorkspaceKey changed when map insertion order changed", resolvedA.WorkspaceKey, resolvedB.WorkspaceKey)
	assertEqualString(t, "ManagerKey changed across turn/call IDs", resolvedA.ManagerKey, resolvedB.ManagerKey)
	assertKeyOmitsTurnCall(t, "WorkspaceKey", resolvedA.WorkspaceKey)
	assertKeyOmitsTurnCall(t, "ManagerKey", resolvedA.ManagerKey)
}

func TestResolveLSPToolScopeGoTopologyLanguageSpecificStable(t *testing.T) {
	repo := canonicalScopePath(t.TempDir(), "")
	backend := filepath.Join(repo, "backend")
	tools := filepath.Join(repo, "tools")
	infoA := GoRootInfo{
		RootKind:      goRootKindGoWork,
		WorkspaceRoot: repo,
		GoWorkPath:    filepath.Join(repo, "go.work"),
		ModuleRoot:    backend,
		GoModPath:     filepath.Join(backend, "go.mod"),
		ModuleRoots:   []string{tools, backend},
		GOWORKMode:    goworkModeAuto,
		ProjectRoot:   repo,
	}
	infoB := infoA
	infoB.ModuleRoots = []string{backend, tools}
	specificA := goLanguageSpecific(infoA)
	specificB := goLanguageSpecific(infoB)

	scopeA := LSPToolScope{
		AgentID:               "agent-a",
		ThreadID:              "thread-1",
		TurnID:                "turn-1",
		CallID:                "call-1",
		LanguageID:            "go",
		WorkspaceRoot:         infoA.WorkspaceRoot,
		LanguageWorkspaceRoot: infoA.ModuleRoot,
		ProjectRoot:           infoA.ProjectRoot,
		RootKind:              infoA.RootKind,
		LanguageSpecific:      specificA,
	}
	scopeB := scopeA
	scopeB.TurnID = "turn-2"
	scopeB.CallID = "call-2"
	scopeB.LanguageSpecific = map[string]string{
		"workspaceFoldersHash": specificB["workspaceFoldersHash"],
		"moduleRootsHash":      specificB["moduleRootsHash"],
		"goWorkPath":           specificB["goWorkPath"],
		"moduleRoot":           specificB["moduleRoot"],
		"goworkMode":           specificB["goworkMode"],
		"goModPath":            specificB["goModPath"],
	}

	resolvedA, err := ResolveLSPToolScope(scopeA)
	if err != nil {
		t.Fatalf("ResolveLSPToolScope(scopeA): %v", err)
	}
	resolvedB, err := ResolveLSPToolScope(scopeB)
	if err != nil {
		t.Fatalf("ResolveLSPToolScope(scopeB): %v", err)
	}
	if resolvedA.WorkspaceKey != resolvedB.WorkspaceKey {
		t.Fatalf("WorkspaceKey changed when Go topology order changed:\nA=%q\nB=%q", resolvedA.WorkspaceKey, resolvedB.WorkspaceKey)
	}
	for _, key := range []string{"goModPath", "goWorkPath", "goworkMode", "moduleRoot", "moduleRootsHash", "workspaceFoldersHash"} {
		if got := resolvedA.LanguageSpecific[key]; got != specificA[key] {
			t.Fatalf("LanguageSpecific[%s] = %q, want %q", key, got, specificA[key])
		}
	}

	parts := strings.Split(resolvedA.WorkspaceKey, scopeKeySeparator)
	expectedSuffix := []string{
		"goModPath=" + specificA["goModPath"],
		"goWorkPath=" + specificA["goWorkPath"],
		"goworkMode=" + specificA["goworkMode"],
		"moduleRoot=" + specificA["moduleRoot"],
		"moduleRootsHash=" + specificA["moduleRootsHash"],
		"workspaceFoldersHash=" + specificA["workspaceFoldersHash"],
	}
	if len(parts) != 5+len(expectedSuffix) {
		t.Fatalf("WorkspaceKey parts = %#v, want 5 canonical fields plus Go topology suffix", parts)
	}
	expectedPrefix := []string{"go", goRootKindGoWork, repo, backend, repo}
	for i, want := range expectedPrefix {
		if parts[i] != want {
			t.Fatalf("WorkspaceKey part[%d] = %q, want %q in %q", i, parts[i], want, resolvedA.WorkspaceKey)
		}
	}
	if got, want := strings.Join(parts[5:], scopeKeySeparator), strings.Join(expectedSuffix, scopeKeySeparator); got != want {
		t.Fatalf("Go topology suffix not sorted/stable:\ngot  %q\nwant %q", got, want)
	}
}

func TestManagerPoolForScopeReusesSameScopedManager(t *testing.T) {
	root := t.TempDir()
	otherRoot := t.TempDir()
	mgr := newManagerPoolTestManager(t, root)

	first := scopedPoolManagerForTest(t, mgr.pool, "first", testLSPToolScope(root, "agent-a", "thread-1"))
	secondScope := testLSPToolScope(root, "agent-a", "thread-1")
	secondScope.TurnID = "turn-2"
	secondScope.CallID = "call-2"
	second := scopedPoolManagerForTest(t, mgr.pool, "second", secondScope)
	assertSameManager(t, "same agent/thread/workspace should reuse manager", first.Manager, second.Manager)
	assertEqualString(t, "same agent/thread/workspace should reuse ManagerKey", first.ResolvedScope.ManagerKey, second.ResolvedScope.ManagerKey)

	otherAgent := scopedPoolManagerForTest(t, mgr.pool, "other agent", testLSPToolScope(root, "agent-b", "thread-1"))
	assertDifferentManager(t, "different trusted agent should get an isolated scoped manager", otherAgent.Manager, first.Manager)
	assertDifferentString(t, "different trusted agent should get a different ManagerKey", otherAgent.ResolvedScope.ManagerKey, first.ResolvedScope.ManagerKey)

	otherWorkspace := scopedPoolManagerForTest(t, mgr.pool, "other workspace", testLSPToolScope(otherRoot, "agent-a", "thread-1"))
	assertDifferentManager(t, "same agent with different workspace should get a different manager", otherWorkspace.Manager, first.Manager)
	assertDifferentString(t, "different workspace should get a different WorkspaceKey", otherWorkspace.ResolvedScope.WorkspaceKey, first.ResolvedScope.WorkspaceKey)
}

func TestManagerPoolForScopeStableShard(t *testing.T) {
	root := t.TempDir()
	mgr := newManagerPoolTestManager(t, root)

	firstScope := testLSPToolScope(root, "agent-stable", "thread-stable")
	firstScope.TurnID = "turn-1"
	firstScope.CallID = "call-1"
	secondScope := firstScope
	secondScope.TurnID = "turn-2"
	secondScope.CallID = "call-2"

	first := scopedPoolManagerForTest(t, mgr.pool, "first", firstScope)
	second := scopedPoolManagerForTest(t, mgr.pool, "second", secondScope)

	if first.ResolvedScope.ShardKey == "" {
		t.Fatalf("ShardKey is empty for trusted scope: %#v", first.ResolvedScope)
	}
	assertEqualString(t, "ShardKey changed across turn/call IDs", first.ResolvedScope.ShardKey, second.ResolvedScope.ShardKey)
	firstShard := shardIndexForKey(first.ResolvedScope.ShardKey, mgr.pool.Size())
	secondShard := shardIndexForKey(second.ResolvedScope.ShardKey, mgr.pool.Size())
	if firstShard != secondShard {
		t.Fatalf("same stable ShardKey routed to different shards: first=%d second=%d", firstShard, secondShard)
	}
	assertSameManager(t, "stable shard should reuse scoped clone", first.Manager, second.Manager)
	assertShardHasScopedClone(t, mgr, firstShard, first.ResolvedScope.ManagerKey, first.Manager)
}

func TestManagerPoolWorkspaceCloneIsolation(t *testing.T) {
	root := t.TempDir()
	otherRoot := t.TempDir()
	mgr := newManagerPoolTestManager(t, root)

	base := scopedPoolManagerForTest(t, mgr.pool, "base", testLSPToolScope(root, "agent-a", "thread-1"))
	same := scopedPoolManagerForTest(t, mgr.pool, "same", testLSPToolScope(root, "agent-a", "thread-1"))
	otherAgent := scopedPoolManagerForTest(t, mgr.pool, "other agent", testLSPToolScope(root, "agent-b", "thread-1"))
	otherWorkspace := scopedPoolManagerForTest(t, mgr.pool, "other workspace", testLSPToolScope(otherRoot, "agent-a", "thread-1"))

	assertSameManager(t, "same agent/workspace should reuse clone", base.Manager, same.Manager)
	assertDifferentManager(t, "different agent reused base clone", otherAgent.Manager, base.Manager)
	assertDifferentManager(t, "different workspace reused base clone", otherWorkspace.Manager, base.Manager)
	assertDifferentString(t, "different agent kept ManagerKey", otherAgent.ResolvedScope.ManagerKey, base.ResolvedScope.ManagerKey)
	assertDifferentString(t, "different workspace kept WorkspaceKey", otherWorkspace.ResolvedScope.WorkspaceKey, base.ResolvedScope.WorkspaceKey)

	baseClone := managerCloneForTest(t, "base manager", base.Manager)
	otherWorkspaceClone := managerCloneForTest(t, "other workspace manager", otherWorkspace.Manager)
	if baseClone.workspaceRoot != base.ResolvedScope.WorkspaceRoot {
		t.Fatalf("base clone workspaceRoot = %q, want %q", baseClone.workspaceRoot, base.ResolvedScope.WorkspaceRoot)
	}
	if otherWorkspaceClone.workspaceRoot != otherWorkspace.ResolvedScope.WorkspaceRoot {
		t.Fatalf("other workspace clone workspaceRoot = %q, want %q", otherWorkspaceClone.workspaceRoot, otherWorkspace.ResolvedScope.WorkspaceRoot)
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

	first := scopedPoolManagerForTest(t, pool, "first", testLSPToolScope(root, "agent-a", "thread-1"))
	second := scopedPoolManagerForTest(t, pool, "second", testLSPToolScope(root, "agent-b", "thread-1"))
	assertDifferentString(t, "test setup expected distinct shard keys before modulo collision", first.ResolvedScope.ShardKey, second.ResolvedScope.ShardKey)
	assertDifferentManager(t, "same shard collision must not share manager clones", first.Manager, second.Manager)

	snapshots := pool.SnapshotManagers()
	assertCollisionSnapshots(t, snapshots, first.ResolvedScope.ManagerKey, second.ResolvedScope.ManagerKey)
}

func TestManagerPoolGoWorkWorkspaceCloneUsesWorkspaceRootAndClose(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	moduleRoot := filepath.Join(root, "backend")
	info := GoRootInfo{
		RootKind:      goRootKindGoWork,
		WorkspaceRoot: root,
		GoWorkPath:    filepath.Join(root, "go.work"),
		ModuleRoot:    moduleRoot,
		GoModPath:     filepath.Join(moduleRoot, "go.mod"),
		ModuleRoots:   []string{moduleRoot},
		GOWORKMode:    goworkModeAuto,
		ProjectRoot:   root,
	}
	mgr := newManagerPoolTestManager(t, root)
	scoped, err := mgr.pool.ForScope(LSPToolScope{
		AgentID:               "agent-a",
		ThreadID:              "thread-1",
		LanguageID:            "go",
		WorkspaceRoot:         info.WorkspaceRoot,
		LanguageWorkspaceRoot: info.ModuleRoot,
		ProjectRoot:           info.ProjectRoot,
		RootKind:              info.RootKind,
		LanguageSpecific:      goLanguageSpecific(info),
	})
	if err != nil {
		t.Fatalf("ForScope: %v", err)
	}
	clone, ok := scoped.Manager.(*manager)
	if !ok {
		t.Fatalf("scoped manager type = %T, want *manager", scoped.Manager)
	}
	if clone.workspaceRoot != scoped.ResolvedScope.WorkspaceRoot {
		t.Fatalf("go.work clone workspaceRoot = %q, want canonical WorkspaceRoot %q", clone.workspaceRoot, scoped.ResolvedScope.WorkspaceRoot)
	}
	if scoped.ResolvedScope.LanguageWorkspaceRoot != moduleRoot {
		t.Fatalf("LanguageWorkspaceRoot = %q, want module root %q", scoped.ResolvedScope.LanguageWorkspaceRoot, moduleRoot)
	}
	workspaceKeyParts := strings.Split(scoped.ResolvedScope.WorkspaceKey, scopeKeySeparator)
	if len(workspaceKeyParts) < 5 || workspaceKeyParts[3] != moduleRoot {
		t.Fatalf("WorkspaceKey must retain module root as LanguageWorkspaceRoot dimension: %q", scoped.ResolvedScope.WorkspaceKey)
	}
	if err := mgr.pool.Close(); err != nil {
		t.Fatalf("pool.Close(): %v", err)
	}
	if !managerIsClosed(clone) {
		t.Fatalf("scoped clone was not closed by pool.Close")
	}
}

func TestAgentStopTriggersLSPReleaseScope(t *testing.T) {
	root := t.TempDir()
	mgr := newManagerPoolTestManager(t, root)

	agentGo, err := mgr.pool.ForScope(testLSPToolScope(root, "agent-stop", "thread-1"))
	if err != nil {
		t.Fatalf("ForScope(agent go): %v", err)
	}
	agentTS, err := mgr.pool.ForScope(testLSPToolScopeForLanguage(root, "agent-stop", "thread-2", "typescript"))
	if err != nil {
		t.Fatalf("ForScope(agent typescript): %v", err)
	}
	otherAgent, err := mgr.pool.ForScope(testLSPToolScopeForLanguage(root, "agent-keep", "thread-1", "python"))
	if err != nil {
		t.Fatalf("ForScope(other agent): %v", err)
	}

	agentGoManager := agentGo.Manager.(*manager)
	agentTSManager := agentTS.Manager.(*manager)
	otherManager := otherAgent.Manager.(*manager)
	result, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{
		ScopeKind: ReleaseScopeAgentAllThreads,
		AgentID:   "agent-stop",
		Drain:     true,
		Reason:    "agent/stopped",
	})
	if err != nil {
		t.Fatalf("ReleaseScope(agent stop): %v", err)
	}
	assertAgentReleaseResult(t, result)
	assertManagerClosed(t, agentGoManager, "agent go manager was not closed by ReleaseScope")
	assertManagerClosed(t, agentTSManager, "agent TypeScript manager was not closed by ReleaseScope")
	assertManagerOpen(t, otherManager, "ReleaseScope closed unrelated agent/language manager")
	assertReleasedAgentAbsent(t, mgr.pool.SnapshotManagers(), "agent-stop")
}

func resolveLSPToolScopeForTest(t *testing.T, label string, scope LSPToolScope) ResolvedLSPToolScope {
	t.Helper()
	resolved, err := ResolveLSPToolScope(scope)
	if err != nil {
		t.Fatalf("ResolveLSPToolScope(%s): %v", label, err)
	}
	return resolved
}

func scopedPoolManagerForTest(t *testing.T, pool *ManagerPool, label string, scope LSPToolScope) ScopedManager {
	t.Helper()
	scoped, err := pool.ForScope(scope)
	if err != nil {
		t.Fatalf("ForScope(%s): %v", label, err)
	}
	return scoped
}

func assertScopeKeyHasTrustedIdentity(t *testing.T, scopeKey string) {
	t.Helper()
	if scopeKey == "" {
		t.Fatalf("ScopeKey is empty for trusted agent/thread identity")
	}
	if !strings.Contains(scopeKey, "agent-a") || !strings.Contains(scopeKey, "thread-1") {
		t.Fatalf("ScopeKey = %q, want canonical agent/thread identity", scopeKey)
	}
}

func assertKeyOmitsTurnCall(t *testing.T, label, key string) {
	t.Helper()
	if strings.Contains(key, "turn-") || strings.Contains(key, "call-") {
		t.Fatalf("%s must not include turn/call identity: %q", label, key)
	}
}

func assertEqualString(t *testing.T, message, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s:\ngot  %q\nwant %q", message, got, want)
	}
}

func assertDifferentString(t *testing.T, message, got, unexpected string) {
	t.Helper()
	if got == unexpected {
		t.Fatalf("%s: %q", message, got)
	}
}

func assertSameManager(t *testing.T, message string, got, want Manager) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got=%p want=%p", message, got, want)
	}
}

func assertDifferentManager(t *testing.T, message string, got, unexpected Manager) {
	t.Helper()
	if got == unexpected {
		t.Fatalf("%s: %p", message, got)
	}
}

func managerCloneForTest(t *testing.T, label string, candidate Manager) *manager {
	t.Helper()
	clone, ok := candidate.(*manager)
	if !ok {
		t.Fatalf("%s type = %T, want *manager", label, candidate)
	}
	return clone
}

func assertShardHasScopedClone(t *testing.T, mgr *manager, shard int, managerKey string, manager Manager) {
	t.Helper()
	for _, clone := range mgr.pool.shards[shard].snapshotClones() {
		if clone.key == managerKey && clone.manager == manager {
			return
		}
	}
	t.Fatalf("stable shard %d missing scoped clone for ManagerKey %q", shard, managerKey)
}

func assertCollisionSnapshots(t *testing.T, snapshots []poolManagerSnapshot, firstKey, secondKey string) {
	t.Helper()
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
	assertCloneKeyPresent(t, cloneKeys, firstKey, "first")
	assertCloneKeyPresent(t, cloneKeys, secondKey, "second")
}

func assertCloneKeyPresent(t *testing.T, cloneKeys map[string]struct{}, key, label string) {
	t.Helper()
	if _, ok := cloneKeys[key]; !ok {
		t.Fatalf("%s clone ManagerKey %q missing from snapshots", label, key)
	}
}

func assertAgentReleaseResult(t *testing.T, result ReleaseScopeResult) {
	t.Helper()
	if result.MatchedManagers != 2 || result.ClosedManagers != 2 || result.BusyLeases != 0 || !result.Drained {
		t.Fatalf("ReleaseScope result = %#v, want matched=2 closed=2 busy=0 drained=true", result)
	}
	if got := result.ManagerKeys; len(got) != 2 {
		t.Fatalf("ReleaseScope manager keys = %#v, want 2 entries", got)
	}
}

func assertManagerClosed(t *testing.T, mgr *manager, message string) {
	t.Helper()
	if !managerIsClosed(mgr) {
		t.Fatalf("%s", message)
	}
}

func assertManagerOpen(t *testing.T, mgr *manager, message string) {
	t.Helper()
	if managerIsClosed(mgr) {
		t.Fatalf("%s", message)
	}
}

func assertReleasedAgentAbsent(t *testing.T, snapshots []poolManagerSnapshot, agentID string) {
	t.Helper()
	for _, snapshot := range snapshots {
		if snapshot.base {
			continue
		}
		if snapshot.resolvedScope.AgentID == agentID {
			t.Fatalf("released agent clone remains in pool snapshot: %#v", snapshot.resolvedScope)
		}
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

func testLSPToolScopeForLanguage(root, agentID, threadID, languageID string) LSPToolScope {
	scope := testLSPToolScope(root, agentID, threadID)
	scope.LanguageID = languageID
	scope.RootKind = "dir_fallback"
	scope.LanguageSpecific = nil
	return scope
}
