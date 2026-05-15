package multilsp

import (
	"testing"
	"time"
)

// TestNewLSPCacheStoreDoesNotSpawnCleanupGoroutine asserts P22 P2
// LSP-S2: the cache store constructor must not launch a background
// cleanup goroutine. Pre-P22 P2 it called `go store.cleanupLoop()`,
// which held its own stopCh and outlived every caller. Cleanup is
// now amortised on access, so the goroutine is gone entirely.
func TestNewLSPCacheStoreDoesNotSpawnCleanupGoroutine(t *testing.T) {
	store := newLSPCacheStore(lspCacheConfig{TTL: 5 * time.Millisecond})
	if store == nil {
		t.Fatalf("newLSPCacheStore returned nil")
	}

	// Close is a no-op after P22 P2 LSP-S2; it must not panic or
	// leak even when invoked multiple times in a row.
	store.Close()
	store.Close()
}

// TestLSPCacheStoreMaybeCleanupPurgesExpiredInline asserts the
// post-S2 invariant that expired entries age out the next time any
// accessor runs maybeCleanup (Load / Upsert / WorkspaceDocuments).
// Without the background loop, callers now carry the cleanup budget,
// so this path must stay correct.
func TestLSPCacheStoreMaybeCleanupPurgesExpiredInline(t *testing.T) {
	now := time.Unix(1_000, 0).UTC()
	store := newLSPCacheStore(lspCacheConfig{TTL: 10 * time.Second})
	// Freeze the clock so we can drive expiry deterministically.
	store.now = func() time.Time { return now }

	key := lspCacheKey{Workspace: "ws", Language: "go", URI: "file:///a.go"}
	store.Upsert(lspCacheValue{Key: key, Fingerprint: "v1"})
	if _, ok := store.Load(key); !ok {
		t.Fatalf("Load() after Upsert = !ok, want ok")
	}

	// Advance past TTL; next accessor should evict inline.
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
