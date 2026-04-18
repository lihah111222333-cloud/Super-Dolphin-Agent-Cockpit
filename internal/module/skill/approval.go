package skill

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ApprovalCache 是 P20 Phase 1 的 skill 审批决议持久化层。
//
// 目的：对不受信任域（TrustProject，来自 git clone）的 skill，在首次扫描或 `skill_expand`
// 调用前要求用户审批；审批结果按 (name, content_hash[:12]) 键缓存，避免每次弹窗骚扰，
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
	writeMu sync.Mutex
}

// ApprovalEntry 是单条审批记录。
type ApprovalEntry struct {
	Name        string     `json:"name"`
	ContentHash string     `json:"content_hash"`
	Trust       TrustScope `json:"trust"`
	ApprovedAt  time.Time  `json:"approved_at"`
	ApprovedBy  string     `json:"approved_by,omitempty"`
}

// approvalFile 是盘上 JSON 的外层结构。
type approvalFile struct {
	Version int             `json:"version"`
	Entries []ApprovalEntry `json:"entries"`
}

const approvalFileVersion = 1

// ErrApprovalCachePathRequired 表示构造 cache 时必须提供非空路径。
var ErrApprovalCachePathRequired = errors.New("approval cache path is required")

// DefaultApprovalCachePath 返回默认的审批缓存文件路径（`~/.multi-agent/skills-trust.json`）。
// 环境变量 `SKILLS_TRUST_PATH` 可覆盖；UserHomeDir 失败兜底到 os.TempDir()。
func DefaultApprovalCachePath() string {
	if override := strings.TrimSpace(os.Getenv("SKILLS_TRUST_PATH")); override != "" {
		return override
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".multi-agent", "skills-trust.json")
	}
	return filepath.Join(os.TempDir(), "multi-agent-skills-trust.json")
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
		for _, entry := range payload.Entries {
			if entry.Name == "" || entry.ContentHash == "" {
				continue
			}
			cache.entries[approvalKey(entry.Name, entry.ContentHash)] = entry
		}
		return cache, nil
	case errors.Is(err, os.ErrNotExist):
		return cache, nil
	default:
		return cache, fmt.Errorf("approval cache %s read failed: %w", path, err)
	}
}

// approvalKey 生成 map key。取 hash 前 12 位（48 bits），在 skill 数量级 < 10⁴ 时
// 碰撞概率忽略不计；全 hash 还是写进 entry 便于严格对比。
func approvalKey(name, contentHash string) string {
	trimmed := strings.ToLower(strings.TrimSpace(contentHash))
	if len(trimmed) > 12 {
		trimmed = trimmed[:12]
	}
	return strings.ToLower(strings.TrimSpace(name)) + "@" + trimmed
}

// Lookup 查询是否存在 (name, contentHash) 的审批记录。严格按全 hash 比对，
// 防止 12 位短 key 碰撞导致的误判。
func (c *ApprovalCache) Lookup(name, contentHash string) (ApprovalEntry, bool) {
	if c == nil {
		return ApprovalEntry{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[approvalKey(name, contentHash)]
	if !ok {
		return ApprovalEntry{}, false
	}
	if !strings.EqualFold(entry.ContentHash, contentHash) {
		return ApprovalEntry{}, false
	}
	return entry, true
}

// Approve 写入审批记录并持久化。approvedBy 可为空。
func (c *ApprovalCache) Approve(name, contentHash string, trust TrustScope, approvedBy string) (ApprovalEntry, error) {
	if c == nil {
		return ApprovalEntry{}, errors.New("approval cache is nil")
	}
	normalizedName, err := validateSkillName(name)
	if err != nil {
		return ApprovalEntry{}, err
	}
	hash := strings.ToLower(strings.TrimSpace(contentHash))
	if hash == "" {
		return ApprovalEntry{}, errors.New("content hash is required")
	}
	if !trust.Valid() {
		trust = TrustProject // 兜底最不信任档
	}
	entry := ApprovalEntry{
		Name:        normalizedName,
		ContentHash: hash,
		Trust:       trust,
		ApprovedAt:  time.Now().UTC(),
		ApprovedBy:  strings.TrimSpace(approvedBy),
	}
	// 写路径串行化：先拿 writeMu，再在其内部操作 mu，保证 snapshot 与 rename 的顺序一致。
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	c.entries[approvalKey(normalizedName, hash)] = entry
	snapshot := c.snapshotLocked()
	c.mu.Unlock()
	if err := writeApprovalFile(c.path, snapshot); err != nil {
		return entry, err
	}
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
	return removed, nil
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
