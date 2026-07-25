package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
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
	fatalErr := errors.New("lsp runner fatal")
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

type stubScopeReleaser struct {
	result multilsp.ReleaseScopeResult
}

func (s stubScopeReleaser) ReleaseScope(multilsp.ReleaseScopeRequest) (multilsp.ReleaseScopeResult, error) {
	return s.result, nil
}

func TestManagerReleaseScopeDoesNotReportDrainedWhenAnyLanguagePoolIsBusy(t *testing.T) {
	manager := &Manager{
		releaseScopes: []multilsp.ScopeReleaser{
			stubScopeReleaser{result: multilsp.ReleaseScopeResult{Drained: true}},
			stubScopeReleaser{result: multilsp.ReleaseScopeResult{MatchedManagers: 1, BusyLeases: 1}},
		},
	}

	result, err := manager.ReleaseScope(mcpdto.LSPReleaseScopeRequest{
		ScopeKind: mcpdto.LSPReleaseScopeAgentThread,
		AgentID:   "agent-busy",
		ThreadID:  "thread-1",
		Drain:     true,
	})
	if err != nil {
		t.Fatalf("ReleaseScope(): %v", err)
	}
	if result.Drained {
		t.Fatalf("ReleaseScope() = %#v, want drained=false while one language pool is busy", result)
	}
}
