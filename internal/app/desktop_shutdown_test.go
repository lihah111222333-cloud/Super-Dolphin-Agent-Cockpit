package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	uiwails "github.com/lihah111222333-cloud/super-dolphin-agent/internal/ui/wails"
)

func TestDesktopShutdownCoordinatorOrdersCancelJoinStopHandoffQuit(t *testing.T) {
	owner := newAppOwnerContext(context.Background())
	var (
		mu     sync.Mutex
		events []string
	)
	record := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}
	if err := owner.RegisterRuntimePreDrain(func(context.Context) error {
		record("drain")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lifecycle := uiwails.NewWailsLifecycle(nil, nil)
	lifecycle.MarkFrontendReady()
	quit := make(chan struct{}, 1)
	lifecycle.SetQuitFunc(func() {
		record("quit")
		quit <- struct{}{}
	})
	coordinator, err := newDesktopShutdownCoordinator(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Configure(func() error {
		record("stop")
		return nil
	}, lifecycle); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() { result <- coordinator.Shutdown(context.Background(), nil) }()
	select {
	case <-owner.RootContext().Done():
		record("cancel")
	case <-time.After(time.Second):
		t.Fatal("root context was not canceled")
	}
	owner.MarkRuntimeDone()
	if err := <-result; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case <-quit:
	case <-time.After(time.Second):
		t.Fatal("quit was not invoked")
	}
	mu.Lock()
	got := strings.Join(events, ",")
	mu.Unlock()
	if got != "cancel,drain,stop,quit" {
		t.Fatalf("shutdown order = %q", got)
	}
}

func TestDesktopShutdownCoordinatorConcurrentTriggersShareOneResult(t *testing.T) {
	owner := newAppOwnerContext(context.Background())
	owner.MarkRuntimeDone()
	triggerErr := errors.New("shared trigger")
	stopErr := errors.New("shared stop")
	stopStarted := make(chan struct{})
	releaseStop := make(chan struct{})
	coordinator, err := newDesktopShutdownCoordinator(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Configure(func() error {
		close(stopStarted)
		<-releaseStop
		return stopErr
	}, uiwails.NewWailsLifecycle(nil, nil)); err != nil {
		t.Fatal(err)
	}

	const callers = 8
	start := make(chan struct{})
	results := make(chan error, callers)
	for range callers {
		go func() {
			<-start
			results <- coordinator.Shutdown(context.Background(), triggerErr)
		}()
	}
	close(start)
	<-stopStarted
	close(releaseStop)
	var first error
	for range callers {
		got := <-results
		if !errors.Is(got, triggerErr) || !errors.Is(got, stopErr) {
			t.Fatalf("Shutdown() error = %v", got)
		}
		if first == nil {
			first = got
		} else if got != first {
			t.Fatalf("concurrent callers observed distinct result instances: %p != %p", got, first)
		}
	}
}

func TestDesktopShutdownCoordinatorRepeatedTriggerReturnsSameResult(t *testing.T) {
	owner := newAppOwnerContext(context.Background())
	owner.MarkRuntimeDone()
	stopErr := errors.New("stop failed")
	coordinator, err := newDesktopShutdownCoordinator(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Configure(func() error { return stopErr }, uiwails.NewWailsLifecycle(nil, nil)); err != nil {
		t.Fatal(err)
	}
	first := coordinator.Shutdown(context.Background(), nil)
	second := coordinator.Shutdown(context.Background(), errors.New("late trigger"))
	if first != second || !errors.Is(second, stopErr) {
		t.Fatalf("repeated results = %v / %v", first, second)
	}
}

func TestDesktopShutdownCoordinatorJoinsRunnerStopAndRunErrors(t *testing.T) {
	owner := newAppOwnerContext(context.Background())
	owner.MarkRuntimeDone()
	drainErr := errors.New("drain failed")
	stopErr := errors.New("stop failed")
	runErr := errors.New("runner failed")
	if err := owner.RegisterRuntimePreDrain(func(context.Context) error { return drainErr }); err != nil {
		t.Fatal(err)
	}
	coordinator, err := newDesktopShutdownCoordinator(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Configure(func() error { return stopErr }, uiwails.NewWailsLifecycle(nil, nil)); err != nil {
		t.Fatal(err)
	}
	got := coordinator.Shutdown(context.Background(), runErr)
	for _, want := range []error{runErr, drainErr, stopErr} {
		if !errors.Is(got, want) {
			t.Fatalf("Shutdown() error = %v, missing %v", got, want)
		}
	}
}

func TestDesktopShutdownCoordinatorRunnerExitAndWindowRace(t *testing.T) {
	owner := newAppOwnerContext(context.Background())
	owner.MarkRuntimeDone()
	runnerErr := errors.New("runner exit")
	windowErr := errors.New("window exit")
	stopStarted := make(chan struct{})
	releaseStop := make(chan struct{})
	coordinator, err := newDesktopShutdownCoordinator(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Configure(func() error {
		close(stopStarted)
		<-releaseStop
		return nil
	}, uiwails.NewWailsLifecycle(nil, nil)); err != nil {
		t.Fatal(err)
	}
	runnerResult := make(chan error, 1)
	windowResult := make(chan error, 1)
	go func() { runnerResult <- coordinator.Shutdown(context.Background(), runnerErr) }()
	<-stopStarted
	go func() { windowResult <- coordinator.Shutdown(context.Background(), windowErr) }()
	deadline := time.Now().Add(time.Second)
	for {
		coordinator.mu.Lock()
		causeCount := len(coordinator.causes)
		coordinator.mu.Unlock()
		if causeCount == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("window trigger did not join in-flight shutdown")
		}
		runtime.Gosched()
	}
	close(releaseStop)
	for _, got := range []error{<-runnerResult, <-windowResult} {
		if !errors.Is(got, runnerErr) || !errors.Is(got, windowErr) {
			t.Fatalf("race result = %v", got)
		}
	}
}

func TestDesktopShutdownCoordinatorStartupFailureUnblocksWaiters(t *testing.T) {
	owner := newAppOwnerContext(context.Background())
	coordinator, err := newDesktopShutdownCoordinator(owner)
	if err != nil {
		t.Fatal(err)
	}
	startupErr := errors.New("startup failed")
	waiter := make(chan error, 1)
	go func() { waiter <- coordinator.Shutdown(context.Background(), nil) }()
	if got := coordinator.FailStartup(startupErr); !errors.Is(got, startupErr) {
		t.Fatalf("FailStartup() error = %v", got)
	}
	select {
	case got := <-waiter:
		if !errors.Is(got, startupErr) {
			t.Fatalf("waiter error = %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("startup failure did not unblock shutdown waiter")
	}
}

func TestBindRuntimeDesktopUsesCoordinatorNotFXShutdowner(t *testing.T) {
	owner := newAppOwnerContext(context.Background())
	coordinator, err := newDesktopShutdownCoordinator(owner)
	if err != nil {
		t.Fatal(err)
	}
	owner.desktopShutdown = coordinator
	stopped := make(chan struct{}, 1)
	if err := coordinator.Configure(func() error {
		stopped <- struct{}{}
		return nil
	}, uiwails.NewWailsLifecycle(nil, nil)); err != nil {
		t.Fatal(err)
	}
	lifecycle := &runtimeTestLifecycle{}
	err = BindRuntime(lifecycle, runtimeParams{
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Shutdowner:        runtimeTestShutdowner{},
		RootCtx:           owner,
		ExtractionDrainer: runtimeDrainStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.hooks[0].OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("desktop coordinator did not own runner-exit shutdown")
	}
}

func TestBindRuntimeRunnerErrorReachesCoordinator(t *testing.T) {
	owner := newAppOwnerContext(context.Background())
	coordinator, err := newDesktopShutdownCoordinator(owner)
	if err != nil {
		t.Fatal(err)
	}
	owner.desktopShutdown = coordinator
	if err := coordinator.Configure(func() error { return nil }, uiwails.NewWailsLifecycle(nil, nil)); err != nil {
		t.Fatal(err)
	}
	lifecycle := &runtimeTestLifecycle{}
	if err := BindRuntime(lifecycle, runtimeParams{
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Shutdowner:        runtimeTestShutdowner{},
		RootCtx:           owner,
		ExtractionDrainer: runtimeDrainStub{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.hooks[0].OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := coordinator.Shutdown(context.Background(), nil)
	if got == nil || !strings.Contains(got.Error(), "no runners registered") {
		t.Fatalf("coordinator result = %v, want runner error", got)
	}
}
