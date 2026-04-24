package app

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestWatchFXShutdownHonorsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan os.Signal)
	stop := make(chan struct{})
	exited := make(chan struct{})
	failed := make(chan struct{}, 1)

	go func() {
		defer close(exited)
		runShutdownWatcher(ctx, done, stop, func() {
			failed <- struct{}{}
		})
	}()

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
	drained := make(chan error, 1)
	go func() {
		drained <- preDrainDesktopRuntime(owner.RootContext(), owner)
	}()
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan os.Signal)
	stop := make(chan struct{})
	exited := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		defer close(exited)
		runShutdownWatcher(ctx, done, stop, func() {
			errCh <- errors.New("watcher should not notify backend failure on explicit stop")
		})
	}()
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
