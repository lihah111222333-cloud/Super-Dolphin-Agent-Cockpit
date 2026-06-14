package runner

import (
	"context"
	"errors"
)

// Contract is a zero-state marker installed by RunnerModule.
type Contract struct{}

// NewContract 创建contract。
func NewContract() Contract { return Contract{} }

type Worker interface {
	Start()
	Stop(context.Context) error
}

type WorkerRunnerOption func(*workerRunner)

type workerRunner struct {
	worker Worker
	ready  chan struct{}
}

// AsRunner 把平台runner处理为runner。
func AsRunner(worker Worker, opts ...WorkerRunnerOption) Runner {
	r := &workerRunner{worker: worker, ready: make(chan struct{})}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

// WithStartedSignal 设置startedsignal。
func WithStartedSignal(ch chan struct{}) WorkerRunnerOption {
	return func(r *workerRunner) {
		if ch != nil {
			r.ready = ch
		}
	}
}

// Run 启动平台runner后台流程。
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
