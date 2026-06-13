package contract

import (
	"context"
	"errors"
)

// Runner is the contract-level interface for long-running goroutines
// managed by the root run.Group. Module code should depend on this
// interface rather than the platform/runner implementation package.
type Runner interface {
	Run(ctx context.Context) error
}

// Worker is the start/stop pair that AsRunner adapts into a Runner.
type Worker interface {
	Start()
	Stop(context.Context) error
}

// WorkerRunnerOption configures the workerRunner returned by AsRunner.
type WorkerRunnerOption func(*workerRunner)

type workerRunner struct {
	worker Worker
	ready  chan struct{}
}

// AsRunner wraps a Worker into a Runner whose Run blocks on ctx, calling
// Start immediately and Stop when the context fires.
// AsRunner 把跨模块契约处理为runner。
func AsRunner(worker Worker, opts ...WorkerRunnerOption) Runner {
	r := &workerRunner{worker: worker, ready: make(chan struct{})}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

// WithStartedSignal returns an option that closes ch after Start returns.
// WithStartedSignal 设置startedsignal。
func WithStartedSignal(ch chan struct{}) WorkerRunnerOption {
	return func(r *workerRunner) {
		if ch != nil {
			r.ready = ch
		}
	}
}

// Run 启动跨模块契约后台流程。
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

func closeOnce(ch chan struct{}) {
	defer func() { _ = recover() }()
	close(ch)
}
