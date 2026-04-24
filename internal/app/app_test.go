package app

import (
	"context"
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
