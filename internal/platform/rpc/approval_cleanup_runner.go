package rpc

import (
	"context"
	"time"

	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// ApprovalCleanupRunner 把审批超时清理 ticker 托管为 platformrunner.Runner。
// 启动恢复仍归 bindApprovalLifecycle 负责；这里仅拥有长期运行的定时清理循环。
type ApprovalCleanupRunner struct {
	approvals *ApprovalManager
	logger    *pkglogger.Logger
	interval  time.Duration
	timeout   time.Duration
}

// NewApprovalCleanupRunner 是 fx 构造入口，返回 Runner 以便挂入根 run group。
// ApprovalManager 可在部分测试装配中为空，Run 会只等待 ctx 结束而不执行清理。
func NewApprovalCleanupRunner(approvals *ApprovalManager, logger *pkglogger.Logger) platformrunner.Runner {
	return newApprovalCleanupRunnerWithConfig(approvals, logger, defaultApprovalCleanupInterval, DefaultApprovalTimeout)
}

// newApprovalCleanupRunnerWithConfig 构造可注入 interval/timeout 的清理 runner。
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

// Run 启动审批清理循环，直到 ctx 取消后返回 ctx.Err。
// timeout 在构造时固定，避免并行测试修改包级默认值造成竞态。
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

// tick 执行一次超时审批清理，并在数量减少时记录告警。
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
