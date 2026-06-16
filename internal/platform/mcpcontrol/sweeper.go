package mcpcontrol

import (
	"context"
	"math/rand"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

const (
	defaultSweepTick      = 5 * time.Second
	defaultSweepJitter    = time.Second
	defaultHeartbeatTTL   = 30 * time.Second
	defaultStaleGraceTime = 5 * time.Second
)

// SweepResult reports how many leases were marked stale or evicted during a sweep.
type SweepResult struct {
	Staled  int
	Evicted int
}

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

// Sweeper periodically marks stale leases and evicts expired MCP tool peers from a ToolRegistry.
type Sweeper struct {
	registry   *ToolRegistry
	logger     *pkglogger.Logger
	tick       time.Duration
	jitter     time.Duration
	timeout    time.Duration
	staleGrace time.Duration
}

// SweeperOptions configures the sweep cadence and stale lease eviction thresholds for a Sweeper.
type SweeperOptions struct {
	Tick       time.Duration
	Jitter     time.Duration
	Timeout    time.Duration
	StaleGrace time.Duration
}

// NewSweeper 创建sweeper。
func NewSweeper(registry *ToolRegistry, logger *pkglogger.Logger) *Sweeper {
	return NewSweeperWithOptions(registry, logger, SweeperOptions{})
}

// NewSweeperWithOptions 创建带选项的sweeper。
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

// Run 启动平台mcpcontrol后台流程。
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

// Sweep 清理过期记录。
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

func (s *Sweeper) logResult(result SweepResult) {
	if s == nil || s.logger == nil || (result.Staled == 0 && result.Evicted == 0) {
		return
	}
	s.logger.Info("mcp control sweep completed", "staled", result.Staled, "evicted", result.Evicted)
}

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

func (s *Sweeper) nextInterval() time.Duration {
	if s.jitter <= 0 {
		return s.tick
	}
	return s.tick + time.Duration(rand.Int63n(int64(s.jitter)))
}
