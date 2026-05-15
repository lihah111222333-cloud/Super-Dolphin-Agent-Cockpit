package multilsp

import (
	"encoding/json"
	"errors"
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

	// Workspace and Language are legacy persistent-cache fields retained so
	// older cache files can still be read and then rewritten with the scoped key.
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
	LastResolvedScope lspResolvedScope `json:"last_resolved_scope"`
	LastFingerprint   string           `json:"last_fingerprint,omitempty"`
	LastSeenAt        time.Time        `json:"last_seen_at"`
}

type lspResolvedScope struct {
	ScopeKey             string `json:"scope_key,omitempty"`
	WorkspaceKey         string `json:"workspace_key,omitempty"`
	ManagerKey           string `json:"manager_key,omitempty"`
	WorkspaceRoot        string `json:"workspace_root,omitempty"`
	LanguageID           string `json:"language_id,omitempty"`
	LanguageSpecificHash string `json:"language_specific_hash,omitempty"`
}

type lspCacheConfig struct {
	TTL        time.Duration
	Persistent bool
	Dir        string
	Logger     *slog.Logger
}

// lspCacheStore holds per-workspace LSP document bootstrap state and
// lets expired entries age out. Pre-P22 P2 LSP-S2 the struct also
// owned a 1h background cleanup loop launched from newLSPCacheStore.
// The cleanup is now amortised across every
// Load/Upsert/WorkspaceDocuments via maybeCleanup, which the plan
// (§483 / §490) classifies as the correct pattern — no constructor-owned
// goroutine, no extra shutdown path beyond the resource's own Close().
type lspCacheStore struct {
	mu sync.RWMutex

	config lspCacheConfig
	now    func() time.Time

	memory map[string]lspCacheValue
	index  map[lspDocumentIndexKey]lspDocumentIndexValue

	tombstones map[string]time.Time

	persistent      bool
	persistentReady bool
	fallbackWarned  bool
}

type lspCacheDiskState struct {
	Documents []lspCacheValue `json:"documents"`
}

func newLSPCacheStoreFromEnv(logger *slog.Logger) *lspCacheStore {
	cfg := lspCacheConfig{
		TTL:        defaultLSPCacheTTL,
		Persistent: strings.TrimSpace(os.Getenv(lspCachePersistentEnv)) == "1",
		Dir:        strings.TrimSpace(os.Getenv(lspCacheDirEnv)),
		Logger:     logger,
	}
	return newLSPCacheStore(cfg)
}

func newLSPCacheStore(cfg lspCacheConfig) *lspCacheStore {
	if cfg.TTL <= 0 {
		cfg.TTL = defaultLSPCacheTTL
	}
	store := &lspCacheStore{
		config:         cfg,
		now:            time.Now,
		memory:         map[string]lspCacheValue{},
		index:          map[lspDocumentIndexKey]lspDocumentIndexValue{},
		tombstones:     map[string]time.Time{},
		persistent:     cfg.Persistent,
		fallbackWarned: false,
	}
	if store.persistent {
		store.ensurePersistentReady()
		_ = store.loadPersistent()
	}
	// P22 P2 LSP-S2: no background cleanup goroutine; TTL expiry is
	// applied inline by maybeCleanup on every Load/Upsert/WorkspaceDocuments.
	return store
}

func (s *lspCacheStore) Enabled() bool {
	return s != nil
}

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

func (s *lspCacheStore) Upsert(value lspCacheValue) {
	if s == nil {
		return
	}
	s.maybeCleanup()
	withWriteLock(&s.mu, func() struct{} {
		value.UpdatedAt = s.now()
		s.memory[value.Key.String()] = value
		delete(s.tombstones, value.Key.String())
		s.persistOnMutation(true)
		return struct{}{}
	})
}

func (s *lspCacheStore) Delete(key lspCacheKey) {
	if s == nil {
		return
	}
	withWriteLock(&s.mu, func() struct{} {
		_, existed := s.memory[key.String()]
		delete(s.memory, key.String())
		s.persistOnMutation(existed)
		return struct{}{}
	})
}

func (s *lspCacheStore) Tombstone(key lspCacheKey) {
	if s == nil {
		return
	}
	withWriteLock(&s.mu, func() struct{} {
		_, existed := s.memory[key.String()]
		delete(s.memory, key.String())
		s.tombstones[key.String()] = s.now()
		s.persistOnMutation(existed)
		return struct{}{}
	})
}

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

func (s *lspCacheStore) ScopeDocuments(scope lspResolvedScope) []lspCacheValue {
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

func (s *lspCacheStore) WorkspaceURIs(workspace string) []string {
	values := s.WorkspaceDocuments(workspace)
	uris := make([]string, 0, len(values))
	for _, value := range values {
		uris = append(uris, value.Key.URI)
	}
	return uris
}

func (s *lspCacheStore) ScopeURIs(scope lspResolvedScope) []string {
	values := s.ScopeDocuments(scope)
	uris := make([]string, 0, len(values))
	for _, value := range values {
		uris = append(uris, value.Key.URI)
	}
	return uris
}

func (s *lspCacheStore) RememberDocumentScope(uri string, scope lspResolvedScope, fingerprint string) {
	if s == nil || strings.TrimSpace(uri) == "" {
		return
	}
	withWriteLock(&s.mu, func() struct{} {
		s.index[lspDocumentIndexKey{URI: uri}] = lspDocumentIndexValue{
			LastResolvedScope: scope,
			LastFingerprint:   fingerprint,
			LastSeenAt:        s.now(),
		}
		return struct{}{}
	})
}

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

// Close is retained as the *lspCacheStore shutdown hook for API
// stability. Pre-P22 P2 LSP-S2 it closed the stopCh that drove the
// background cleanupLoop; cleanup is now amortised across every access,
// so this method is a no-op. Keeping the method means callers like
// closeBootstrapCoordinator don't need to change and a future persistent
// flush-on-close can hang off the same entry point without another
// refactor.
func (s *lspCacheStore) Close() {
	if s == nil {
		return
	}
}

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
		}
		s.persistOnMutation(changed)
		return struct{}{}
	})
}

func (s *lspCacheStore) ensurePersistentReady() {
	path := s.cachePath()
	if path == "" {
		s.fallbackToMemory(errors.New("cache path is empty"))
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.fallbackToMemory(err)
		return
	}
	probePath := path + ".probe"
	if err := os.WriteFile(probePath, []byte("ok"), 0o644); err != nil {
		s.fallbackToMemory(err)
		return
	}
	_ = os.Remove(probePath)
	s.persistentReady = true
}

func (s *lspCacheStore) loadPersistent() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.persistent || !s.persistentReady {
		return nil
	}
	payload, err := os.ReadFile(s.cachePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		s.fallbackToMemory(err)
		return err
	}
	var disk lspCacheDiskState
	if err := json.Unmarshal(payload, &disk); err != nil {
		s.fallbackToMemory(err)
		return err
	}
	now := s.now()
	for _, value := range disk.Documents {
		if s.expired(value, now) {
			continue
		}
		s.memory[value.Key.String()] = value
	}
	return nil
}

func (s *lspCacheStore) persistLocked() error {
	if !s.persistent || !s.persistentReady {
		return nil
	}
	disk := lspCacheDiskState{
		Documents: make([]lspCacheValue, 0, len(s.memory)),
	}
	for _, value := range s.memory {
		disk.Documents = append(disk.Documents, value)
	}
	payload, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.cachePath(), payload, 0o644)
}

func (s *lspCacheStore) fallbackToMemory(err error) {
	s.persistent = false
	s.persistentReady = false
	if s.fallbackWarned || s.config.Logger == nil {
		s.fallbackWarned = true
		return
	}
	s.fallbackWarned = true
	s.config.Logger.Warn("LSP cache fell back to memory", "err", err)
}

func (s *lspCacheStore) expired(value lspCacheValue, now time.Time) bool {
	if s.config.TTL <= 0 || value.UpdatedAt.IsZero() {
		return false
	}
	return now.Sub(value.UpdatedAt) > s.config.TTL
}

func (k lspCacheKey) String() string {
	return cacheKeyScope(k) + "\x00" +
		cacheKeyWorkspace(k) + "\x00" +
		cacheKeyLanguage(k) + "\x00" +
		k.URI + "\x00" +
		k.LanguageSpecificHash
}

func (s lspResolvedScope) cacheKey(languageID, uri string) lspCacheKey {
	lang := normalizeLanguageID(languageID)
	if lang == "" {
		lang = normalizeLanguageID(s.LanguageID)
	}
	return lspCacheKey{
		ScopeKey:             s.ScopeKey,
		WorkspaceKey:         s.WorkspaceKey,
		LanguageID:           lang,
		URI:                  uri,
		LanguageSpecificHash: s.LanguageSpecificHash,
	}
}

func (s lspResolvedScope) bootstrapKey() string {
	if s.ManagerKey != "" {
		return s.ManagerKey
	}
	if s.ScopeKey != "" || s.WorkspaceKey != "" {
		return s.ScopeKey + "\x00" + s.WorkspaceKey
	}
	return s.WorkspaceRoot
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
