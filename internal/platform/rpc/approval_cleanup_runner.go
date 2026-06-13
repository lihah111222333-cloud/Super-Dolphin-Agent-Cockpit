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
	timeout   time.Duration
}

// NewApprovalCleanupRunner is the fx constructor; returned as
// platformrunner.Runner so the module-level Provide list can tag it with
// `group:"runners"` without referencing the concrete struct.
//
// The ApprovalManager may be nil (some test / partial-wiring configs); the
// Run loop handles that by blocking on ctx.Done without doing any work.
// NewApprovalCleanupRunner 创建审批cleanuprunner。
func NewApprovalCleanupRunner(approvals *ApprovalManager, logger *pkglogger.Logger) platformrunner.Runner {
	return newApprovalCleanupRunnerWithConfig(approvals, logger, defaultApprovalCleanupInterval, DefaultApprovalTimeout)
}

func newApprovalCleanupRunnerWithConfig(approvals *ApprovalManager, logger *pkglogger.Logger, interval, timeout time.Duration) *ApprovalCleanupRunner {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &ApprovalCleanupRunner{
		approvals: approvals,
		logger:    logger,
		interval:  interval,
		timeout:   timeout,
	}
}

// Run implements platformrunner.Runner. Blocks on the cleanup ticker until
// ctx.Done; returns ctx.Err(). The timeout is captured on the runner instance
// at construction time so parallel tests do not race on package-level defaults.
// Run 启动平台RPC后台流程。
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
	timeout := r.timeout
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
