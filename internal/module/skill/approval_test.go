package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestCache(t *testing.T) *ApprovalCache {
	t.Helper()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "skills-trust.json")
	cache, err := NewApprovalCache(path)
	if err != nil {
		t.Fatalf("NewApprovalCache() error = %v", err)
	}
	return cache
}

func TestApprovalCache_NewLoadsEmptyWhenFileMissing(t *testing.T) {
	cache := newTestCache(t)
	if got := cache.Entries(); len(got) != 0 {
		t.Fatalf("expected empty cache, got %d entries", len(got))
	}
	// 文件此时还不存在，这是允许的
	if _, err := os.Stat(cache.Path()); !os.IsNotExist(err) {
		t.Fatalf("path should not exist yet: err=%v", err)
	}
}

func TestApprovalCache_NewRequiresPath(t *testing.T) {
	if _, err := NewApprovalCache(""); err != ErrApprovalCachePathRequired {
		t.Fatalf("expected ErrApprovalCachePathRequired, got %v", err)
	}
}

func TestApprovalCache_ApproveAndLookup(t *testing.T) {
	cache := newTestCache(t)
	hash := strings.Repeat("a", 64)
	entry, err := cache.Approve("foo", hash, TrustProject, "user@local")
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if entry.Name != "foo" || entry.ContentHash != hash || entry.Trust != TrustProject {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.ApprovedAt.IsZero() {
		t.Fatalf("ApprovedAt should be set")
	}

	// Lookup 命中
	got, ok := cache.Lookup("foo", hash)
	if !ok {
		t.Fatalf("lookup miss after approve")
	}
	if got.Name != "foo" || got.ContentHash != hash {
		t.Fatalf("lookup returned wrong entry: %+v", got)
	}

	// 文件确实落盘
	data, err := os.ReadFile(cache.Path())
	if err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	if !strings.Contains(string(data), hash) || !strings.Contains(string(data), "foo") {
		t.Fatalf("file content missing expected fields: %s", data)
	}
}

func TestApprovalCache_HashChangeForcesReApproval_TOCTOU(t *testing.T) {
	cache := newTestCache(t)
	oldHash := strings.Repeat("a", 64)
	newHash := strings.Repeat("b", 64)

	if _, err := cache.Approve("foo", oldHash, TrustProject, ""); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}

	// 旧 hash 能命中
	if _, ok := cache.Lookup("foo", oldHash); !ok {
		t.Fatalf("old hash should hit")
	}
	// 新 hash 必须 miss（TOCTOU 防护）
	if _, ok := cache.Lookup("foo", newHash); ok {
		t.Fatalf("new hash MUST miss; TOCTOU defense broken")
	}
}

func TestApprovalCache_InvalidNameRejected(t *testing.T) {
	cache := newTestCache(t)
	_, err := cache.Approve("../etc", strings.Repeat("a", 64), TrustProject, "")
	if err == nil {
		t.Fatalf("expected error for invalid name")
	}
}

func TestApprovalCache_EmptyHashRejected(t *testing.T) {
	cache := newTestCache(t)
	_, err := cache.Approve("foo", "", TrustProject, "")
	if err == nil {
		t.Fatalf("expected error for empty hash")
	}
}

func TestApprovalCache_RevokeRemovesAllHashes(t *testing.T) {
	cache := newTestCache(t)
	h1 := strings.Repeat("a", 64)
	h2 := strings.Repeat("b", 64)
	if _, err := cache.Approve("foo", h1, TrustProject, ""); err != nil {
		t.Fatalf("approve h1: %v", err)
	}
	if _, err := cache.Approve("foo", h2, TrustProject, ""); err != nil {
		t.Fatalf("approve h2: %v", err)
	}
	if _, err := cache.Approve("bar", h1, TrustUser, ""); err != nil {
		t.Fatalf("approve bar: %v", err)
	}

	removed, err := cache.Revoke("foo")
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d want 2", removed)
	}
	if _, ok := cache.Lookup("foo", h1); ok {
		t.Fatalf("foo@h1 should be revoked")
	}
	if _, ok := cache.Lookup("foo", h2); ok {
		t.Fatalf("foo@h2 should be revoked")
	}
	if _, ok := cache.Lookup("bar", h1); !ok {
		t.Fatalf("bar@h1 must NOT be revoked (different name)")
	}
}

func TestApprovalCache_RevokeNonExistentIsNoop(t *testing.T) {
	cache := newTestCache(t)
	removed, err := cache.Revoke("nonexistent")
	if err != nil {
		t.Fatalf("Revoke non-existent should not error: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d want 0", removed)
	}
}

func TestApprovalCache_ReloadFromFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "skills-trust.json")
	first, err := NewApprovalCache(path)
	if err != nil {
		t.Fatalf("first NewApprovalCache: %v", err)
	}
	hash := strings.Repeat("f", 64)
	if _, err := first.Approve("foo", hash, TrustSigned, "ci"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	second, err := NewApprovalCache(path)
	if err != nil {
		t.Fatalf("second NewApprovalCache: %v", err)
	}
	got, ok := second.Lookup("foo", hash)
	if !ok {
		t.Fatalf("reload did not recover entry")
	}
	if got.Trust != TrustSigned || got.ApprovedBy != "ci" {
		t.Fatalf("reloaded entry fields mismatch: %+v", got)
	}
}

func TestApprovalCache_CorruptedFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "skills-trust.json")
	if err := os.WriteFile(path, []byte("not json at all {"), 0o600); err != nil {
		t.Fatalf("write corrupted: %v", err)
	}
	cache, err := NewApprovalCache(path)
	if err == nil {
		t.Fatalf("expected error on corrupted file")
	}
	if cache == nil {
		t.Fatalf("expected cache to still be returned for degraded mode")
	}
	if len(cache.Entries()) != 0 {
		t.Fatalf("corrupted file should yield empty entries")
	}
}

func TestApprovalCache_AtomicWriteNoLeftoverTemp(t *testing.T) {
	cache := newTestCache(t)
	hash := strings.Repeat("1", 64)
	if _, err := cache.Approve("foo", hash, TrustUser, ""); err != nil {
		t.Fatalf("approve: %v", err)
	}
	dir := filepath.Dir(cache.Path())
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "skills-trust-") && strings.HasSuffix(name, ".json") {
			t.Fatalf("leftover temp file detected: %s", name)
		}
	}
}
