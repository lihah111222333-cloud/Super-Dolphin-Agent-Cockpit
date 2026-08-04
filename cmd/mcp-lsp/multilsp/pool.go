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

	lifecycleMu sync.RWMutex // 封闭 ForScope/ReleaseScope 与 pool Close 的创建边界。
	closing     bool         // pool 已禁止新请求，仍可能在重试 cleanup。
	closed      bool         // pool 的全部 manager cleanup 已完成。

	closeMu          sync.Mutex            // 串行化 pool Close 与失败重试。
	closePending     map[*manager]struct{} // 已摘除、仍需确认完成关闭的 manager。
	closeTerminalErr error                 // cleanup 完成后的稳定错误结果。

	releaseMu       sync.Mutex                              // 串行化 release，避免 detach 与 pending 登记之间出现可见空窗。
	pendingMu       sync.Mutex                              // 保护等待最后租约释放或关闭确认的 manager。
	pendingReleases map[*manager]pendingManagerReleaseState // 保留 cleanup receipt，直到调用方取得完成结果。

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

// pendingManagerRelease 保存已从 shard 摘除、等待最后租约释放的旧 manager 代际。
type pendingManagerRelease struct {
	manager *manager
	scope   ResolvedLSPToolScope
}

type pendingManagerReleaseState struct {
	scope     ResolvedLSPToolScope
	completed bool
	closeErr  error
	report    bool
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
		primary:         primary,
		size:            clamped,
		shardCap:        PoolShardCapFromEnv(),
		closePending:    make(map[*manager]struct{}),
		pendingReleases: make(map[*manager]pendingManagerReleaseState),
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
	_, err := p.closeStatus()
	return err
}

// ForScope 为可信 scope 选择或创建对应 manager。
// manager key 只在这里计算一次，诊断、缓存和 bootstrap 调用方必须复用返回的 ResolvedScope。
func (p *ManagerPool) ForScope(scope LSPToolScope) (ScopedManager, error) {
	if p == nil {
		return ScopedManager{}, errors.New("LSP manager pool is nil")
	}
	p.lifecycleMu.RLock()
	defer p.lifecycleMu.RUnlock()
	if p.closing || p.closed {
		return ScopedManager{}, ErrManagerPoolClosed
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

// rememberPendingReleases 登记已从 shard 隔离、等待最后租约释放的旧 manager 代际。
func (p *ManagerPool) rememberPendingReleases(managers []pendingManagerRelease) {
	if p == nil || len(managers) == 0 {
		return
	}
	p.pendingMu.Lock()
	for _, pending := range managers {
		if pending.manager != nil {
			if _, exists := p.pendingReleases[pending.manager]; !exists {
				p.pendingReleases[pending.manager] = pendingManagerReleaseState{
					scope:  pending.scope,
					report: true,
				}
			}
		}
	}
	p.pendingMu.Unlock()
}

// drainPendingReleases 关闭租约已经归零的延迟释放 manager。
func (p *ManagerPool) drainPendingReleases() {
	if p == nil {
		return
	}
	p.pendingMu.Lock()
	candidates := make([]*manager, 0, len(p.pendingReleases))
	for mgr, state := range p.pendingReleases {
		if state.completed {
			continue
		}
		candidates = append(candidates, mgr)
	}
	p.pendingMu.Unlock()

	for _, mgr := range candidates {
		if !p.managerIdleEligibleForRelease(mgr) {
			continue
		}
		done, err := mgr.closeWithoutPoolStatus()
		p.recordPendingCloseAttempt(mgr, done, err)
		if err != nil && mgr.logger != nil {
			mgr.logger.Warn("LSP deferred release close failed", "err", err)
		}
	}
}

// managerIdleEligibleForRelease 防止普通 scope release 在最后租约归零后绕过完整 idle window。
// Manager.Close 不经过此函数，仍保留确定性 owner shutdown 语义。
func (p *ManagerPool) managerIdleEligibleForRelease(mgr *manager) bool {
	if mgr == nil {
		return false
	}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	now := mgr.managerNow()
	for _, workspace := range mgr.workspaces {
		if workspace == nil || workspace.client == nil {
			continue
		}
		if !idleEligible(workspace, now, mgr.idleTimeout) {
			return false
		}
	}
	return true
}

// recordPendingCloseAttempt 更新 cleanup receipt；无需上报的成功回收会立即移除 tombstone。
func (p *ManagerPool) recordPendingCloseAttempt(mgr *manager, done bool, closeErr error) {
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	state, exists := p.pendingReleases[mgr]
	if !exists {
		return
	}
	if done && closeErr == nil && !state.report {
		delete(p.pendingReleases, mgr)
		return
	}
	state.completed = done
	state.closeErr = closeErr
	p.pendingReleases[mgr] = state
}

func (p *ManagerPool) takeAllPendingReleases() []*manager {
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	managers := make([]*manager, 0, len(p.pendingReleases))
	for mgr := range p.pendingReleases {
		managers = append(managers, mgr)
	}
	clear(p.pendingReleases)
	return managers
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
	for i := range size {
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
	closeErr := p.closeDetachedPoolManagers(toClose, "close shard-cap evicted LSP manager")
	if closeErr != nil {
		return nil, closeErr
	}
	return pooled, nil
}

// evictIdleClonesLocked 在 shard 锁内挑选可关闭的闲置 clone。
// keepKey 和仍有租约的 manager 不会被驱逐，避免关闭当前请求或忙碌客户端。
func (p *ManagerPool) evictIdleClonesLocked(shard *managerShard, keepKey string) []pendingManagerRelease {
	if p == nil || shard == nil || p.shardCap <= 0 {
		return nil
	}
	var toClose []pendingManagerRelease
	for len(shard.clones) > p.shardCap {
		evictKey, evict := p.oldestIdleCloneLocked(shard, keepKey)
		if evict == nil {
			break
		}
		if !p.retireManagerIfIdle(evict.manager) {
			break
		}
		delete(shard.clones, evictKey)
		toClose = append(toClose, pendingManagerRelease{
			manager: evict.manager,
			scope:   evict.resolvedScope,
		})
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

// retireManagerIfIdle 在 manager 写锁内复核租约并建立代际栅栏，避免容量淘汰后旧指针重新创建 client。
func (p *ManagerPool) retireManagerIfIdle(mgr *manager) bool {
	if p == nil || mgr == nil {
		return false
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if mgr.closed {
		return true
	}
	if mgr.retiring {
		return false
	}
	now := mgr.managerNow()
	for _, workspace := range mgr.workspaces {
		if !p.retireWorkspaceEligible(mgr, workspace, now) {
			return false
		}
	}
	mgr.retiring = true
	return true
}

// retireWorkspaceEligible 判断容量淘汰时单个 workspace 是否满足代际、租约和完整 idle window。
func (p *ManagerPool) retireWorkspaceEligible(mgr *manager, workspace *workspaceClient, now time.Time) bool {
	if workspace == nil || workspace.client == nil {
		return true
	}
	if mgr == nil || mgr.idleTimeout <= 0 {
		return false
	}
	if workspace.generation == 0 || p.activeLeasesForWorkspace(workspace) > 0 {
		return false
	}
	// Capacity pressure never bypasses the full idle timeout for a registered generation.
	return idleEligible(workspace, now, mgr.idleTimeout)
}

// detachIdleEmptyClones 摘除早于 cutoff 且没有 workspace 或租约的 clone。
func (p *ManagerPool) detachIdleEmptyClones(cutoff time.Time) []pendingManagerRelease {
	if p == nil || cutoff.IsZero() {
		return nil
	}
	var detached []pendingManagerRelease
	for _, shard := range p.shards {
		if shard == nil {
			continue
		}
		shard.mu.Lock()
		for key, clone := range shard.clones {
			if !p.retireExpiredEmptyClone(clone, cutoff) {
				continue
			}
			delete(shard.clones, key)
			detached = append(detached, pendingManagerRelease{
				manager: clone.manager,
				scope:   clone.resolvedScope,
			})
		}
		shard.mu.Unlock()
	}
	return detached
}

// retireExpiredEmptyClone 在确认 clone 过期且为空后建立代际栅栏，封住摘除到关闭之间的新请求窗口。
func (p *ManagerPool) retireExpiredEmptyClone(clone *pooledManager, cutoff time.Time) bool {
	if clone == nil || clone.manager == nil || clone.lastUsedAt.IsZero() || clone.lastUsedAt.After(cutoff) {
		return false
	}
	clone.manager.mu.Lock()
	defer clone.manager.mu.Unlock()
	if len(clone.manager.workspaces) != 0 || clone.manager.retiring {
		return false
	}
	clone.manager.retiring = true
	return true
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

// closeStatus 原子封闭新 scope，并重试所有尚未完成进程级 Close 的 manager。
// done 后返回结果保持稳定；未完成时保留 closePending owner，后续 Close 会继续清理。
func (p *ManagerPool) closeStatus() (done bool, err error) {
	if p == nil {
		return true, nil
	}
	p.closeMu.Lock()
	defer p.closeMu.Unlock()

	p.lifecycleMu.RLock()
	if p.closed {
		result := p.closeTerminalErr
		p.lifecycleMu.RUnlock()
		return true, result
	}
	alreadyClosing := p.closing
	p.lifecycleMu.RUnlock()
	if !alreadyClosing {
		p.beginClose()
	}

	var retryErr error
	for mgr := range p.closePending {
		completed, closeErr := mgr.closeWithoutPoolStatus()
		if !completed {
			retryErr = errors.Join(retryErr, closeErr)
			continue
		}
		delete(p.closePending, mgr)
		if closeErr != nil {
			p.closeTerminalErr = errors.Join(p.closeTerminalErr, closeErr)
		}
	}
	if len(p.closePending) > 0 {
		return false, errors.Join(p.closeTerminalErr, retryErr)
	}
	p.lifecycleMu.Lock()
	p.closed = true
	p.lifecycleMu.Unlock()
	return true, p.closeTerminalErr
}

// beginClose 在 lifecycle 写锁下摘除所有 shard/pending manager，封住并发创建后再交给 closePending。
func (p *ManagerPool) beginClose() {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	if p.closing || p.closed {
		return
	}
	p.closing = true
	for _, shard := range p.shards {
		p.detachShardManagersForClose(shard)
	}
	for _, pending := range p.takeAllPendingReleases() {
		if pending != nil {
			p.closePending[pending] = struct{}{}
		}
	}
}

// detachShardManagersForClose 在单 shard 锁内转移 manager cleanup owner 并清空可见缓存。
func (p *ManagerPool) detachShardManagersForClose(shard *managerShard) {
	if p == nil || shard == nil {
		return
	}
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if shard.base != nil {
		p.closePending[shard.base] = struct{}{}
		shard.base = nil
	}
	for key, clone := range shard.clones {
		if clone != nil && clone.manager != nil {
			p.closePending[clone.manager] = struct{}{}
		}
		delete(shard.clones, key)
	}
}

func cloneManagerFactory(primary *manager) ManagerFactory {
	return managerFactoryFunc(func(_ string, workspaceRoot string, _ RootOptions) (*manager, error) {
		if primary == nil {
			return nil, errors.New("primary LSP manager is nil")
		}
		return primary.cloneForWorkspace(workspaceRoot), nil
	})
}
