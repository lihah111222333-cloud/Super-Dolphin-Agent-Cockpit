package skill

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
)

// ApprovalCache 是 P20 Phase 1 的 skill 审批决议持久化层。
//
// 目的：对不受信任域（TrustProject，来自 git clone）的 skill，在 artifact approval
// 查询前提供可持久化的审批状态；审批结果按 (name, content_hash[:12]) 键缓存，避免每次弹窗骚扰，
// 又能在 SKILL.md 改动后自动失效重审（TOCTOU 防护）。
//
// 存储格式（JSON，版本化）：
//
//	{
//	  "version": 1,
//	  "entries": [
//	    {"name": "foo", "content_hash": "abcd...", "trust": "project",
//	     "approved_at": "2026-04-18T12:34:56Z", "approved_by": "user@local"}
//	  ]
//	}
//
// 线程安全：所有公开方法加 RWMutex；写盘用临时文件 + rename 做原子替换。
type ApprovalCache struct {
	path    string
	mu      sync.RWMutex
	entries map[string]ApprovalEntry
	// writeMu 系列化盘面写入，避免并发 Approve/Revoke 因 snapshot 时序不同
	// 而相互覆盖（A.unlock 后比 B.rename 晚就会丢失 B 的写入）。
	// writeMu 与 mu 分开：读路径（Lookup/Entries）不被盘 IO 阻塞。
	writeMu  sync.Mutex
	revision uint64
}

// ApprovalEntry 是单条审批记录，持久化到 skills-trust.json 的 entries 数组中。
//
// 字段语义：
//   - Name：skill 标识符，已经 validateSkillName 规范化（小写、trim）。
//   - ContentHash：SKILL.md 全文 SHA-256 (hex lowercase)；任一字符改动都会导致 hash 变化
//     从而失效本条审批，强制重审（TOCTOU 防御）。
//   - Trust：审批时的信任归属结果（从 frontmatter 或 inferTrustFromRoot 得到）。
//     注意：entry.Trust 记录的是审批当时的快照；若 skill 被移至不同 root，
//     新 scan 产生的 SkillInfo.Trust 会不同，审批条目并不自动迁移。
//   - ApprovedAt：UTC 时标；重复 Approve 同一 (name, hash) 会覆盖。
//   - ApprovedBy：批准人标识（用户名 / OAuth subject / “ci” 等）；可为空。
type ApprovalEntry struct {
	// RepoFingerprint：P20.1 新增。项目根稳定指纹（RepoFingerprint() 生成）；空串表示
	// 旧 JSON / 全局审批范围。换项目后即使 name+hash 相同也会审批独立。
	RepoFingerprint string `json:"repo_fingerprint,omitempty"`
	Name            string `json:"name"`
	// ArtifactKind：P20.1 新增。metadata/body/resource。空串视同 body（旧 JSON 兼容）。
	ArtifactKind string `json:"artifact_kind,omitempty"`
	// ArtifactLocator：P20.1 新增。经 NormalizeArtifactLocator 规范化后的稳定字符串。
	ArtifactLocator string     `json:"artifact_locator,omitempty"`
	ContentHash     string     `json:"content_hash"`
	Trust           TrustScope `json:"trust"`
	ApprovedAt      time.Time  `json:"approved_at"`
	ApprovedBy      string     `json:"approved_by,omitempty"`
}

// ApprovalRequest 是 P20.1 artifact-level API（ApproveArtifact / LookupArtifact）的入参。
// 旧 Approve / Lookup 等价于 "Kind=body, Locator=SKILL.md, RepoFingerprint=\"\"" 的特例。
type ApprovalRequest struct {
	RepoFingerprint string
	Name            string
	ArtifactKind    string
	ArtifactLocator string
	ContentHash     string
	Trust           TrustScope // Approve 时必填；Lookup 无意义
	ApprovedBy      string     // Approve 时可选；Lookup 无意义
}

// approvalFile 是盘上 JSON 的外层结构。
type approvalFile struct {
	Version int             `json:"version"`
	Entries []ApprovalEntry `json:"entries"`
}

const approvalFileVersion = 1

// ErrApprovalCachePathRequired 表示构造 cache 时必须提供非空路径。
var ErrApprovalCachePathRequired = errors.New("approval cache path is required")

// DefaultApprovalCachePath 返回默认的审批缓存文件路径（`~/.super-dolphin/skills-trust.json`）。
// 环境变量 `SKILLS_TRUST_PATH` 可覆盖；UserHomeDir 失败兜底到 os.TempDir()。
func DefaultApprovalCachePath() string {
	if override := strings.TrimSpace(os.Getenv("SKILLS_TRUST_PATH")); override != "" {
		return override
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".super-dolphin", "skills-trust.json")
	}
	return filepath.Join(os.TempDir(), "super-dolphin-skills-trust.json")
}

// NewApprovalCache 从指定路径加载已有审批记录；文件不存在时返回空 cache（不报错）。
// 文件存在但损坏时返回空 cache + 错误，调用方可选择是否继续运行（降级策略）。
func NewApprovalCache(path string) (*ApprovalCache, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, ErrApprovalCachePathRequired
	}
	cache := &ApprovalCache{
		path:    path,
		entries: make(map[string]ApprovalEntry),
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		var payload approvalFile
		if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
			return cache, fmt.Errorf("approval cache %s is corrupted: %w", path, unmarshalErr)
		}
		cache.entries = loadEntriesFromPayload(payload)
		return cache, nil
	case errors.Is(err, os.ErrNotExist):
		return cache, nil
	default:
		return cache, fmt.Errorf("approval cache %s read failed: %w", path, err)
	}
}

// loadEntriesFromPayload 从反序列化后的 JSON 载荷构建 entries map，
// 并为旧 JSON 缺失的 P20.1 新字段提供向后兼容兜底。
func loadEntriesFromPayload(payload approvalFile) map[string]ApprovalEntry {
	entries := make(map[string]ApprovalEntry, len(payload.Entries))
	for _, entry := range payload.Entries {
		if entry.Name == "" || entry.ContentHash == "" {
			continue
		}
		// P20.1：旧 JSON 缺失新字段 → 按 body/SKILL.md 兜底（向后兼容）。
		if entry.ArtifactKind == "" {
			entry.ArtifactKind = ArtifactKindBody
		}
		if entry.ArtifactKind == ArtifactKindBody && entry.ArtifactLocator == "" {
			entry.ArtifactLocator = "SKILL.md"
		}
		entries[artifactApprovalKey(ApprovalRequest{
			RepoFingerprint: entry.RepoFingerprint,
			Name:            entry.Name,
			ArtifactKind:    entry.ArtifactKind,
			ArtifactLocator: entry.ArtifactLocator,
			ContentHash:     entry.ContentHash,
		})] = entry
	}
	return entries
}

// artifactApprovalKey 生成 P20.1 §3.2 规定的五元组 map key。
//
// key 格式：<repo_fp>::<name>::<kind>::<locator>@<hash[:12]>
//   - 字段均经过 lower/trim 规范化
//   - repo_fp 为空时等价“全局范围 / legacy”（旧 JSON 向后兼容）
//   - kind 为空视同 body
//   - hash 取 12 位短 key；全 hash 写进 entry，Lookup 再做严格全匹配
func artifactApprovalKey(req ApprovalRequest) string {
	repoFp := strings.ToLower(strings.TrimSpace(req.RepoFingerprint))
	name := strings.ToLower(strings.TrimSpace(req.Name))
	kind := strings.TrimSpace(req.ArtifactKind)
	if kind == "" {
		kind = ArtifactKindBody
	}
	locator := strings.TrimSpace(req.ArtifactLocator)
	hash := strings.ToLower(strings.TrimSpace(req.ContentHash))
	if len(hash) > 12 {
		hash = hash[:12]
	}
	return repoFp + "::" + name + "::" + kind + "::" + locator + "@" + hash
}

// Lookup 查询是否存在 (name, contentHash) 的审批记录。严格按全 hash 比对，
// 防止 12 位短 key 碰撞导致的误判。
func (c *ApprovalCache) Lookup(name, contentHash string) (ApprovalEntry, bool) {
	return c.LookupArtifact(ApprovalRequest{
		Name:            name,
		ArtifactKind:    ArtifactKindBody,
		ArtifactLocator: "SKILL.md",
		ContentHash:     contentHash,
	})
}

// LookupArtifact 是 P20.1 §3.2 artifact-level 查询入口。严格按全 hash 比对防碰撞。
// P20.1 Phase 10 Step C：miss (未备案 / hash mismatch) 时计数；
// nil receiver 的默认 no-approval 路径不计数（持有者未配置备案 cache，
// 不属于 "合法查询但未命中" 的有效 miss）。
func (c *ApprovalCache) LookupArtifact(req ApprovalRequest) (ApprovalEntry, bool) {
	if c == nil {
		skillmetrics.IncSkillArtifactApprovalMiss()
		return ApprovalEntry{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[artifactApprovalKey(req)]
	if !ok {
		skillmetrics.IncSkillArtifactApprovalMiss()
		return ApprovalEntry{}, false
	}
	if !strings.EqualFold(entry.ContentHash, req.ContentHash) {
		skillmetrics.IncSkillArtifactApprovalMiss()
		return ApprovalEntry{}, false
	}
	return entry, true
}

// Approve 写入审批记录并持久化到盘上。
//
// 输入校验：
//   - name：经 validateSkillName 验证，不合法返回 ErrInvalidSkillName 的 wrapped error
//     （调用方可 errors.Is(err, ErrInvalidSkillName) 检查）。
//   - contentHash：不能为空，无格式强制但建议传 SHA-256 hex（8-64 字符）。
//   - trust：若非法，兑底为 TrustProject（最不受信任）。
//   - approvedBy：可为空字符串。
//
// 并发语义：
//   - 多个 Approve/Revoke 调用被 writeMu 串行化，保证 snapshot 与 rename 顺序一致、
//     不会出现“先写盘的被后写盘覆盖”的丢失写入。
//   - 读路径 Lookup/Entries 不被盘 IO 阻塞。
//
// 幂等性：同一 (name, contentHash) 重复 Approve 会更新 ApprovedAt/ApprovedBy/Trust
// 字段（修诂效果是“刷新批准时间戳”），返回值为最新 entry。
//
// 部分失败：写盘失败时，entry 已经写入内存 map、但盘上未更新，函数返回非 nil err
// 与最新 entry。调用方应：
//   - 记录错误日志，但可继续使用内存 cache（下次 Approve 会重试整 snapshot）。
//   - 若需硬保证持久化，应将 err 向上传递给用户、让其重试。
func (c *ApprovalCache) Approve(name, contentHash string, trust TrustScope, approvedBy string) (ApprovalEntry, error) {
	return c.ApproveArtifact(ApprovalRequest{
		Name:            name,
		ArtifactKind:    ArtifactKindBody,
		ArtifactLocator: "SKILL.md",
		ContentHash:     contentHash,
		Trust:           trust,
		ApprovedBy:      approvedBy,
	})
}

// ApproveArtifact 是 P20.1 §3.2 artifact-level 写入入口。
//
// 额外校验：
//   - ArtifactKind：空 → 视同 body；非空时必须是合法 kind。
//   - ArtifactLocator：经 NormalizeArtifactLocator 规范化，非法路径直接拒绝。
func (c *ApprovalCache) ApproveArtifact(req ApprovalRequest) (ApprovalEntry, error) {
	if c == nil {
		return ApprovalEntry{}, errors.New("approval cache is nil")
	}
	normalizedName, err := validateSkillName(req.Name)
	if err != nil {
		return ApprovalEntry{}, err
	}
	hash := strings.ToLower(strings.TrimSpace(req.ContentHash))
	if hash == "" {
		return ApprovalEntry{}, errors.New("content hash is required")
	}
	kind := strings.TrimSpace(req.ArtifactKind)
	if kind == "" {
		kind = ArtifactKindBody
	}
	if !IsValidArtifactKind(kind) {
		return ApprovalEntry{}, fmt.Errorf("invalid artifact kind: %q", kind)
	}
	normalizedLocator, err := NormalizeArtifactLocator(kind, req.ArtifactLocator)
	if err != nil {
		return ApprovalEntry{}, err
	}
	trust := req.Trust
	if !trust.Valid() {
		trust = TrustProject
	}
	entry := ApprovalEntry{
		RepoFingerprint: strings.TrimSpace(req.RepoFingerprint),
		Name:            normalizedName,
		ArtifactKind:    kind,
		ArtifactLocator: normalizedLocator,
		ContentHash:     hash,
		Trust:           trust,
		ApprovedAt:      time.Now().UTC(),
		ApprovedBy:      strings.TrimSpace(req.ApprovedBy),
	}
	key := artifactApprovalKey(ApprovalRequest{
		RepoFingerprint: entry.RepoFingerprint,
		Name:            entry.Name,
		ArtifactKind:    entry.ArtifactKind,
		ArtifactLocator: entry.ArtifactLocator,
		ContentHash:     entry.ContentHash,
	})
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	c.entries[key] = entry
	snapshot := c.snapshotLocked()
	c.mu.Unlock()
	if err := writeApprovalFile(c.path, snapshot); err != nil {
		return entry, err
	}
	atomic.AddUint64(&c.revision, 1)
	return entry, nil
}

// Revoke 移除某 name 的所有审批记录（所有 hash 变体），持久化后返回被移除条数。
func (c *ApprovalCache) Revoke(name string) (int, error) {
	if c == nil {
		return 0, errors.New("approval cache is nil")
	}
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return 0, errors.New("name is required")
	}
	// 同 Approve：写路径串行化避免并发丢失。
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	removed := 0
	for key, entry := range c.entries {
		if strings.EqualFold(entry.Name, normalized) {
			delete(c.entries, key)
			removed++
		}
	}
	snapshot := c.snapshotLocked()
	c.mu.Unlock()
	if removed == 0 {
		return 0, nil
	}
	if err := writeApprovalFile(c.path, snapshot); err != nil {
		return removed, err
	}
	atomic.AddUint64(&c.revision, 1)
	return removed, nil
}

// Revision returns a monotonic in-memory approval revision.
// Revision 处理revision。
func (c *ApprovalCache) Revision() uint64 {
	if c == nil {
		return 0
	}
	return atomic.LoadUint64(&c.revision)
}

// Path 暴露缓存文件路径（测试 / 诊断用）。
func (c *ApprovalCache) Path() string {
	if c == nil {
		return ""
	}
	return c.path
}

// Entries 返回当前所有条目的拷贝（只读快照）。
func (c *ApprovalCache) Entries() []ApprovalEntry {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotLocked()
}

// snapshotLocked 要求调用方持有 c.mu（读或写锁均可）。
func (c *ApprovalCache) snapshotLocked() []ApprovalEntry {
	out := make([]ApprovalEntry, 0, len(c.entries))
	for _, entry := range c.entries {
		out = append(out, entry)
	}
	return out
}

// writeApprovalFile 原子写盘：先写临时文件到同目录，再 rename 替换目标文件。
// 临时文件失败时尽力清理，避免残留。
func writeApprovalFile(path string, entries []ApprovalEntry) error {
	payload := approvalFile{
		Version: approvalFileVersion,
		Entries: entries,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal approval cache: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "skills-trust-*.json")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename temp file to %s: %w", path, err)
	}
	return nil
}
