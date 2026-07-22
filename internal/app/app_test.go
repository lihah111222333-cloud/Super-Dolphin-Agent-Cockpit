package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	uiwails "github.com/lihah111222333-cloud/super-dolphin-agent/internal/ui/wails"
)

func startAppGoroutineForTest(t *testing.T, label string, run func()) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		defer close(done)
		run()
	})
	t.Cleanup(func() {
		select {
		case <-done:
			wg.Wait()
		case <-time.After(time.Second):
			t.Fatalf("%s goroutine did not stop", label)
		}
	})
	return done
}

func startAppErrorGoroutineForTest(t *testing.T, label string, run func() error) <-chan error {
	t.Helper()
	errCh := make(chan error, 1)
	startAppGoroutineForTest(t, label, func() {
		errCh <- run()
	})
	return errCh
}

func TestWatchFXShutdownHonorsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan os.Signal)
	stop := make(chan struct{})
	failed := make(chan struct{}, 1)

	exited := startAppGoroutineForTest(t, "shutdown watcher", func() {
		runShutdownWatcher(ctx, done, stop, func() error {
			return errors.New("watcher should not stop backend on context cancellation")
		}, func(error) {
			failed <- struct{}{}
		})
	})

	cancel()

	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("shutdown watcher did not exit after parent context cancellation")
	}

	select {
	case <-failed:
		t.Fatal("shutdown watcher treated context cancellation as backend failure")
	default:
	}
}

func TestRunDesktopPreDrain(t *testing.T) {
	owner := newAppOwnerContext(context.Background())
	drainStarted := make(chan struct{})
	drainRelease := make(chan struct{})
	owner.RegisterRuntimePreDrain(func(context.Context) error {
		close(drainStarted)
		<-drainRelease
		return nil
	})
	owner.Cancel()
	drained := startAppErrorGoroutineForTest(t, "desktop pre-drain", func() error {
		return preDrainDesktopRuntime(owner.RootContext(), owner)
	})
	select {
	case <-drainStarted:
		t.Fatal("preDrainDesktopRuntime started runtime drain before runtime done")
	case <-time.After(100 * time.Millisecond):
	}

	select {
	case err := <-drained:
		t.Fatalf("preDrainDesktopRuntime returned before runtime drain: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	owner.MarkRuntimeDone()
	select {
	case <-drainStarted:
	case <-time.After(time.Second):
		t.Fatal("preDrainDesktopRuntime did not start registered runtime drain after runtime done")
	}
	close(drainRelease)
	select {
	case err := <-drained:
		if err != nil {
			t.Fatalf("preDrainDesktopRuntime() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("preDrainDesktopRuntime did not finish after runtime done")
	}
}

func TestRootCtxSymmetry(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	owner := newAppOwnerContext(parent)
	select {
	case <-owner.RootContext().Done():
		t.Fatal("owner root context canceled before parent")
	default:
	}
	cancelParent()
	select {
	case <-owner.RootContext().Done():
	case <-time.After(time.Second):
		t.Fatal("owner root context did not inherit parent cancellation")
	}
}

func TestShutdownWatcherStopAndWaitJoins(t *testing.T) {
	ctx := t.Context()
	done := make(chan os.Signal)
	stop := make(chan struct{})
	errCh := make(chan error, 1)
	exited := startAppGoroutineForTest(t, "shutdown watcher", func() {
		runShutdownWatcher(ctx, done, stop, func() error {
			return errors.New("watcher should not stop backend on explicit stop")
		}, func(error) {
			errCh <- errors.New("watcher should not notify backend failure on explicit stop")
		})
	})
	close(stop)
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("shutdown watcher did not join after explicit stop")
	}
	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
}

func TestRunShutdownWatcherStopsBackendBeforeAllowingQuit(t *testing.T) {
	done := make(chan os.Signal, 1)
	stop := make(chan struct{})
	events := make([]string, 0, 2)
	done <- os.Interrupt

	runShutdownWatcher(context.Background(), done, stop, func() error {
		events = append(events, "stop")
		return nil
	}, func(err error) {
		if err != nil {
			t.Fatalf("onStopped error = %v", err)
		}
		events = append(events, "quit")
	})

	if len(events) != 2 || events[0] != "stop" || events[1] != "quit" {
		t.Fatalf("events = %#v, want stop before quit", events)
	}
}

func TestPrepareDesktopRuntimeStartsAndValidatesBeforeRun(t *testing.T) {
	events := make([]string, 0, 3)
	err := prepareDesktopRuntime(
		context.Background(),
		func(context.Context) error {
			events = append(events, "start")
			return nil
		},
		func() error {
			events = append(events, "validate")
			return nil
		},
		func() error {
			events = append(events, "stop")
			return nil
		},
	)
	if err != nil {
		t.Fatalf("prepareDesktopRuntime() error = %v", err)
	}
	if got := strings.Join(events, ","); got != "start,validate" {
		t.Fatalf("events = %q, want start,validate", got)
	}
}

func TestPrepareDesktopRuntimeValidationFailureStopsFXOnce(t *testing.T) {
	validationErr := errors.New("validate Wails dependencies")
	stopCalls := 0
	err := prepareDesktopRuntime(
		context.Background(),
		func(context.Context) error { return nil },
		func() error { return validationErr },
		func() error {
			stopCalls++
			return nil
		},
	)
	if !errors.Is(err, validationErr) {
		t.Fatalf("prepareDesktopRuntime() error = %v, want %v", err, validationErr)
	}
	if stopCalls != 1 {
		t.Fatalf("stop calls = %d, want 1", stopCalls)
	}
}

func TestRunActivatedDesktopDoesNotACKBeforeApplicationStarted(t *testing.T) {
	readiness := uiwails.NewActivationReadiness()
	readyCalls := 0
	runErr := errors.New("wails run failed before activation")
	err := runActivatedDesktop(
		context.Background(),
		readiness,
		func(context.Context, DesktopACKPublisher) error { readyCalls++; return nil },
		func() error { return runErr },
		func() {},
	)
	if !errors.Is(err, runErr) || !errors.Is(err, errDesktopNotActivated) {
		t.Fatalf("runActivatedDesktop() error = %v, want run and activation errors", err)
	}
	if readyCalls != 0 {
		t.Fatalf("ready calls = %d, want 0", readyCalls)
	}
}

func TestRunActivatedDesktopDoesNotACKAfterNativeApplicationStartedWithoutFrontendRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	readiness := uiwails.NewActivationReadiness()
	readyCalls := 0
	quitCalls := 0

	err := runActivatedDesktop(
		ctx,
		readiness,
		func(context.Context, DesktopACKPublisher) error {
			readyCalls++
			return nil
		},
		func() error {
			readiness.MarkApplicationStarted()
			<-ctx.Done()
			return nil
		},
		func() { quitCalls++ },
	)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, errDesktopNotFrontendReady) {
		t.Fatalf("runActivatedDesktop() error = %v, want frontend readiness timeout", err)
	}
	if readyCalls != 0 {
		t.Fatalf("ready calls = %d, want 0 before frontend RPC readiness", readyCalls)
	}
	if quitCalls != 1 {
		t.Fatalf("quit calls = %d, want 1 after frontend readiness timeout", quitCalls)
	}
}

func TestRunActivatedDesktopACKsAfterFrontendReadinessSignal(t *testing.T) {
	readiness := uiwails.NewActivationReadiness()
	readyCalled := make(chan struct{})
	err := runActivatedDesktop(
		context.Background(),
		readiness,
		func(_ context.Context, publish DesktopACKPublisher) error {
			return publish(func() error { close(readyCalled); return nil })
		},
		func() error {
			readiness.MarkApplicationStarted()
			epoch, err := readiness.CurrentEpoch()
			if err != nil {
				return err
			}
			if err := readiness.MarkFrontendReady(epoch); err != nil {
				return err
			}
			<-readyCalled
			return nil
		},
		func() {},
	)
	if err != nil {
		t.Fatalf("runActivatedDesktop() error = %v", err)
	}
}

func TestRunActivatedDesktopRejectsACKWhenRunReturnsDuringReadyCallback(t *testing.T) {
	readiness := uiwails.NewActivationReadiness()
	readyStarted := make(chan struct{})
	readyContext := make(chan context.Context, 1)
	releaseReady := make(chan struct{})
	published := make(chan struct{})
	runReturned := make(chan struct{})

	errDone := startAppErrorGoroutineForTest(t, "desktop activation", func() error {
		return runActivatedDesktop(
			context.Background(),
			readiness,
			func(ctx context.Context, publish DesktopACKPublisher) error {
				readyContext <- ctx
				close(readyStarted)
				<-releaseReady
				return publish(func() error { close(published); return nil })
			},
			func() error {
				readiness.MarkApplicationStarted()
				epoch, err := readiness.CurrentEpoch()
				if err != nil {
					return err
				}
				if err := readiness.MarkFrontendReady(epoch); err != nil {
					return err
				}
				<-readyStarted
				close(runReturned)
				return nil
			},
			func() {},
		)
	})

	readyCtx := <-readyContext
	<-runReturned
	<-readyCtx.Done()
	close(releaseReady)
	err := <-errDone
	if !errors.Is(err, errDesktopRunBeforeACK) {
		t.Fatalf("runActivatedDesktop() error = %v, want %v", err, errDesktopRunBeforeACK)
	}
	select {
	case <-published:
		t.Fatal("healthy ACK published after Wails Run returned")
	default:
	}
}

func TestRunActivatedDesktopPropagatesACKFailureAndQuits(t *testing.T) {
	readiness := uiwails.NewActivationReadiness()
	readyErr := errors.New("record healthy ACK")
	quit := make(chan struct{})
	err := runActivatedDesktop(
		context.Background(),
		readiness,
		func(_ context.Context, publish DesktopACKPublisher) error {
			return publish(func() error { return readyErr })
		},
		func() error {
			readiness.MarkApplicationStarted()
			epoch, err := readiness.CurrentEpoch()
			if err != nil {
				return err
			}
			if err := readiness.MarkFrontendReady(epoch); err != nil {
				return err
			}
			<-quit
			return nil
		},
		func() { close(quit) },
	)
	if !errors.Is(err, readyErr) {
		t.Fatalf("runActivatedDesktop() error = %v, want %v", err, readyErr)
	}
}
