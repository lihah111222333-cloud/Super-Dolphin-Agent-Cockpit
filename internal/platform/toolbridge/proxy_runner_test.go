package toolbridge

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// TestProxyRunnerServeReturnsOnContextCancel pins the P2 Finding 9 shutdown
// contract: Run blocks on ServeProxy, and when the runner's ctx is
// cancelled it closes the listener itself to unblock ServeProxy, joins the
// inner goroutine, and returns ctx.Err().
func TestProxyRunnerServeReturnsOnContextCancel(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	runner := NewProxyRunner(h)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen(): %v", err)
	}
	runner.SetListener(ln)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	// Give Serve a moment to start.
	time.Sleep(20 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("Run returned before cancel: err=%v", err)
	default:
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}

	// Listener closed twice should be a no-op (P22 contract says Run closes it).
	_ = ln.Close()
}

// TestProxyRunnerNilListenerBlocksUntilCtxDone exercises the defensive
// branch where the runner is scheduled before registerProxyLifecycle has
// armed the listener (or listener setup failed). Run must simply wait for
// ctx cancellation; no panic, no spurious serve.
func TestProxyRunnerNilListenerBlocksUntilCtxDone(t *testing.T) {
	t.Parallel()
	runner := NewProxyRunner(&Handler{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return with nil listener")
	}
}

// TestProxyRunnerNilHandlerBlocksUntilCtxDone mirrors the defensive branch
// for NewProxyRunner(nil) — a possible test / partial-wiring configuration.
func TestProxyRunnerNilHandlerBlocksUntilCtxDone(t *testing.T) {
	t.Parallel()
	runner := NewProxyRunner(nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen(): %v", err)
	}
	defer ln.Close()
	runner.SetListener(ln)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return with nil handler")
	}
}

// TestProxyRunnerServeFailureSurfaces ensures a non-cancel-triggered Serve
// failure (e.g. some unexpected condition) is surfaced as the Run return
// value rather than silently swallowed. We simulate the failure path by
// closing the listener externally before Run starts; ServeProxy then
// returns quickly with net.ErrClosed, which it maps to nil internally.
// Therefore the surfaced error is also nil — consistent with the
// Handler.ServeProxy contract that swallows close-style errors.
func TestProxyRunnerServeFailureSurfaces(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	runner := NewProxyRunner(h)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen(): %v", err)
	}
	// Close BEFORE handing off: ServeProxy will return net.ErrClosed → nil.
	_ = ln.Close()
	runner.SetListener(ln)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	select {
	case err := <-done:
		// Normal close → nil per ServeProxy contract.
		if err != nil {
			t.Fatalf("Run err = %v, want nil on closed-listener early return", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after closed-listener serve")
	}
}

func TestProxyRunnerServePanicSurfaces(t *testing.T) {
	t.Parallel()

	runner := NewProxyRunner(&Handler{})
	runner.SetListener(panicListener{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "proxy serve panic") {
			t.Fatalf("Run err = %v, want proxy serve panic", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ServeProxy panic")
	}
}

type panicListener struct{}

func (panicListener) Accept() (net.Conn, error) {
	// archguard:ignore panic_count -- this test double verifies accept-loop panic recovery.
	panic("accept failed")
}
func (panicListener) Close() error   { return nil }
func (panicListener) Addr() net.Addr { return dummyAddr("panic") }

type dummyAddr string

func (a dummyAddr) Network() string { return string(a) }
func (a dummyAddr) String() string  { return string(a) }
