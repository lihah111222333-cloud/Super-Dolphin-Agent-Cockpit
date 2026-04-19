package turn

import (
	"strings"
	"sync"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
)

func TestNewExpandedArtifactState_DefaultTTL(t *testing.T) {
	if got := NewExpandedArtifactState(0).TTL(); got != DefaultExpandedTTL {
		t.Fatalf("ttl=0 should default: got %d", got)
	}
	if got := NewExpandedArtifactState(-1).TTL(); got != DefaultExpandedTTL {
		t.Fatalf("ttl<0 should default: got %d", got)
	}
	if got := NewExpandedArtifactState(3).TTL(); got != 3 {
		t.Fatalf("ttl=3 should pass through: got %d", got)
	}
}

func TestArtifactKey_StableFormat(t *testing.T) {
	key := ArtifactKey("Foo", "body", "SKILL.md", strings.Repeat("a", 64))
	want := "foo::body::SKILL.md@" + strings.Repeat("a", 12)
	if key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}
	// name lower + hash short 12
	key2 := ArtifactKey("FOO", "resource", "refs/x.md", "ABCDEF012345XYZ")
	if !strings.HasPrefix(key2, "foo::resource::refs/x.md@abcdef012345") {
		t.Fatalf("key normalization unexpected: %q", key2)
	}
}

func TestArtifactKey_UnknownKindDefaultsToBody(t *testing.T) {
	key := ArtifactKey("foo", "exec", "any", "h")
	if !strings.HasPrefix(key, "foo::body::") {
		t.Fatalf("unknown kind should default to body, got %q", key)
	}
}

func TestExpandedArtifactState_MarkAndFresh(t *testing.T) {
	s := NewExpandedArtifactState(5)
	ref := dto.SkillRef{Name: "go-testing", Version: "abcd1234"}
	if s.IsFresh(ref, 0) {
		t.Fatalf("should not be fresh before Mark")
	}
	s.Mark(ref, 0)
	if !s.IsFresh(ref, 0) {
		t.Fatalf("should be fresh at the same turn")
	}
	if !s.IsFresh(ref, 4) {
		t.Fatalf("should be fresh within TTL")
	}
	if s.IsFresh(ref, 5) {
		t.Fatalf("turnIdx - LastTurnIdx == TTL should NOT be fresh")
	}
	if s.IsFresh(ref, 10) {
		t.Fatalf("far future should not be fresh")
	}
}

func TestExpandedArtifactState_HashChangeBreaksFresh(t *testing.T) {
	s := NewExpandedArtifactState(5)
	old := dto.SkillRef{Name: "foo", Version: "hash-v1"}
	s.Mark(old, 0)
	newHash := dto.SkillRef{Name: "foo", Version: "hash-v2"}
	if s.IsFresh(newHash, 1) {
		t.Fatalf("hash change MUST invalidate freshness (P20.1 §3.6)")
	}
	// 旧 hash 仍 fresh
	if !s.IsFresh(old, 1) {
		t.Fatalf("old hash should still be fresh within TTL")
	}
}

func TestExpandedArtifactState_BodyVsResourceIsolated(t *testing.T) {
	s := NewExpandedArtifactState(5)
	s.MarkArtifact("foo", skillpkg.ArtifactKindBody, "SKILL.md", "hashA", 0)
	// 同 name 但不同 kind/locator 必须互不抑制
	if s.IsArtifactFresh("foo", skillpkg.ArtifactKindResource, "references/api.md", "hashA", 0) {
		t.Fatalf("body fresh should NOT imply resource fresh")
	}
	if s.IsArtifactFresh("foo", skillpkg.ArtifactKindBody, "SKILL.md#Usage", "hashA", 0) {
		t.Fatalf("body SKILL.md fresh should NOT imply different anchor fresh")
	}
	// 再 Mark resource 之后两者都 fresh，但彼此独立
	s.MarkArtifact("foo", skillpkg.ArtifactKindResource, "references/api.md", "hashB", 0)
	if !s.IsArtifactFresh("foo", skillpkg.ArtifactKindBody, "SKILL.md", "hashA", 0) {
		t.Fatalf("body should still be fresh after marking resource")
	}
	if !s.IsArtifactFresh("foo", skillpkg.ArtifactKindResource, "references/api.md", "hashB", 0) {
		t.Fatalf("resource should be fresh after marking")
	}
}

func TestExpandedArtifactState_ResetClearsAll(t *testing.T) {
	s := NewExpandedArtifactState(5)
	s.MarkArtifact("a", "body", "SKILL.md", "h1", 0)
	s.MarkArtifact("b", "body", "SKILL.md", "h2", 0)
	if len(s.Snapshot()) != 2 {
		t.Fatalf("expected 2 entries before reset")
	}
	s.Reset()
	if len(s.Snapshot()) != 0 {
		t.Fatalf("Reset should clear all entries")
	}
	if s.IsArtifactFresh("a", "body", "SKILL.md", "h1", 0) {
		t.Fatalf("after Reset, no entry is fresh")
	}
}

func TestExpandedArtifactState_TurnIdxRegressionInvalidates(t *testing.T) {
	// Thread resume 后 turnIdx 可能倒退（UI 从旧 turn 开始）；此时应视为不 fresh
	// 强制重注入，避免注入决策基于陈旧 turn 状态。
	s := NewExpandedArtifactState(5)
	ref := dto.SkillRef{Name: "foo", Version: "h"}
	s.Mark(ref, 10)
	if s.IsFresh(ref, 5) {
		t.Fatalf("turnIdx regression should NOT be fresh")
	}
	// 仍然在当前 turn fresh
	if !s.IsFresh(ref, 10) {
		t.Fatalf("same turn should be fresh")
	}
}

func TestExpandedArtifactState_EmptyNameNoOp(t *testing.T) {
	s := NewExpandedArtifactState(5)
	if entry := s.Mark(dto.SkillRef{Name: "  "}, 0); entry.Name != "" {
		t.Fatalf("empty name should no-op")
	}
	if entry := s.MarkArtifact("", "body", "SKILL.md", "h", 0); entry.Name != "" {
		t.Fatalf("MarkArtifact empty name should no-op")
	}
	if s.IsFresh(dto.SkillRef{Name: ""}, 0) {
		t.Fatalf("empty name never fresh")
	}
	if s.IsArtifactFresh("", "body", "SKILL.md", "h", 0) {
		t.Fatalf("IsArtifactFresh empty name never fresh")
	}
}

func TestExpandedArtifactState_NilSafety(t *testing.T) {
	var s *ExpandedArtifactState
	if s.TTL() != 0 {
		t.Fatalf("nil TTL should return 0")
	}
	if entry := s.Mark(dto.SkillRef{Name: "foo"}, 0); entry.Name != "" {
		t.Fatalf("nil Mark should no-op")
	}
	if s.IsFresh(dto.SkillRef{Name: "foo"}, 0) {
		t.Fatalf("nil IsFresh should return false")
	}
	s.Reset() // 不应 panic
	if snap := s.Snapshot(); snap != nil {
		t.Fatalf("nil Snapshot should return nil")
	}
}

// TestExpandedArtifactState_ShortHashCollisionStrictCompare 防御：两个不同全 hash
// 但前 12 位 hex 相同时，IsArtifactFresh 须按全 hash 严格对比而不靠 short key。
// （12 位 hex = 48 bits，sha256 实际不会碰撞；这里用人工构造锁定实现。）
func TestExpandedArtifactState_ShortHashCollisionStrictCompare(t *testing.T) {
	s := NewExpandedArtifactState(5)
	shared := "abcdef012345"
	h1 := shared + strings.Repeat("1", 52)
	h2 := shared + strings.Repeat("2", 52)
	s.MarkArtifact("foo", "body", "SKILL.md", h1, 0)
	// 同 short hash 前缀，但全 hash 不同 → 必须 miss
	if s.IsArtifactFresh("foo", "body", "SKILL.md", h2, 0) {
		t.Fatalf("short-hash prefix collision MUST NOT false-hit (strict full-hash compare)")
	}
	// 同 h1 仍 fresh
	if !s.IsArtifactFresh("foo", "body", "SKILL.md", h1, 0) {
		t.Fatalf("same full hash should still be fresh")
	}
}

func TestExpandedArtifactState_ConcurrentMarkAndFresh(t *testing.T) {
	// -race 验证：N 个 goroutine 并发 Mark + IsFresh 应无数据竞争。
	s := NewExpandedArtifactState(5)
	const N = 20
	var wg sync.WaitGroup
	wg.Add(N * 2)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			s.MarkArtifact("skill", "body", "SKILL.md", strings.Repeat("a", 63)+string(rune('a'+(i%26))), i)
		}()
		go func() {
			defer wg.Done()
			_ = s.IsArtifactFresh("skill", "body", "SKILL.md", strings.Repeat("a", 64), i)
		}()
	}
	wg.Wait()
}
