package mcpcontrol

import (
	"context"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

// SweeperRunner 把 Sweeper 接入 platformrunner.Runner。
// 它同步调用 sweeper.Run(ctx)，不再内部开 goroutine；扫描循环的所有权交给 root run.Group。
type SweeperRunner struct {
	sweeper *Sweeper
}

// NewSweeperRunner 构造 runner，并以 platformrunner.Runner 形式供 fx group 注入。
func NewSweeperRunner(sweeper *Sweeper) platformrunner.Runner {
	return &SweeperRunner{sweeper: sweeper}
}

// Run 阻塞到 ctx 取消或 sweeper 返回；nil sweeper 也等待 ctx 以保持 run.Group 停止语义。
func (r *SweeperRunner) Run(ctx context.Context) error {
	if r == nil || r.sweeper == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	r.sweeper.Run(ctx)
	return ctx.Err()
}
