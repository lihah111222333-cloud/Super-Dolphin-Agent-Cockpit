package turn

import (
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// ExpandedArtifact 记录一次 skill artifact 的注入历史。
// 去重维度包含 name、kind、locator 和 hash，避免同一 skill 的正文、
// 资源文件或 metadata 互相压制。
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

// ExpandedArtifactState 是线程安全的 artifact 注入状态表。
// 它与 turn 进度绑定，用 TTL 限制“已注入”记忆的有效窗口：
//
//   - TTL 默认 5 turn：`turnIdx - entry.LastTurnIdx < TTL` 视为 Fresh（已注入）
//   - `Reset()` 清空所有条目：thread resume / history compact / 显式刷新时调
//   - `Mark` / `IsFresh` 都用共享 artifact key，保证 body 与 resource 不互斥
//
// 使用时建议绑定到 turn service 的 session 级字段，供 Resolver 查询/更新。
type ExpandedArtifactState struct {
	mu      sync.RWMutex                // 保护 entries 的并发读写
	ttl     int                         // Fresh 判定使用的 turn 数窗口
	entries map[string]ExpandedArtifact // artifact key 到最近注入记录
}

// DefaultExpandedTTL 是 artifact 注入记忆默认保留的 turn 数窗口。
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

// Mark 把一次 SkillRef 注入写入 state。
// SkillRef 只携带 name/version，因此这里按 body/SKILL.md 记录；更细的
// artifact 粒度由 MarkArtifact 入口直接传入。
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

// MarkArtifact 是 artifact 审批入口；provider 端的 skill 正文发现已切到
// provider-native mirrors，审批态仍用该结构记录具体 artifact 粒度：
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
	if entry.Kind == contract.ArtifactKindBody && entry.Locator == "" {
		entry.Locator = "SKILL.md"
	}
	key := ArtifactKey(entry.Name, entry.Kind, entry.Locator, entry.Hash)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = entry
	return entry
}

// IsFresh 判断对应 SkillRef 的默认 body artifact 是否仍在 TTL 窗口内。
// 同 name 但 kind、locator 或 hash 不同的 artifact 不会互相抑制。
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

// IsArtifactFresh 按完整 artifact 维度查询是否仍在 TTL 窗口内。
// 调用方可以传入资源文件或 metadata locator，避免只按 skill 名称误判。
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
	if normalizedKind == contract.ArtifactKindBody && normalizedLocator == "" {
		normalizedLocator = "SKILL.md"
	}
	normalizedHash := strings.ToLower(strings.TrimSpace(hash))
	return s.isFreshByKey(ArtifactKey(normalizedName, normalizedKind, normalizedLocator, normalizedHash), normalizedHash, turnIdx)
}

// isFreshByKey 在锁内按 artifact key 和完整 hash 判定是否仍在 TTL 内。
// 完整 hash 用于补强短 hash key，turnIdx 倒退时强制视为不 fresh 以便 resume 后重注入。
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

// CompactStale 淘汰 (currentTurnIdx - entry.LastTurnIdx) >= TTL 的条目。
//
// 用途：long session 下 Mark 只增不删，map 会无界增长。外部可在每个
// turn 结尾或每 N 个 turn 调用该方法回收内存。
// 返回被移除的条目数作为指标输出。
//
// 与 Reset() 的区别：
//   - Reset 无条件全清（resume/compact 语义），所有条目失效
//   - CompactStale 仅清逻辑上已经失效的条目（当前 turn 无人使用），保留 fresh
//     条目的去重效果
func (s *ExpandedArtifactState) CompactStale(currentTurnIdx int) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for key, entry := range s.entries {
		// turnIdx 倒退不视为 stale（可能是 resume 到旧 turn ，保留条目等 resume 后再评）
		if currentTurnIdx < entry.LastTurnIdx {
			continue
		}
		if (currentTurnIdx - entry.LastTurnIdx) >= s.ttl {
			delete(s.entries, key)
			removed++
		}
	}
	return removed
}

// Len 返回当前条目数（诊断 / 指标用）。
func (s *ExpandedArtifactState) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
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

// ArtifactKey 生成 expanded state 使用的稳定 map key。
// 外部调用方用同一规则构造审批键，确保 body/resource/metadata 共用一致的短 hash 策略。
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

// normalizeArtifactKind 将输入规范化为已知 kind 常量。
// 未知值只在内部缓存层按 body 处理，不改变外部 artifact 校验和审批边界。
func normalizeArtifactKind(kind string) string {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case contract.ArtifactKindMetadata:
		return contract.ArtifactKindMetadata
	case contract.ArtifactKindResource:
		return contract.ArtifactKindResource
	default:
		return contract.ArtifactKindBody
	}
}

// artifactFromRef 将 SkillRef 映射为默认 body artifact。
// SkillRef.Version 承载内容 hash 的短版本，用于跨 turn 判断同一 skill 正文是否已注入。
func artifactFromRef(ref dto.SkillRef, turnIdx int) ExpandedArtifact {
	name := strings.ToLower(strings.TrimSpace(ref.Name))
	if name == "" {
		return ExpandedArtifact{}
	}
	return ExpandedArtifact{
		Name:        name,
		Kind:        contract.ArtifactKindBody,
		Locator:     "SKILL.md",
		Hash:        strings.ToLower(strings.TrimSpace(ref.Version)),
		LastTurnIdx: turnIdx,
		LastUsedAt:  time.Now().UTC(),
	}
}
