package runner

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type fakeWorker struct{ calls []string }

func startRunnerForTest(t *testing.T, run func(context.Context) error) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		defer close(finished)
		done <- run(ctx)
	})
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
			wg.Wait()
		case <-time.After(time.Second):
			t.Fatal("runner goroutine did not stop")
		}
	})
	return cancel, done
}

func (w *fakeWorker) Start() { w.calls = append(w.calls, "start") }
func (w *fakeWorker) Stop(context.Context) error {
	w.calls = append(w.calls, "stop")
	return nil
}

func TestWorkerAsRunnerAdapter(t *testing.T) {
	worker := &fakeWorker{}
	started := make(chan struct{})
	runner := AsRunner(worker, WithStartedSignal(started))
	cancel, done := startRunnerForTest(t, runner.Run)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := []string{"start", "stop"}; !reflect.DeepEqual(worker.calls, want) {
		t.Fatalf("calls = %v, want %v", worker.calls, want)
	}
}

type shutdownProbeWorker struct {
	stopCtxErr error
	started    bool
}

func (w *shutdownProbeWorker) Start() { w.started = true }

func (w *shutdownProbeWorker) Stop(ctx context.Context) error {
	w.stopCtxErr = ctx.Err()
	if w.stopCtxErr != nil {
		return w.stopCtxErr
	}
	return context.DeadlineExceeded
}

func TestWorkerRunnerStopUsesFreshShutdownContext(t *testing.T) {
	worker := &shutdownProbeWorker{}
	started := make(chan struct{})
	runner := AsRunner(worker, WithStartedSignal(started))
	cancel, done := startRunnerForTest(t, runner.Run)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	if !worker.started {
		t.Fatal("worker Start() was not called before started signal")
	}
	cancel()
	err := <-done
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context.DeadlineExceeded from worker Stop", err)
	}
	if worker.stopCtxErr != nil {
		t.Fatalf("Stop ctx err at entry = %v, want nil fresh shutdown context", worker.stopCtxErr)
	}
}
