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
	blockedStdin := installBlockingTestStdin(t, tr)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		_, err := tr.request(ctx, "workspace/executeCommand", map[string]string{
			"payload": "x",
		})
		errCh <- err
	})

	assertBlockedWriteStartedBeforeDeadline(t, ctx, tr, blockedStdin)

	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("request() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(2 * time.Second):
		tr.closeInput()
		_ = tr.killProcess()
		select {
		case err := <-errCh:
			t.Fatalf("request() returned only after forced process kill with error %v; want ctx deadline to cancel the blocked write", err)
		case <-time.After(2 * time.Second):
			t.Fatalf("request() stayed blocked after context deadline and forced transport close")
		}
	}

	assertRequestContextTerminatedTransport(t, tr)
}

// assertBlockedWriteStartedBeforeDeadline 等待阻塞写入启动，并证明它发生在请求上下文截止时间之前。
func assertBlockedWriteStartedBeforeDeadline(
	t *testing.T,
	ctx context.Context,
	tr *transport,
	blockedStdin *testBlockingWriteCloser,
) {
	t.Helper()
	select {
	case <-blockedStdin.writeStarted:
	case <-ctx.Done():
		t.Fatalf("test fixture deadline expired before blocked stdin Write started: %v", ctx.Err())
	case <-time.After(2 * time.Second):
		tr.closeInput()
		_ = tr.killProcess()
		t.Fatalf("request() did not start writing before the deadlock guard expired")
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("test fixture context has no deadline")
	}
	if blockedStdin.startedAt.IsZero() {
		t.Fatal("test fixture observed writeStarted without recording the Write start time")
	}
	if !blockedStdin.startedAt.Before(deadline) {
		t.Fatalf("test fixture blocked stdin Write started at %v; want before context deadline %v", blockedStdin.startedAt, deadline)
	}
}

type testBlockingWriteCloser struct {
	io.WriteCloser
	writeStarted chan struct{}
	startedAt    time.Time
	closed       chan struct{}
	writeOnce    sync.Once
	closeOnce    sync.Once
	closeErr     error
}

func installBlockingTestStdin(t *testing.T, tr *transport) *testBlockingWriteCloser {
	t.Helper()
	tr.stdinMu.Lock()
	defer tr.stdinMu.Unlock()
	if tr.stdin == nil {
		t.Fatal("transport stdin is nil before installing blocked-write test barrier")
	}
	stdin := &testBlockingWriteCloser{
		WriteCloser:  tr.stdin,
		writeStarted: make(chan struct{}),
		closed:       make(chan struct{}),
	}
	tr.stdin = stdin
	return stdin
}

func (w *testBlockingWriteCloser) Write(_ []byte) (int, error) {
	w.writeOnce.Do(func() {
		w.startedAt = time.Now()
		close(w.writeStarted)
	})
	<-w.closed
	return 0, io.ErrClosedPipe
}

func (w *testBlockingWriteCloser) Close() error {
	w.closeOnce.Do(func() {
		close(w.closed)
		w.closeErr = w.WriteCloser.Close()
	})
	return w.closeErr
}

func assertRequestContextTerminatedTransport(t *testing.T, tr *transport) {
	t.Helper()
	if !tr.closed.Load() {
		t.Fatalf("request() returned context deadline without closing transport; want request context cancellation path to close it")
	}
	select {
	case <-tr.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("request() returned on context deadline but LSP process stayed alive; want request context cancellation path to terminate the process before cleanup")
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
