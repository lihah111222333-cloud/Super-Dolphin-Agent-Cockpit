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
	blockedWriter := newBlockingTransportWriteCloser(tr.stdin)
	tr.stdinMu.Lock()
	tr.stdin = blockedWriter
	tr.stdinMu.Unlock()

	errCh := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	goroutines.Go(func() {
		_, err := tr.request(ctx, "workspace/executeCommand", map[string]string{
			"payload": "blocked-write",
		})
		errCh <- err
	})
	select {
	case <-blockedWriter.started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not enter the blocking stdin Write before cancellation")
	}
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("request() error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		_ = tr.killProcess()
		select {
		case err := <-errCh:
			t.Fatalf("request() returned only after forced process kill with error %v; want ctx cancellation to cancel the blocked write", err)
		case <-time.After(2 * time.Second):
			t.Fatalf("request() stayed blocked after context cancellation and forced transport close")
		}
	}

	assertRequestContextTerminatedTransport(t, tr)
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

type blockingTransportWriteCloser struct {
	delegate  io.WriteCloser
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func newBlockingTransportWriteCloser(delegate io.WriteCloser) *blockingTransportWriteCloser {
	return &blockingTransportWriteCloser{
		delegate: delegate,
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (w *blockingTransportWriteCloser) Write([]byte) (int, error) {
	w.startOnce.Do(func() { close(w.started) })
	<-w.release
	return 0, io.ErrClosedPipe
}

func (w *blockingTransportWriteCloser) Close() error {
	var err error
	w.closeOnce.Do(func() {
		close(w.release)
		err = w.delegate.Close()
	})
	return err
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
