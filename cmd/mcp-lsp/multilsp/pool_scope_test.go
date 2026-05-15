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
	if strings.Contains(resolvedA.WorkspaceKey, "turn-") || strings.Contains(resolvedA.WorkspaceKey, "call-") {
		t.Fatalf("WorkspaceKey must not include turn/call identity: %q", resolvedA.WorkspaceKey)
	}
	if strings.Contains(resolvedA.ManagerKey, "turn-") || strings.Contains(resolvedA.ManagerKey, "call-") {
		t.Fatalf("ManagerKey must not include turn/call identity: %q", resolvedA.ManagerKey)
	}
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

func TestManagerPoolForScopeStableShard(t *testing.T) {
	root := t.TempDir()
	mgr := newManagerPoolTestManager(t, root)

	firstScope := testLSPToolScope(root, "agent-stable", "thread-stable")
	firstScope.TurnID = "turn-1"
	firstScope.CallID = "call-1"
	secondScope := firstScope
	secondScope.TurnID = "turn-2"
	secondScope.CallID = "call-2"

	first, err := mgr.pool.ForScope(firstScope)
	if err != nil {
		t.Fatalf("ForScope(first): %v", err)
	}
	second, err := mgr.pool.ForScope(secondScope)
	if err != nil {
		t.Fatalf("ForScope(second): %v", err)
	}

	if first.ResolvedScope.ShardKey == "" {
		t.Fatalf("ShardKey is empty for trusted scope: %#v", first.ResolvedScope)
	}
	if first.ResolvedScope.ShardKey != second.ResolvedScope.ShardKey {
		t.Fatalf("ShardKey changed across turn/call IDs:\nfirst=%q\nsecond=%q", first.ResolvedScope.ShardKey, second.ResolvedScope.ShardKey)
	}
	firstShard := shardIndexForKey(first.ResolvedScope.ShardKey, mgr.pool.Size())
	secondShard := shardIndexForKey(second.ResolvedScope.ShardKey, mgr.pool.Size())
	if firstShard != secondShard {
		t.Fatalf("same stable ShardKey routed to different shards: first=%d second=%d", firstShard, secondShard)
	}
	if first.Manager != second.Manager {
		t.Fatalf("stable shard should reuse scoped clone: first=%p second=%p", first.Manager, second.Manager)
	}

	found := false
	for _, clone := range mgr.pool.shards[firstShard].snapshotClones() {
		if clone.key == first.ResolvedScope.ManagerKey && clone.manager == first.Manager {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("stable shard %d missing scoped clone for ManagerKey %q", firstShard, first.ResolvedScope.ManagerKey)
	}
}

func TestManagerPoolWorkspaceCloneIsolation(t *testing.T) {
	root := t.TempDir()
	otherRoot := t.TempDir()
	mgr := newManagerPoolTestManager(t, root)

	base, err := mgr.pool.ForScope(testLSPToolScope(root, "agent-a", "thread-1"))
	if err != nil {
		t.Fatalf("ForScope(base): %v", err)
	}
	same, err := mgr.pool.ForScope(testLSPToolScope(root, "agent-a", "thread-1"))
	if err != nil {
		t.Fatalf("ForScope(same): %v", err)
	}
	otherAgent, err := mgr.pool.ForScope(testLSPToolScope(root, "agent-b", "thread-1"))
	if err != nil {
		t.Fatalf("ForScope(other agent): %v", err)
	}
	otherWorkspace, err := mgr.pool.ForScope(testLSPToolScope(otherRoot, "agent-a", "thread-1"))
	if err != nil {
		t.Fatalf("ForScope(other workspace): %v", err)
	}

	if base.Manager != same.Manager {
		t.Fatalf("same agent/workspace should reuse clone: base=%p same=%p", base.Manager, same.Manager)
	}
	if otherAgent.Manager == base.Manager {
		t.Fatalf("different agent reused base clone: %p", base.Manager)
	}
	if otherWorkspace.Manager == base.Manager {
		t.Fatalf("different workspace reused base clone: %p", base.Manager)
	}
	if otherAgent.ResolvedScope.ManagerKey == base.ResolvedScope.ManagerKey {
		t.Fatalf("different agent kept ManagerKey %q", base.ResolvedScope.ManagerKey)
	}
	if otherWorkspace.ResolvedScope.WorkspaceKey == base.ResolvedScope.WorkspaceKey {
		t.Fatalf("different workspace kept WorkspaceKey %q", base.ResolvedScope.WorkspaceKey)
	}

	baseClone, ok := base.Manager.(*manager)
	if !ok {
		t.Fatalf("base manager type = %T, want *manager", base.Manager)
	}
	otherWorkspaceClone, ok := otherWorkspace.Manager.(*manager)
	if !ok {
		t.Fatalf("other workspace manager type = %T, want *manager", otherWorkspace.Manager)
	}
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
