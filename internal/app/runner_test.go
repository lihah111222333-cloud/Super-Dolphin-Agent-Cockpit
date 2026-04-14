package app

import (
	"context"
	"errors"
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
}

func (r runtimeBlockRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	close(r.canceled)
	return ctx.Err()
}

func TestBindRuntimeDrainsExtractionBeforeCancel(t *testing.T) {
	lifecycle := &runtimeTestLifecycle{}
	drainer := runtimeDrainStub{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	runner := runtimeBlockRunner{canceled: make(chan struct{})}

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
	case <-drainer.started:
	case <-time.After(time.Second):
		t.Fatal("DrainPendingExtraction() was not called")
	}
	select {
	case <-runner.canceled:
		t.Fatal("runner canceled before drain completed")
	case <-time.After(150 * time.Millisecond):
	}

	close(drainer.release)
	select {
	case err := <-stopDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("OnStop() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnStop() did not finish")
	}
}
