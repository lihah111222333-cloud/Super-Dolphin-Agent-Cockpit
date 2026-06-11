package runner

import (
	"context"
	"reflect"
	"testing"
	"time"
)

type fakeWorker struct{ calls []string }

func (w *fakeWorker) Start() { w.calls = append(w.calls, "start") }
func (w *fakeWorker) Stop(context.Context) error {
	w.calls = append(w.calls, "stop")
	return nil
}

func TestWorkerAsRunnerAdapter(t *testing.T) {
	worker := &fakeWorker{}
	started := make(chan struct{})
	runner := AsRunner(worker, WithStartedSignal(started))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
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
