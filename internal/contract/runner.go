package contract

import (
	"context"
	"errors"
)

// Runner 是根 run.Group 管理后台任务时使用的契约接口。
// 业务模块依赖该接口而不是 platform/runner 实现包，保持跨层引用方向稳定。
type Runner interface {
	Run(ctx context.Context) error
}

// Worker 是可启动/停止的后台组件，AsRunner 会把它适配为阻塞式 Runner。
type Worker interface {
	Start()
	Stop(context.Context) error
}

// WorkerRunnerOption 调整 AsRunner 创建的 workerRunner，例如注入启动完成信号。
type WorkerRunnerOption func(*workerRunner)

// workerRunner 把 Start/Stop 生命周期接到 context 取消信号上。
type workerRunner struct {
	worker Worker
	ready  chan struct{}
}

// AsRunner 将 Worker 适配为 Runner。
// Run 会先调用 Start，随后阻塞等待 ctx 取消，再调用 Stop 完成资源清理。
func AsRunner(worker Worker, opts ...WorkerRunnerOption) Runner {
	r := &workerRunner{worker: worker, ready: make(chan struct{})}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

// WithStartedSignal 设置启动完成信号；Start 返回后该 channel 会被关闭一次。
func WithStartedSignal(ch chan struct{}) WorkerRunnerOption {
	return func(r *workerRunner) {
		if ch != nil {
			r.ready = ch
		}
	}
}

// Run 启动 Worker 并在 context 取消后停止它。
// Stop 返回 context.Canceled 视为正常取消，其余错误会向 run.Group 传播。
func (r *workerRunner) Run(ctx context.Context) error {
	if r == nil || r.worker == nil {
		return errors.New("runner worker is nil")
	}
	r.worker.Start()
	closeOnce(r.ready)
	<-ctx.Done()
	if err := r.worker.Stop(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// closeOnce 关闭启动信号，已关闭的 channel 会被恢复路径吞掉以保持 Stop 路径幂等。
func closeOnce(ch chan struct{}) {
	defer func() { _ = recover() }()
	close(ch)
}
