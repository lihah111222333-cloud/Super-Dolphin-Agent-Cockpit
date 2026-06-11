package wails

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type stubLifecycleTimer struct {
	stopped bool
}

func (t *stubLifecycleTimer) Stop() bool {
	t.stopped = true
	return true
}

func TestRequestBackendShutdownArmsHardDeadline(t *testing.T) {
	var (
		scheduledDelay time.Duration
		scheduledFn    func()
		timer          = &stubLifecycleTimer{}
	)

	lifecycle := NewWailsLifecycle(nil, nil)
	lifecycle.afterFunc = func(delay time.Duration, fn func()) lifecycleTimer {
		scheduledDelay = delay
		scheduledFn = fn
		return timer
	}
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

func TestNewActiveAgentCounterFailsFastWithoutActiveAgentSource(t *testing.T) {
	t.Parallel()

	counter := NewActiveAgentCounter(activeAgentCounterParams{})
	count, err := counter.ActiveAgentCount(context.Background())
	if err == nil || !strings.Contains(err.Error(), "active agent source is not configured") {
		t.Fatalf("ActiveAgentCount() error = %v, want missing active agent source", err)
	}
	if count != 0 {
		t.Fatalf("ActiveAgentCount() count = %d, want 0 with error", count)
	}
}

func TestNewActiveAgentCounterUsesThreadListerWithoutOrchestrationService(t *testing.T) {
	t.Parallel()

	counter := NewActiveAgentCounter(activeAgentCounterParams{
		Threads: stubThreadLister{refs: []contract.ThreadRef{
			{ID: "created", Status: "created"},
			{ID: "stopped", Status: "stopped"},
			{ID: "archived", Status: "archived"},
			{ID: "failed", Status: "failed"},
			{ID: "empty"},
		}},
	})

	count, err := counter.ActiveAgentCount(context.Background())
	if err != nil {
		t.Fatalf("ActiveAgentCount() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("ActiveAgentCount() count = %d, want 1", count)
	}
}

func TestShouldQuitStartsShutdownWhenActiveAgentCountFails(t *testing.T) {
	t.Parallel()

	counterErr := errors.New("count failed")
	lifecycle := NewWailsLifecycle(ActiveAgentCounterFunc(func(context.Context) (int, error) {
		return 0, counterErr
	}), nil)
	lifecycle.MarkFrontendReady()

	shutdownCalled := make(chan struct{}, 1)
	lifecycle.SetShutdownerFunc(func() {
		shutdownCalled <- struct{}{}
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
	select {
	case <-shutdownCalled:
	case <-time.After(time.Second):
		t.Fatal("shutdown was not requested after active-agent counter error")
	}
	if emittedName != "app-quit-error" {
		t.Fatalf("emitted event = %q/%#v, want app-quit-error", emittedName, emittedPayload)
	}
}

func TestShouldQuitWaitsForBackendStopWithoutActiveAgents(t *testing.T) {
	t.Parallel()

	lifecycle := NewWailsLifecycle(ActiveAgentCounterFunc(func(context.Context) (int, error) {
		return 0, nil
	}), nil)
	lifecycle.MarkFrontendReady()

	shutdownCalled := make(chan struct{}, 1)
	lifecycle.SetShutdownerFunc(func() {
		shutdownCalled <- struct{}{}
	})
	quitCalled := make(chan struct{}, 1)
	lifecycle.SetQuitFunc(func() {
		quitCalled <- struct{}{}
	})

	if lifecycle.ShouldQuit() {
		t.Fatal("ShouldQuit() = true without active agents, want false until backend shutdown completes")
	}
	select {
	case <-shutdownCalled:
	case <-time.After(time.Second):
		t.Fatal("shutdown was not requested before allowing quit")
	}
	select {
	case <-quitCalled:
		t.Fatal("quit was invoked before backend shutdown completed")
	default:
	}
	if lifecycle.ShouldQuit() {
		t.Fatal("second ShouldQuit() = true before backend stop, want false")
	}

	lifecycle.NotifyBackendStopped()

	select {
	case <-quitCalled:
	case <-time.After(time.Second):
		t.Fatal("quit was not invoked after backend shutdown completed")
	}
	if !lifecycle.ShouldQuit() {
		t.Fatal("ShouldQuit() = false after backend stop, want true")
	}
}

func TestRequestQuitAllowsImmediateQuit(t *testing.T) {
	t.Parallel()

	lifecycle := NewWailsLifecycle(nil, nil)
	lifecycle.MarkFrontendReady()
	quitCalled := make(chan struct{}, 1)
	lifecycle.SetQuitFunc(func() {
		quitCalled <- struct{}{}
	})

	lifecycle.RequestQuit()

	select {
	case <-quitCalled:
	case <-time.After(time.Second):
		t.Fatal("RequestQuit() did not invoke quit function")
	}
	if !lifecycle.ShouldQuit() {
		t.Fatal("ShouldQuit() = false after RequestQuit, want true")
	}
}

type stubThreadLister struct {
	refs []contract.ThreadRef
	err  error
}

func (s stubThreadLister) List(context.Context) ([]contract.ThreadRef, error) {
	return s.refs, s.err
}
