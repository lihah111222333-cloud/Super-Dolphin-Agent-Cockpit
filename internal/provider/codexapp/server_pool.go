package codexapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// SpawnedServer is the narrow surface a ServerPool entry exposes. Real
// production values wrap *transport; tests can inject a fake that
// only cares about URL + Close. Keeping the interface small lets the
// pool be unit-tested without booting the codex Rust binary.
type SpawnedServer interface {
	ServerURL() string
	Close(ctx context.Context) error
	Alive() bool
}

// Spawner is the factory the pool uses to create a new entry when a
// codexHome has no existing server. It returns a SpawnedServer and
// an error; transient errors are recorded against the home so future
// Get calls back off according to SpawnBackoff.
type Spawner func(ctx context.Context, home string) (SpawnedServer, error)

// PoolConfig sets the ServerPool's runtime knobs. Zero fields fall
// back to plan-documented defaults.
type PoolConfig struct {
	// Capacity caps the number of simultaneously live servers. A Get
	// that would exceed the cap evicts the least-recently-used idle
	// entry; if nothing is evictable it returns ErrPoolExhausted so
	// callers surface the pressure instead of silently queueing.
	Capacity int
	// IdleTimeout is the duration after which an unused non-session entry
	// becomes eligible for eviction. Session-owned servers are closed as soon
	// as their last release runs so archived agents reclaim app-server, MCP,
	// and LSP child processes immediately.
	IdleTimeout time.Duration
	// SpawnBackoff is the cooldown applied after a spawn failure: the
	// same codexHome re-requested within this window returns the
	// cached error without attempting a fresh spawn. Defaults to
	// DefaultPoolSpawnBackoff.
	SpawnBackoff time.Duration
}

// Plan-documented defaults. Exposed so tests / callers can reference
// them without hand-coding magic numbers.
const (
	DefaultPoolCapacity     = 16
	DefaultPoolIdleTimeout  = 30 * time.Minute
	DefaultPoolSpawnBackoff = 2 * time.Minute
)

// Sentinel errors for pool failure modes. Callers branch on these for
// retry / failover decisions.
var (
	ErrPoolExhausted   = errors.New("codexapp: server pool capacity exhausted")
	ErrSpawnBackoff    = errors.New("codexapp: spawn backoff active for codexHome")
	ErrPoolClosed      = errors.New("codexapp: server pool is closed")
	ErrInvalidIdentity = errors.New("codexapp: invalid codex identity")
)

// ServerPool manages one SpawnedServer per canonicalized codexHome.
// It is safe for concurrent use.
//
// Semantics:
//   - Get resolves the identity (canonicalizes codexHome), returns
//     the existing entry when present and alive, otherwise spawns a
//     fresh one. Capacity pressure evicts the least-recently-used
//     idle entry; if every entry is still in use the call returns
//     ErrPoolExhausted. Clients should treat that as a fail-fast
//     rather than block, because unbounded queueing hides real
//     capacity mismatches.
//   - Release decrements the entry refCount. When the last session
//     releases a server, the entry is removed and the owned process group is
//     closed; Codex MCP/LSP sidecars inherit that group and are reclaimed with
//     the app-server.
//   - Spawn failures stamp the codexHome with a backoff window so
//     rapid retries don't thrash the underlying child-process
//     machinery; the cached error is returned until SpawnBackoff
//     elapses.
//   - Close tears every entry down and refuses subsequent Get calls.
type ServerPool struct {
	logger  *slog.Logger
	spawner Spawner
	cfg     PoolConfig
	now     func() time.Time

	mu      sync.Mutex
	entries map[string]*poolEntry
	closed  bool
}

type poolEntry struct {
	home          string
	instanceKey   string
	modelProvider string
	server        SpawnedServer
	lastUsed      time.Time
	refCount      int
	spawnErr      error
	backoffUntil  time.Time
}

// NewServerPool constructs a pool. spawner is required; a nil spawner
// produces a pool that always fails Get with ErrInvalidIdentity so
// the mistake is loud.
func NewServerPool(logger *slog.Logger, spawner Spawner, cfg PoolConfig) *ServerPool {
	if logger == nil {
		logger = pkglogger.Get()
	}
	if cfg.Capacity <= 0 {
		cfg.Capacity = DefaultPoolCapacity
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = DefaultPoolIdleTimeout
	}
	if cfg.SpawnBackoff <= 0 {
		cfg.SpawnBackoff = DefaultPoolSpawnBackoff
	}
	return &ServerPool{
		logger:  logger,
		spawner: spawner,
		cfg:     cfg,
		now:     time.Now,
		entries: make(map[string]*poolEntry),
	}
}

// Acquire returns the SpawnedServer bound to the identity, spawning a
// new one if necessary. The returned release function MUST be called
// exactly once when the caller is done with the server — it updates
// the entry's refCount + lastUsed so LRU eviction sees accurate
// utilisation.
//
// A nil server + non-nil error is always returned together; the
// release callback is a safe no-op in that case.
func (p *ServerPool) Acquire(ctx context.Context, identity providershared.CodexIdentity) (SpawnedServer, func(), error) {
	home, key, provider, err := normalizePoolIdentity(identity)
	if err != nil {
		return nil, noopRelease, err
	}
	p.mu.Lock()
	fastPath := p.acquireFastPathLocked(home, key, provider)
	if fastPath.done {
		p.mu.Unlock()
		return fastPath.server, fastPath.release, fastPath.err
	}
	// Respect spawn backoff before we try to expand capacity.
	if err := p.checkBackoffLocked(fastPath.entry, fastPath.now); err != nil {
		p.mu.Unlock()
		return nil, noopRelease, err
	}
	// Capacity pressure: try to evict the LRU idle entry. If no entry
	// is evictable (everything is refcounted), surface
	// ErrPoolExhausted so the caller fails fast.
	if err := p.ensureCapacityLocked(fastPath.entry, fastPath.now); err != nil {
		p.mu.Unlock()
		return nil, noopRelease, err
	}
	spawnAt := fastPath.now
	p.mu.Unlock()

	server, err := p.spawner(ctx, home)
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entries[home]
	if err != nil {
		p.recordSpawnErrorLocked(entry, home, key, provider, err, spawnAt)
		return nil, noopRelease, fmt.Errorf("codexapp: spawn %q: %w", home, err)
	}
	p.storeSpawnedLocked(entry, home, key, provider, server, spawnAt)
	return server, p.releaser(home), nil
}

func noopRelease() {}

type acquireFastPathResult struct {
	entry   *poolEntry
	now     time.Time
	server  SpawnedServer
	release func()
	err     error
	done    bool
}

func (p *ServerPool) acquireFastPathLocked(home, key, provider string) acquireFastPathResult {
	result := acquireFastPathResult{release: noopRelease}
	if p.closed {
		result.err = ErrPoolClosed
		result.done = true
		return result
	}
	if p.spawner == nil {
		result.err = fmt.Errorf("%w: pool has no spawner", ErrInvalidIdentity)
		result.done = true
		return result
	}
	result.now = p.now()
	result.entry = p.entries[home]
	if result.entry == nil {
		return result
	}
	if result.entry.instanceKey != "" && result.entry.instanceKey != key {
		result.err = fmt.Errorf("%w: codexHome %q already bound to instance key %q", ErrInvalidIdentity, home, result.entry.instanceKey)
		result.done = true
		return result
	}
	if result.entry.modelProvider != "" && result.entry.modelProvider != provider {
		result.err = fmt.Errorf("%w: codexHome %q instance key %q already bound to model provider %q", ErrInvalidIdentity, home, key, result.entry.modelProvider)
		result.done = true
		return result
	}
	// Live entry: happy path.
	if result.entry.server != nil && result.entry.server.Alive() {
		result.entry.refCount++
		result.entry.lastUsed = result.now
		result.server = result.entry.server
		result.release = p.releaser(home)
		result.done = true
		return result
	}
	// Dead entry: drop the stale reference so we fall through to
	// the spawn path. Backoff state is preserved so a flapping
	// server doesn't spin on respawn.
	result.entry.server = nil
	return result
}

func (p *ServerPool) checkBackoffLocked(entry *poolEntry, now time.Time) error {
	if entry == nil || entry.spawnErr == nil || !now.Before(entry.backoffUntil) {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrSpawnBackoff, entry.spawnErr)
}

func (p *ServerPool) ensureCapacityLocked(entry *poolEntry, now time.Time) error {
	if entry != nil || len(p.entries) < p.cfg.Capacity {
		return nil
	}
	if p.evictLRULocked(now) {
		return nil
	}
	return ErrPoolExhausted
}

func (p *ServerPool) recordSpawnErrorLocked(entry *poolEntry, home, key, provider string, err error, now time.Time) {
	// Record the failure for backoff. Preserve the existing
	// entry slot if one exists so backoff state persists.
	if entry == nil {
		entry = &poolEntry{home: home, instanceKey: key, modelProvider: provider}
		p.entries[home] = entry
	}
	entry.spawnErr = err
	entry.backoffUntil = now.Add(p.cfg.SpawnBackoff)
	entry.lastUsed = now
}

func (p *ServerPool) storeSpawnedLocked(entry *poolEntry, home, key, provider string, server SpawnedServer, now time.Time) {
	if entry == nil {
		entry = &poolEntry{home: home, instanceKey: key, modelProvider: provider}
		p.entries[home] = entry
	}
	entry.server = server
	entry.spawnErr = nil
	entry.backoffUntil = time.Time{}
	entry.refCount = 1
	entry.lastUsed = now
}

// releaser returns the per-home release callback used by Acquire. When the
// final session releases an entry we remove it immediately and close the
// app-server process group. That makes archive/trash reclaim the app-server
// and its MCP/LSP sidecars without waiting for the periodic idle runner.
// The callback is idempotent on the "already released" branch: an extra call
// after deletion is a no-op.
func (p *ServerPool) releaser(home string) func() {
	return func() {
		var server SpawnedServer
		p.mu.Lock()
		entry := p.entries[home]
		if entry == nil {
			p.mu.Unlock()
			return
		}
		if entry.refCount > 0 {
			entry.refCount--
		}
		entry.lastUsed = p.now()
		if entry.refCount == 0 && entry.server != nil {
			server = entry.server
			delete(p.entries, home)
		}
		p.mu.Unlock()
		if server != nil {
			closeWithTimeout(server, 2*time.Second, p.logger, home)
		}
	}
}

// evictLRULocked selects the oldest idle entry (refCount == 0) and
// closes it. Returns true when an entry was evicted, false when every
// entry is in use.
//
// MUST be called with p.mu held. Close is invoked with a short
// background context because the pool lock is held; a blocking Close
// would stall every other Acquire. Production SpawnedServer.Close
// implementations must respect that deadline.
func (p *ServerPool) evictLRULocked(now time.Time) bool {
	var oldestKey string
	var oldestTime time.Time
	for key, entry := range p.entries {
		if entry.refCount > 0 {
			continue
		}
		if oldestKey == "" || entry.lastUsed.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.lastUsed
		}
	}
	if oldestKey == "" {
		return false
	}
	entry := p.entries[oldestKey]
	delete(p.entries, oldestKey)
	if entry.server != nil {
		go closeWithTimeout(entry.server, 2*time.Second, p.logger, oldestKey)
	}
	p.logger.Debug("codexapp: pool evicted entry",
		slog.String("codex_home", oldestKey),
		slog.Time("last_used", oldestTime),
	)
	_ = now // reserved for future idle-gc metrics
	return true
}

// EvictIdle closes every entry whose lastUsed is older than
// IdleTimeout. Intended to be invoked periodically by a Runner in
// production; returns the number of entries evicted so callers can
// emit a metric.
func (p *ServerPool) EvictIdle() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0
	}
	now := p.now()
	removed := 0
	for key, entry := range p.entries {
		if entry.refCount > 0 {
			continue
		}
		if now.Sub(entry.lastUsed) < p.cfg.IdleTimeout {
			continue
		}
		delete(p.entries, key)
		if entry.server != nil {
			go closeWithTimeout(entry.server, 2*time.Second, p.logger, key)
		}
		removed++
	}
	return removed
}

// Close tears the pool down. Subsequent Acquire returns ErrPoolClosed.
// Close is safe to call multiple times.
func (p *ServerPool) Close(ctx context.Context) error {
	p.mu.Lock()
	entries, alreadyClosed := p.snapshotCloseEntriesLocked()
	p.mu.Unlock()
	if alreadyClosed {
		return nil
	}
	var firstErr error
	for _, entry := range entries {
		if entry.server == nil {
			continue
		}
		if err := entry.server.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (p *ServerPool) snapshotCloseEntriesLocked() ([]*poolEntry, bool) {
	if p.closed {
		return nil, true
	}
	p.closed = true
	entries := make([]*poolEntry, 0, len(p.entries))
	for _, entry := range p.entries {
		entries = append(entries, entry)
	}
	p.entries = nil
	return entries, false
}

// Size returns the current number of pool entries. Useful for tests
// and metrics.
func (p *ServerPool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// normalizePoolIdentity returns the canonicalized codexHome plus the
// trimmed codexInstanceKey/codexModelProvider pair. The home produced by
// providershared.ResolveCodexIdentity is already canonicalized; we re-run
// CanonicalizeCodexHome here when callers pass raw input so the pool remains
// the single authority on home->entry binding. P22 P1a requires the full
// identity triple to fail closed: no pool entry may silently collapse two
// providers that share the same home + instance key.
func normalizePoolIdentity(identity providershared.CodexIdentity) (home, key, provider string, err error) {
	raw := strings.TrimSpace(identity.Home)
	if raw == "" {
		return "", "", "", fmt.Errorf("%w: codexHome is empty", ErrInvalidIdentity)
	}
	canonical, err := providershared.CanonicalizeCodexHome(raw)
	if err != nil {
		return "", "", "", fmt.Errorf("%w: %v", ErrInvalidIdentity, err)
	}
	key = strings.TrimSpace(identity.InstanceKey)
	if key == "" {
		return "", "", "", fmt.Errorf("%w: codexInstanceKey is empty", ErrInvalidIdentity)
	}
	provider = strings.TrimSpace(identity.ModelProvider)
	if provider == "" {
		return "", "", "", fmt.Errorf("%w: codexModelProvider is empty", ErrInvalidIdentity)
	}
	return canonical, key, provider, nil
}

// closeWithTimeout invokes Close with a background deadline so the
// pool never blocks an eviction on a slow child process. Logged at
// debug level because the failure rarely indicates real trouble —
// shutdown ordering races commonly surface as transient Close errors.
func closeWithTimeout(server SpawnedServer, timeout time.Duration, logger *slog.Logger, key string) {
	ctx, cancel := withTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Close(ctx); err != nil {
		logger.Debug("codexapp: pool close entry failed",
			slog.String("codex_home", key),
			slog.String("error", err.Error()),
		)
	}
}
