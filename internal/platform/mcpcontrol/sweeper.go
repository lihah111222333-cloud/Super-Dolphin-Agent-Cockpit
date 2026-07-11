package mcpcontrol

import (
	"context"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"math/rand"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

const (
	// sweeper 默认节奏与 heartbeat TTL，staleGrace 给临时抖动留一次恢复窗口。
	defaultSweepTick      = 5 * time.Second
	defaultSweepJitter    = time.Second
	defaultHeartbeatTTL   = 30 * time.Second
	defaultStaleGraceTime = 5 * time.Second
)

// SweepResult 汇总一次扫描标记 stale 和驱逐的租约数量。
type SweepResult struct {
	Staled  int
	Evicted int
}

// sweepTarget 是锁内捕获的驱逐日志快照，peer 会在锁外关闭。
type sweepTarget struct {
	key           LeaseKey
	peer          Peer
	reason        string
	binaryName    string
	agentID       string
	threadID      string
	pid           int
	peerKind      string
	clientKind    string
	status        string
	lastHeartbeat time.Time
}

// Sweeper 周期性标记 stale 租约并驱逐过期 MCP peer。
type Sweeper struct {
	registry   *ToolRegistry
	logger     *pkglogger.Logger
	tick       time.Duration
	jitter     time.Duration
	timeout    time.Duration
	staleGrace time.Duration
}

// SweeperOptions 配置扫描节奏、heartbeat 超时和 stale 后的宽限时间。
type SweeperOptions struct {
	Tick       time.Duration
	Jitter     time.Duration
	Timeout    time.Duration
	StaleGrace time.Duration
}

// NewSweeper 使用默认扫描参数创建 sweeper。
func NewSweeper(registry *ToolRegistry, logger *pkglogger.Logger) *Sweeper {
	return NewSweeperWithOptions(registry, logger, SweeperOptions{})
}

// NewSweeperWithOptions 创建可测试配置的 sweeper，零值选项会落到控制面默认值。
func NewSweeperWithOptions(registry *ToolRegistry, logger *pkglogger.Logger, opts SweeperOptions) *Sweeper {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &Sweeper{
		registry:   registry,
		logger:     logger,
		tick:       durationOrDefault(opts.Tick, defaultSweepTick),
		jitter:     durationOrDefault(opts.Jitter, defaultSweepJitter),
		timeout:    durationOrDefault(opts.Timeout, defaultHeartbeatTTL),
		staleGrace: durationOrDefault(opts.StaleGrace, defaultStaleGraceTime),
	}
}

// Run 按带 jitter 的间隔扫描注册表，直到 ctx 取消；不在内部再启动 goroutine。
func (s *Sweeper) Run(ctx context.Context) {
	if s == nil || s.registry == nil {
		return
	}
	timer := time.NewTimer(s.nextInterval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-timer.C:
			s.logResult(s.Sweep(now))
			timer.Reset(s.nextInterval())
		}
	}
}

// Sweep 在锁内标记/摘除过期租约，随后锁外关闭 peer 并清理 hook 生命周期。
func (s *Sweeper) Sweep(now time.Time) SweepResult {
	if s == nil || s.registry == nil {
		return SweepResult{}
	}
	result := SweepResult{}
	var evicted []sweepTarget

	s.registry.mu.Lock()
	for key, instance := range s.registry.instances {
		switch {
		case instance.Status == dto.StatusDisconnected:
			target := newSweepTarget(key, instance, "disconnected")
			target.peer = s.registry.evictLocked(key)
			evicted = append(evicted, target)
			result.Evicted++
		case instance.LastHeartbeat.Add(s.timeout).Before(now):
			if instance.Status != dto.StatusStale {
				instance.Status = dto.StatusStale
				result.Staled++
				s.logStaled(key, instance, now)
			}
			if instance.LastHeartbeat.Add(s.timeout + s.staleGrace).Before(now) {
				target := newSweepTarget(key, instance, "heartbeat_timeout")
				target.peer = s.registry.evictLocked(key)
				evicted = append(evicted, target)
				result.Evicted++
			}
		}
	}
	s.registry.mu.Unlock()

	for _, target := range evicted {
		s.logEvicted(target, now)
		_ = s.registry.disconnectLease(target.key, disconnectLeaseOptions{
			peer:    target.peer,
			timeout: true,
		})
	}
	return result
}

// logResult 只在本轮有状态变化时输出扫描摘要。
func (s *Sweeper) logResult(result SweepResult) {
	if s == nil || s.logger == nil || (result.Staled == 0 && result.Evicted == 0) {
		return
	}
	s.logger.Info("mcp control sweep completed", "staled", result.Staled, "evicted", result.Evicted)
}

// newSweepTarget 复制驱逐日志所需字段，避免锁外读取已删除实例。
func newSweepTarget(key LeaseKey, instance *ToolInstance, reason string) sweepTarget {
	target := sweepTarget{key: key, reason: reason}
	if instance == nil {
		return target
	}
	target.binaryName = instance.BinaryName
	target.agentID = instance.AgentID
	target.threadID = instance.ThreadID
	target.pid = instance.PID
	target.peerKind = instance.PeerKind
	target.clientKind = instance.ClientKind
	target.status = instance.Status
	target.lastHeartbeat = instance.LastHeartbeat
	return target
}

// logStaled 记录租约首次超时进入 stale 的上下文，保留 heartbeat age 便于排查。
func (s *Sweeper) logStaled(key LeaseKey, instance *ToolInstance, now time.Time) {
	if s == nil || s.logger == nil || instance == nil {
		return
	}
	s.logger.Warn("mcp control sweep marked peer stale",
		"instance_id", key.InstanceID,
		"generation", key.Generation,
		"binary", instance.BinaryName,
		"client_kind", instance.ClientKind,
		"peer_kind", instance.PeerKind,
		"pid", instance.PID,
		"agent_id", instance.AgentID,
		"thread_id", instance.ThreadID,
		"last_heartbeat", instance.LastHeartbeat,
		"heartbeat_age", now.Sub(instance.LastHeartbeat),
		"timeout", s.timeout)
}

// logEvicted 记录最终驱逐信息，字段来自锁内快照而不是已删除实例。
func (s *Sweeper) logEvicted(target sweepTarget, now time.Time) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Warn("mcp control sweep evicting peer",
		"instance_id", target.key.InstanceID,
		"generation", target.key.Generation,
		"binary", target.binaryName,
		"client_kind", target.clientKind,
		"peer_kind", target.peerKind,
		"pid", target.pid,
		"agent_id", target.agentID,
		"thread_id", target.threadID,
		"status", target.status,
		"reason", target.reason,
		"last_heartbeat", target.lastHeartbeat,
		"heartbeat_age", now.Sub(target.lastHeartbeat),
		"timeout", s.timeout,
		"stale_grace", s.staleGrace)
}

// nextInterval 返回下一次扫描间隔，jitter 用来避免多个 peer 同步抖动。
func (s *Sweeper) nextInterval() time.Duration {
	if s.jitter <= 0 {
		return s.tick
	}
	return s.tick + time.Duration(rand.Int63n(int64(s.jitter)))
}
