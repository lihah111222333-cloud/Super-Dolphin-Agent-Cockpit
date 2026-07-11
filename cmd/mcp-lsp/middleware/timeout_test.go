package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestTimeoutUsesShorterToolLimitWhenParentDeadlineIsLonger(t *testing.T) {
	parentCtx, parentCancel := context.WithTimeout(context.Background(), time.Second)
	defer parentCancel()
	release := make(chan struct{})
	started := make(chan struct{})
	t.Cleanup(func() { close(release) })

	handler := Timeout(20 * time.Millisecond)(func(context.Context, json.RawMessage) (any, error) {
		close(started)
		<-release
		return map[string]any{"ok": true}, nil
	})

	done := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		_, err := handler(parentCtx, nil)
		done <- err
	})
	defer goroutines.Wait()

	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("handler did not start")
	}
	select {
	case err := <-done:
		var coded *common.CodedToolError
		if !errors.As(err, &coded) {
			t.Fatalf("Timeout error = %T %v, want CodedToolError", err, err)
		}
		if coded.Code != "lsp_timeout" {
			t.Fatalf("Timeout code = %q, want lsp_timeout", coded.Code)
		}
	case <-time.After(120 * time.Millisecond):
		t.Fatal("Timeout did not return at shorter tool limit")
	}
}

func TestTimeoutCapacityRejectsWhileTimedOutHandlerIsStillRunning(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var startedCount atomic.Int32
	handler := timeoutWithWorkerLimit(10*time.Millisecond, 1)(func(context.Context, json.RawMessage) (any, error) {
		if startedCount.Add(1) == 1 {
			close(started)
		}
		<-release
		return map[string]any{"ok": true}, nil
	})

	firstDone := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		_, err := handler(context.Background(), nil)
		firstDone <- err
	})
	defer goroutines.Wait()
	waitForTimeoutTestSignal(t, started, "first handler did not start")
	requireTimeoutCode(t, waitForTimeoutTestError(t, firstDone, "first timeout did not return"), "lsp_timeout")

	_, err := handler(context.Background(), nil)
	requireTimeoutCode(t, err, "lsp_timeout_capacity")
	if startedCount.Load() != 1 {
		t.Fatal("second handler started despite timeout capacity being full")
	}
	close(release)
}

func waitForTimeoutTestSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(100 * time.Millisecond):
		t.Fatal(message)
	}
}

func waitForTimeoutTestError(t *testing.T, done <-chan error, message string) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(120 * time.Millisecond):
		t.Fatal(message)
		return nil
	}
}

func requireTimeoutCode(t *testing.T, err error, code string) {
	t.Helper()
	var coded *common.CodedToolError
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("timeout error = %T %v, want %s", err, err, code)
	}
}
