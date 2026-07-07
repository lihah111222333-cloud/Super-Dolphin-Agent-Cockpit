package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// TestApprovalCache_HashPrefixCollision 验证 Lookup 在 12 位 key 前缀碰撞场景下
// 依然严格按全 hash 返回。由于 sha256 48-bit 前缀碰撞概率极低，此处用手工
// 构造的 64 hex 字符串模拟 —— Approve 时 map key 只取前 12 位，新写入会覆盖条目数据，
// 但 Lookup 的全 hash 对比能拦截误中。该用例保障未来重构误改成 short-hash 对比不会静默失效。
func TestApprovalCache_HashPrefixCollisionStrictLookup(t *testing.T) {
	cache := newTestCache(t)
	shared := "abcdef012345" // 共享前 12 位
	firstHash := shared + strings.Repeat("0", 52)
	secondHash := shared + strings.Repeat("f", 52)
	if _, err := cache.Approve("foo", firstHash, TrustProject, ""); err != nil {
		t.Fatalf("approve first: %v", err)
	}
	// 二次 Approve 同 name、同短前缀但不同全 hash，会覆盖短 key 下的条目。
	if _, err := cache.Approve("foo", secondHash, TrustProject, ""); err != nil {
		t.Fatalf("approve second: %v", err)
	}
	// 被覆盖的旧 hash 必须未命中，防止短 key 冲突误放行旧内容。
	if _, ok := cache.Lookup("foo", firstHash); ok {
		t.Fatalf("first hash should miss after overwrite")
	}
	// 新 hash 是当前审批内容，应按全 hash 精确命中。
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	got := DefaultApprovalCachePath()
	want := filepath.Join(home, ".super-dolphin", "skills-trust.json")
	if got != want {
		t.Fatalf("default path = %q, want %q", got, want)
	}
}

func TestLookupArtifactApproval_NilCacheReturnsFalse(t *testing.T) {
	svc := NewService(t.TempDir()).(*service)
	svc.approval = nil
	approved, err := svc.LookupArtifactApproval(context.Background(), contract.ArtifactApprovalRequest{
		RepoFingerprint: "repo",
		Name:            "demo",
		ArtifactKind:    ArtifactKindBody,
		ArtifactLocator: "SKILL.md",
		ContentHash:     "hash",
	})
	if err != nil {
		t.Fatalf("LookupArtifactApproval() error = %v", err)
	}
	if approved {
		t.Fatal("LookupArtifactApproval() approved = true, want false for nil cache")
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

	// 刚审批的 name/hash 必须立即命中内存索引。
	assertLookupEntry(t, cache, hash)

	// 审批还必须同步落盘，重启后才能继续信任该 hash。
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

	// 已审批的旧 hash 在内容未变化时应继续命中。
	if _, ok := cache.Lookup("foo", oldHash); !ok {
		t.Fatalf("old hash should hit")
	}
	// 新 hash 代表审批后内容变化，必须未命中以防 TOCTOU。
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
// 盘上终态必须包含所有条目，证明 snapshot 时序受 writeMu 串行化保护。
func TestApprovalCache_ConcurrentApproveAllEntriesPersisted(t *testing.T) {
	cache := newTestCache(t)
	const N = 20
	var wg sync.WaitGroup
	for i := range N {
		wg.Go(func() {
			name := fmt.Sprintf("skill-%02d", i)
			hash := fmt.Sprintf("%064x", i+1) // 64 位十六进制字符串，保持唯一。
			if _, err := cache.Approve(name, hash, TrustProject, ""); err != nil {
				t.Errorf("Approve(%s) error = %v", name, err)
			}
		})
	}
	wg.Wait()

	// 内存索引应保留全部并发审批条目。
	if got := len(cache.Entries()); got != N {
		t.Fatalf("in-memory entries = %d want %d", got, N)
	}

	// 从磁盘重载也应保留全部条目，验证并发写入没有丢失最终快照。
	reloaded, err := NewApprovalCache(cache.Path())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := len(reloaded.Entries()); got != N {
		t.Fatalf("reloaded entries = %d want %d (concurrent write lost)", got, N)
	}
}

// 产物级审批回归：同名 skill 的 body/resource 审批必须按 kind、
// locator 和 repo 指纹隔离，避免跨产物误命中。
func TestApprovalCache_ApproveArtifact_BodyAndResourceIsolated(t *testing.T) {
	cache := newTestCache(t)
	hashBody := strings.Repeat("a", 64)
	hashRes := strings.Repeat("b", 64)
	// 同一 skill name 下，正文和资源审批写入不同 hash，后续 lookup 必须区分产物类型。
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
	// body lookup 只接受 body hash，不能被同名 resource hash 误命中。
	if _, ok := cache.LookupArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: ArtifactKindBody, ArtifactLocator: "SKILL.md",
		ContentHash: hashBody,
	}); !ok {
		t.Fatalf("body lookup should hit")
	}
	if _, ok := cache.LookupArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: ArtifactKindBody, ArtifactLocator: "SKILL.md",
		ContentHash: hashRes, // 用 resource hash 查 body 会未命中。
	}); ok {
		t.Fatalf("body lookup with resource hash MUST miss")
	}
	// resource lookup 只接受 resource locator/hash，防止正文审批外溢到资源文件。
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
	// 相同 name/kind/locator/hash 在不同 repo 指纹下必须互不命中。
	if _, ok := cache.LookupArtifact(ApprovalRequest{
		RepoFingerprint: fp2, Name: "foo",
		ArtifactKind: ArtifactKindBody, ArtifactLocator: "SKILL.md",
		ContentHash: hash,
	}); ok {
		t.Fatalf("different repo fingerprint MUST miss")
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
		ArtifactLocator: "SKILL.md", // 无 anchor 应未命中。
		ContentHash:     hash,
	}); ok {
		t.Fatalf("different anchor MUST miss")
	}
	if _, ok := cache.LookupArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: ArtifactKindBody,
		ArtifactLocator: "SKILL.md#Usage", // 同 anchor 必须命中。
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
	// resource locator 不允许路径逃逸，避免把审批范围扩到 skill 根目录外。
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
	// 旧 Approve wrapper 内部映射到 body/SKILL.md，保证旧调用方不需要立即迁移。
	cache := newTestCache(t)
	hash := strings.Repeat("a", 64)
	if _, err := cache.Approve("foo", hash, TrustProject, ""); err != nil {
		t.Fatalf("legacy Approve: %v", err)
	}
	// 旧 Lookup 仍读取同一审批记录，保持老 API 行为兼容。
	if _, ok := cache.Lookup("foo", hash); !ok {
		t.Fatalf("legacy Lookup should hit")
	}
	// 新 LookupArtifact 使用默认 body/SKILL.md 也应命中同一记录。
	if _, ok := cache.LookupArtifact(ApprovalRequest{
		Name: "foo", ArtifactKind: ArtifactKindBody, ArtifactLocator: "SKILL.md",
		ContentHash: hash,
	}); !ok {
		t.Fatalf("artifact Lookup should hit the same entry")
	}
}

func TestApprovalCache_LegacyJSONRoundtrip(t *testing.T) {
	// 模拟旧版写盘 JSON，缺失的产物字段需要在读取时回填默认值。
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
	// 旧 Lookup(name, hash) 必须能命中，避免升级后废弃已有审批文件。
	hash := "abc123def456789012345678901234567890123456789012345678901234cdef"
	if _, ok := cache.Lookup("legacy-skill", hash); !ok {
		t.Fatalf("legacy JSON should be lookupable via legacy API")
	}
	// Entries 返回时自动回填 body/SKILL.md，给新 API 提供完整产物维度。
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
