package runner

import (
	"context"
	"errors"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

// workerRunnerShutdownGrace 限制 WorkerRunner 传给 Stop 的独立关闭时间。
const workerRunnerShutdownGrace = 10 * time.Second

// Contract 是 RunnerModule 安装的零状态标记，用于表达平台 runner 能力已装配。
type Contract struct{}

// NewContract 创建 runner 模块标记对象。
func NewContract() Contract { return Contract{} }

// Worker 是可由 runner 适配的后台组件最小接口。
type Worker interface {
	Start()
	Stop(context.Context) error
}

// WorkerRunnerOption 调整 workerRunner 的测试或生命周期选项。
type WorkerRunnerOption func(*workerRunner)

// workerRunner 把 Start/Stop 风格 worker 适配为 Runner 接口。
type workerRunner struct {
	worker Worker
	ready  chan struct{}
}

// AsRunner 将 Worker 适配为 Runner，交给 run group 统一托管。
func AsRunner(worker Worker, opts ...WorkerRunnerOption) Runner {
	r := &workerRunner{worker: worker, ready: make(chan struct{})}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

// WithStartedSignal 替换 runner 启动完成信号通道，主要供测试同步使用。
func WithStartedSignal(ch chan struct{}) WorkerRunnerOption {
	return func(r *workerRunner) {
		if ch != nil {
			r.ready = ch
		}
	}
}

// Run 启动 worker，发出 ready 信号后阻塞等待 ctx 取消，再调用 Stop。
func (r *workerRunner) Run(ctx context.Context) error {
	if r == nil || r.worker == nil {
		return errors.New("runner worker is nil")
	}
	r.worker.Start()
	closeOnce(r.ready)
	<-ctx.Done()
	shutdownCtx, cancel := platformconfig.WithTimeout(context.WithoutCancel(ctx), workerRunnerShutdownGrace)
	defer cancel()
	if err := r.worker.Stop(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// closeOnce 关闭 ready 通道，通道已关闭时吞掉 panic 以保持 runner 停止幂等。
func closeOnce(ch chan struct{}) {
	defer func() { _ = recover() }()
	close(ch)
}
