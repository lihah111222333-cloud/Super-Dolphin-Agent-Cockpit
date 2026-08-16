package multilsp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

type bufferWriteCloser struct{ bytes.Buffer }

func (*bufferWriteCloser) Close() error { return nil }

func TestDefaultLSPRequestTimeoutIsSixtySeconds(t *testing.T) {
	if defaultRequestTimeout != 60*time.Second {
		t.Fatalf("defaultRequestTimeout = %s, want 60s per LSP step", defaultRequestTimeout)
	}
}

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

func TestTransportRequestWriteHonorsDeadline(t *testing.T) {
	if os.Getenv("MCP_LSP_BLOCK_STDIN_HELPER") == "1" {
		time.Sleep(30 * time.Second)
		return
	}

	tr, err := newTransport(transportOptions{
		Binary: os.Args[0],
		Args:   []string{"-test.run=^TestTransportRequestWriteHonorsDeadline$"},
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
	t.Cleanup(func() {
		if err := gate.Close(); err != nil {
			t.Errorf("close gated transport stdin: %v", err)
		}
	})

	errCh := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, err := tr.request(ctx, "workspace/executeCommand", map[string]string{"command": "test"})
		errCh <- err
	})

	waitForGatedWrite(t, tr, gate)
	waitForRequestDeadline(t, tr, gate, errCh)
	assertRequestContextTerminatedTransport(t, tr)
	closeOriginalStdin()
}

func TestTransportResponseDeadlinePreservesHealthyTransport(t *testing.T) {
	writer := &bufferWriteCloser{}
	tr := &transport{
		stdin:   writer,
		pending: map[string]chan pendingResult{},
		done:    make(chan struct{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := tr.request(ctx, "textDocument/definition", map[string]string{"uri": "file:///slow.go"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request() error = %v, want context deadline exceeded", err)
	}
	if tr.closed.Load() {
		t.Fatal("response deadline closed transport; slow server progress must survive for retry")
	}
	tr.pendingMu.Lock()
	pendingCount := len(tr.pending)
	tr.pendingMu.Unlock()
	if pendingCount != 0 {
		t.Fatalf("pending requests after response deadline = %d, want 0", pendingCount)
	}
	if writer.Len() == 0 {
		t.Fatal("request was not written before response deadline")
	}
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
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("request() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(2 * time.Second):
		_ = tr.killProcess()
		if err := gate.Close(); err != nil {
			t.Errorf("close gated transport stdin after forced process kill: %v", err)
		}
		select {
		case err := <-errCh:
			t.Fatalf("request() returned only after forced process kill with error %v; want ctx deadline to cancel the blocked write", err)
		case <-time.After(2 * time.Second):
			t.Fatalf("request() stayed blocked after context deadline and forced process kill")
		}
	}
}

func assertRequestContextTerminatedTransport(t *testing.T, tr *transport) {
	t.Helper()
	if !tr.closed.Load() {
		t.Fatalf("request() returned context termination without closing transport; want request context termination path to close it")
	}
	assertTransportContextTerminationPlatform(t, tr)
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
