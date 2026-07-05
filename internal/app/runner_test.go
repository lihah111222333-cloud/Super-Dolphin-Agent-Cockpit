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

func TestRegisterRuntimePreDrainReturnsErrorOnDuplicate(t *testing.T) {
	owner := newAppOwnerContext(context.Background())
	drain := func(context.Context) error { return nil }
	requireRuntimeNoError(t, "RegisterRuntimePreDrain()", owner.RegisterRuntimePreDrain(drain))

	err := owner.RegisterRuntimePreDrain(drain)
	if err == nil {
		t.Fatal("second RegisterRuntimePreDrain() error = nil, want duplicate registration error")
	}
	if got, want := err.Error(), "app: runtime pre-drain already registered"; got != want {
		t.Fatalf("second RegisterRuntimePreDrain() error = %q, want %q", got, want)
	}
}

func TestRegisterRuntimePreDrainRejectsNilOwner(t *testing.T) {
	var owner *appOwnerContext
	err := owner.RegisterRuntimePreDrain(func(context.Context) error { return nil })
	if err == nil {
		t.Fatal("nil owner RegisterRuntimePreDrain() error = nil, want owner error")
	}
	if got, want := err.Error(), "app: runtime pre-drain owner is nil"; got != want {
		t.Fatalf("nil owner RegisterRuntimePreDrain() error = %q, want %q", got, want)
	}
}

func TestRegisterRuntimePreDrainRejectsNilFunction(t *testing.T) {
	owner := newAppOwnerContext(context.Background())
	err := owner.RegisterRuntimePreDrain(nil)
	if err == nil {
		t.Fatal("nil function RegisterRuntimePreDrain() error = nil, want function error")
	}
	if got, want := err.Error(), "app: runtime pre-drain function is nil"; got != want {
		t.Fatalf("nil function RegisterRuntimePreDrain() error = %q, want %q", got, want)
	}
}

func TestBindRuntimeRequiresRuntimePreDrainRegistrar(t *testing.T) {
	drainer := runtimeDrainStub{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	err := BindRuntime(&runtimeTestLifecycle{}, runtimeParams{
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Shutdowner:        runtimeTestShutdowner{},
		ExtractionDrainer: drainer,
	})
	if err == nil {
		t.Fatal("BindRuntime() without registrar error = nil, want registrar error")
	}
	if got, want := err.Error(), "app: runtime pre-drain registrar is required"; got != want {
		t.Fatalf("BindRuntime() without registrar error = %q, want %q", got, want)
	}
}

func TestBindRuntimeRequiresExtractionDrainer(t *testing.T) {
	owner := newAppOwnerContext(context.Background())
	err := BindRuntime(&runtimeTestLifecycle{}, runtimeParams{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Shutdowner: runtimeTestShutdowner{},
		RootCtx:    owner,
	})
	if err == nil {
		t.Fatal("BindRuntime() without extraction drainer error = nil, want drainer error")
	}
	if got, want := err.Error(), "app: extraction drainer is required"; got != want {
		t.Fatalf("BindRuntime() without extraction drainer error = %q, want %q", got, want)
	}
}

func TestBindRuntimeWaitsRunGroupBeforeDrain(t *testing.T) {
	lifecycle := &runtimeTestLifecycle{}
	owner := newAppOwnerContext(context.Background())
	drainer := runtimeDrainStub{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	runner := runtimeBlockRunner{
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}

	requireRuntimeNoError(t, "BindRuntime()", BindRuntime(lifecycle, runtimeParams{
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Runners:           []platformrunner.Runner{runner},
		Shutdowner:        runtimeTestShutdowner{},
		RootCtx:           owner,
		ExtractionDrainer: drainer,
	}))
	hook := singleRuntimeHook(t, lifecycle)
	requireRuntimeNoError(t, "OnStart()", hook.OnStart(context.Background()))

	stopDone := startAppErrorGoroutineForTest(t, "runtime stop", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return hook.OnStop(ctx)
	})

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
	drainer := runtimeDrainStub{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	close(drainer.release)
	runner := runtimeBlockRunner{canceled: make(chan struct{})}

	requireRuntimeNoError(t, "BindRuntime()", BindRuntime(lifecycle, runtimeParams{
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Runners:           []platformrunner.Runner{runner},
		Shutdowner:        runtimeTestShutdowner{},
		RootCtx:           owner,
		ExtractionDrainer: drainer,
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
