package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
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

type runtimeDrainStub struct {
	started chan struct{}
	release chan struct{}
}

func (d runtimeDrainStub) DrainPendingExtraction(context.Context) error {
	close(d.started)
	<-d.release
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

func TestRegisterRuntimePreDrainPanicsOnDuplicate(t *testing.T) {
	owner := newAppOwnerContext(context.Background())
	drain := func(context.Context) error { return nil }
	owner.RegisterRuntimePreDrain(drain)

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("second RegisterRuntimePreDrain() did not panic")
		}
		if recovered != "app: runtime pre-drain already registered" {
			t.Fatalf("panic = %v, want app: runtime pre-drain already registered", recovered)
		}
	}()
	owner.RegisterRuntimePreDrain(drain)
}

func TestBindRuntimeWaitsRunGroupBeforeDrain(t *testing.T) {
	lifecycle := &runtimeTestLifecycle{}
	drainer := runtimeDrainStub{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	runner := runtimeBlockRunner{
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}

	BindRuntime(lifecycle, runtimeParams{
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Runners:           []platformrunner.Runner{runner},
		Shutdowner:        runtimeTestShutdowner{},
		ExtractionDrainer: drainer,
	})
	hook := singleRuntimeHook(t, lifecycle)
	requireRuntimeNoError(t, "OnStart()", hook.OnStart(context.Background()))

	stopDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		stopDone <- hook.OnStop(ctx)
	}()

	waitRuntimeSignal(t, runner.canceled, time.Second, "runner was not canceled before RunGroup wait")
	assertRuntimeSignalBlocked(t, drainer.started, 150*time.Millisecond, "DrainPendingExtraction() started before RunGroup completed")

	close(runner.release)
	waitRuntimeSignal(t, drainer.started, time.Second, "DrainPendingExtraction() was not called after RunGroup completed")

	close(drainer.release)
	waitRuntimeStop(t, stopDone)
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

func assertRuntimeSignalBlocked(t *testing.T, signal <-chan struct{}, timeout time.Duration, failMessage string) {
	t.Helper()
	select {
	case <-signal:
		t.Fatal(failMessage)
	case <-time.After(timeout):
	}
}

func waitRuntimeStop(t *testing.T, stopDone <-chan error) {
	t.Helper()
	select {
	case err := <-stopDone:
		requireRuntimeNoError(t, "OnStop()", err)
	case <-time.After(2 * time.Second):
		t.Fatal("OnStop() did not finish")
	}
}

func TestBindRuntimeInheritsRootContext(t *testing.T) {
	lifecycle := &runtimeTestLifecycle{}
	owner := newAppOwnerContext(context.Background())
	runner := runtimeBlockRunner{canceled: make(chan struct{})}

	BindRuntime(lifecycle, runtimeParams{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Runners:    []platformrunner.Runner{runner},
		Shutdowner: runtimeTestShutdowner{},
		RootCtx:    owner,
	})
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
