package codexapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/discovery"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"golang.org/x/sync/singleflight"
)

// SpawnedServer 是 ServerPool 持有的最小 app-server 生命周期接口。
// 生产实现包装 transport，测试可注入 fake，避免单元测试必须启动 Codex 二进制。
type SpawnedServer interface {
	ServerURL() string
	Close(ctx context.Context) error
	Alive() bool
}

// Spawner 为指定 Codex 身份创建新的 app-server。
// 临时启动错误会记录到池条目中，后续请求在 SpawnBackoff 窗口内直接返回缓存错误。
type Spawner func(ctx context.Context, home, modelProvider string) (SpawnedServer, error)

// PoolConfig 配置 ServerPool 的空闲回收和启动退避参数。
// 零值会使用默认值，调用方无需自己复制常量。
type PoolConfig struct {
	// IdleTimeout 控制无引用条目多久后可被后台回收。
	// session owner 最后一次 release 会立即关闭进程树，不等待该超时。
	IdleTimeout time.Duration
	// SpawnBackoff 控制同一身份启动失败后的冷却窗口。
	// 窗口内返回缓存错误，避免反复拉起同一个失败进程。
	SpawnBackoff time.Duration
	// MaxLive 控制池内同时存活的 app-server 总数，0 表示不限制。
	MaxLive int
	// MaxLivePerHome 控制同一 canonical home 同时存活的 app-server 数，0 表示不限制。
	MaxLivePerHome int
}

// ServerPool 默认空闲回收与启动退避时间。
const (
	DefaultPoolIdleTimeout  = 30 * time.Minute
	DefaultPoolSpawnBackoff = 2 * time.Minute
)

// ServerPool 对外暴露的哨兵错误。
// 调用方依赖这些错误区分退避、池关闭和身份配置问题。
var (
	ErrSpawnBackoff    = errors.New("codexapp: spawn backoff active for codexHome")
	ErrPoolClosed      = errors.New("codexapp: server pool is closed")
	ErrInvalidIdentity = errors.New("codexapp: invalid codex identity")
	ErrPoolCapacity    = errors.New("codexapp: server pool capacity exhausted")
	noopRelease        = func() {}
)

// ServerPool 按 canonical Codex 身份和 owner 管理 app-server 实例。
// 所有公开方法都受内部锁保护，可并发调用。
//
// Acquire 会复用仍存活的条目，否则启动新进程；release 到零引用时立即关闭该进程树。
// 启动失败会在 identity+owner 维度退避，Close 后拒绝后续 Acquire。
type ServerPool struct {
	logger  *slog.Logger
	spawner Spawner
	cfg     PoolConfig
	now     func() time.Time

	mu      sync.Mutex
	entries map[poolEntryKey]*poolEntry
	closed  bool
	flight  singleflight.Group
}

type poolEntryKey struct{ home, instanceKey, modelProvider, processPolicy, ownerKey string }

type poolEntry struct {
	key                    poolEntryKey
	server                 SpawnedServer
	lastUsed, backoffUntil time.Time
	refCount               int
	spawnErr               error
}

// NewServerPool 创建 Codex app-server 池。
// spawner 为空时池仍可构造，但 Acquire 会显式失败，避免在 fx 装配阶段静默吞掉配置错误。
func NewServerPool(logger *slog.Logger, spawner Spawner, cfg PoolConfig) *ServerPool {
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

// Acquire 返回指定 Codex 身份和 owner 对应的 app-server，必要时会启动新进程。
// 返回的 release 必须调用一次，用于维护引用计数和空闲时间；失败时 release 是安全 no-op。
//
// 身份缺字段会 fail-fast，避免不同 provider 实例因为 home 相同而错误复用同一进程。
func (p *ServerPool) Acquire(ctx context.Context, identity providershared.CodexIdentity, ownerKey string) (SpawnedServer, func(), error) {
	home, key, provider, err := normalizePoolIdentity(identity)
	if err != nil {
		return nil, noopRelease, err
	}
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return nil, noopRelease, fmt.Errorf("%w: pool owner agentID is empty", ErrInvalidIdentity)
	}
	entryKey := poolEntryKey{home: home, instanceKey: key, modelProvider: provider, processPolicy: poolSpawnPolicySignature(ctx), ownerKey: ownerKey}
	p.mu.Lock()
	fastPath := p.acquireFastPathLocked(entryKey)
	if fastPath.done {
		p.mu.Unlock()
		return fastPath.server, fastPath.release, fastPath.err
	}
	// 启动新进程前先检查退避窗口，避免失败身份被快速重试打爆。
	if err := p.checkBackoffLocked(fastPath.entry, fastPath.now); err != nil {
		p.mu.Unlock()
		return nil, noopRelease, err
	}
	if err := p.checkCapacityLocked(entryKey); err != nil {
		p.mu.Unlock()
		return nil, noopRelease, err
	}
	spawnAt := fastPath.now
	p.mu.Unlock()

	flightKey := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", entryKey.home, entryKey.instanceKey, entryKey.modelProvider, entryKey.processPolicy, entryKey.ownerKey)
	if _, err, _ := p.flight.Do(flightKey, func() (any, error) {
		return p.spawnAndStore(ctx, entryKey, home, provider, spawnAt)
	}); err != nil {
		return nil, noopRelease, err
	}
	p.mu.Lock()
	fastPath = p.acquireFastPathLocked(entryKey)
	p.mu.Unlock()
	if fastPath.done {
		return fastPath.server, fastPath.release, fastPath.err
	}
	return nil, noopRelease, errors.New("codexapp: pool spawned server unavailable")
}

type acquireFastPathResult struct {
	entry   *poolEntry
	now     time.Time
	server  SpawnedServer
	release func()
	err     error
	done    bool
}

// acquireFastPathLocked 在持锁状态下处理复用、关闭和死进程快路径。
// 返回 done=false 时调用方必须释放锁后执行真正的 spawn，避免长时间持锁启动进程。
func (p *ServerPool) acquireFastPathLocked(entryKey poolEntryKey) acquireFastPathResult {
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
	result.entry = p.entries[entryKey]
	if result.entry == nil {
		return result
	}
	// 存活条目直接增加引用计数并返回对应 release。
	if result.entry.server != nil && result.entry.server.Alive() {
		result.entry.refCount++
		result.entry.lastUsed = result.now
		result.server = result.entry.server
		result.release = p.releaser(entryKey)
		result.done = true
		return result
	}
	// 死条目只清空 server，保留退避状态，防止不稳定进程持续重启。
	result.entry.server = nil
	return result
}

// spawnAndStore 在 singleflight leader 中启动进程并写入池；引用计数由每个 Acquire 返回前单独增加。
func (p *ServerPool) spawnAndStore(ctx context.Context, entryKey poolEntryKey, home, provider string, spawnAt time.Time) (SpawnedServer, error) {
	server, err := p.spawner(ctx, home, provider)
	p.mu.Lock()
	entry := p.entries[entryKey]
	if err != nil {
		p.recordSpawnErrorLocked(entry, entryKey, err, spawnAt)
		p.mu.Unlock()
		return nil, fmt.Errorf("codexapp: spawn %q: %w", home, err)
	}
	if p.closed {
		p.mu.Unlock()
		closeWithTimeout(server, 2*time.Second, p.logger, entryKey)
		return nil, ErrPoolClosed
	}
	p.storeSpawnedLocked(entry, entryKey, server, spawnAt)
	p.mu.Unlock()
	return server, nil
}

func (p *ServerPool) checkBackoffLocked(entry *poolEntry, now time.Time) error {
	if entry == nil || entry.spawnErr == nil || !now.Before(entry.backoffUntil) {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrSpawnBackoff, entry.spawnErr)
}

// checkCapacityLocked 在启动新进程前执行容量门，避免超限身份继续拉起 app-server。
func (p *ServerPool) checkCapacityLocked(entryKey poolEntryKey) error {
	if p.cfg.MaxLive > 0 && p.liveEntriesLocked("") >= p.cfg.MaxLive {
		return fmt.Errorf("%w: max_live=%d", ErrPoolCapacity, p.cfg.MaxLive)
	}
	if p.cfg.MaxLivePerHome > 0 && p.liveEntriesLocked(entryKey.home) >= p.cfg.MaxLivePerHome {
		return fmt.Errorf("%w: home=%q max_live_per_home=%d", ErrPoolCapacity, entryKey.home, p.cfg.MaxLivePerHome)
	}
	return nil
}

// liveEntriesLocked 统计当前仍存活的池条目；home 为空时统计全局容量。
func (p *ServerPool) liveEntriesLocked(home string) int {
	count := 0
	for _, entry := range p.entries {
		if entry == nil || entry.server == nil || !entry.server.Alive() {
			continue
		}
		if home != "" && entry.key.home != home {
			continue
		}
		count++
	}
	return count
}

func (p *ServerPool) recordSpawnErrorLocked(entry *poolEntry, entryKey poolEntryKey, err error, now time.Time) {
	// 记录启动失败并保留条目，后续同一身份可复用退避状态。
	if entry == nil {
		entry = newPoolEntry(entryKey)
		p.entries[entryKey] = entry
	}
	entry.spawnErr, entry.backoffUntil, entry.lastUsed = err, now.Add(p.cfg.SpawnBackoff), now
}

func (p *ServerPool) storeSpawnedLocked(entry *poolEntry, entryKey poolEntryKey, server SpawnedServer, now time.Time) {
	if entry == nil {
		entry = newPoolEntry(entryKey)
		p.entries[entryKey] = entry
	}
	entry.server, entry.spawnErr, entry.backoffUntil, entry.refCount, entry.lastUsed = server, nil, time.Time{}, 0, now
}

func newPoolEntry(entryKey poolEntryKey) *poolEntry {
	return &poolEntry{key: entryKey}
}

// releaser 返回 Acquire 对应的引用释放闭包。
// 最后一个引用释放时会删除池条目并关闭 app-server 进程树，重复调用只会成为 no-op。
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

// EvictIdle 关闭超过 IdleTimeout 且无引用的池条目。
// 该方法供后台 runner 周期调用，返回回收数量用于日志或指标。
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

// Close 关闭池中所有 app-server，并阻止后续 Acquire。
// 多次调用是幂等的；单个条目关闭失败会返回第一个错误。
func (p *ServerPool) Close(ctx context.Context) error {
	ctx = nonNilContext(ctx)
	p.mu.Lock()
	entries, alreadyClosed := p.snapshotCloseEntriesLocked()
	p.mu.Unlock()
	if alreadyClosed {
		return nil
	}
	var firstErr error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			if firstErr != nil {
				return firstErr
			}
			return err
		}
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

// Size 返回当前池条目数量。
// 该值主要用于测试和轻量观测，读取时持锁保证一致快照。
func (p *ServerPool) Size() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.entries) }

// normalizePoolIdentity 规范化池 key 所需的 Codex 身份三元组。
// home、instanceKey、modelProvider 任一缺失都直接报错，避免多个 provider 实例静默合并。
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

// closeWithTimeout 用后台超时关闭池条目。
// 回收路径不能被慢进程无限阻塞；关闭失败只记 debug，因为常见于进程已先行退出的竞态。
func closeWithTimeout(server SpawnedServer, timeout time.Duration, logger *slog.Logger, key poolEntryKey) {
	ctx, cancel := withTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Close(ctx); err != nil {
		logger.Debug("codexapp: pool close entry failed", slog.String("codex_home", key.home), slog.String("owner", key.ownerKey), slog.String("error", err.Error()))
	}
}

const defaultPoolEvictInterval = time.Minute

type poolEvictRunner struct {
	logger   *slog.Logger
	pool     *ServerPool
	interval time.Duration
}

func newPoolEvictRunner(logger *slog.Logger, pool *ServerPool) *poolEvictRunner {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &poolEvictRunner{logger: logger, pool: pool, interval: defaultPoolEvictInterval}
}

var _ platformrunner.Runner = (*poolEvictRunner)(nil)

// Run 周期性回收 ServerPool 中无引用的空闲条目。
// pool 为空时只等待 ctx 结束，保持 runner 生命周期和上层一致。
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
					slog.Int("count", removed),
				)
			}
		}
	}
}

func poolEvictRunnerAsRunner(r *poolEvictRunner) platformrunner.Runner { return r }

// cleanPeerDiscoveryFiles 清理当前进程写出的 peer MCP discovery 文件。
// ServerManager shutdown 会调用它作为安全网，失败仅告警，不阻断主关闭流程。
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
