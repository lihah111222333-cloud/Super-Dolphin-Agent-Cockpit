package wails

import (
	"testing"
	"time"
)

type stubLifecycleTimer struct {
	stopped bool
}

func (t *stubLifecycleTimer) Stop() bool {
	t.stopped = true
	return true
}

func TestRequestBackendShutdownArmsHardDeadline(t *testing.T) {
	oldAfterFunc := lifecycleAfterFunc
	defer func() { lifecycleAfterFunc = oldAfterFunc }()

	var (
		scheduledDelay time.Duration
		scheduledFn    func()
		timer          = &stubLifecycleTimer{}
	)
	lifecycleAfterFunc = func(delay time.Duration, fn func()) lifecycleTimer {
		scheduledDelay = delay
		scheduledFn = fn
		return timer
	}

	lifecycle := NewWailsLifecycle(nil, nil)
	lifecycle.MarkFrontendReady()

	shutdownCalled := make(chan struct{}, 1)
	lifecycle.SetShutdownerFunc(func() {
		shutdownCalled <- struct{}{}
	})

	quitCalled := make(chan struct{}, 1)
	lifecycle.SetQuitFunc(func() {
		quitCalled <- struct{}{}
	})

	lifecycle.requestBackendShutdown()

	select {
	case <-shutdownCalled:
	case <-time.After(time.Second):
		t.Fatal("expected shutdowner to be invoked")
	}

	if scheduledFn == nil {
		t.Fatal("expected shutdown hard deadline to be scheduled")
	}
	if scheduledDelay != shutdownHardDeadline {
		t.Fatalf("scheduled delay = %s, want %s", scheduledDelay, shutdownHardDeadline)
	}

	scheduledFn()

	select {
	case <-quitCalled:
	case <-time.After(time.Second):
		t.Fatal("expected hard deadline to force quit")
	}

	if !timer.stopped {
		t.Fatal("expected shutdown timer to be stopped after forced quit")
	}
}
