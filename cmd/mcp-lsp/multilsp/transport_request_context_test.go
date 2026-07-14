package multilsp

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

type gatedWriteCloser struct {
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
	closeOnce   sync.Once
}

func newGatedWriteCloser() *gatedWriteCloser {
	return &gatedWriteCloser{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (w *gatedWriteCloser) Write(_ []byte) (int, error) {
	w.enteredOnce.Do(func() { close(w.entered) })
	<-w.release
	return 0, io.ErrClosedPipe
}

func (w *gatedWriteCloser) Close() error {
	w.closeOnce.Do(func() { close(w.release) })
	return nil
}

func TestTransportRequestWriteHonorsContext(t *testing.T) {
	if os.Getenv("MCP_LSP_BLOCK_STDIN_HELPER") == "1" {
		time.Sleep(30 * time.Second)
		return
	}

	tr, err := newTransport(transportOptions{
		Binary: os.Args[0],
		Args:   []string{"-test.run=^TestTransportRequestWriteHonorsContext$"},
		Env:    []string{"MCP_LSP_BLOCK_STDIN_HELPER=1"},
	})
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	cleanupLeakedTransportAfterFailure(t, tr)

	gate := newGatedWriteCloser()
	tr.stdinMu.Lock()
	originalStdin := tr.stdin
	tr.stdin = gate
	tr.stdinMu.Unlock()
	var closeOriginalOnce sync.Once
	closeOriginalStdin := func() {
		closeOriginalOnce.Do(func() {
			if err := originalStdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				t.Errorf("close original transport stdin: %v", err)
			}
		})
	}
	t.Cleanup(closeOriginalStdin)

	errCh := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	t.Cleanup(func() {
		if err := gate.Close(); err != nil {
			t.Errorf("close gated transport stdin: %v", err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	goroutines.Go(func() {
		_, err := tr.request(ctx, "workspace/executeCommand", map[string]string{
			"payload": "blocked-write",
		})
		errCh <- err
	})

	waitForGatedWrite(t, tr, gate)
	cancel()
	waitForRequestDeadline(t, tr, gate, errCh)
	assertRequestContextTerminatedTransport(t, tr)
	closeOriginalStdin()
}

func waitForGatedWrite(t *testing.T, tr *transport, gate *gatedWriteCloser) {
	t.Helper()
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		_ = tr.Close()
		t.Fatalf("request() did not enter the gated stdin Write within watchdog")
	}
}

func waitForRequestDeadline(t *testing.T, tr *transport, gate *gatedWriteCloser, errCh <-chan error) {
	t.Helper()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("request() error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		_ = tr.killProcess()
		if err := gate.Close(); err != nil {
			t.Errorf("close gated transport stdin after forced process kill: %v", err)
		}
		select {
		case err := <-errCh:
			t.Fatalf("request() returned only after forced process kill with error %v; want ctx cancellation to cancel the blocked write", err)
		case <-time.After(2 * time.Second):
			t.Fatalf("request() stayed blocked after context cancellation and forced transport close")
		}
	}
}

func assertRequestContextTerminatedTransport(t *testing.T, tr *transport) {
	t.Helper()
	if !tr.closed.Load() {
		t.Fatalf("request() returned context cancellation without closing transport; want request context cancellation path to close it")
	}
	select {
	case <-tr.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("request() returned on context cancellation but LSP process stayed alive; want request context cancellation path to terminate the process before cleanup")
	}
}

func cleanupLeakedTransportAfterFailure(t *testing.T, tr *transport) {
	t.Helper()
	t.Cleanup(func() {
		select {
		case <-tr.done:
			return
		default:
		}
		if !t.Failed() {
			t.Errorf("transport still running at cleanup; test must observe request context terminating the process before cleanup")
		}
		_ = tr.Close()
	})
}
