package orchestration

import (
	"context"
	"errors"
	"time"
)

// Scheduler 是 cron 调度器接口（骨架阶段 stub） —— 蓝图 v2 §11 阶段 F5
// + 实施计划 S2.3。F5.1-F5.3 真实实现：cron daemon 进程 + tick 扫
// next_run_at + 多实例锁。
type Scheduler interface {
	// Tick 在 now 时间扫描所有 next_run_at <= now 的 trigger=scheduled DAG，
	// 对每个调用 service.StartDAG。返回触发的 DAG 数量。
	Tick(ctx context.Context, now time.Time) (int, error)

	// Schedule 把一个 DAG 的下次触发时间写入 next_run_at（基于 cron_expr）。
	// 用于 DAG 创建/编辑后刷新调度；F5.1 真实实现。
	Schedule(ctx context.Context, dagKey string) error
}

// ErrSchedulerNotImplemented 是骨架阶段 stub 方法的 sentinel 错误。
var ErrSchedulerNotImplemented = errors.New("scheduler: not implemented in skeleton stage (F5.x)")

// noopScheduler 是骨架阶段的 stub 实现，所有方法返回 ErrSchedulerNotImplemented。
type noopScheduler struct{}

func (noopScheduler) Tick(_ context.Context, _ time.Time) (int, error) {
	return 0, ErrSchedulerNotImplemented
}

func (noopScheduler) Schedule(_ context.Context, _ string) error {
	return ErrSchedulerNotImplemented
}

// NewNoopScheduler 返回骨架阶段的 stub Scheduler。
// 生产路径在 F5.1 用 cron daemon 实现替换。
func NewNoopScheduler() Scheduler {
	return noopScheduler{}
}
