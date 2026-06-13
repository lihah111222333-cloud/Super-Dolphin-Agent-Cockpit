package mcpcontrol

import (
	"context"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

// SweeperRunner wraps Sweeper as a platformrunner.Runner. Introduced by P22
// P1b (Finding 3) to move the long-running sweep loop from `OnStart -> go
// sweeper.Run(ctx)` under registerSweeperLifecycle into the root
// `group:"runners"` aggregation.
//
// The runner performs a synchronous sweeper.Run(ctx) with no inner `go`: the
// sweeper's existing timer+jitter cadence is preserved unchanged; only the
// owner (run.Group actor vs fx OnStart goroutine) moves.
type SweeperRunner struct {
	sweeper *Sweeper
}

// NewSweeperRunner constructs the runner. Returned as platformrunner.Runner
// for fx group annotation, kept as a constructor (not a factory) so the
// module's Provide list can stay small.
// NewSweeperRunner 创建sweeperrunner。
func NewSweeperRunner(sweeper *Sweeper) platformrunner.Runner {
	return &SweeperRunner{sweeper: sweeper}
}

// Run implements platformrunner.Runner. Blocks until ctx.Done or sweeper.Run
// returns (which only happens on ctx.Done). Returns ctx.Err() so the
// run.Group actor sees a clean cancellation.
// Run 启动平台mcpcontrol后台流程。
func (r *SweeperRunner) Run(ctx context.Context) error {
	if r == nil || r.sweeper == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	r.sweeper.Run(ctx)
	return ctx.Err()
}
