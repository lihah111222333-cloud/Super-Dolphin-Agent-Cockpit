package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestApprovalCache_HashPrefixCollision 验证 Lookup 在 12 位 key 前缀碰撞场景下
// 依然严格按全 hash 返回。由于 sha256 48-bit 前缀碰撞概率极低，此处用手工
// 构造的 64 hex 字符串模拟 —— Approve 时 map key 只取前 12 位，新写入会覆盖条目数据，
// 但 Lookup 的全 hash 对比能拦截误中。该 case 保障未来重构误改成 short-hash 对比不会静默失效。
func TestApprovalCache_HashPrefixCollisionStrictLookup(t *testing.T) {
	cache := newTestCache(t)
	shared := "abcdef012345" // 共享前 12 位
	firstHash := shared + strings.Repeat("0", 52)
	secondHash := shared + strings.Repeat("f", 52)
	if _, err := cache.Approve("foo", firstHash, TrustProject, ""); err != nil {
		t.Fatalf("approve first: %v", err)
	}
	// 二次 Approve 同 name 的不同 hash 但同前缀——会覆盖 map entry
	if _, err := cache.Approve("foo", secondHash, TrustProject, ""); err != nil {
		t.Fatalf("approve second: %v", err)
	}
	// 旧 hash 必须 miss（已被覆盖）
	if _, ok := cache.Lookup("foo", firstHash); ok {
		t.Fatalf("first hash should miss after overwrite")
	}
	// 新 hash 命中
	if _, ok := cache.Lookup("foo", secondHash); !ok {
		t.Fatalf("second hash should hit")
	}
}

func TestDefaultApprovalCachePath_EnvOverride(t *testing.T) {
	t.Setenv("SKILLS_TRUST_PATH", "/tmp/test-skills-trust.json")
	if got := DefaultApprovalCachePath(); got != "/tmp/test-skills-trust.json" {
		t.Fatalf("env override failed: got %q", got)
	}
}

func TestDefaultApprovalCachePath_DefaultsToHome(t *testing.T) {
	t.Setenv("SKILLS_TRUST_PATH", "")
	got := DefaultApprovalCachePath()
	if got == "" {
		t.Fatalf("default path must not be empty")
	}
	// 要么是 ~/.multi-agent/skills-trust.json 或 tmp fallback
	if !strings.HasSuffix(got, "skills-trust.json") {
		t.Fatalf("default path should end with skills-trust.json, got %q", got)
	}
}

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
	assertApprovedEntry(t, entry, hash)

	// Lookup 命中
	assertLookupEntry(t, cache, hash)

	// 文件确实落盘
	assertApprovalFileContains(t, cache.Path(), hash, "foo")
}

func assertApprovedEntry(t *testing.T, entry ApprovalEntry, hash string) {
	t.Helper()
	if entry.Name != "foo" || entry.ContentHash != hash || entry.Trust != TrustProject {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.ApprovedAt.IsZero() {
		t.Fatalf("ApprovedAt should be set")
	}
}

func assertLookupEntry(t *testing.T, cache *ApprovalCache, hash string) {
	t.Helper()
	got, ok := cache.Lookup("foo", hash)
	if !ok {
		t.Fatalf("lookup miss after approve")
	}
	if got.Name != "foo" || got.ContentHash != hash {
		t.Fatalf("lookup returned wrong entry: %+v", got)
	}
}

func assertApprovalFileContains(t *testing.T, path, hash, name string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	if !strings.Contains(string(data), hash) || !strings.Contains(string(data), name) {
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

// TestApprovalCache_ConcurrentApproveAllEntriesPersisted 盗写修复回归：并发 Approve 时
// 盘上终态必须包含所有 entry（snapshot 时序被 writeMu 串行化）。
func TestApprovalCache_ConcurrentApproveAllEntriesPersisted(t *testing.T) {
	cache := newTestCache(t)
	const N = 20
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("skill-%02d", i)
			hash := fmt.Sprintf("%064x", i+1) // 64 hex chars, unique
			if _, err := cache.Approve(name, hash, TrustProject, ""); err != nil {
				t.Errorf("Approve(%s) error = %v", name, err)
			}
		}()
	}
	wg.Wait()

	// 内存中应服 N 条
	if got := len(cache.Entries()); got != N {
		t.Fatalf("in-memory entries = %d want %d", got, N)
	}

	// 盘上重新加载应也是 N 条（验证 writeMu 串行化有效）
	reloaded, err := NewApprovalCache(cache.Path())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := len(reloaded.Entries()); got != N {
		t.Fatalf("reloaded entries = %d want %d (concurrent write lost)", got, N)
	}
}

// ============================================================================
// P20.1 §3.2 artifact-level 审批新测
// ============================================================================

func TestApprovalCache_ApproveArtifact_BodyAndResourceIsolated(t *testing.T) {
	cache := newTestCache(t)
	hashBody := strings.Repeat("a", 64)
	hashRes := strings.Repeat("b", 64)
	// 同 name 下，body 和 resource 写入不同 hash。
	if _, err := cache.ApproveArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: ArtifactKindBody, ArtifactLocator: "SKILL.md",
		ContentHash: hashBody, Trust: TrustProject,
	}); err != nil {
		t.Fatalf("approve body: %v", err)
	}
	if _, err := cache.ApproveArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: ArtifactKindResource, ArtifactLocator: "references/api.md",
		ContentHash: hashRes, Trust: TrustProject,
	}); err != nil {
		t.Fatalf("approve resource: %v", err)
	}
	// body Lookup 仅命中 body hash、不命中 resource hash
	if _, ok := cache.LookupArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: ArtifactKindBody, ArtifactLocator: "SKILL.md",
		ContentHash: hashBody,
	}); !ok {
		t.Fatalf("body lookup should hit")
	}
	if _, ok := cache.LookupArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: ArtifactKindBody, ArtifactLocator: "SKILL.md",
		ContentHash: hashRes, // 用 resource hash 查 body → miss
	}); ok {
		t.Fatalf("body lookup with resource hash MUST miss")
	}
	// resource 反向同理
	if _, ok := cache.LookupArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: ArtifactKindResource, ArtifactLocator: "references/api.md",
		ContentHash: hashRes,
	}); !ok {
		t.Fatalf("resource lookup should hit")
	}
}

func TestApprovalCache_ApproveArtifact_RepoFingerprintIsolation(t *testing.T) {
	cache := newTestCache(t)
	hash := strings.Repeat("a", 64)
	fp1 := "repo-alpha"
	fp2 := "repo-beta"
	if _, err := cache.ApproveArtifact(ApprovalRequest{
		RepoFingerprint: fp1, Name: "foo",
		ArtifactKind: ArtifactKindBody, ArtifactLocator: "SKILL.md",
		ContentHash: hash, Trust: TrustProject,
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	// 相同 (name, kind, locator, hash) 但不同 repo 必须互不命中
	if _, ok := cache.LookupArtifact(ApprovalRequest{
		RepoFingerprint: fp2, Name: "foo",
		ArtifactKind: ArtifactKindBody, ArtifactLocator: "SKILL.md",
		ContentHash: hash,
	}); ok {
		t.Fatalf("different repo fingerprint MUST miss (P20.1 §3.2)")
	}
	if _, ok := cache.LookupArtifact(ApprovalRequest{
		RepoFingerprint: fp1, Name: "foo",
		ArtifactKind: ArtifactKindBody, ArtifactLocator: "SKILL.md",
		ContentHash: hash,
	}); !ok {
		t.Fatalf("same repo fingerprint should hit")
	}
}

func TestApprovalCache_ApproveArtifact_AnchorIsolation(t *testing.T) {
	cache := newTestCache(t)
	hash := strings.Repeat("a", 64)
	if _, err := cache.ApproveArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: ArtifactKindBody,
		ArtifactLocator: "SKILL.md#Usage",
		ContentHash:     hash, Trust: TrustProject,
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, ok := cache.LookupArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: ArtifactKindBody,
		ArtifactLocator: "SKILL.md", // 无 anchor 应 miss
		ContentHash:     hash,
	}); ok {
		t.Fatalf("different anchor MUST miss")
	}
	if _, ok := cache.LookupArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: ArtifactKindBody,
		ArtifactLocator: "SKILL.md#Usage", // 同 anchor 必命
		ContentHash:     hash,
	}); !ok {
		t.Fatalf("same anchor should hit")
	}
}

func TestApprovalCache_ApproveArtifact_InvalidKindRejected(t *testing.T) {
	cache := newTestCache(t)
	hash := strings.Repeat("a", 64)
	_, err := cache.ApproveArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: "exec",
		ArtifactLocator: "irrelevant", ContentHash: hash,
	})
	if err == nil {
		t.Fatalf("invalid kind should be rejected")
	}
}

func TestApprovalCache_ApproveArtifact_InvalidLocatorRejected(t *testing.T) {
	cache := newTestCache(t)
	hash := strings.Repeat("a", 64)
	// resource 路径逃逸
	_, err := cache.ApproveArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: ArtifactKindResource,
		ArtifactLocator: "../../etc/passwd",
		ContentHash:     hash, Trust: TrustProject,
	})
	if err == nil {
		t.Fatalf("path escape in resource locator MUST be rejected")
	}
}

func TestApprovalCache_LegacyApproveStillWorks(t *testing.T) {
	// 旧 wrapper Approve(name, hash, trust, by) 内部会走 body / SKILL.md 默认值。
	cache := newTestCache(t)
	hash := strings.Repeat("a", 64)
	if _, err := cache.Approve("foo", hash, TrustProject, ""); err != nil {
		t.Fatalf("legacy Approve: %v", err)
	}
	// 旧 Lookup 应当命中
	if _, ok := cache.Lookup("foo", hash); !ok {
		t.Fatalf("legacy Lookup should hit")
	}
	// 新 LookupArtifact 传 body/SKILL.md 亦应命中
	if _, ok := cache.LookupArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: ArtifactKindBody, ArtifactLocator: "SKILL.md",
		ContentHash: hash,
	}); !ok {
		t.Fatalf("artifact Lookup should hit the same entry")
	}
}

func TestApprovalCache_LegacyJSONRoundtrip(t *testing.T) {
	// 模拟旧版写盘的 JSON（无 ArtifactKind/Locator/RepoFingerprint 字段）
	tmp := t.TempDir()
	path := filepath.Join(tmp, "skills-trust.json")
	legacyJSON := `{
  "version": 1,
  "entries": [
    {
      "name": "legacy-skill",
      "content_hash": "abc123def456789012345678901234567890123456789012345678901234cdef",
      "trust": "project",
      "approved_at": "2026-01-01T00:00:00Z",
      "approved_by": "legacy-user"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(legacyJSON), 0o600); err != nil {
		t.Fatalf("write legacy JSON: %v", err)
	}
	cache, err := NewApprovalCache(path)
	if err != nil {
		t.Fatalf("load legacy: %v", err)
	}
	// 旧 Lookup(name, hash) 必须能命中
	hash := "abc123def456789012345678901234567890123456789012345678901234cdef"
	if _, ok := cache.Lookup("legacy-skill", hash); !ok {
		t.Fatalf("legacy JSON should be lookupable via legacy API")
	}
	// entries 返回时 ArtifactKind/Locator 自动回填为 body/SKILL.md
	entries := cache.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ArtifactKind != ArtifactKindBody || entries[0].ArtifactLocator != "SKILL.md" {
		t.Fatalf("legacy entry should default to body/SKILL.md, got %+v", entries[0])
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
