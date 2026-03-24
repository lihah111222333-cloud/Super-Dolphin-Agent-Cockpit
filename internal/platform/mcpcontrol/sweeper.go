package mcpcontrol

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

const (
	defaultSweepTick      = 5 * time.Second
	defaultSweepJitter    = time.Second
	defaultHeartbeatTTL   = 30 * time.Second
	defaultStaleGraceTime = 5 * time.Second
)

type SweepResult struct {
	Staled  int
	Evicted int
}

type Sweeper struct {
	registry   *ToolRegistry
	logger     *slog.Logger
	tick       time.Duration
	jitter     time.Duration
	timeout    time.Duration
	staleGrace time.Duration
}

type SweeperOptions struct {
	Tick       time.Duration
	Jitter     time.Duration
	Timeout    time.Duration
	StaleGrace time.Duration
}

func NewSweeper(registry *ToolRegistry, logger *slog.Logger) *Sweeper {
	return NewSweeperWithOptions(registry, logger, SweeperOptions{})
}

func NewSweeperWithOptions(registry *ToolRegistry, logger *slog.Logger, opts SweeperOptions) *Sweeper {
	if logger == nil {
		logger = slog.Default()
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

func (s *Sweeper) Sweep(now time.Time) SweepResult {
	if s == nil || s.registry == nil {
		return SweepResult{}
	}
	result := SweepResult{}
	var peers []Peer
	var evicted []LeaseKey

	s.registry.mu.Lock()
	for key, instance := range s.registry.instances {
		switch {
		case instance.Status == dto.StatusDisconnected:
			peers = append(peers, s.registry.evictLocked(key))
			evicted = append(evicted, key)
			result.Evicted++
		case instance.LastHeartbeat.Add(s.timeout).Before(now):
			if instance.Status != dto.StatusStale {
				instance.Status = dto.StatusStale
				result.Staled++
			}
			if instance.LastHeartbeat.Add(s.timeout + s.staleGrace).Before(now) {
				peers = append(peers, s.registry.evictLocked(key))
				evicted = append(evicted, key)
				result.Evicted++
			}
		}
	}
	s.registry.mu.Unlock()

	for i, peer := range peers {
		s.registry.cleanupLease(context.Background(), evicted[i])
		closePeer(peer)
	}
	return result
}

func (s *Sweeper) logResult(result SweepResult) {
	if s == nil || s.logger == nil || (result.Staled == 0 && result.Evicted == 0) {
		return
	}
	s.logger.Info("mcp control sweep completed", "staled", result.Staled, "evicted", result.Evicted)
}

func (s *Sweeper) nextInterval() time.Duration {
	if s.jitter <= 0 {
		return s.tick
	}
	return s.tick + time.Duration(rand.Int63n(int64(s.jitter)))
}
