package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	"go.uber.org/fx"
)

type testLifecycle struct {
	hook fx.Hook
}

func (l *testLifecycle) Append(hook fx.Hook) {
	l.hook = hook
}

type testShutdowner struct {
	once   sync.Once
	called chan struct{}
}

func (s *testShutdowner) Shutdown(...fx.ShutdownOption) error {
	s.once.Do(func() {
		close(s.called)
	})
	return nil
}

type fatalRuntimeRunner struct {
	err error
}

func (r fatalRuntimeRunner) Run(context.Context) error {
	return r.err
}

func TestBindRuntimeReturnsRunnerFatalErrorOnStop(t *testing.T) {
	fatalErr := errors.New("orch runner fatal")
	lifecycle := &testLifecycle{}
	shutdowner := &testShutdowner{called: make(chan struct{})}
	bindRuntime(lifecycle, runtimeParams{
		Runners:    []platformrunner.Runner{fatalRuntimeRunner{err: fatalErr}},
		Shutdowner: shutdowner,
	})
	if lifecycle.hook.OnStart == nil || lifecycle.hook.OnStop == nil {
		t.Fatalf("bindRuntime() hook = %#v, want start and stop callbacks", lifecycle.hook)
	}

	if err := lifecycle.hook.OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart() error = %v", err)
	}
	waitForRuntimeShutdown(t, shutdowner.called)

	err := lifecycle.hook.OnStop(context.Background())
	if !errors.Is(err, fatalErr) {
		t.Fatalf("OnStop() error = %v, want runner fatal %v", err, fatalErr)
	}
}

func waitForRuntimeShutdown(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("runtime shutdown was not requested")
	}
}
