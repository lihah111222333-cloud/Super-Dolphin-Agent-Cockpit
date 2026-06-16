package multilsp

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

const (
	defaultPoolSize = 10
	maxPoolSize     = 20
)

const (
	lspPoolSizeEnv     = "AGENT_LSP_POOL_SIZE"
	lspPoolShardCapEnv = "AGENT_LSP_POOL_SHARD_CAP"
	defaultShardCap    = 64
	maxShardCap        = 1024
)

// ManagerPool describes a multilsp API type.
type ManagerPool struct {
	primary  *manager
	factory  ManagerFactory
	size     int
	shardCap int

	shards []*managerShard

	leases   map[Client]int
	leasesMu sync.Mutex

	recycler *poolRecycler
}

type managerShard struct {
	index int
	base  *manager

	mu     sync.RWMutex
	clones map[string]*pooledManager
}

type pooledManager struct {
	key           string
	resolvedScope ResolvedLSPToolScope
	manager       *manager
	lastUsedAt    time.Time
}

type poolManagerSnapshot struct {
	index         int
	managerKey    string
	base          bool
	manager       *manager
	resolvedScope ResolvedLSPToolScope
	lastUsedAt    time.Time
}

// RootOptions carries canonical root metadata into ManagerFactory without
// making the factory re-derive keys that ManagerPool.ForScope already owns.
type RootOptions struct {
	RootKind         string
	ProjectRoot      string
	LanguageSpecific map[string]string
	ResolvedScope    ResolvedLSPToolScope
}

// ManagerFactory creates per-shard/per-scope manager clones. It is package
// local in practice because it returns the unexported concrete manager while
// callers outside multilsp only receive the exported Manager interface.
type ManagerFactory interface {
	NewManager(language string, workspaceRoot string, options RootOptions) (*manager, error)
}

type managerFactoryFunc func(language string, workspaceRoot string, options RootOptions) (*manager, error)

// NewManager 创建manager。
func (fn managerFactoryFunc) NewManager(language string, workspaceRoot string, options RootOptions) (*manager, error) {
	return fn(language, workspaceRoot, options)
}

// NewManagerPool 创建managerpool。
func NewManagerPool(primary *manager, size int) *ManagerPool {
	clamped := clampPoolSize(size)
	pool := &ManagerPool{
		primary:  primary,
		size:     clamped,
		shardCap: PoolShardCapFromEnv(),
		leases:   map[Client]int{},
	}
	pool.factory = cloneManagerFactory(primary)
	pool.shards = pool.buildShards(primary, clamped)
	// P22 P2 LSP-S1: do not start the recycler loop from the
	// constructor. The root `group:"runners"` aggregation owns it via
	// RecyclerRunner() below so shutdown is driven by ctx cancellation.
	pool.recycler = newPoolRecycler(pool)
	return pool
}

// RecyclerRunner exposes the pool's background recycler as a
// platformrunner.Runner. The root bridge collects recyclers from each
// language pool and feeds them into `group:"runners"`.
// RecyclerRunner 处理recyclerrunner。
func (p *ManagerPool) RecyclerRunner() platformrunner.Runner {
	if p == nil {
		return nil
	}
	return p.recycler
}

// PoolSizeFromEnv 从env处理poolsize。
func PoolSizeFromEnv() int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(lspPoolSizeEnv)))
	if err != nil {
		return defaultPoolSize
	}
	return clampPoolSize(value)
}

// PoolShardCapFromEnv 从env处理poolshardcap。
func PoolShardCapFromEnv() int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(lspPoolShardCapEnv)))
	if err != nil {
		return defaultShardCap
	}
	switch {
	case value <= 0:
		return defaultShardCap
	case value > maxShardCap:
		return maxShardCap
	default:
		return value
	}
}

// Primary 返回主 manager。
func (p *ManagerPool) Primary() Manager {
	if p == nil {
		return nil
	}
	return p.primary
}

// Size 返回池内 manager 数量。
func (p *ManagerPool) Size() int {
	if p == nil {
		return 0
	}
	return p.size
}

// StopAll is retained as the *ManagerPool shutdown hook for non-runner
// resources (leases bookkeeping, etc.). Recycler lifecycle is now owned
// by the root runner group via ctx cancellation, so this method is a
// no-op for the recycler; callers that previously relied on StopAll
// halting the loop must now cancel the runner context instead.
// StopAll 停止all。
func (p *ManagerPool) StopAll() error {
	return nil
}

// Close 关闭 LSP 管理器资源。
func (p *ManagerPool) Close() error {
	return p.closeManagersExcept(nil)
}

// ForScope resolves the canonical manager key exactly once, routes the request
// to a stable shard, and returns the scoped manager plus the canonical scope
// that diagnostics/cache/bootstrap callers must reuse instead of rebuilding
// their own keys.
// ForScope 为作用域处理LSP。
func (p *ManagerPool) ForScope(scope LSPToolScope) (ScopedManager, error) {
	if p == nil {
		return ScopedManager{}, errors.New("LSP manager pool is nil")
	}
	resolved, err := ResolveLSPToolScope(scope)
	if err != nil {
		return ScopedManager{}, err
	}
	shard := p.shardForKey(resolved.ShardKey)
	if shard == nil {
		return ScopedManager{}, errors.New("LSP manager pool has no shard")
	}
	if p.recycler != nil {
		p.recycler.TouchShard(shard.index)
	}
	pooled, err := p.managerForResolvedScope(shard, resolved)
	if err != nil {
		return ScopedManager{}, err
	}
	return ScopedManager{
		Manager:       pooled.manager,
		ResolvedScope: resolved,
	}, nil
}

func (p *ManagerPool) acquire(client Client) {
	if p == nil || client == nil {
		return
	}
	p.trackLease(client, 1)
}

func (p *ManagerPool) release(client Client) {
	if p == nil || client == nil {
		return
	}
	p.trackLease(client, -1)
}

// snapshotManagers 处理快照managers。
func (p *ManagerPool) snapshotManagers() []poolManagerSnapshot {
	if p == nil {
		return nil
	}
	snapshots := make([]poolManagerSnapshot, 0, len(p.shards))
	for _, shard := range p.shards {
		if shard == nil {
			continue
		}
		if shard.base != nil {
			snapshots = append(snapshots, poolManagerSnapshot{
				index:      shard.index,
				managerKey: "base",
				base:       true,
				manager:    shard.base,
			})
		}
		clones := shard.snapshotClones()
		for _, clone := range clones {
			if clone.manager == nil {
				continue
			}
			snapshots = append(snapshots, poolManagerSnapshot{
				index:         shard.index,
				managerKey:    clone.key,
				manager:       clone.manager,
				resolvedScope: clone.resolvedScope,
				lastUsedAt:    clone.lastUsedAt,
			})
		}
	}
	return snapshots
}

// SnapshotManagers 处理快照managers。
func (p *ManagerPool) SnapshotManagers() []poolManagerSnapshot {
	return p.snapshotManagers()
}

func (p *ManagerPool) activeLeases(client Client) int {
	p.leasesMu.Lock()
	defer p.leasesMu.Unlock()
	return p.leases[client]
}

func (p *ManagerPool) trackLease(client Client, delta int) {
	if p == nil || client == nil {
		return
	}
	p.leasesMu.Lock()
	defer p.leasesMu.Unlock()

	next := p.leases[client] + delta
	if next <= 0 {
		delete(p.leases, client)
		return
	}
	p.leases[client] = next
}

func clampPoolSize(size int) int {
	switch {
	case size <= 0:
		return defaultPoolSize
	case size > maxPoolSize:
		return maxPoolSize
	default:
		return size
	}
}

// buildShards 构建shards。
func (p *ManagerPool) buildShards(primary *manager, size int) []*managerShard {
	shards := make([]*managerShard, size)
	for i := 0; i < size; i++ {
		base := primary
		if i > 0 && primary != nil {
			base = primary.cloneForWorkspace(primary.workspaceRoot)
		}
		if base != nil {
			base.pool = p
		}
		shards[i] = &managerShard{
			index:  i,
			base:   base,
			clones: map[string]*pooledManager{},
		}
	}
	if primary != nil {
		primary.pool = p
	}
	return shards
}

func (p *ManagerPool) shardForKey(key string) *managerShard {
	if p == nil || len(p.shards) == 0 {
		return nil
	}
	return p.shards[shardIndexForKey(key, len(p.shards))]
}

// managerForResolvedScope 为已解析作用域处理manager。
func (p *ManagerPool) managerForResolvedScope(shard *managerShard, resolved ResolvedLSPToolScope) (*pooledManager, error) {
	if shard == nil {
		return nil, errors.New("LSP manager shard is nil")
	}
	shard.mu.Lock()

	if pooled := shard.clones[resolved.ManagerKey]; pooled != nil {
		pooled.lastUsedAt = time.Now()
		shard.mu.Unlock()
		return pooled, nil
	}
	if p.factory == nil {
		shard.mu.Unlock()
		return nil, errors.New("LSP manager pool factory is nil")
	}
	workspaceRoot := managerWorkspaceRoot(resolved)
	mgr, err := p.factory.NewManager(resolved.LanguageID, workspaceRoot, RootOptions{
		RootKind:         resolved.RootKind,
		ProjectRoot:      resolved.ProjectRoot,
		LanguageSpecific: copyLanguageSpecific(resolved.LanguageSpecific),
		ResolvedScope:    resolved,
	})
	if err != nil {
		shard.mu.Unlock()
		return nil, fmt.Errorf("create scoped LSP manager: %w", err)
	}
	if mgr == nil {
		shard.mu.Unlock()
		return nil, errors.New("create scoped LSP manager: nil manager")
	}
	mgr.pool = p
	pooled := &pooledManager{
		key:           resolved.ManagerKey,
		resolvedScope: resolved,
		manager:       mgr,
		lastUsedAt:    time.Now(),
	}
	shard.clones[resolved.ManagerKey] = pooled
	toClose := p.evictIdleClonesLocked(shard, resolved.ManagerKey)
	shard.mu.Unlock()
	_, _ = closeReleaseScopeManagers(ReleaseScopeRequest{ScopeKind: ReleaseScopeManagerKey, ManagerKey: resolved.ManagerKey, Drain: true, Reason: "shard_cap_evict"}, toClose)
	return pooled, nil
}

// evictIdleClonesLocked 处理evictidlecloneslocked。
func (p *ManagerPool) evictIdleClonesLocked(shard *managerShard, keepKey string) []*manager {
	if p == nil || shard == nil || p.shardCap <= 0 {
		return nil
	}
	var toClose []*manager
	for len(shard.clones) > p.shardCap {
		evictKey, evict := p.oldestIdleCloneLocked(shard, keepKey)
		if evict == nil {
			break
		}
		delete(shard.clones, evictKey)
		toClose = append(toClose, evict.manager)
	}
	return toClose
}

func (p *ManagerPool) oldestIdleCloneLocked(shard *managerShard, keepKey string) (string, *pooledManager) {
	var evictKey string
	var evict *pooledManager
	for key, clone := range shard.clones {
		if !p.canEvictClone(key, keepKey, clone) {
			continue
		}
		if evict == nil || clone.lastUsedAt.Before(evict.lastUsedAt) {
			evictKey = key
			evict = clone
		}
	}
	return evictKey, evict
}

func (p *ManagerPool) canEvictClone(key, keepKey string, clone *pooledManager) bool {
	if key == keepKey || clone == nil || clone.manager == nil {
		return false
	}
	return p.activeLeasesForManager(clone.manager) == 0
}

func (s *managerShard) snapshotClones() []pooledManager {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	clones := make([]pooledManager, 0, len(s.clones))
	for _, clone := range s.clones {
		if clone != nil {
			clones = append(clones, *clone)
		}
	}
	return clones
}

// closeManagersExcept 关闭managersexcept。
func (p *ManagerPool) closeManagersExcept(skip *manager) error {
	if p == nil {
		return nil
	}
	var firstErr error
	seen := map[*manager]struct{}{}
	for _, snapshot := range p.snapshotManagers() {
		mgr := snapshot.manager
		if mgr == nil || mgr == skip {
			continue
		}
		if _, ok := seen[mgr]; ok {
			continue
		}
		seen[mgr] = struct{}{}
		firstErr = firstNonNilError(firstErr, mgr.closeWithoutPool())
	}
	return firstErr
}

func cloneManagerFactory(primary *manager) ManagerFactory {
	return managerFactoryFunc(func(_ string, workspaceRoot string, _ RootOptions) (*manager, error) {
		if primary == nil {
			return nil, errors.New("primary LSP manager is nil")
		}
		return primary.cloneForWorkspace(workspaceRoot), nil
	})
}
