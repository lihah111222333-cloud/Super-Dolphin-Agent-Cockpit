package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	"go.uber.org/fx"
)

type runtimeTestLifecycle struct {
	hooks []fx.Hook
}

func (l *runtimeTestLifecycle) Append(hook fx.Hook) {
	l.hooks = append(l.hooks, hook)
}

type runtimeTestShutdowner struct{}

func (runtimeTestShutdowner) Shutdown(...fx.ShutdownOption) error {
	return nil
}

type runtimeBlockRunner struct {
	canceled chan struct{}
	release  chan struct{}
}

func (r runtimeBlockRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	close(r.canceled)
	if r.release != nil {
		<-r.release
	}
	return ctx.Err()
}

func singleRuntimeHook(t *testing.T, lifecycle *runtimeTestLifecycle) fx.Hook {
	t.Helper()
	if len(lifecycle.hooks) != 1 {
		t.Fatalf("len(hooks) = %d, want 1", len(lifecycle.hooks))
	}
	return lifecycle.hooks[0]
}

func requireRuntimeNoError(t *testing.T, label string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s error = %v", label, err)
	}
}

func waitRuntimeSignal(t *testing.T, signal <-chan struct{}, timeout time.Duration, timeoutMessage string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(timeout):
		t.Fatal(timeoutMessage)
	}
}

func TestBindRuntimeInheritsRootContext(t *testing.T) {
	lifecycle := &runtimeTestLifecycle{}
	owner := newAppOwnerContext(context.Background())
	runner := runtimeBlockRunner{canceled: make(chan struct{})}

	requireRuntimeNoError(t, "BindRuntime()", BindRuntime(lifecycle, runtimeParams{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Runners:    []platformrunner.Runner{runner},
		Shutdowner: runtimeTestShutdowner{},
		RootCtx:    owner,
	}))
	if len(lifecycle.hooks) != 1 {
		t.Fatalf("len(hooks) = %d, want 1", len(lifecycle.hooks))
	}
	if err := lifecycle.hooks[0].OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart() error = %v", err)
	}

	owner.Cancel()
	select {
	case <-runner.canceled:
	case <-time.After(time.Second):
		t.Fatal("runner did not observe owner root context cancellation")
	}
	select {
	case <-owner.runtimeDone:
	case <-time.After(time.Second):
		t.Fatal("owner was not marked runtime done after root context cancellation")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := lifecycle.hooks[0].OnStop(ctx); err != nil {
		t.Fatalf("OnStop() error = %v", err)
	}
}
