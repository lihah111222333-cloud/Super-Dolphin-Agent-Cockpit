package multilsp

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
)

const (
	defaultPoolSize = 10
	maxPoolSize     = 20
)

// LSP 池环境变量控制全局池大小和单 shard 可缓存的 scoped manager 数量。
const (
	lspPoolSizeEnv     = "AGENT_LSP_POOL_SIZE"
	lspPoolShardCapEnv = "AGENT_LSP_POOL_SHARD_CAP"
	defaultShardCap    = 64
	maxShardCap        = 1024
)

// ManagerPool 按解析后的 scope 复用 manager，避免不同 agent/thread/workspace 共享错误状态。
type ManagerPool struct {
	primary  *manager       // 根 manager，承接默认 workspace 与公共配置。
	factory  ManagerFactory // 为新 scope 创建 clone manager 的工厂。
	size     int            // shard 数量，来自环境变量并经过上限裁剪。
	shardCap int            // 每个 shard 可保留的 clone manager 数。

	shards []*managerShard // 按 shard key 分布的 scoped manager 缓存。

	leases   map[Client]int // 客户端当前租约计数，阻止 recycler 关闭忙碌客户端。
	leasesMu sync.Mutex     // 保护 leases。

	recycler *poolRecycler // 后台回收器，由应用 runner 生命周期托管。
}

// managerShard 是 ManagerPool 的单个缓存分片，锁范围只覆盖本 shard。
type managerShard struct {
	index int      // shard 编号，仅用于日志和快照。
	base  *manager // primary manager 对应的基础实例。

	mu     sync.RWMutex              // 保护 clones。
	clones map[string]*pooledManager // manager key 到 scoped clone。
}

// pooledManager 记录一个可回收的 scoped manager 及最近使用时间。
type pooledManager struct {
	key           string               // ManagerPool 内部缓存键。
	resolvedScope ResolvedLSPToolScope // 该 manager 绑定的规范 scope。
	manager       *manager             // scoped clone 实例。
	lastUsedAt    time.Time            // recycler 判断闲置的时间戳。
}

// poolManagerSnapshot 是 recycler 遍历时使用的只读快照，避免持锁关闭 manager。
type poolManagerSnapshot struct {
	index         int                  // shard 编号。
	managerKey    string               // manager 缓存键。
	base          bool                 // 是否是 primary/base manager。
	manager       *manager             // 待检查的 manager 实例。
	resolvedScope ResolvedLSPToolScope // manager 绑定的 scope。
	lastUsedAt    time.Time            // 快照时的最近使用时间。
}

// RootOptions 携带 ManagerPool 已解析的 root metadata。
// 工厂只能消费这些规范字段，不能重新推导 manager key，否则会破坏 scope 复用边界。
type RootOptions struct {
	RootKind         string
	ProjectRoot      string
	LanguageSpecific map[string]string
	ResolvedScope    ResolvedLSPToolScope
}

// ManagerFactory 为 shard/scope 创建 manager clone。
// 返回未导出的 concrete manager，确保包外调用方只能通过 Manager 接口使用池化实例。
type ManagerFactory interface {
	NewManager(language string, workspaceRoot string, options RootOptions) (*manager, error)
}

type managerFactoryFunc func(language string, workspaceRoot string, options RootOptions) (*manager, error)

// NewManager 将函数适配为 ManagerFactory，用于测试和默认 clone wiring。
func (fn managerFactoryFunc) NewManager(language string, workspaceRoot string, options RootOptions) (*manager, error) {
	return fn(language, workspaceRoot, options)
}

// NewManagerPool 创建分片池并登记 recycler。
// 构造函数不启动 goroutine，回收循环必须由上层 runner 生命周期控制。
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
	// recycler 只挂到 runner，由 ctx cancellation 关闭，避免构造阶段启动无法托管的后台循环。
	pool.recycler = newPoolRecycler(pool)
	return pool
}

// RecyclerRunner 返回池回收器 runner。
// nil pool 没有后台任务；非 nil runner 由应用根生命周期统一托管。
func (p *ManagerPool) RecyclerRunner() platformrunner.Runner {
	if p == nil {
		return nil
	}
	return p.recycler
}

// PoolSizeFromEnv 读取 LSP 池大小并裁剪到安全范围。
// 环境变量缺失或非法时使用默认值，避免错误配置导致无法启动工具。
func PoolSizeFromEnv() int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(lspPoolSizeEnv)))
	if err != nil {
		return defaultPoolSize
	}
	return clampPoolSize(value)
}

// PoolShardCapFromEnv 读取单 shard clone 上限并裁剪到安全范围。
// 非法配置回到默认值，防止缓存无限增长或被设置成不可用大小。
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

// StopAll 保留为池的关闭钩子，但不直接停止 recycler。
// recycler 的生命周期由 runner context 取消驱动；这里保持无副作用，避免和根生命周期重复关闭。
func (p *ManagerPool) StopAll() error {
	return nil
}

// Close 关闭 LSP 管理器资源。
func (p *ManagerPool) Close() error {
	return p.closeManagersExcept(nil)
}

// ForScope 为可信 scope 选择或创建对应 manager。
// manager key 只在这里计算一次，诊断、缓存和 bootstrap 调用方必须复用返回的 ResolvedScope。
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

// snapshotManagers 复制当前池内 manager 列表，供关闭和回收逻辑在不长时间持锁的情况下遍历。
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

// SnapshotManagers 返回池内 manager 快照，供测试和状态检查读取。
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

// buildShards 初始化固定数量的 shard。
// 第一个 shard 复用 primary manager，其余 shard clone 独立状态以隔离客户端和诊断缓存。
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

// managerForResolvedScope 返回已解析 scope 对应的池内 manager。
// 命中 clone 会刷新 lastUsedAt；未命中时在 shard 锁外创建，避免阻塞同 shard 的读写路径。
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

// evictIdleClonesLocked 在 shard 锁内挑选可关闭的闲置 clone。
// keepKey 和仍有租约的 manager 不会被驱逐，避免关闭当前请求或忙碌客户端。
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

// closeManagersExcept 关闭池内除 skip 外的所有唯一 manager。
// 通过快照和 seen 集合避免持锁关闭，也避免同一 manager 被多个 shard 重复关闭。
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
