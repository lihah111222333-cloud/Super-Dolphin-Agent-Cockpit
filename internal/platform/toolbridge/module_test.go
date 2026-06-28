package toolbridge

import (
	"context"
	"strings"
	"testing"

	"github.com/kelindar/event"
	"go.uber.org/fx"
)

type recordingLifecycle struct {
	hooks []fx.Hook
}

func (l *recordingLifecycle) Append(hook fx.Hook) {
	l.hooks = append(l.hooks, hook)
}

func TestProvideDiffEmitterRejectsNilDispatcher(t *testing.T) {
	t.Parallel()

	emitter, err := provideDiffEmitter(nil)
	if err == nil || !strings.Contains(err.Error(), "nil dispatcher") {
		t.Fatalf("provideDiffEmitter() error = %v, want nil dispatcher", err)
	}
	if emitter != nil {
		t.Fatalf("provideDiffEmitter() = %#v, want nil emitter on invalid deps", emitter)
	}
}

func TestProvideDiffEmitterAcceptsDispatcher(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() {
		_ = dispatcher.Close()
	})

	emitter, err := provideDiffEmitter(dispatcher)
	if err != nil {
		t.Fatalf("provideDiffEmitter() error = %v", err)
	}
	if emitter == nil {
		t.Fatal("provideDiffEmitter() = nil, want emitter")
	}
}

func TestRegisterProxyLifecycleRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	handler := &Handler{}
	runner := NewProxyRunner(handler)
	lifecycle := &recordingLifecycle{}

	tests := []struct {
		name      string
		lifecycle fx.Lifecycle
		handler   *Handler
		runner    *ProxyRunner
		want      string
	}{
		{
			name:      "nil lifecycle",
			lifecycle: nil,
			handler:   handler,
			runner:    runner,
			want:      "nil lifecycle",
		},
		{
			name:      "nil handler",
			lifecycle: lifecycle,
			handler:   nil,
			runner:    runner,
			want:      "nil handler",
		},
		{
			name:      "nil runner",
			lifecycle: lifecycle,
			handler:   handler,
			runner:    nil,
			want:      "nil proxy runner",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := registerProxyLifecycle(tt.lifecycle, tt.handler, tt.runner)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("registerProxyLifecycle() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestRegisterProxyLifecycleOnStartPublishesAddrAndListener(t *testing.T) {
	proxyAddr.Store("")
	t.Cleanup(func() {
		proxyAddr.Store("")
	})

	handler := &Handler{}
	runner := NewProxyRunner(handler)
	lifecycle := &recordingLifecycle{}
	if err := registerProxyLifecycle(lifecycle, handler, runner); err != nil {
		t.Fatalf("registerProxyLifecycle() error = %v", err)
	}
	if len(lifecycle.hooks) != 1 {
		t.Fatalf("lifecycle hooks = %d, want 1", len(lifecycle.hooks))
	}

	if err := lifecycle.hooks[0].OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart() error = %v", err)
	}
	runner.mu.Lock()
	listener := runner.listener
	runner.mu.Unlock()
	if listener == nil {
		t.Fatal("runner listener = nil, want listener from OnStart")
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})
	if got, _ := proxyAddr.Load().(string); strings.TrimSpace(got) == "" {
		t.Fatalf("proxyAddr = %q, want published address", got)
	}
	if err := lifecycle.hooks[0].OnStop(context.Background()); err != nil {
		t.Fatalf("OnStop() error = %v", err)
	}
	if got, _ := proxyAddr.Load().(string); got != "" {
		t.Fatalf("proxyAddr after OnStop = %q, want empty", got)
	}
}
