package multilsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	defaultLSPCacheTTL = 7 * 24 * time.Hour
	lspCacheFileName   = "lsp-cache.json"
)

const (
	lspCachePersistentEnv = "AGENT_LSP_CACHE_PERSISTENT"
	lspCacheDirEnv        = "AGENT_LSP_CACHE_DIR"
)

type lspCacheKey struct {
	ScopeKey             string `json:"scope_key,omitempty"`
	WorkspaceKey         string `json:"workspace_key,omitempty"`
	LanguageID           string `json:"language_id,omitempty"`
	URI                  string `json:"uri"`
	LanguageSpecificHash string `json:"language_specific_hash,omitempty"`

	// 旧磁盘缓存可能只写入 Workspace/Language；读取后会按 scoped key 重写，避免丢失既有缓存。
	Workspace string `json:"workspace,omitempty"`
	Language  string `json:"language,omitempty"`
}

type lspCacheValue struct {
	Key             lspCacheKey `json:"key"`
	Version         int         `json:"version"`
	Fingerprint     string      `json:"fingerprint"`
	ModTimeUnixNano int64       `json:"mod_time_unix_nano"`
	Size            int64       `json:"size"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

type lspDocumentIndexKey struct {
	URI string
}

type lspDocumentIndexValue struct {
	LastResolvedScope ResolvedLSPToolScope `json:"last_resolved_scope"`
	LastFingerprint   string               `json:"last_fingerprint,omitempty"`
	LastSeenAt        time.Time            `json:"last_seen_at"`
}

type lspCacheConfig struct {
	TTL        time.Duration
	Persistent bool
	Path       string
	Dir        string
	Logger     *slog.Logger
}

// lspCacheStore 保存 LSP 文档启动缓存、最近 scope 索引和删除墓碑。
// TTL 清理在读写入口摊销执行，构造函数不启动后台 goroutine，关闭路径也不承担额外清理责任。
type lspCacheStore struct {
	mu sync.RWMutex

	config lspCacheConfig
	now    func() time.Time

	memory map[string]lspCacheValue
	index  map[lspDocumentIndexKey]lspDocumentIndexValue

	tombstones map[string]time.Time

	persistent      bool
	persistentReady bool
}

type lspCacheMutationStore = lspCacheStore

type lspCacheDiskState struct {
	Documents  []lspCacheValue  `json:"documents"`
	Tombstones map[string]int64 `json:"tombstones,omitempty"`
}

func newLSPCacheStoreFromEnv(logger *slog.Logger) (*lspCacheStore, error) {
	cfg := lspCacheConfig{
		TTL:        defaultLSPCacheTTL,
		Persistent: strings.TrimSpace(os.Getenv(lspCachePersistentEnv)) == "1",
		Dir:        strings.TrimSpace(os.Getenv(lspCacheDirEnv)),
		Logger:     logger,
	}
	return newLSPCacheStore(cfg)
}

func newLSPCacheStore(cfg lspCacheConfig) (*lspCacheStore, error) {
	if cfg.TTL <= 0 {
		cfg.TTL = defaultLSPCacheTTL
	}
	store := &lspCacheStore{
		config:     cfg,
		now:        time.Now,
		memory:     map[string]lspCacheValue{},
		index:      map[lspDocumentIndexKey]lspDocumentIndexValue{},
		tombstones: map[string]time.Time{},
		persistent: cfg.Persistent,
	}
	if store.persistent {
		if err := store.ensurePersistentReady(); err != nil {
			return nil, fmt.Errorf("persistent cache setup: %w", err)
		}
		if err := store.loadPersistent(); err != nil {
			return nil, fmt.Errorf("persistent cache load: %w", err)
		}
	}
	// 过期清理由读写路径触发，避免缓存构造时隐式启动长期 goroutine。
	return store, nil
}

// Enabled 报告缓存实例是否已创建。
// nil store 会被视为关闭状态，调用方可据此跳过缓存路径。
func (s *lspCacheStore) Enabled() bool {
	return s != nil
}

// Load 按完整缓存 key 读取未过期文档记录。
// 命中删除墓碑或 TTL 过期时返回 false，防止旧持久化记录被重新使用。
func (s *lspCacheStore) Load(key lspCacheKey) (lspCacheValue, bool) {
	if s == nil {
		return lspCacheValue{}, false
	}
	s.maybeCleanup()
	result := withReadLock(&s.mu, func() struct {
		value lspCacheValue
		ok    bool
	} {
		value, ok := s.memory[key.String()]
		if _, tombstoned := s.tombstones[key.String()]; tombstoned {
			return struct {
				value lspCacheValue
				ok    bool
			}{}
		}
		if !ok || s.expired(value, s.now()) {
			return struct {
				value lspCacheValue
				ok    bool
			}{}
		}
		return struct {
			value lspCacheValue
			ok    bool
		}{value: value, ok: true}
	})
	return result.value, result.ok
}

// Upsert 写入文档缓存并清除同 key 墓碑。
// 持久化开启时会同步落盘，落盘失败直接返回给调用方，避免内存和磁盘状态悄悄分叉。
func (s *lspCacheMutationStore) Upsert(value lspCacheValue) error {
	if s == nil {
		return nil
	}
	s.maybeCleanup()
	return withWriteLock(&s.mu, func() error {
		value.UpdatedAt = s.now()
		s.memory[value.Key.String()] = value
		delete(s.tombstones, value.Key.String())
		return s.persistOnMutation(true)
	})
}

// Delete 从内存和磁盘缓存中移除文档记录。
// 它不写墓碑，适合普通过期或显式释放场景。
func (s *lspCacheMutationStore) Delete(key lspCacheKey) error {
	if s == nil {
		return nil
	}
	return withWriteLock(&s.mu, func() error {
		_, existed := s.memory[key.String()]
		delete(s.memory, key.String())
		return s.persistOnMutation(existed)
	})
}

// Tombstone 删除记录并留下短期墓碑。
// 墓碑用于阻止旧磁盘缓存或延迟刷新把已释放文档重新加载回来。
func (s *lspCacheMutationStore) Tombstone(key lspCacheKey) error {
	if s == nil {
		return nil
	}
	return withWriteLock(&s.mu, func() error {
		_, existed := s.memory[key.String()]
		delete(s.memory, key.String())
		_, alreadyTombstoned := s.tombstones[key.String()]
		s.tombstones[key.String()] = s.now()
		return s.persistOnMutation(existed || !alreadyTombstoned)
	})
}

// WorkspaceDocuments 返回指定 workspace 下未过期的缓存文档。
// 结果按最近更新时间倒序排列，更新时间相同则按 URI 稳定排序。
func (s *lspCacheStore) WorkspaceDocuments(workspace string) []lspCacheValue {
	if s == nil {
		return nil
	}
	s.maybeCleanup()
	return withReadLock(&s.mu, func() []lspCacheValue {
		now := s.now()
		values := make([]lspCacheValue, 0, len(s.memory))
		for _, value := range s.memory {
			if cacheKeyWorkspace(value.Key) != workspace || s.expired(value, now) {
				continue
			}
			values = append(values, value)
		}
		slices.SortFunc(values, func(left, right lspCacheValue) int {
			if left.UpdatedAt.Equal(right.UpdatedAt) {
				return strings.Compare(left.Key.URI, right.Key.URI)
			}
			if left.UpdatedAt.After(right.UpdatedAt) {
				return -1
			}
			return 1
		})
		return values
	})
}

// ScopeDocuments 返回指定 resolved scope 下未过期的缓存文档。
// scope key 与 workspace key 必须同时匹配，避免跨 agent 或跨 worktree 泄漏诊断状态。
func (s *lspCacheStore) ScopeDocuments(scope ResolvedLSPToolScope) []lspCacheValue {
	if s == nil {
		return nil
	}
	s.maybeCleanup()
	return withReadLock(&s.mu, func() []lspCacheValue {
		now := s.now()
		values := make([]lspCacheValue, 0, len(s.memory))
		for _, value := range s.memory {
			if cacheKeyScope(value.Key) != scope.ScopeKey || cacheKeyWorkspace(value.Key) != scope.WorkspaceKey || s.expired(value, now) {
				continue
			}
			values = append(values, value)
		}
		slices.SortFunc(values, func(left, right lspCacheValue) int {
			if left.UpdatedAt.Equal(right.UpdatedAt) {
				return strings.Compare(left.Key.URI, right.Key.URI)
			}
			if left.UpdatedAt.After(right.UpdatedAt) {
				return -1
			}
			return 1
		})
		return values
	})
}

// WorkspaceURIs 返回指定 workspace 的缓存文档 URI 列表。
func (s *lspCacheStore) WorkspaceURIs(workspace string) []string {
	values := s.WorkspaceDocuments(workspace)
	uris := make([]string, 0, len(values))
	for _, value := range values {
		uris = append(uris, value.Key.URI)
	}
	return uris
}

// ScopeURIs 返回指定 resolved scope 的缓存文档 URI 列表。
func (s *lspCacheStore) ScopeURIs(scope ResolvedLSPToolScope) []string {
	values := s.ScopeDocuments(scope)
	uris := make([]string, 0, len(values))
	for _, value := range values {
		uris = append(uris, value.Key.URI)
	}
	return uris
}

// RememberDocumentScope 记录文档最近一次解析出的 resolved scope。
// 该索引用于后续释放或重新启动时找回文档归属，不写入持久化缓存。
func (s *lspCacheMutationStore) RememberDocumentScope(uri string, scope ResolvedLSPToolScope, fingerprint string) error {
	if s == nil || strings.TrimSpace(uri) == "" {
		return nil
	}
	withWriteLock(&s.mu, func() struct{} {
		s.index[lspDocumentIndexKey{URI: uri}] = lspDocumentIndexValue{
			LastResolvedScope: scope,
			LastFingerprint:   fingerprint,
			LastSeenAt:        s.now(),
		}
		return struct{}{}
	})
	return nil
}

// LastResolvedScope 查询文档最近一次记录的 resolved scope。
// 空 URI 或未命中时返回 false，调用方需要重新解析目标作用域。
func (s *lspCacheStore) LastResolvedScope(uri string) (lspDocumentIndexValue, bool) {
	if s == nil || strings.TrimSpace(uri) == "" {
		return lspDocumentIndexValue{}, false
	}
	result := withReadLock(&s.mu, func() struct {
		value lspDocumentIndexValue
		ok    bool
	} {
		value, ok := s.index[lspDocumentIndexKey{URI: uri}]
		return struct {
			value lspDocumentIndexValue
			ok    bool
		}{value: value, ok: ok}
	})
	return result.value, result.ok
}

func (s *lspCacheStore) cachePath() string {
	if path := strings.TrimSpace(s.config.Path); path != "" {
		return filepath.Clean(path)
	}
	dir := strings.TrimSpace(s.config.Dir)
	if dir == "" {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(cacheDir, "super-agent-v3", "mcp-lsp")
	}
	return filepath.Join(dir, lspCacheFileName)
}

// Close 保留缓存关闭钩子但当前不做额外工作。
// 清理和持久化都发生在读写路径；保留方法是为了让上层资源释放流程保持统一。
func (s *lspCacheStore) Close() {
	if s == nil {
		return
	}
}

// maybeCleanup 摊销清理过期文档和过期墓碑。
// 持久化开启时，发生变更会立即重写磁盘状态；失败只记录告警，不阻断当前缓存读写。
func (s *lspCacheStore) maybeCleanup() {
	if s == nil {
		return
	}
	withWriteLock(&s.mu, func() struct{} {
		now := s.now()
		changed := false
		for key, value := range s.memory {
			if !s.expired(value, now) {
				continue
			}
			delete(s.memory, key)
			changed = true
		}
		for key, createdAt := range s.tombstones {
			if s.config.TTL > 0 && now.Sub(createdAt) <= minDuration(time.Minute, s.config.TTL) {
				continue
			}
			delete(s.tombstones, key)
			changed = true
		}
		if err := s.persistOnMutation(changed); err != nil && s.config.Logger != nil {
			s.config.Logger.Warn("LSP persistent cache cleanup failed", "err", err)
		}
		return struct{}{}
	})
}

func (s *lspCacheStore) ensurePersistentReady() error {
	path := s.cachePath()
	if path == "" {
		return errors.New("cache path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	probePath := path + ".probe"
	if err := os.WriteFile(probePath, []byte("ok"), 0o644); err != nil {
		return err
	}
	_ = os.Remove(probePath)
	s.persistentReady = true
	return nil
}

func (s *lspCacheStore) loadPersistent() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.persistent || !s.persistentReady {
		return nil
	}
	disk, err := s.readPersistentDiskStateLocked()
	if err != nil {
		return err
	}
	if s.loadPersistentDiskStateLocked(disk) {
		return s.persistLocked()
	}
	return nil
}

func (s *lspCacheStore) readPersistentDiskStateLocked() (lspCacheDiskState, error) {
	payload, err := os.ReadFile(s.cachePath())
	if err != nil {
		if os.IsNotExist(err) {
			return lspCacheDiskState{}, nil
		}
		return lspCacheDiskState{}, err
	}
	var disk lspCacheDiskState
	if err := json.Unmarshal(payload, &disk); err != nil {
		return lspCacheDiskState{}, err
	}
	return disk, nil
}

func (s *lspCacheStore) loadPersistentDiskStateLocked(disk lspCacheDiskState) bool {
	now := s.now()
	changed := s.loadPersistentTombstonesLocked(disk.Tombstones, now)
	for _, value := range disk.Documents {
		if s.shouldSkipPersistentValue(value, now) {
			changed = true
			continue
		}
		s.memory[value.Key.String()] = value
	}
	return changed
}

func (s *lspCacheStore) loadPersistentTombstonesLocked(raw map[string]int64, now time.Time) bool {
	changed := false
	for key, unixNano := range raw {
		createdAt, ok := s.validPersistentTombstone(key, unixNano, now)
		if !ok {
			changed = true
			continue
		}
		s.tombstones[key] = createdAt
	}
	return changed
}

func (s *lspCacheStore) validPersistentTombstone(key string, unixNano int64, now time.Time) (time.Time, bool) {
	if strings.TrimSpace(key) == "" || unixNano <= 0 {
		return time.Time{}, false
	}
	createdAt := time.Unix(0, unixNano)
	if s.config.TTL > 0 && now.Sub(createdAt) > minDuration(time.Minute, s.config.TTL) {
		return time.Time{}, false
	}
	return createdAt, true
}

func (s *lspCacheStore) shouldSkipPersistentValue(value lspCacheValue, now time.Time) bool {
	if s.expired(value, now) {
		return true
	}
	_, tombstoned := s.tombstones[value.Key.String()]
	return tombstoned
}

func (s *lspCacheStore) persistLocked() error {
	if !s.persistent || !s.persistentReady {
		return nil
	}
	disk := s.persistentDiskStateLocked()
	payload, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	return s.writePersistentPayloadLocked(payload)
}

// persistentDiskStateLocked 在持锁状态下生成磁盘快照。
// 墓碑为空时省略字段，避免持久化文件长期保留空 map 噪声。
func (s *lspCacheStore) persistentDiskStateLocked() lspCacheDiskState {
	disk := lspCacheDiskState{
		Documents:  make([]lspCacheValue, 0, len(s.memory)),
		Tombstones: make(map[string]int64, len(s.tombstones)),
	}
	for _, value := range s.memory {
		disk.Documents = append(disk.Documents, value)
	}
	for key, createdAt := range s.tombstones {
		if strings.TrimSpace(key) != "" && !createdAt.IsZero() {
			disk.Tombstones[key] = createdAt.UnixNano()
		}
	}
	if len(disk.Tombstones) == 0 {
		disk.Tombstones = nil
	}
	return disk
}

// writePersistentPayloadLocked 通过临时文件加 rename 原子写入缓存文件。
// 任一步失败都会清理临时文件并返回错误，避免留下半写入 JSON。
func (s *lspCacheStore) writePersistentPayloadLocked(payload []byte) error {
	path := s.cachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func (s *lspCacheStore) expired(value lspCacheValue, now time.Time) bool {
	if s.config.TTL <= 0 || value.UpdatedAt.IsZero() {
		return false
	}
	return now.Sub(value.UpdatedAt) > s.config.TTL
}

// String 生成缓存 key 的稳定拼接形式。
// 分隔符使用 NUL，避免普通路径或语言标识里的字符造成歧义。
func (k lspCacheKey) String() string {
	return cacheKeyScope(k) + "\x00" +
		cacheKeyWorkspace(k) + "\x00" +
		cacheKeyLanguage(k) + "\x00" +
		k.URI + "\x00" +
		k.LanguageSpecificHash
}

func (s ResolvedLSPToolScope) cacheKey(languageID, uri string) lspCacheKey {
	lang := normalizeLanguageID(languageID)
	if lang == "" {
		lang = normalizeLanguageID(s.LanguageID)
	}
	return lspCacheKey{
		ScopeKey:             s.ScopeKey,
		WorkspaceKey:         s.WorkspaceKey,
		LanguageID:           lang,
		URI:                  uri,
		LanguageSpecificHash: languageSpecificCacheHash(s.LanguageSpecific),
	}
}

func (s ResolvedLSPToolScope) bootstrapKey() string {
	if s.ManagerKey != "" {
		return s.ManagerKey
	}
	if s.ScopeKey != "" || s.WorkspaceKey != "" {
		return s.ScopeKey + "\x00" + s.WorkspaceKey
	}
	return s.WorkspaceRoot
}

func languageSpecificCacheHash(values map[string]string) string {
	encoded := encodeLanguageSpecific(values)
	if encoded == "" {
		return ""
	}
	return hashDocument([]byte(encoded))
}

func cacheKeyScope(key lspCacheKey) string {
	return strings.TrimSpace(key.ScopeKey)
}

func cacheKeyWorkspace(key lspCacheKey) string {
	if key.WorkspaceKey != "" {
		return key.WorkspaceKey
	}
	return key.Workspace
}

func cacheKeyLanguage(key lspCacheKey) string {
	if key.LanguageID != "" {
		return normalizeLanguageID(key.LanguageID)
	}
	return normalizeLanguageID(key.Language)
}
