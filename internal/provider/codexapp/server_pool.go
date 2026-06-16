package codexapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/discovery"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
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
type Spawner func(ctx context.Context, home, modelProvider string) (SpawnedServer, error)

// PoolConfig sets the ServerPool's runtime knobs. Zero fields fall
// back to defaults.
type PoolConfig struct {
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

// Defaults exposed so tests / callers can reference them without
// hand-coding magic numbers.
const (
	DefaultPoolIdleTimeout  = 30 * time.Minute
	DefaultPoolSpawnBackoff = 2 * time.Minute
)

// Sentinel errors for pool failure modes. Callers branch on these for
// retry / failover decisions.
var (
	ErrSpawnBackoff    = errors.New("codexapp: spawn backoff active for codexHome")
	ErrPoolClosed      = errors.New("codexapp: server pool is closed")
	ErrInvalidIdentity = errors.New("codexapp: invalid codex identity")
)

// ServerPool manages one SpawnedServer per canonicalized codex identity and
// owning agent.
// It is safe for concurrent use.
//
// Semantics:
//   - Get resolves the identity (canonicalizes codexHome), returns
//     the existing entry when present and alive, otherwise spawns a
//     fresh one. The pool does not cap the number of simultaneously
//     live servers.
//   - Release decrements the entry refCount. When the owning session releases
//     a server, the entry is removed and the owned process group is closed;
//     Codex MCP/LSP sidecars inherit that group and are reclaimed with the
//     app-server.
//   - Spawn failures stamp the identity+owner entry with a backoff window so
//     rapid retries don't thrash the underlying child-process machinery; the
//     cached error is returned until SpawnBackoff
//     elapses.
//   - Close tears every entry down and refuses subsequent Get calls.
type ServerPool struct {
	logger  *pkglogger.Logger
	spawner Spawner
	cfg     PoolConfig
	now     func() time.Time

	mu      sync.Mutex
	entries map[poolEntryKey]*poolEntry
	closed  bool
}

type poolEntryKey struct {
	home          string
	instanceKey   string
	modelProvider string
	processPolicy string
	ownerKey      string
}

type poolEntry struct {
	key           poolEntryKey
	home          string
	instanceKey   string
	modelProvider string
	ownerKey      string
	server        SpawnedServer
	lastUsed      time.Time
	refCount      int
	spawnErr      error
	backoffUntil  time.Time
}

// NewServerPool constructs a pool. spawner is required; a nil spawner
// produces a pool that always fails Get with ErrInvalidIdentity so
// the mistake is loud.
// NewServerPool 创建服务端pool。
func NewServerPool(logger *pkglogger.Logger, spawner Spawner, cfg PoolConfig) *ServerPool {
	if logger == nil {
		logger = pkglogger.Get()
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
		entries: make(map[poolEntryKey]*poolEntry),
	}
}

// Acquire returns the SpawnedServer bound to the identity, spawning a
// new one if necessary. The returned release function MUST be called
// exactly once when the caller is done with the server — it updates
// the entry's refCount + lastUsed so idle eviction sees accurate
// utilization.
//
// A nil server + non-nil error is always returned together; the
// release callback is a safe no-op in that case.
// Acquire 获取锁或租约。
func (p *ServerPool) Acquire(ctx context.Context, identity providershared.CodexIdentity, ownerKey string) (SpawnedServer, func(), error) {
	home, key, provider, err := normalizePoolIdentity(identity)
	if err != nil {
		return nil, newNoopRelease(), err
	}
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return nil, newNoopRelease(), fmt.Errorf("%w: pool owner agentID is empty", ErrInvalidIdentity)
	}
	entryKey := poolEntryKey{
		home:          home,
		instanceKey:   key,
		modelProvider: provider,
		processPolicy: poolSpawnPolicySignature(ctx),
		ownerKey:      ownerKey,
	}
	p.mu.Lock()
	fastPath := p.acquireFastPathLocked(entryKey)
	if fastPath.done {
		p.mu.Unlock()
		return fastPath.server, fastPath.release, fastPath.err
	}
	// Respect spawn backoff before spawning.
	if err := p.checkBackoffLocked(fastPath.entry, fastPath.now); err != nil {
		p.mu.Unlock()
		return nil, newNoopRelease(), err
	}
	spawnAt := fastPath.now
	p.mu.Unlock()

	server, err := p.spawner(ctx, home, provider)
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entries[entryKey]
	if err != nil {
		p.recordSpawnErrorLocked(entry, entryKey, err, spawnAt)
		return nil, newNoopRelease(), fmt.Errorf("codexapp: spawn %q: %w", home, err)
	}
	p.storeSpawnedLocked(entry, entryKey, server, spawnAt)
	return server, p.releaser(entryKey), nil
}

func newNoopRelease() func() {
	return func() {}
}

type acquireFastPathResult struct {
	entry   *poolEntry
	now     time.Time
	server  SpawnedServer
	release func()
	err     error
	done    bool
}

// acquireFastPathLocked 处理acquirefast路径locked。
func (p *ServerPool) acquireFastPathLocked(entryKey poolEntryKey) acquireFastPathResult {
	result := acquireFastPathResult{release: newNoopRelease()}
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
	result.entry = p.entries[entryKey]
	if result.entry == nil {
		return result
	}
	// Live entry: happy path.
	if result.entry.server != nil && result.entry.server.Alive() {
		result.entry.refCount++
		result.entry.lastUsed = result.now
		result.server = result.entry.server
		result.release = p.releaser(entryKey)
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
	return fmt.Errorf("%w: %w", ErrSpawnBackoff, entry.spawnErr)
}

func (p *ServerPool) recordSpawnErrorLocked(entry *poolEntry, entryKey poolEntryKey, err error, now time.Time) {
	// Record the failure for backoff. Preserve the existing
	// entry slot if one exists so backoff state persists.
	if entry == nil {
		entry = newPoolEntry(entryKey)
		p.entries[entryKey] = entry
	}
	entry.spawnErr = err
	entry.backoffUntil = now.Add(p.cfg.SpawnBackoff)
	entry.lastUsed = now
}

func (p *ServerPool) storeSpawnedLocked(entry *poolEntry, entryKey poolEntryKey, server SpawnedServer, now time.Time) {
	if entry == nil {
		entry = newPoolEntry(entryKey)
		p.entries[entryKey] = entry
	}
	entry.server = server
	entry.spawnErr = nil
	entry.backoffUntil = time.Time{}
	entry.refCount = 1
	entry.lastUsed = now
}

func newPoolEntry(entryKey poolEntryKey) *poolEntry {
	return &poolEntry{
		key:           entryKey,
		home:          entryKey.home,
		instanceKey:   entryKey.instanceKey,
		modelProvider: entryKey.modelProvider,
		ownerKey:      entryKey.ownerKey,
	}
}

// releaser returns the per-owner release callback used by Acquire. When the
// final session releases an entry we remove it immediately and close the
// app-server process group. That makes archive/trash reclaim the app-server
// and its MCP/LSP sidecars without waiting for the periodic idle runner.
// The callback is idempotent on the "already released" branch: an extra call
// after deletion is a no-op.
// releaser 处理releaser。
func (p *ServerPool) releaser(entryKey poolEntryKey) func() {
	return func() {
		var server SpawnedServer
		p.mu.Lock()
		entry := p.entries[entryKey]
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
			delete(p.entries, entryKey)
		}
		p.mu.Unlock()
		if server != nil {
			closeWithTimeout(server, 2*time.Second, p.logger, entryKey)
		}
	}
}

// EvictIdle closes every entry whose lastUsed is older than
// IdleTimeout. Intended to be invoked periodically by a Runner in
// production; returns the number of entries evicted so callers can
// emit a metric.
// EvictIdle 处理evictidle。
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
// Close 关闭codexapp provider资源。
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
// Size 返回池内 manager 数量。
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
// identity triple to fail closed: the pool key includes all three fields, so no
// entry may silently collapse two providers that share the same home + instance
// key.
func normalizePoolIdentity(identity providershared.CodexIdentity) (home, key, provider string, err error) {
	raw := strings.TrimSpace(identity.Home)
	if raw == "" {
		return "", "", "", fmt.Errorf("%w: codexHome is empty", ErrInvalidIdentity)
	}
	canonical, err := providershared.CanonicalizeCodexHome(raw)
	if err != nil {
		return "", "", "", fmt.Errorf("%w: %w", ErrInvalidIdentity, err)
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
func closeWithTimeout(server SpawnedServer, timeout time.Duration, logger *pkglogger.Logger, key poolEntryKey) {
	ctx, cancel := withTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Close(ctx); err != nil {
		logger.Debug("codexapp: pool close entry failed",
			pkglogger.String("codex_home", key.home),
			pkglogger.String("owner", key.ownerKey),
			pkglogger.String("error", err.Error()),
		)
	}
}

const defaultPoolEvictInterval = time.Minute

type poolEvictRunner struct {
	logger   *pkglogger.Logger
	pool     *ServerPool
	interval time.Duration
}

func newPoolEvictRunner(logger *pkglogger.Logger, pool *ServerPool) *poolEvictRunner {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &poolEvictRunner{logger: logger, pool: pool, interval: defaultPoolEvictInterval}
}

var _ platformrunner.Runner = (*poolEvictRunner)(nil)

// Run 启动codexapp provider后台流程。
func (r *poolEvictRunner) Run(ctx context.Context) error {
	if r.pool == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if removed := r.pool.EvictIdle(); removed > 0 {
				r.logger.Info("codexapp: pool evicted idle entries",
					pkglogger.Int("count", removed),
				)
			}
		}
	}
}

func poolEvictRunnerAsRunner(r *poolEvictRunner) platformrunner.Runner { return r }

// cleanPeerDiscoveryFiles removes HTTP discovery files for peer MCP processes.
// Called during ServerManager shutdown as a safety net.
// cleanPeerDiscoveryFiles 处理cleanpeerdiscovery文件。
func cleanPeerDiscoveryFiles() {
	myPID := os.Getpid()
	for _, binary := range []string{"mcp-orch", "mcp-lsp"} {
		if err := discovery.CleanupDiscoveryFile(binary, myPID); err != nil {
			if !os.IsNotExist(err) {
				pkglogger.Warn("peer discovery cleanup failed",
					"binary", binary, "pid", myPID, "error", err)
			}
		}
	}
}
