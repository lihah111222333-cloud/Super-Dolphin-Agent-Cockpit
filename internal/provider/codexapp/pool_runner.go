package codexapp

import (
	"context"
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// defaultPoolEvictInterval is the tick period at which the idle
// reaper wakes. Short enough that eviction latency is bounded by the
// interval on top of IdleTimeout; long enough that the wake-up cost
// is negligible.
const defaultPoolEvictInterval = time.Minute

// ServerPoolParams carries the fx dependencies for provideServerPool.
// The logger and pid registry are optional so downstream consumers can
// construct the pool in tests without wiring the full app graph.
type ServerPoolParams struct {
	fx.In

	Lifecycle   fx.Lifecycle
	Logger      *slog.Logger              `optional:"true"`
	PIDRegistry *pidregistry.Registry     `optional:"true"`
}

// provideServerPool builds a production ServerPool using the
// transport-backed Spawner. The pool is closed on fx Stop so every
// remaining app-server child receives SIGTERM before the process tree
// tears down.
//
// The pool is intentionally provided independently of the legacy
// ServerManager. Consumers that opt into pool-backed spawning will
// take *ServerPool via fx; existing consumers keep talking to
// ServerManager unchanged. That split lets the cutover land in a
// follow-up PR once we have real codex-binary validation.
func provideServerPool(p ServerPoolParams) *ServerPool {
	logger := p.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	spawner := NewTransportSpawner(p.PIDRegistry, logger)
	pool := NewServerPool(logger, spawner, PoolConfig{})
	p.Lifecycle.Append(fx.Hook{
		OnStop: func(ctx context.Context) error { return pool.Close(ctx) },
	})
	return pool
}

// poolEvictRunner wakes on a fixed interval and asks the pool to
// evict entries whose lastUsed exceeds IdleTimeout. The runner
// intentionally exits silently on ctx cancellation: closing the pool
// is the app-level owner's job (see fx lifecycle above).
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

// Run loops until ctx cancels. Each tick calls ServerPool.EvictIdle
// and logs whenever at least one entry was actually evicted — a silent
// tick is the common case and would otherwise flood the log.
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

// poolEvictRunnerAsRunner narrows *poolEvictRunner to the
// platformrunner.Runner interface for the group:"runners" collector.
func poolEvictRunnerAsRunner(r *poolEvictRunner) platformrunner.Runner { return r }
