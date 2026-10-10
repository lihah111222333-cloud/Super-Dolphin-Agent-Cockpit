// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type mockServer struct {
	runErr error
	runFn  func(context.Context) error
}

func (m *mockServer) Run(ctx context.Context) error {
	if m.runFn != nil {
		return m.runFn(ctx)
	}
	return m.runErr
}

type mockManager struct {
	closeErr   error
	closeCalls atomic.Int32
}

func (m *mockManager) Close() error {
	m.closeCalls.Add(1)
	return m.closeErr
}

func TestStdioRunnerExecution(t *testing.T) {
	t.Parallel()

	t.Run("nil server returns error", func(t *testing.T) {
		t.Parallel()
		runner := stdioRunner{server: nil}
		err := runner.Run(context.Background())
		if err == nil {
			t.Fatal("expected error on nil server, got nil")
		}
	})

	t.Run("server run normal completion closes manager", func(t *testing.T) {
		t.Parallel()
		mgr := &mockManager{}
		runner := stdioRunner{
			server:  &mockServer{runErr: nil},
			manager: mgr,
		}
		err := runner.Run(context.Background())
		if err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}
		if calls := mgr.closeCalls.Load(); calls != 1 {
			t.Fatalf("expected manager.Close to be called 1 time, got %d", calls)
		}
	})

	t.Run("server error joins manager close error", func(t *testing.T) {
		t.Parallel()
		serverErr := errors.New("server read eof")
		closeErr := errors.New("manager close failed")
		mgr := &mockManager{closeErr: closeErr}
		runner := stdioRunner{
			server:  &mockServer{runErr: serverErr},
			manager: mgr,
		}
		err := runner.Run(context.Background())
		if !errors.Is(err, serverErr) || !errors.Is(err, closeErr) {
			t.Fatalf("expected error joining serverErr and closeErr, got: %v", err)
		}
	})
}

func TestStdioRunnerHardExitWatchdog(t *testing.T) {
	t.Parallel()

	t.Run("stdio eof hard exit timeout invokes exit callback when shutdown hangs", func(t *testing.T) {
		t.Parallel()
		exitCodeCh := make(chan int, 1)

		runner := stdioRunner{
			server:      &mockServer{runErr: nil},
			hardTimeout: 20 * time.Millisecond,
			hardExitFunc: func(code int) {
				select {
				case exitCodeCh <- code:
				default:
				}
			},
		}

		err := runner.Run(context.Background())
		if err != nil {
			t.Fatalf("expected runner.Run to return nil, got: %v", err)
		}

		select {
		case code := <-exitCodeCh:
			if code != 1 {
				t.Fatalf("expected hardExitFunc to receive failure code 1, got %d", code)
			}
		case <-time.After(1 * time.Second):
			t.Fatal("timed out waiting for stdio EOF hard exit watchdog to fire")
		}
	})
}

// TestStdioRunnerCancellationStartsHardExitBeforeServerReturns 锁定取消时 stdio 阻塞不能阻止退出时限启动。
func TestStdioRunnerCancellationStartsHardExitBeforeServerReturns(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	exitCodes := make(chan int, 2)
	group := newTestGoroutineGroup(t)
	defer group.Wait()
	defer close(release)
	runner := stdioRunner{
		server: &mockServer{runFn: func(context.Context) error {
			close(started)
			<-release
			return nil
		}},
		hardTimeout: 20 * time.Millisecond,
		hardExitFunc: func(code int) {
			exitCodes <- code
		},
	}
	group.Go(func() {
		if err := runner.Run(ctx); err != nil {
			t.Errorf("Run() error = %v", err)
		}
	})
	<-started
	select {
	case code := <-exitCodes:
		t.Fatalf("active server triggered hard exit %d before cancellation", code)
	case <-time.After(40 * time.Millisecond):
	}
	cancel()
	select {
	case code := <-exitCodes:
		if code != 1 {
			t.Fatalf("hard shutdown exit code = %d, want 1", code)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not arm hard exit while server.Run remained blocked")
	}
}
