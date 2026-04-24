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

func TestBindRuntimeCancelsRunGroupBeforeDrain(t *testing.T) {
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
	if len(lifecycle.hooks) != 1 {
		t.Fatalf("len(hooks) = %d, want 1", len(lifecycle.hooks))
	}
	hook := lifecycle.hooks[0]
	if err := hook.OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart() error = %v", err)
	}

	stopDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		stopDone <- hook.OnStop(ctx)
	}()

	select {
	case <-runner.canceled:
	case <-time.After(time.Second):
		t.Fatal("runner was not canceled before drain")
	}
	select {
	case <-drainer.started:
	case <-time.After(time.Second):
		t.Fatal("DrainPendingExtraction() was not called after root cancel")
	}

	select {
	case err := <-stopDone:
		t.Fatalf("OnStop() finished before RunGroup completed: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	close(drainer.release)
	close(runner.release)
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("OnStop() error = %v", err)
		}
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
