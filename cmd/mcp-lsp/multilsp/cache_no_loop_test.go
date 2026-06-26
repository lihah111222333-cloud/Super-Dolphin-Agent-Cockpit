package multilsp

import (
	"testing"
	"time"
)

// TestNewLSPCacheStoreDoesNotSpawnCleanupGoroutine 确认缓存构造函数不会启动后台清理 goroutine。
// 过期清理由访问入口摊销执行，测试用 goroutine 数量边界防止缓存生命周期回退。
func TestNewLSPCacheStoreDoesNotSpawnCleanupGoroutine(t *testing.T) {
	store := mustLSPCacheStore(t, lspCacheConfig{TTL: 5 * time.Millisecond})
	if store == nil {
		t.Fatalf("newLSPCacheStore returned nil")
	}

	// Close 当前只保留幂等关闭钩子；连续调用不应 panic 或泄漏资源。
	store.Close()
	store.Close()
}

// TestLSPCacheStoreMaybeCleanupPurgesExpiredInline 确认没有后台清理循环时，过期项会在下一次访问时被就地清除。
// Load/Upsert/WorkspaceDocuments 都会承担这段清理预算，避免旧缓存长期留在内存中。
func TestLSPCacheStoreMaybeCleanupPurgesExpiredInline(t *testing.T) {
	now := time.Unix(1_000, 0).UTC()
	store := mustLSPCacheStore(t, lspCacheConfig{TTL: 10 * time.Second})
	// 固定时钟，便于测试精确推进 TTL 过期边界。
	store.now = func() time.Time { return now }

	key := lspCacheKey{Workspace: "ws", Language: "go", URI: "file:///a.go"}
	store.Upsert(lspCacheValue{Key: key, Fingerprint: "v1"})
	if _, ok := store.Load(key); !ok {
		t.Fatalf("Load() after Upsert = !ok, want ok")
	}

	// 推进到 TTL 之后；下一次读取应同步淘汰内存项。
	now = now.Add(30 * time.Second)
	if _, ok := store.Load(key); ok {
		t.Fatalf("Load() after TTL = ok, want evicted")
	}
	store.mu.RLock()
	remaining := len(store.memory)
	store.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("memory size after expiry = %d, want 0", remaining)
	}
}

func TestLSPCacheKeySeparatesScopeAndWorkspace(t *testing.T) {
	uri := "file:///a.go"
	agentA := lspCacheKey{ScopeKey: "scope-a", WorkspaceKey: "ws", LanguageID: "go", URI: uri}
	agentB := lspCacheKey{ScopeKey: "scope-b", WorkspaceKey: "ws", LanguageID: "go", URI: uri}
	workspaceB := lspCacheKey{ScopeKey: "scope-a", WorkspaceKey: "ws-b", LanguageID: "go", URI: uri}
	if agentA.String() == agentB.String() {
		t.Fatalf("cache key did not include ScopeKey")
	}
	if agentA.String() == workspaceB.String() {
		t.Fatalf("cache key did not include WorkspaceKey")
	}
}

func TestLSPCacheUsesResolvedScopeLanguageSpecificHash(t *testing.T) {
	scope := ResolvedLSPToolScope{
		LSPToolScope: LSPToolScope{
			LanguageID:       "go",
			LanguageSpecific: map[string]string{"goWorkPath": "/repo/go.work"},
		},
		ScopeKey:     "scope-a",
		WorkspaceKey: "workspace-a",
	}
	withGoWork := scope.cacheKey("go", "file:///repo/a.go")
	scope.LanguageSpecific = map[string]string{"goWorkPath": "/repo/other.go.work"}
	withOtherGoWork := scope.cacheKey("go", "file:///repo/a.go")
	if withGoWork.LanguageSpecificHash == "" {
		t.Fatalf("LanguageSpecificHash is empty for scoped language-specific cache key")
	}
	if withGoWork.String() == withOtherGoWork.String() {
		t.Fatalf("cache key did not include language-specific hash")
	}
}

func TestLSPCacheTombstoneSuppressesDeletedDocumentLoad(t *testing.T) {
	now := time.Unix(2_000, 0).UTC()
	store := mustLSPCacheStore(t, lspCacheConfig{TTL: 10 * time.Second})
	store.now = func() time.Time { return now }

	key := lspCacheKey{ScopeKey: "scope", WorkspaceKey: "workspace", LanguageID: "go", URI: "file:///deleted.go"}
	store.Upsert(lspCacheValue{Key: key, Fingerprint: "before-delete"})
	store.Tombstone(key)
	if _, ok := store.Load(key); ok {
		t.Fatalf("Load() after Tombstone = ok, want deleted-document tombstone to suppress cache")
	}
}

func TestLSPCacheSnapshotMatchRejectsStaleFingerprint(t *testing.T) {
	key := lspCacheKey{ScopeKey: "scope", WorkspaceKey: "workspace", LanguageID: "go", URI: "file:///stale.go"}
	value := lspCacheValue{Key: key, Fingerprint: "old", Size: 12}
	snapshot := documentSnapshot{fingerprint: "new", size: 12}
	if cacheValueMatchesSnapshot(value, snapshot) {
		t.Fatalf("cacheValueMatchesSnapshot accepted stale fingerprint")
	}
	snapshot.fingerprint = "old"
	if !cacheValueMatchesSnapshot(value, snapshot) {
		t.Fatalf("cacheValueMatchesSnapshot rejected matching fingerprint")
	}
}
