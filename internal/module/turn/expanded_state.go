package turn

import (
	"strings"
	"sync"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
)

// ExpandedArtifact 记录一次 skill artifact 的注入历史。
//
// P20.1 §3.6：expanded state 的去重键从 P20 原设计的纯 `name` 升级到
// 五元组 `(name, kind, locator, hash)`，避免同 skill 不同 body/resource
// 被错误去重。
type ExpandedArtifact struct {
	// Name 是 skill 标识符，已规范化为小写 trim。
	Name string
	// Kind 是 artifact 种类：metadata / body / resource。空值视同 body。
	Kind string
	// Locator 是 artifact 子路径：
	//   - body: "SKILL.md" 或 "SKILL.md#Anchor"
	//   - resource: "references/api.md" 等
	//   - metadata: 空字符串
	Locator string
	// Hash 是注入时的内容 hash（hex），用于严格对比。short 版本见 ArtifactKey。
	Hash string
	// LastTurnIdx 是最近注入该 artifact 的 turn 序号。
	LastTurnIdx int
	// LastUsedAt 是最近标记时间（UTC），用于诊断 / 指标。
	LastUsedAt time.Time
}

// ExpandedArtifactState 线程安全的 artifact 注入状态表，实现 P20.1 §3.6 的
// 细粒度去重。状态与 turn lifecycle 绑定：
//
//   - TTL 默认 5 turn：`turnIdx - entry.LastTurnIdx < TTL` 视为 Fresh（已注入）
//   - `Reset()` 清空所有条目：thread resume / history compact / 显式刷新时调
//   - `Mark` / `IsFresh` 都用共享 artifact key，保证 body 与 resource 不互斥
//
// 使用时建议绑定到 turn service 的 session 级字段，供 Resolver 查询/更新。
type ExpandedArtifactState struct {
	mu      sync.RWMutex
	ttl     int
	entries map[string]ExpandedArtifact
}

// DefaultExpandedTTL 是 P20.1 §3.6 建议的 TTL（turn 数）。
const DefaultExpandedTTL = 5

// NewExpandedArtifactState 构造一个新的 state。ttl <= 0 时兜底为 DefaultExpandedTTL。
func NewExpandedArtifactState(ttl int) *ExpandedArtifactState {
	if ttl <= 0 {
		ttl = DefaultExpandedTTL
	}
	return &ExpandedArtifactState{
		ttl:     ttl,
		entries: make(map[string]ExpandedArtifact),
	}
}

// TTL 返回当前配置的 TTL（turn 数）。
func (s *ExpandedArtifactState) TTL() int {
	if s == nil {
		return 0
	}
	return s.ttl
}

// Mark 把一次注入记录写入 state。
//
// 从 SkillRef 提取 (name, version hash) 作为 artifact hash；Kind 与 Locator
// 当前默认 body/SKILL.md（Phase 6 skill_expand_body / skill_read_resource
// 会传入更细的 kind/locator，但本 Phase 只做 body 场景的骨架）。
func (s *ExpandedArtifactState) Mark(ref dto.SkillRef, turnIdx int) ExpandedArtifact {
	if s == nil {
		return ExpandedArtifact{}
	}
	entry := artifactFromRef(ref, turnIdx)
	if entry.Name == "" {
		return ExpandedArtifact{}
	}
	key := ArtifactKey(entry.Name, entry.Kind, entry.Locator, entry.Hash)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = entry
	return entry
}

// MarkArtifact 是 Phase 6 skill_expand_body / skill_read_resource 调用入口：
// 直接传 kind/locator/hash 可覆盖 body/SKILL.md 默认值。name 会被 trim+lower。
func (s *ExpandedArtifactState) MarkArtifact(name, kind, locator, hash string, turnIdx int) ExpandedArtifact {
	if s == nil {
		return ExpandedArtifact{}
	}
	normalizedName := strings.ToLower(strings.TrimSpace(name))
	if normalizedName == "" {
		return ExpandedArtifact{}
	}
	entry := ExpandedArtifact{
		Name:        normalizedName,
		Kind:        normalizeArtifactKind(kind),
		Locator:     strings.TrimSpace(locator),
		Hash:        strings.ToLower(strings.TrimSpace(hash)),
		LastTurnIdx: turnIdx,
		LastUsedAt:  time.Now().UTC(),
	}
	if entry.Kind == skillpkg.ArtifactKindBody && entry.Locator == "" {
		entry.Locator = "SKILL.md"
	}
	key := ArtifactKey(entry.Name, entry.Kind, entry.Locator, entry.Hash)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = entry
	return entry
}

// IsFresh 判断对应 SkillRef 的 artifact 是否在 TTL 内已注入过。
// 同 name 但不同 kind/locator/hash 互不抑制（P20.1 §3.6 细粒度语义）。
func (s *ExpandedArtifactState) IsFresh(ref dto.SkillRef, turnIdx int) bool {
	if s == nil {
		return false
	}
	probe := artifactFromRef(ref, turnIdx)
	if probe.Name == "" {
		return false
	}
	return s.isFreshByKey(ArtifactKey(probe.Name, probe.Kind, probe.Locator, probe.Hash), probe.Hash, turnIdx)
}

// IsArtifactFresh 直接按 (name, kind, locator, hash) 查询 Fresh，供 Phase 6 工具使用。
func (s *ExpandedArtifactState) IsArtifactFresh(name, kind, locator, hash string, turnIdx int) bool {
	if s == nil {
		return false
	}
	normalizedName := strings.ToLower(strings.TrimSpace(name))
	if normalizedName == "" {
		return false
	}
	normalizedKind := normalizeArtifactKind(kind)
	normalizedLocator := strings.TrimSpace(locator)
	if normalizedKind == skillpkg.ArtifactKindBody && normalizedLocator == "" {
		normalizedLocator = "SKILL.md"
	}
	normalizedHash := strings.ToLower(strings.TrimSpace(hash))
	return s.isFreshByKey(ArtifactKey(normalizedName, normalizedKind, normalizedLocator, normalizedHash), normalizedHash, turnIdx)
}

func (s *ExpandedArtifactState) isFreshByKey(key, fullHash string, turnIdx int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[key]
	if !ok {
		return false
	}
	// 严格对比完整 hash 防止 short-hash 碰撞误判
	if fullHash != "" && !strings.EqualFold(entry.Hash, fullHash) {
		return false
	}
	// TTL 检查：turnIdx 倒退（resume 至旧 turn）视为不 fresh，强制重注入
	if turnIdx < entry.LastTurnIdx {
		return false
	}
	return (turnIdx - entry.LastTurnIdx) < s.ttl
}

// Reset 清空全部 entries。应在 thread resume / history compact / 显式刷新时调用，
// 保证上下文陈旧状态不影响新 turn 决策。
func (s *ExpandedArtifactState) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[string]ExpandedArtifact)
}

// Snapshot 返回当前所有 entries 的副本（诊断用，不可修改内部状态）。
func (s *ExpandedArtifactState) Snapshot() []ExpandedArtifact {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ExpandedArtifact, 0, len(s.entries))
	for _, entry := range s.entries {
		out = append(out, entry)
	}
	return out
}

// ArtifactKey 是 P20.1 §3.6 的 map key 生成器，对外导出供 expanded state 的
// 调用方（skill_expand_body / skill_read_resource 等 Phase 6 工具）构造一致
// 的键。
//
// 格式：lower(name) + "::" + kind + "::" + locator + "@" + short(hash)
// 其中 short(hash) 取前 12 位小写 hex，与 skill.approvalKey 短 hash 策略一致。
func ArtifactKey(name, kind, locator, hash string) string {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	kind = normalizeArtifactKind(kind)
	locator = strings.TrimSpace(locator)
	shortHash := strings.ToLower(strings.TrimSpace(hash))
	if len(shortHash) > 12 {
		shortHash = shortHash[:12]
	}
	return lowerName + "::" + kind + "::" + locator + "@" + shortHash
}

// normalizeArtifactKind 将输入规范化为已知 kind 常量，未知值兜底为 body。
// 与 skill.IsValidArtifactKind 语义对齐但更宽松：对非法值不报错而是降级，
// 因为 expanded state 作为内部缓存结构不应打断主流程。
func normalizeArtifactKind(kind string) string {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case skillpkg.ArtifactKindMetadata:
		return skillpkg.ArtifactKindMetadata
	case skillpkg.ArtifactKindResource:
		return skillpkg.ArtifactKindResource
	default:
		return skillpkg.ArtifactKindBody
	}
}

// artifactFromRef 将 SkillRef 映射为 ExpandedArtifact（本 Phase 默认 body/SKILL.md）。
// SkillRef.Version 被当作 artifact hash 使用（Phase 1 hydrate 阶段把
// SkillInfo.ContentHash 的前 12 位写入 Version）。
func artifactFromRef(ref dto.SkillRef, turnIdx int) ExpandedArtifact {
	name := strings.ToLower(strings.TrimSpace(ref.Name))
	if name == "" {
		return ExpandedArtifact{}
	}
	return ExpandedArtifact{
		Name:        name,
		Kind:        skillpkg.ArtifactKindBody,
		Locator:     "SKILL.md",
		Hash:        strings.ToLower(strings.TrimSpace(ref.Version)),
		LastTurnIdx: turnIdx,
		LastUsedAt:  time.Now().UTC(),
	}
}
