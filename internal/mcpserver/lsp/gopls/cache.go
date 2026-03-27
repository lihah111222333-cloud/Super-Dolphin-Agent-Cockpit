package gopls

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
	defaultLSPCacheTTL      = 7 * 24 * time.Hour
	lspCacheCleanupInterval = time.Hour
	lspCacheFileName        = "gopls-cache.json"
)

const (
	lspCachePersistentEnv = "AGENT_LSP_CACHE_PERSISTENT"
	lspCacheDirEnv        = "AGENT_LSP_CACHE_DIR"
)

type lspCacheKey struct {
	Workspace string `json:"workspace"`
	Language  string `json:"language"`
	URI       string `json:"uri"`
}

type lspCacheValue struct {
	Key             lspCacheKey `json:"key"`
	Version         int         `json:"version"`
	Fingerprint     string      `json:"fingerprint"`
	ModTimeUnixNano int64       `json:"mod_time_unix_nano"`
	Size            int64       `json:"size"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

type lspCacheBaseline struct {
	Workspace string    `json:"workspace"`
	Language  string    `json:"language"`
	URIs      []string  `json:"uris"`
	UpdatedAt time.Time `json:"updated_at"`
}

type lspCacheConfig struct {
	TTL        time.Duration
	Persistent bool
	Dir        string
	Logger     *slog.Logger
}

type lspCacheStore struct {
	mu sync.RWMutex

	config lspCacheConfig
	now    func() time.Time
	stopCh chan struct{}

	memory    map[string]lspCacheValue
	baselines map[string]lspCacheBaseline

	persistent      bool
	persistentReady bool
	fallbackWarned  bool
	closeOnce       sync.Once
}

type lspCacheDiskState struct {
	Documents []lspCacheValue    `json:"documents"`
	Baselines []lspCacheBaseline `json:"baselines"`
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
		stopCh:         make(chan struct{}),
		memory:         map[string]lspCacheValue{},
		baselines:      map[string]lspCacheBaseline{},
		persistent:     cfg.Persistent,
		fallbackWarned: false,
	}
	if store.persistent {
		store.ensurePersistentReady()
		_ = store.loadPersistent()
	}
	go store.cleanupLoop()
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

	s.mu.RLock()
	defer s.mu.RUnlock()

	value, ok := s.memory[key.String()]
	if !ok || s.expired(value, s.now()) {
		return lspCacheValue{}, false
	}
	return value, true
}

func (s *lspCacheStore) Upsert(value lspCacheValue) {
	if s == nil {
		return
	}
	s.maybeCleanup()

	s.mu.Lock()
	defer s.mu.Unlock()

	value.UpdatedAt = s.now()
	s.memory[value.Key.String()] = value
	s.upsertBaselineLocked(value.Key)
	if err := s.persistLocked(); err != nil {
		s.fallbackToMemory(err)
	}
}

func (s *lspCacheStore) Delete(key lspCacheKey) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.memory, key.String())
	s.rebuildBaselineLocked(key.Workspace, key.Language)
	if err := s.persistLocked(); err != nil {
		s.fallbackToMemory(err)
	}
}

func (s *lspCacheStore) WorkspaceDocuments(workspace string) []lspCacheValue {
	if s == nil {
		return nil
	}
	s.maybeCleanup()

	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.now()
	values := make([]lspCacheValue, 0, len(s.memory))
	for _, value := range s.memory {
		if value.Key.Workspace != workspace || s.expired(value, now) {
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
}

func (s *lspCacheStore) WorkspaceURIs(workspace string) []string {
	values := s.WorkspaceDocuments(workspace)
	uris := make([]string, 0, len(values))
	for _, value := range values {
		uris = append(uris, value.Key.URI)
	}
	return uris
}

func (s *lspCacheStore) cachePath() string {
	dir := strings.TrimSpace(s.config.Dir)
	if dir == "" {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(cacheDir, "super-agent-v3", "gopls")
	}
	return filepath.Join(dir, lspCacheFileName)
}

func (s *lspCacheStore) cleanupLoop() {
	ticker := time.NewTicker(lspCacheCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.maybeCleanup()
		}
	}
}

func (s *lspCacheStore) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		close(s.stopCh)
	})
}

func (s *lspCacheStore) maybeCleanup() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	changed := false
	for key, value := range s.memory {
		if !s.expired(value, now) {
			continue
		}
		delete(s.memory, key)
		s.rebuildBaselineLocked(value.Key.Workspace, value.Key.Language)
		changed = true
	}
	if changed {
		if err := s.persistLocked(); err != nil {
			s.fallbackToMemory(err)
		}
	}
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
	for _, baseline := range disk.Baselines {
		s.baselines[baseline.String()] = baseline
	}
	return nil
}

func (s *lspCacheStore) persistLocked() error {
	if !s.persistent || !s.persistentReady {
		return nil
	}
	disk := lspCacheDiskState{
		Documents: make([]lspCacheValue, 0, len(s.memory)),
		Baselines: make([]lspCacheBaseline, 0, len(s.baselines)),
	}
	for _, value := range s.memory {
		disk.Documents = append(disk.Documents, value)
	}
	for _, baseline := range s.baselines {
		disk.Baselines = append(disk.Baselines, baseline)
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
	s.config.Logger.Warn("gopls cache fell back to memory", "err", err)
}

func (s *lspCacheStore) upsertBaselineLocked(key lspCacheKey) {
	baselineKey := key.workspaceBaseline()
	baseline := s.baselines[baselineKey.String()]
	baseline.Workspace = key.Workspace
	baseline.Language = key.Language
	if !slices.Contains(baseline.URIs, key.URI) {
		baseline.URIs = append(baseline.URIs, key.URI)
		slices.Sort(baseline.URIs)
	}
	baseline.UpdatedAt = s.now()
	s.baselines[baselineKey.String()] = baseline
}

func (s *lspCacheStore) rebuildBaselineLocked(workspace, language string) {
	baselineKey := lspCacheKey{Workspace: workspace, Language: language}.workspaceBaseline()
	uris := make([]string, 0, len(s.memory))
	for _, value := range s.memory {
		if value.Key.Workspace == workspace && value.Key.Language == language {
			uris = append(uris, value.Key.URI)
		}
	}
	if len(uris) == 0 {
		delete(s.baselines, baselineKey.String())
		return
	}
	slices.Sort(uris)
	s.baselines[baselineKey.String()] = lspCacheBaseline{
		Workspace: workspace,
		Language:  language,
		URIs:      uris,
		UpdatedAt: s.now(),
	}
}

func (s *lspCacheStore) expired(value lspCacheValue, now time.Time) bool {
	if s.config.TTL <= 0 || value.UpdatedAt.IsZero() {
		return false
	}
	return now.Sub(value.UpdatedAt) > s.config.TTL
}

func (k lspCacheKey) String() string {
	return k.Workspace + "\x00" + k.Language + "\x00" + k.URI
}

func (k lspCacheKey) workspaceBaseline() lspCacheBaseline {
	return lspCacheBaseline{Workspace: k.Workspace, Language: k.Language}
}

func (b lspCacheBaseline) String() string {
	return b.Workspace + "\x00" + b.Language
}
