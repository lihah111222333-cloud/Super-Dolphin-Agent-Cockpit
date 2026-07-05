package app

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func startAppGoroutineForTest(t *testing.T, label string, run func()) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		run()
	}()
	t.Cleanup(func() {
		select {
		case <-done:
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
