package codexapp

import (
	"context"
	"log/slog"
	"time"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// defaultPoolEvictInterval is the tick period at which the idle
// reaper wakes. Short enough that eviction latency is bounded by the
// interval on top of IdleTimeout; long enough that the wake-up cost
// is negligible.
const defaultPoolEvictInterval = time.Minute

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
