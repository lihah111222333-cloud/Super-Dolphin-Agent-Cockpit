package rpc

import (
	"context"
	"time"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// ApprovalCleanupRunner wraps the approval-timeout cleanup ticker as a
// platformrunner.Runner. Introduced by P22 P1b (Finding 4) to move the
// long-running cleanup loop from `bindApprovalLifecycle -> OnStart ->
// go startApprovalCleanupLoop(...)` into the root `group:"runners"`
// aggregation. Startup restore stays in bindApprovalLifecycle; this runner
// only owns the ticker-driven cleanup.
type ApprovalCleanupRunner struct {
	approvals *ApprovalManager
	logger    *pkglogger.Logger
	interval  time.Duration
}

// NewApprovalCleanupRunner is the fx constructor; returned as
// platformrunner.Runner so the module-level Provide list can tag it with
// `group:"runners"` without referencing the concrete struct.
//
// The ApprovalManager may be nil (some test / partial-wiring configs); the
// Run loop handles that by blocking on ctx.Done without doing any work.
func NewApprovalCleanupRunner(approvals *ApprovalManager, logger *pkglogger.Logger) platformrunner.Runner {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &ApprovalCleanupRunner{
		approvals: approvals,
		logger:    logger,
		interval:  approvalCleanupInterval,
	}
}

// Run implements platformrunner.Runner. Blocks on the cleanup ticker until
// ctx.Done; returns ctx.Err(). Reads DefaultApprovalTimeout lazily on each
// tick so tests that mutate it (approval_test.go does this) continue to see
// the override even after the runner starts.
func (r *ApprovalCleanupRunner) Run(ctx context.Context) error {
	if r == nil || r.approvals == nil || r.interval <= 0 {
		<-ctx.Done()
		return ctx.Err()
	}
	timer := time.NewTimer(timerDelayWithJitter(r.interval))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			r.tick()
			timer.Reset(timerDelayWithJitter(r.interval))
		}
	}
}

func (r *ApprovalCleanupRunner) tick() {
	timeout := DefaultApprovalTimeout
	if timeout <= 0 {
		return
	}
	before := len(r.approvals.PendingSnapshot())
	r.approvals.Cleanup(timeout)
	if r.logger == nil {
		return
	}
	after := len(r.approvals.PendingSnapshot())
	if after < before {
		r.logger.Warn("rpc: cleaned expired pending approvals",
			"removed", before-after, "timeout", timeout.String())
	}
}
