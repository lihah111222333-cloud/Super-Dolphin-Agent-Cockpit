package wails

import (
	"context"
	"errors"
	"strings"
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

func TestNewActiveAgentCounterFailsFastWithoutOrchestrationService(t *testing.T) {
	t.Parallel()

	counter := NewActiveAgentCounter(activeAgentCounterParams{})
	count, err := counter.ActiveAgentCount(context.Background())
	if err == nil || !strings.Contains(err.Error(), "orchestration service is not configured") {
		t.Fatalf("ActiveAgentCount() error = %v, want missing orchestration service", err)
	}
	if count != 0 {
		t.Fatalf("ActiveAgentCount() count = %d, want 0 with error", count)
	}
}

func TestShouldQuitBlocksWhenActiveAgentCountFails(t *testing.T) {
	t.Parallel()

	counterErr := errors.New("count failed")
	lifecycle := NewWailsLifecycle(ActiveAgentCounterFunc(func(context.Context) (int, error) {
		return 0, counterErr
	}), nil)
	lifecycle.MarkFrontendReady()

	shutdownCalled := false
	lifecycle.SetShutdownerFunc(func() {
		shutdownCalled = true
	})

	var emittedName string
	var emittedPayload any
	lifecycle.SetEventEmitter(func(name string, payload any) {
		emittedName = name
		emittedPayload = payload
	})

	if lifecycle.ShouldQuit() {
		t.Fatal("ShouldQuit() = true, want quit blocked on counter error")
	}
	if shutdownCalled {
		t.Fatal("shutdown was requested despite active-agent counter error")
	}
	if emittedName != "app-quit-error" {
		t.Fatalf("emitted event = %q/%#v, want app-quit-error", emittedName, emittedPayload)
	}
}
