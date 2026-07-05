package runner

import (
	"context"
	"errors"
	"testing"
	"time"
)

type delayedCancelErrorRunner struct {
	started chan struct{}
	err     error
	delay   time.Duration
}

func (r delayedCancelErrorRunner) Run(ctx context.Context) error {
	close(r.started)
	<-ctx.Done()
	time.Sleep(r.delay)
	return r.err
}

func TestRunGroupCancellationReturnsRunnerError(t *testing.T) {
	wantErr := errors.New("final flush failed")
	runner := delayedCancelErrorRunner{
		started: make(chan struct{}),
		err:     wantErr,
		delay:   50 * time.Millisecond,
	}
	cancel, done := startRunnerForTest(t, func(ctx context.Context) error {
		return RunGroup(ctx, []Runner{runner}, GroupOptions{})
	})
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, wantErr) {
			t.Fatalf("RunGroup() error = %v, want %v", err, wantErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunGroup() did not return")
	}
}
